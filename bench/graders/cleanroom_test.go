package graders

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestReadTarFileTooLarge(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "reward.json", Mode: 0o644, Size: 10, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		limit int64
		want  string
	}{
		{name: "over the limit is typed", limit: 9, want: "10 bytes, over the 9 byte limit"},
		{name: "at the limit reads", limit: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := readTarFile(bytes.NewReader(buf.Bytes()), tt.limit)
			if tt.want == "" {
				if err != nil || string(data) != "0123456789" {
					t.Fatalf("got %q, %v", data, err)
				}
				return
			}
			var tl *tooLargeError
			if !errors.As(err, &tl) || err.Error() != tt.want || data != nil {
				t.Fatalf("got %q, %v", data, err)
			}
		})
	}
}

func TestGatherInputsImages(t *testing.T) {
	taskDir, err := filepath.Abs("testdata/task")
	if err != nil {
		t.Fatal(err)
	}
	rc := &schemas.RunContext{
		Task:  &schemas.Task{Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}},
		Suite: &schemas.Suite{BaseImage: schemas.ImageSpec{Tag: "base:1"}, Verifier: schemas.SuiteVerifier{Command: []string{"pytest"}, TimeoutS: 7}},
	}
	diff := &schemas.Diff{Path: filepath.Join("testdata", "patches", "pass.diff")}
	tests := []struct {
		name, image, want string
	}{
		{name: "base image for both", want: "base:1"},
		{name: "override for both", image: "helper:1", want: "helper:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, err := gatherInputs(rc, diff, tt.image)
			if err != nil {
				t.Fatalf("gatherInputs: %v", err)
			}
			if in.image != tt.want || in.prepImage != tt.want || in.workdir != "" || in.timeout != 7*time.Second {
				t.Fatalf("inputs = %+v", in)
			}
		})
	}
}

func TestNewGradeJob(t *testing.T) {
	tests := []struct {
		name, exp, run string
	}{
		{name: "names carry the run id", exp: "e_1", run: "r_abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := &gradeInputs{}
			a, b := newGradeJob(tt.exp, tt.run, in), newGradeJob(tt.exp, tt.run, in)
			if a.expID != tt.exp || a.runID != tt.run || a.in != in {
				t.Fatalf("job = %+v", a)
			}
			for prefix, name := range map[string]string{"bench-grade-" + tt.run + "-": a.workspace, "bench-gout-" + tt.run + "-": a.out, "bench-" + tt.run + "-verify-": a.container} {
				if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
					t.Errorf("name %q does not start with %q plus a suffix", name, prefix)
				}
			}
			if a.workspace == b.workspace || a.out == b.out || a.container == b.container {
				t.Errorf("two jobs share names: %+v %+v", a, b)
			}
		})
	}
}

