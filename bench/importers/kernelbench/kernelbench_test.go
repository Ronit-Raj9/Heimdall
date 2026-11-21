package kernelbench

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// The fixtures in testdata are hand-written in KernelBench's problem format
// (class Model, get_inputs, get_init_inputs); no upstream file is committed.

func TestEstimateInputBytes(t *testing.T) {
	cases := []struct {
		src  string
		want int64
		why  string
	}{
		{"N = 2048 * 2\ndef get_inputs():\n    A = torch.rand(N, N)\n    B = torch.rand(N, N)\n    return [A, B]\n", 2 * 4096 * 4096 * 4, ""},
		{"b = 16\nh = w = 8\ndef get_inputs():\n    return [torch.randn(b, 3, h, w)]\ndef get_init_inputs():\n    return []\n", 16 * 3 * 8 * 8 * 4, ""},
		{"n = 2 ** 10\ndef get_inputs():\n    return [torch.randint(0, 10, (n,))]\n", 1024 * 4, ""},
		{"s = (4000,)\ndef get_inputs():\n    return [torch.rand(128, *s)]\n", 128 * 4000 * 4, ""},
		{"b = 2\ns = (b * 3, 5)\ndef get_inputs():\n    return [torch.rand(b, *s)]\n", 2 * 6 * 5 * 4, ""},
		{"depth, height, width = 4, 8, 2 * 8\ndef get_inputs():\n    return [torch.rand(2, depth, height, width)]\n", 2 * 4 * 8 * 16 * 4, ""},
		{"a, b = 4, n\ndef get_inputs():\n    return [torch.rand(a)]\n", 0, "unknown name a"},
		{"def get_inputs():\n    return [torch.rand(128, *shape)]\n", 0, "not a known tuple constant"},
		{"def get_inputs():\n    return [torch.rand(n)]\n", 0, "unknown name n"},
		{"def get_inputs():\n    return [torch.rand(4) for _ in range(3)]\n", 0, "loops"},
		{"x = 3\n", 0, "no module-level def get_inputs"},
		{"def get_inputs():\n    return [3]\n", 0, "makes no tensor"},
	}
	for i, c := range cases {
		got, why := EstimateInputBytes([]byte(c.src))
		if c.why == "" && (why != "" || got != c.want) {
			t.Errorf("%d: %d %q, want %d", i, got, why, c.want)
		}
		if c.why != "" && !strings.Contains(why, c.why) {
			t.Errorf("%d: why %q, want %q", i, why, c.why)
		}
	}
}

