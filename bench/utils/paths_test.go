package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// pathFixture builds root/task with a repo, a file outside the task, and a
// set of links, and returns the task folder and the outside folder.
func pathFixture(t *testing.T) (task, outside string) {
	t.Helper()
	base := t.TempDir()
	task = filepath.Join(base, "task")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(task, "repo", "pkg"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(task, "instruction.md"):       "do it\n",
		filepath.Join(task, "repo", "pkg", "a.txt"): "a\n",
		filepath.Join(outside, "secret.txt"):        "secret\n",
	}
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		filepath.Join(task, "etc"):                  "/etc",
		filepath.Join(task, "notes.md"):             "../outside/secret.txt",
		filepath.Join(task, "repo", "up"):           "../../outside",
		filepath.Join(task, "repo", "same.txt"):     "pkg/a.txt",
		filepath.Join(task, "repo", "pkg", "self"):  "..",
		filepath.Join(task, "repo", "pkg", "top"):   "../..",
		filepath.Join(task, "repo", "pkg", "climb"): "top/..",
	}
	for p, target := range links {
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	return task, outside
}

func TestResolveInside(t *testing.T) {
	task, _ := pathFixture(t)
	real, err := filepath.EvalSymlinks(task)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, rel string
		want      string // relative to the real task folder; empty when an error is wanted
		wantErr   string
	}{
		{name: "file", rel: "instruction.md", want: "instruction.md"},
		{name: "folder", rel: "repo", want: "repo"},
		{name: "link inside", rel: "repo/same.txt", want: "repo/pkg/a.txt"},
		{name: "link to parent inside", rel: "repo/pkg/self/pkg", want: "repo/pkg"},
		{name: "absolute", rel: "/etc/passwd", wantErr: "want a path relative to the folder"},
		{name: "lexical escape", rel: "../outside/secret.txt", wantErr: "path leaves the folder"},
		{name: "empty", rel: "", wantErr: "empty path"},
		{name: "missing", rel: "nope.md", wantErr: "nope.md not found"},
		{name: "folder link to /etc", rel: "etc", wantErr: "resolves outside the folder"},
		{name: "file link outside", rel: "notes.md", wantErr: "resolves outside the folder"},
		{name: "through an escaping folder link", rel: "repo/up/secret.txt", wantErr: "resolves outside the folder"},
		{name: "dot-dot after a link climbs from its target", rel: "repo/pkg/climb", wantErr: "resolves outside the folder"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveInside(task, tt.rel)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ResolveInside(%q) = %q, %v; want error %q", tt.rel, got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(real, filepath.FromSlash(tt.want)); got != want {
				t.Fatalf("ResolveInside(%q) = %q, want %q", tt.rel, got, want)
			}
		})
	}
}

func TestReadFileInside(t *testing.T) {
	task, _ := pathFixture(t)
	if b, err := ReadFileInside(task, "repo/same.txt"); err != nil || string(b) != "a\n" {
		t.Fatalf("link inside: %q, %v", b, err)
	}
	for _, rel := range []string{"notes.md", "repo/up/secret.txt", "etc/passwd", "../outside/secret.txt"} {
		if b, err := ReadFileInside(task, rel); err == nil {
			t.Errorf("ReadFileInside(%q) read %q, want an error", rel, b)
		}
	}
}

func TestFindSymlinks(t *testing.T) {
	task, _ := pathFixture(t)
	if err := os.Symlink("missing/file", filepath.Join(task, "repo", "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../nowhere", filepath.Join(task, "repo", "dangling-out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop", filepath.Join(task, "repo", "loop")); err != nil {
		t.Fatal(err)
	}
	got, err := FindSymlinks(task, filepath.Join(task, "repo"))
	if err != nil {
		t.Fatal(err)
	}
	escapes := map[string]bool{}
	for _, l := range got {
		escapes[l.Path] = l.Escapes
	}
	want := map[string]bool{
		"up": true, "same.txt": false, "pkg/self": false, "pkg/top": false, "pkg/climb": true,
		"dangling": false, "dangling-out": true, "loop": true,
	}
	if !reflect.DeepEqual(escapes, want) {
		t.Fatalf("FindSymlinks = %v, want %v", escapes, want)
	}
	all, err := FindSymlinks(task, task)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range all {
		if (l.Path == "etc" || l.Path == "notes.md") && !l.Escapes {
			t.Errorf("%s -> %s should escape", l.Path, l.Target)
		}
	}
	if none, err := FindSymlinks(task, filepath.Join(task, "absent")); err != nil || none != nil {
		t.Fatalf("missing folder: %v, %v", none, err)
	}
}

// ResolveInsideFrom gives the same answer as ResolveInside for every path of
// the fixture, from the already resolved task folder.
func TestResolveInsideFromMatchesResolveInside(t *testing.T) {
	task, _ := pathFixture(t)
	real, err := filepath.EvalSymlinks(task)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"instruction.md", "repo", "repo/pkg", "repo/pkg/a.txt", "./repo/pkg/a.txt", "repo/./pkg", "repo/pkg/../pkg/a.txt",
		"repo/same.txt", "repo/pkg/self/pkg", "/etc/passwd", "../outside/secret.txt", "repo/../../outside", "", "nope.md",
		"repo/nope/a.txt", "instruction.md/x", "etc", "notes.md", "repo/up/secret.txt", "repo/pkg/climb", "repo/pkg/top",
	} {
		want, wantErr := ResolveInside(real, rel)
		got, gotErr := ResolveInsideFrom(real, rel)
		if (wantErr == nil) != (gotErr == nil) {
			t.Errorf("%q: ResolveInsideFrom = %q, %v; ResolveInside = %q, %v", rel, got, gotErr, want, wantErr)
			continue
		}
		if wantErr != nil {
			if gotErr.Error() != wantErr.Error() {
				t.Errorf("%q: error %q, want %q", rel, gotErr, wantErr)
			}
			continue
		}
		if got != want {
			t.Errorf("%q: %q, want %q", rel, got, want)
		}
	}
}

// A symlink anywhere below the resolved root that leaves it is refused, as
// by ResolveInside: a folder link, a file link and a link reached through a
// plain folder.
func TestResolveInsideFromRefusesEscapingSymlink(t *testing.T) {
	task, outside := pathFixture(t)
	real, err := filepath.EvalSymlinks(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(task, "deep", "er"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(task, "deep", "er", "leak.txt")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"etc", "etc/passwd", "notes.md", "repo/up/secret.txt", "deep/er/leak.txt", "repo/pkg/climb"} {
		if got, err := ResolveInsideFrom(real, rel); err == nil || !strings.Contains(err.Error(), "resolves outside the folder") {
			t.Errorf("ResolveInsideFrom(%q) = %q, %v; want a refusal", rel, got, err)
		}
	}
}