func TestTimedOutReason(t *testing.T) {
	tests := []struct {
		timeout time.Duration
		want    string
	}{
		{3 * time.Second, "test run broken by the patch: verifier timed out after 3s"},
		{5 * time.Minute, "test run broken by the patch: verifier timed out after 5m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := timedOutReason(tt.timeout); got != tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestApplyScenario(t *testing.T) {
	ref := "sha256:" + strings.Repeat("ab", 32)
	mkRun := func(t *testing.T, files map[string]string) string {
		dir := t.TempDir()
		for rel, c := range files {
			p := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	t.Run("plain task is unchanged", func(t *testing.T) {
		in := &gradeInputs{image: "base:1", workdir: "/workspace"}
		rc := &schemas.RunContext{Suite: &schemas.Suite{ID: "s"}, Task: &schemas.Task{ID: "t"}, Dir: t.TempDir()}
		if err := applyScenario(rc, in); err != nil || !reflect.DeepEqual(in, &gradeInputs{image: "base:1", workdir: "/workspace"}) {
			t.Fatalf("in %+v %v", in, err)
		}
	})
	t.Run("task image verifies, verifier image wins, workspace path seeds", func(t *testing.T) {
		in := &gradeInputs{image: "base:1"}
		task := &schemas.Task{ID: "t", Environment: &schemas.TaskEnvironment{Image: "tb/x:1", WorkspacePath: "/app"}}
		if err := applyScenario(&schemas.RunContext{Suite: &schemas.Suite{ID: "s"}, Task: task}, in); err != nil {
			t.Fatal(err)
		}
		if in.image != "tb/x:1" || in.seedImage != "tb/x:1" || in.seedPath != "/app" || in.workdir != "/app" {
			t.Fatalf("in %+v", in)
		}
		task.Environment.VerifierImage = "tb/x-verify:1"
		if err := applyScenario(&schemas.RunContext{Suite: &schemas.Suite{ID: "s"}, Task: task}, in); err != nil || in.image != "tb/x-verify:1" {
			t.Fatalf("verifier image: %+v %v", in, err)
		}
		task.Environment.WorkspacePath = "/grader/x"
		if err := applyScenario(&schemas.RunContext{Suite: &schemas.Suite{ID: "s"}, Task: task}, &gradeInputs{}); err == nil {
			t.Fatal("workspace path under /grader accepted")
		}
	})
	t.Run("snapshot reads the ref and mounts state and answer", func(t *testing.T) {
		dir := mkRun(t, map[string]string{"snapshot.ref": ref + "\n", "state/kv/dump": "{}", "answer.txt": "42"})
		rc := &schemas.RunContext{Suite: &schemas.Suite{ID: "s", Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot}},
			Task: &schemas.Task{ID: "t"}, Handle: &schemas.EnvironmentHandle{Volume: "bench-ws-r_1"}, Dir: dir}
		in := &gradeInputs{}
		if err := applyScenario(rc, in); err != nil {
			t.Fatal(err)
		}
		if in.snapshot != ref || in.workspace != "bench-ws-r_1" || in.stateDir != filepath.Join(dir, "state") || in.answerFile != filepath.Join(dir, "answer.txt") {
			t.Fatalf("in %+v", in)
		}
		want := []string{"-v", filepath.Join(dir, "state") + ":/grader/state:ro", "-v", filepath.Join(dir, "answer.txt") + ":/grader/answer.txt:ro"}
		if got := scenarioMounts(in); !reflect.DeepEqual(got, want) {
			t.Fatalf("mounts %v", got)
		}
		j := &gradeJob{in: in, workspace: "w", out: "o"}
		if !reflect.DeepEqual(j.volumes(), []string{"w", "o"}) {
			t.Fatalf("snapshot volumes %v", j.volumes())
		}
		if in.root {
			t.Fatal("default task verifies as root")
		}
	})
	t.Run("artifacts folder is mounted read-only in both modes", func(t *testing.T) {
		for _, mode := range []string{schemas.VerifierCleanRoom, schemas.VerifierSnapshot} {
			dir := mkRun(t, map[string]string{"snapshot.ref": ref + "\n", "artifacts/report/out.csv": "a,b\n"})
			rc := &schemas.RunContext{Suite: &schemas.Suite{ID: "s", Verifier: schemas.SuiteVerifier{Mode: mode}},
				Task: &schemas.Task{ID: "t"}, Handle: &schemas.EnvironmentHandle{Volume: "bench-ws-r_1"}, Dir: dir}
			in := &gradeInputs{}
			if err := applyScenario(rc, in); err != nil {
				t.Fatal(err)
			}
			if in.artifactsDir != filepath.Join(dir, "artifacts") || in.stateDir != "" || in.answerFile != "" {
				t.Fatalf("%s: in %+v", mode, in)
			}
			want := []string{"-v", filepath.Join(dir, "artifacts") + ":/grader/artifacts:ro"}
			if got := scenarioMounts(in); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: mounts %v", mode, got)
			}
		}
		// A file named artifacts is not the exported folder.
		dir := mkRun(t, map[string]string{"artifacts": "x"})
		in := &gradeInputs{}
		if err := applyScenario(&schemas.RunContext{Suite: &schemas.Suite{ID: "s"}, Task: &schemas.Task{ID: "t"}, Dir: dir}, in); err != nil || in.artifactsDir != "" {
			t.Fatalf("artifacts file mounted: %+v %v", in, err)
		}
	})
	for name, files := range map[string]map[string]string{
		"missing ref": {},
		"bad ref":     {"snapshot.ref": "bench/snap-r_1:latest\n"},
	} {
		t.Run(name, func(t *testing.T) {
			rc := &schemas.RunContext{Suite: &schemas.Suite{ID: "s", Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot}},
				Task: &schemas.Task{ID: "t"}, Handle: &schemas.EnvironmentHandle{Volume: "v"}, Dir: mkRun(t, files)}
			if err := applyScenario(rc, &gradeInputs{}); err == nil || !strings.Contains(err.Error(), "snapshot grading") {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestGradeJobVolumes(t *testing.T) {
	plain := newGradeJob("e", "r_1", &gradeInputs{})
	if v := plain.volumes(); len(v) != 2 || plain.pristine != "" {
		t.Fatalf("plain volumes %v", v)
	}
	seeded := newGradeJob("e", "r_1", &gradeInputs{seedImage: "tb/x:1", seedPath: "/app"})
	if v := seeded.volumes(); len(v) != 3 || !strings.HasPrefix(seeded.pristine, "bench-gsrc-r_1-") {
		t.Fatalf("seeded volumes %v", v)
	}
}

// TestGatherInputsSkipsRepo checks that a task seeded from its image, or
// graded from a snapshot, needs no repo folder.
func TestGatherInputsSkipsRepo(t *testing.T) {
	task := t.TempDir()
	if err := os.MkdirAll(filepath.Join(task, "verifier", "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	diff := filepath.Join(t.TempDir(), "workspace.diff")
	if err := os.WriteFile(diff, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &schemas.Suite{ID: "s", BaseImage: schemas.ImageSpec{Tag: "b:1"}, Verifier: schemas.SuiteVerifier{Command: []string{"sh"}}}
	tk := &schemas.Task{ID: "t", Dir: task, Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}}
	if _, err := gatherInputs(&schemas.RunContext{Suite: suite, Task: tk}, &schemas.Diff{Path: diff}, ""); err == nil {
		t.Fatal("plain task without a repo folder accepted")
	}
	tk.Environment = &schemas.TaskEnvironment{Image: "tb/x:1", WorkspacePath: "/app"}
	if in, err := gatherInputs(&schemas.RunContext{Suite: suite, Task: tk}, &schemas.Diff{Path: diff}, ""); err != nil || in.repoDir != "" {
		t.Fatalf("image-seeded task: %+v %v", in, err)
	}
}

func TestVerifierIdentity(t *testing.T) {
	plain := strings.Join(verifierIdentity(false), " ")
	if plain != "--user 1000:1000 --cap-drop ALL --tmpfs /home/agent:exec,size=64m,mode=0700,uid=1000,gid=1000 -e HOME=/home/agent" {
		t.Fatalf("default: %s", plain)
	}
	root := strings.Join(verifierIdentity(true), " ")
	if !strings.HasPrefix(root, "--user 0:0 --cap-drop ALL --cap-add AUDIT_WRITE") || !strings.HasSuffix(root, "-e HOME=/root") || strings.Contains(root, "SYS_ADMIN") {
		t.Fatalf("root: %s", root)
	}
	in := &gradeInputs{}
	task := &schemas.Task{ID: "t", Environment: &schemas.TaskEnvironment{Image: "tb/x:1", User: "root"}}
	if err := applyScenario(&schemas.RunContext{Suite: &schemas.Suite{ID: "s"}, Task: task}, in); err != nil || !in.root {
		t.Fatalf("root task: %+v %v", in, err)
	}
}
