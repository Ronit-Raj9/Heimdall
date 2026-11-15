package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	stampGUID = "0f6c1a2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b"
	oldGUID   = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

func TestEnsureCanary(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		created bool
		wantErr bool
	}{
		{"absent appends", "id: demo\nversion: 1.0.0\n# keep me\ntasks: [t01]\n", true, false},
		{"empty value is set", "id: demo\ncanary:\ntasks: [t01]\n", true, false},
		{"existing kept", "id: demo\ncanary: " + oldGUID + "\ntasks: [t01]\n", false, false},
		{"invalid refused", "id: demo\ncanary: hello\n", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, SuiteFile)
			if err := os.WriteFile(path, []byte(c.yaml), 0o640); err != nil {
				t.Fatal(err)
			}
			guid, created, err := EnsureCanary(dir)
			if c.wantErr {
				if err == nil {
					t.Fatal("no error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if created != c.created || !utils.ValidCanary(guid) {
				t.Fatalf("EnsureCanary = %q %v, want created %v", guid, created, c.created)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := utils.SetSuiteYAMLCanary([]byte(c.yaml), guid)
			if string(got) != string(want) {
				t.Errorf("suite.yaml = %q, want %q", got, want)
			}
			if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o640 {
				t.Errorf("suite.yaml mode changed: %v %v", fi.Mode(), err)
			}
			again, created, err := EnsureCanary(dir)
			if err != nil || created || again != guid {
				t.Errorf("second EnsureCanary = %q %v %v, want %q kept", again, created, err, guid)
			}
		})
	}
}

func TestStampText(t *testing.T) {
	line := utils.CanaryLine("instruction.md", stampGUID)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Fix the bug.\n", "Fix the bug.\n\n" + line + "\n"},
		{"no final newline", "Fix the bug.", "Fix the bug.\n\n" + line + "\n"},
		{"empty", "", line + "\n"},
		{"already blank line", "Fix the bug.\n\n", "Fix the bug.\n\n" + line + "\n"},
		{"old canary replaced", "<!-- " + utils.CanaryText(oldGUID) + " -->\nFix the bug.\n", "Fix the bug.\n\n" + line + "\n"},
		{"crlf", "Fix the bug.\r\nThen test.\r\n", "Fix the bug.\r\nThen test.\r\n\r\n" + line + "\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := StampText("instruction.md", c.in, stampGUID)
			if got != c.want || changed != (c.in != c.want) {
				t.Fatalf("StampText = %q %v, want %q", got, changed, c.want)
			}
			again, changed := StampText("instruction.md", got, stampGUID)
			if changed || again != got {
				t.Errorf("second StampText changed the text: %q", again)
			}
			if strings.Count(got, stampGUID) != 1 || strings.Contains(got, oldGUID) {
				t.Errorf("want exactly one canary for %s: %q", stampGUID, got)
			}
		})
	}
	if got, _ := StampText("instruction.txt", "Answer.\n", stampGUID); !strings.HasSuffix(got, "\n"+utils.CanaryText(stampGUID)+"\n") {
		t.Errorf("plain text file stamp = %q", got)
	}
}

// TestStampCanary stamps a temp suite with a two-session task whose first
// session reuses the task instruction, and checks every instruction file is
// stamped once, a second run changes nothing, and a new GUID replaces the
// old line.
func TestStampCanary(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"tasks/t01/instruction.md":     "Fix the parser.\n",
		"tasks/t02/sessions/learn.md":  "Read the notes.\n",
		"tasks/t02/sessions/recall.md": "When is the launch?\n",
		"tasks/t02/notes.txt":          "not an instruction\n",
	}
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &schemas.Suite{ID: "demo", Root: root, Tasks: []*schemas.Task{
		{ID: "t01", Dir: filepath.Join(root, "tasks", "t01"), Instruction: "instruction.md"},
		{ID: "t02", Dir: filepath.Join(root, "tasks", "t02"), Instruction: "sessions/learn.md", Sessions: []schemas.SessionSpec{
			{ID: "learn", Instruction: "sessions/learn.md"},
			{ID: "recall", Instruction: "sessions/recall.md"},
		}},
	}}
	res, err := StampCanary(s, oldGUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("stamped %d files, want 3: %+v", len(res), res)
	}
	for _, r := range res {
		if !r.Changed {
			t.Errorf("%s not changed on the first run", r.Path)
		}
	}
	res, err = StampCanary(s, oldGUID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Changed {
			t.Errorf("%s changed on the second run", r.Path)
		}
	}
	if _, err := StampCanary(s, stampGUID); err != nil {
		t.Fatal(err)
	}
	for rel, orig := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(rel, "notes.txt") {
			if string(got) != orig {
				t.Errorf("%s was touched: %q", rel, got)
			}
			continue
		}
		want := orig + "\n" + utils.CanaryLine(rel, stampGUID) + "\n"
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := StampCanary(s, "nope"); err == nil {
		t.Error("StampCanary with a bad GUID: no error")
	}
	s.Tasks[0].Instruction = "../../outside.md"
	if _, err := StampCanary(s, stampGUID); err == nil {
		t.Error("StampCanary with an instruction outside the task folder: no error")
	}
}
