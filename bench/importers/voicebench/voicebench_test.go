package voicebench

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/suite"
)

func runImport(t *testing.T, root string, extra map[string]string) *importers.Result {
	t.Helper()
	im, ok := importers.Get(Name)
	if !ok {
		t.Fatal("voicebench not registered")
	}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/extracted", SuiteID: "voicebench", Extra: extra})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestImportFixture(t *testing.T) {
	root := t.TempDir()
	res := runImport(t, root, nil)
	want := []string{"vb-openbookqa-0000", "vb-mmsu-law-886", "vb-bbh-bbh-navigate-147", "vb-bbh-bbh-hyperbaton-3", "vb-advbench-0000"}
	if strings.Join(res.Tasks, ",") != strings.Join(want, ",") || len(res.Skipped) != 0 {
		t.Fatalf("tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	dir := filepath.Join(root, "benchmarks", "voicebench")
	for _, rel := range []string{"suite.yaml", ".gitignore", "services/customer/Dockerfile", "services/customer/voicecaller.py", "services/customer/simuser.py",
		"tasks/vb-mmsu-law-886/audio/question.wav", "tasks/vb-mmsu-law-886/verifier/tests/vbeval.py", "tasks/vb-mmsu-law-886/controls/mutant-wrong.sh"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s", rel)
		}
	}
	var user map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, "tasks/vb-mmsu-law-886/user.json"))
	if err := json.Unmarshal(b, &user); err != nil {
		t.Fatal(err)
	}
	opening := user["script"].(map[string]any)["opening"].(string)
	if user["audio"].(map[string]any)[opening] != "/sim/question.wav" {
		t.Errorf("opening is not mapped to the recording: %v", user["audio"])
	}
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "voicebench")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			t.Errorf("lint error: %s: %s", p.Path, p.Message)
		}
	}
	if len(s.Tasks) != 5 {
		t.Errorf("%d tasks loaded", len(s.Tasks))
	}
}

func TestRefusesLeftOutSubset(t *testing.T) {
	im, _ := importers.Get(Name)
	_, err := im.Import(context.Background(), importers.Options{BenchRoot: t.TempDir(), From: "testdata/extracted", SuiteID: "v", Extra: map[string]string{"subsets": "ifeval"}})
	if err == nil || !strings.Contains(err.Error(), "not imported") {
		t.Fatalf("err %v", err)
	}
}

// TestVerifierGradesControls runs the generated verifier on the answers the
// reference and the mutant speak, and on silence: reference 1, others 0.
func TestVerifierGradesControls(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	res := runImport(t, root, nil)
	items := map[string]item{}
	for _, s := range Subsets {
		got, err := readItems(filepath.Join("testdata/extracted", s, "items.jsonl"), s)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range got {
			items[TaskID(it)] = it
		}
	}
	for _, id := range res.Tasks {
		right, wrong := answers(items[id])
		for _, c := range []struct {
			name, answer string
			want         float64
		}{{"reference", right, 1}, {"mutant", wrong, 0}, {"silence", "", 0}} {
			state := t.TempDir()
			out := t.TempDir()
			events := []map[string]any{{"role": "agent", "content": "Hello, I am listening."}, {"role": "user", "content": items[id].Prompt}}
			if c.answer != "" {
				events = append(events, map[string]any{"role": "agent", "content": c.answer})
			}
			os.MkdirAll(filepath.Join(state, "customer"), 0o755)
			b, _ := json.Marshal(map[string]any{"events": events})
			os.WriteFile(filepath.Join(state, "customer", "transcript.json"), b, 0o644)
			cmd := exec.Command("python3", "-I", filepath.Join(root, "benchmarks/voicebench/tasks", id, "verifier/tests/verify.py"))
			cmd.Env = append(os.Environ(), "GRADER_STATE="+state, "GRADER_OUT="+out)
			if o, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s %s: %v\n%s", id, c.name, err, o)
			}
			var r struct{ Reward float64 }
			rb, _ := os.ReadFile(filepath.Join(out, "reward.json"))
			json.Unmarshal(rb, &r)
			if r.Reward != c.want {
				t.Errorf("%s %s (%q): reward %v, want %v", id, c.name, c.answer, r.Reward, c.want)
			}
		}
	}
}
