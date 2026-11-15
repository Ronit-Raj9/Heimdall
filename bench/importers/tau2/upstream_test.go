package tau2

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitHead(t *testing.T) {
	sha := "c06b07dd7c4d4ec61ba6ff9bcdf0b15dd7ef36d4"
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{name: "detached", files: map[string]string{"HEAD": sha + "\n"}, want: sha},
		{name: "loose ref", files: map[string]string{"HEAD": "ref: refs/heads/main\n", "refs/heads/main": sha + "\n"}, want: sha},
		{name: "packed ref", files: map[string]string{"HEAD": "ref: refs/heads/main\n", "packed-refs": "# pack-refs with: peeled\n" + sha + " refs/heads/main\n"}, want: sha},
		{name: "missing ref", files: map[string]string{"HEAD": "ref: refs/heads/main\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range c.files {
				p := filepath.Join(root, ".git", filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := gitHead(root)
			if c.want == "" {
				if err == nil {
					t.Fatalf("got %q, want an error", got)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestParseMutatingTools(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "upstream", "src", "tau2", "domains", "airline", "tools.py"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseMutatingTools(string(b))
	want := map[string]bool{"cancel_booking": true, "add_note": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestOpenUpstream(t *testing.T) {
	u, err := openUpstream(fixtureDir(t), fixtureRef)
	if err != nil {
		t.Fatal(err)
	}
	if u.version != "9.9.9" || u.revision != fixtureRef {
		t.Fatalf("upstream = %+v", u)
	}
	if _, err := openUpstream(t.TempDir(), fixtureRef); err == nil {
		t.Fatal("an empty folder opened as a clone")
	}
	if _, err := openUpstream(fixtureDir(t), "main"); err == nil {
		t.Fatal("a branch name passed as a commit")
	}
}

func TestPublishedTasks(t *testing.T) {
	u, err := openUpstream(fixtureDir(t), fixtureRef)
	if err != nil {
		t.Fatal(err)
	}
	tasks, name, err := u.publishedTasks("retail")
	if err != nil {
		t.Fatal(err)
	}
	if name != "text-v1/retail_50" || len(tasks) != 3 || tasks[0].ID != "1" {
		t.Fatalf("got %s, %d tasks", name, len(tasks))
	}
	acts, err := tasks[0].Criteria.actions()
	if err != nil || len(acts) != 1 || acts[0].Name != "return_items" {
		t.Fatalf("actions = %+v, %v", acts, err)
	}
	airline, _, err := u.publishedTasks("airline")
	if err != nil || len(airline) != 6 {
		t.Fatalf("airline = %d tasks, %v", len(airline), err)
	}
	// A null action list stays nil; an empty one is not.
	if a, _ := (&upCriteria{Actions: []byte("null")}).actions(); a != nil {
		t.Fatalf("null actions = %v", a)
	}
}
