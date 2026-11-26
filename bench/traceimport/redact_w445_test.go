package traceimport

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedactHomePath checks that home folder names are scrubbed from text,
// JSON and a coding session's workspace, keep the rest of the path, and that
// redacting twice changes nothing more.
func TestRedactHomePath(t *testing.T) {
	r := NewRedactor()
	in := "opened /home/alice/proj/main.go and /Users/bob.smith/notes.txt"
	out := r.Text(in)
	want := "opened /home/[REDACTED path]/proj/main.go and /Users/[REDACTED path]/notes.txt"
	if out != want {
		t.Fatalf("Text = %q, want %q", out, want)
	}
	if r.Report()[RedactPath] != 2 {
		t.Errorf("report = %v, want 2 path", r.Report())
	}
	if again := r.Text(out); again != out || r.Report()[RedactPath] != 2 {
		t.Errorf("second pass changed %q to %q (report %v)", out, again, r.Report())
	}

	js := string(r.JSON(json.RawMessage(`{"file_path":"/home/alice/x.go"}`)))
	if strings.Contains(js, "alice") {
		t.Errorf("JSON kept the home folder name: %s", js)
	}

	s := &Session{
		ID:        "s1",
		Workspace: &Workspace{Cwd: "/home/alice/work/repo", Branch: "main"},
		Turns:     []Turn{{Role: "user", Text: "look at /home/alice/work/repo/a.go"}},
	}
	NewRedactor().Session(s)
	if s.Workspace.Cwd != "/home/[REDACTED path]/work/repo" {
		t.Errorf("Workspace.Cwd = %q", s.Workspace.Cwd)
	}
	if strings.Contains(s.Turns[0].Text, "alice") {
		t.Errorf("turn kept the home folder name: %q", s.Turns[0].Text)
	}
	for _, safe := range []string{"/homework/a", "/usr/home/x", "/tmp/alice/x"} {
		if got := NewRedactor().Text(safe); got != safe {
			t.Errorf("Text(%q) = %q, want it unchanged", safe, got)
		}
	}
}
