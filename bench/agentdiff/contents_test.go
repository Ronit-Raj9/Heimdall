package agentdiff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootContents(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "v1.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "big.md"), []byte(strings.Repeat("x", MaxFileBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	read := RootContents(root)
	cases := []struct {
		rel  string
		want string
		ok   bool
	}{
		{"notes/v1.md", "hello\n", true},
		{"notes/../notes/v1.md", "hello\n", true},
		{"../outside.md", "", false},
		{"/etc/hostname", "", false},
		{"missing.md", "", false},
		{"big.md", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		t.Run(c.rel, func(t *testing.T) {
			got, ok := read(c.rel)
			if ok != c.ok || string(got) != c.want {
				t.Fatalf("got %q %v", got, ok)
			}
		})
	}
	if RootContents("") != nil {
		t.Fatal("empty root reads nothing")
	}
}
