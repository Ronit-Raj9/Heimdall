package harbor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadTaskHarbor(t *testing.T) {
	tk, err := readTask(filepath.Join("testdata", "tb", "hello-file"))
	if err != nil {
		t.Fatalf("readTask: %v", err)
	}
	want := upstreamTask{
		ID: "hello-file", Layout: LayoutHarbor, Name: "terminal-bench/hello-file", Description: "Write a greeting file.",
		Difficulty: "easy", Category: "file-operations", Tags: []string{"shell", "files", "basics"},
		AgentTimeoutS: 300, VerifierTimeoutS: 120, DockerImage: "example/hello-file:20251031", AllowInternet: true,
		EnvEnv: map[string]string{"GREETING_LANG": "en"}, TestsMount: "/tests", SolutionMount: "/solution",
	}
	got := *tk
	got.Dir, got.EnvDir, got.SolutionDir, got.TestsDir, got.Instruction = "", "", "", "", ""
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if !strings.HasPrefix(tk.Instruction, "Create the file /app/hello.txt") || filepath.Base(tk.TestsDir) != "tests" || filepath.Base(tk.SolutionDir) != "solution" {
		t.Errorf("instruction %q, tests %s, solution %s", tk.Instruction, tk.TestsDir, tk.SolutionDir)
	}
}

func TestReadTaskTaskMD(t *testing.T) {
	tk, err := readTask(filepath.Join("testdata", "skills", "tasks", "csv-sum"))
	if err != nil {
		t.Fatalf("readTask: %v", err)
	}
	if tk.Layout != LayoutTaskMD || tk.Difficulty != "medium" || tk.Category != "data-processing" || strings.Join(tk.Tags, ",") != "csv" ||
		tk.AgentTimeoutS != 600 || tk.VerifierTimeoutS != 300 || tk.Workdir != "/app" || !tk.AllowInternet ||
		tk.VerifierEnv["SUM_PORT"] != "8090" || tk.SolutionEnv["WORKSPACE"] != "/app" || tk.TestsMount != "/verifier" || tk.SolutionMount != "/oracle" {
		t.Errorf("task = %+v", tk)
	}
	if tk.Instruction != "Sum the `amount` column of /app/data.csv and write the total to /app/total.txt.\n" {
		t.Errorf("instruction = %q", tk.Instruction)
	}
}

func TestReadTaskProblems(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no task file", map[string]string{"README.md": "x"}, "neither task.toml nor task.md"},
		{"bad toml", map[string]string{"task.toml": "a = \n", "instruction.md": "x"}, "task.toml line 1"},
		{"no instruction", map[string]string{"task.toml": "[task]\n"}, "instruction.md"},
		{"open front matter", map[string]string{"task.md": "---\na: 1\n"}, "front matter is not closed"},
		{"no front matter", map[string]string{"task.md": "just text\n"}, "no front matter"},
		{"env not a string", map[string]string{"task.toml": "[verifier.env]\nX = [1]\n", "instruction.md": "x"}, "[verifier.env] X: not a string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			for rel, body := range c.files {
				write(rel, body)
			}
			if _, err := readTask(dir); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want error containing %q", err, c.want)
			}
		})
	}
}

func TestReadTaskCompose(t *testing.T) {
	tk, err := readTask(filepath.Join("testdata", "tb", "needs-compose"))
	if err != nil || !tk.Compose {
		t.Fatalf("compose task: %+v, %v", tk, err)
	}
	tk, err = readTask(filepath.Join("testdata", "tb", "pip-check"))
	if err != nil || tk.Compose {
		t.Fatalf("plain task read as compose: %+v, %v", tk, err)
	}
}

func TestDedupe(t *testing.T) {
	if got := dedupe([]string{"a", " ", "b", "a", " b "}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("got %q", got)
	}
}

func TestReadTaskSeparateVerifier(t *testing.T) {
	cases := map[string]bool{
		"[verifier]\ntimeout_sec = 60.0\n":                          false,
		"[verifier]\nenvironment_mode = \"shared\"\n":               false,
		"[verifier]\nenvironment_mode = \"separate\"\n":             true,
		"[verifier.environment]\ndocker_image = \"x\"\n":            true,
		"[[verifier.collect]]\ncommand = \"git format-patch -1\"\n": true,
	}
	for toml, want := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "task.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "instruction.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		tk, err := readTask(dir)
		if err != nil {
			t.Fatalf("%q: %v", toml, err)
		}
		if tk.SeparateVerifier != want {
			t.Errorf("%q: separate = %v, want %v", toml, tk.SeparateVerifier, want)
		}
		if _, reason := plan(tk, settings{image: "build"}); want && !strings.Contains(reason, "separate verifier container") {
			t.Errorf("%q: plan reason %q", toml, reason)
		}
	}
}
