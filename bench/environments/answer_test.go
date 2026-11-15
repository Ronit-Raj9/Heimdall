package environments

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestAnswerRel(t *testing.T) {
	task := func(p string, env *schemas.TaskEnvironment) *schemas.Task {
		return &schemas.Task{Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer, AnswerPath: p}, Environment: env}
	}
	suite := &schemas.Suite{Workdir: "/workspace", Output: &schemas.OutputSpec{AnswerPath: "out/suite.txt"}}
	app := &schemas.TaskEnvironment{Image: "x", WorkspacePath: "/app"}
	tests := []struct {
		name    string
		suite   *schemas.Suite
		task    *schemas.Task
		want    string
		wantErr bool
	}{
		{"default", &schemas.Suite{}, &schemas.Task{}, "answer.txt", false},
		{"suite path", suite, &schemas.Task{}, "out/suite.txt", false},
		{"task wins", suite, task("final.md", nil), "final.md", false},
		{"absolute in workspace", suite, task("/workspace/a.txt", nil), "a.txt", false},
		{"task image workspace", suite, task("answer.txt", app), "answer.txt", false},
		{"absolute in task image workspace", suite, task("/app/sub/a.txt", app), "sub/a.txt", false},
		{"outside", suite, task("/etc/passwd", nil), "", true},
		{"climbs out", suite, task("../x", nil), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := answerRel(&schemas.RunContext{Suite: tt.suite, Task: tt.task})
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %q %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestRemoveEntrypoint(t *testing.T) {
	in := []string{"run", "--entrypoint", "/bin/sh", "-e", "A=1", "--entrypoint", "python3", "img"}
	if got := removeEntrypoint(in); !reflect.DeepEqual(got, []string{"run", "-e", "A=1", "--entrypoint", "python3", "img"}) {
		t.Fatalf("got %v", got)
	}
}

// fakeAnswerDocker is a docker stand-in whose `run` prints stdout and exits
// with code.
func fakeAnswerDocker(t *testing.T, stdout string, code int) utils.Docker {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.WriteFile(out, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"$*\" > " + filepath.Join(dir, "args") + "\ncat " + out + "\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}
}

func TestExportAnswer(t *testing.T) {
	rc := func() *schemas.RunContext {
		return &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Suite: &schemas.Suite{}, Task: &schemas.Task{},
			Handle: &schemas.EnvironmentHandle{Volume: "bench-ws-r_1"}, Dir: t.TempDir()}
	}
	t.Run("file", func(t *testing.T) {
		r := rc()
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: fakeAnswerDocker(t, "42\n", 0)})
		a, err := e.ExportAnswer(t.Context(), r)
		if err != nil || a.Source != "file" || a.Text != "42\n" || a.SHA256 != utils.SHA256Bytes([]byte("42\n")) || a.Truncated {
			t.Fatalf("answer %+v %v", a, err)
		}
		if got := string(readOut(t, filepath.Join(r.Dir, AnswerFile))); got != "42\n" {
			t.Fatalf("answer.txt %q", got)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: fakeAnswerDocker(t, strings.Repeat("x", maxAnswerBytes+1), 0)})
		a, err := e.ExportAnswer(t.Context(), rc())
		if err != nil || !a.Truncated || len(a.Text) != maxAnswerBytes {
			t.Fatalf("answer len %d truncated %v %v", len(a.Text), a.Truncated, err)
		}
	})
	for name, code := range map[string]int{"missing": answerMissing, "escapes": answerEscapes} {
		t.Run(name, func(t *testing.T) {
			r := rc()
			e := newDockerWorkspace(DockerWorkspaceConfig{Docker: fakeAnswerDocker(t, "", code)})
			a, err := e.ExportAnswer(t.Context(), r)
			if err != nil || a.Source != "missing" || a.Text != "" {
				t.Fatalf("answer %+v %v", a, err)
			}
			if _, err := os.Stat(filepath.Join(r.Dir, AnswerFile)); !os.IsNotExist(err) {
				t.Fatalf("answer.txt written for a missing answer: %v", err)
			}
		})
	}
	t.Run("helper failure", func(t *testing.T) {
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: fakeAnswerDocker(t, "", 7)})
		if _, err := e.ExportAnswer(t.Context(), rc()); err == nil {
			t.Fatal("helper failure not an error")
		}
	})
}