func TestEvalInt(t *testing.T) {
	env := map[string]int64{"a": 6}
	for expr, want := range map[string]int64{"1_024": 1024, "a * (2 + 1)": 18, "2 ** 3 ** 2": 512, "a // 4": 1, "-a + 10": 4} {
		if got, err := evalInt(expr, env); err != nil || got != want {
			t.Errorf("%s = %d %v, want %d", expr, got, err, want)
		}
	}
	for _, bad := range []string{"a / 2", "b", "1 +", "torch.rand(2)", "2 ** 99"} {
		if _, err := evalInt(bad, env); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestImportFixtures(t *testing.T) {
	root := t.TempDir()
	im, ok := importers.Get(Format)
	if !ok {
		t.Fatal("kernelbench is not registered")
	}
	if info := im.Info(); info.Track != schemas.TrackMLEngineering || info.License != "MIT" {
		t.Errorf("info %+v", info)
	}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "kb1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Tasks, ",") != "l1-001-square-matrix-multiplication,l1-050-conv-standard-2d,l1-089-cumsum" {
		t.Errorf("tasks %v", res.Tasks)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.ID] = s.Reason
	}
	if !strings.Contains(reasons["19_ReLU"], "over max_input_mb 256") || len(res.Skipped) != 1 {
		t.Errorf("skips %+v", res.Skipped)
	}
	dir := filepath.Join(root, "benchmarks", "kb1")
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "kb1")
	if err != nil {
		t.Fatalf("the imported suite does not load: %v", err)
	}
	if s.Track != schemas.TrackMLEngineering || s.Imported == nil || s.Imported.Revision != revision || s.BaseImage.Tag != defaultImage {
		t.Errorf("suite %+v", s)
	}
	for _, task := range s.Tasks {
		if task.GPUCount() != 1 || task.Resources.GPU.MinVRAMMB != defaultMinVRAMMB {
			t.Errorf("%s: resources %+v", task.ID, task.Resources)
		}
	}
	if problems := suite.Lint(s); suite.Errors(problems) > 0 {
		t.Errorf("lint: %+v", problems)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Error("no .gitignore")
	}
	kb, _ := os.ReadFile(filepath.Join(dir, "tasks", "l1-001-square-matrix-multiplication", "verifier", "tests", "kb.json"))
	if !strings.Contains(string(kb), `"calls": 2`) || !strings.Contains(string(kb), `"speedup_target": 0`) {
		t.Errorf("kb.json %s", kb)
	}
	// The reference and mutant patches apply to the starter.
	if git, err := exec.LookPath("git"); err == nil {
		for _, patch := range []string{"solution/patch.diff", "controls/mutant-skew.diff"} {
			tdir := filepath.Join(dir, "tasks", "l1-050-conv-standard-2d")
			work := t.TempDir()
			b, _ := os.ReadFile(filepath.Join(tdir, "repo", "solution.py"))
			os.WriteFile(filepath.Join(work, "solution.py"), b, 0o644)
			cmd := exec.Command(git, "apply", "--check", "-p1", filepath.Join(tdir, patch))
			cmd.Dir = work
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s does not apply: %v %s", patch, err, out)
			}
		}
	}
	// A second import adds to the suite; a foreign suite.yaml is refused.
	if _, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "kb1", IDs: []string{"1"}}); err == nil {
		t.Error("re-import without -force must skip the existing task and then report nothing imported")
	}
	other := filepath.Join(root, "benchmarks", "other")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "suite.yaml"), []byte("id: other\n"), 0o644)
	if _, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "other"}); err == nil || !strings.Contains(err.Error(), "not written by the kernelbench importer") {
		t.Errorf("foreign suite: %v", err)
	}
}

func TestSettings(t *testing.T) {
	for _, lv := range []string{"2", "3"} {
		if s, err := parseSettings(map[string]string{"level": lv}); err != nil || strconv.Itoa(s.level) != lv {
			t.Errorf("level %s: %+v %v", lv, s, err)
		}
	}
	for _, lv := range []string{"4", "0", "x"} {
		if _, err := parseSettings(map[string]string{"level": lv}); err == nil {
			t.Errorf("level %s accepted", lv)
		}
	}
	if s, _ := parseSettings(nil); s.level != 1 {
		t.Errorf("default level %d, want 1", s.level)
	}
	if _, err := parseSettings(map[string]string{"nope": "1"}); err == nil {
		t.Error("unknown setting accepted")
	}
	s, err := parseSettings(map[string]string{"speedup_target": "1", "max_input_mb": "512", "image": "x:1"})
	if err != nil || s.speedupTarget != 1 || s.maxInputMB != 512 || s.image != "x:1" {
		t.Errorf("%+v %v", s, err)
	}
	root := t.TempDir()
	im, _ := importers.Get(Format)
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "kb2", IDs: []string{"50"}, Extra: map[string]string{"speedup_target": "1.5"}})
	if err != nil || len(res.Tasks) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "benchmarks", "kb2", "tasks", res.Tasks[0], "instruction.md"))
	if !strings.Contains(string(b), "at least 1.5 times as fast") {
		t.Errorf("instruction: %s", b)
	}
}

func TestReadProblemsLevel(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "KernelBench", "level2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "import torch\nclass Model(torch.nn.Module):\n    pass\nN = 4\ndef get_inputs():\n    return [torch.rand(N, N)]\ndef get_init_inputs():\n    return []\n"
	if err := os.WriteFile(filepath.Join(dir, "7_Conv_ReLU.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	probs, err := readProblems(root, 2)
	if err != nil || len(probs) != 1 || probs[0].level != 2 || probs[0].num != 7 {
		t.Fatalf("%+v %v", probs, err)
	}
	if id := taskID(probs[0]); id != "l2-007-conv-relu" {
		t.Errorf("task id %s", id)
	}
	if _, err := readProblems(root, 1); err == nil {
		t.Error("level 1 read from a clone with only level2")
	}
}
