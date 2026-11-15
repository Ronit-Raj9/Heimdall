package agentdojo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCommit = "089ed468cf3ed0322acc66b0211f26d9d90dbf60"

func TestOpenUpstream(t *testing.T) {
	up, err := openUpstream(filepath.Join("testdata", "upstream"), "banking")
	if err != nil {
		t.Fatal(err)
	}
	if up.version != "0.1.35" || up.license != "MIT" || up.revision != "" {
		t.Fatalf("upstream %+v", up)
	}
	tree, err := up.packageTree()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"pyproject.toml", "README.md", "LICENSE", "src/agentdojo/__init__.py", "src/agentdojo/data/suites/banking/injection_vectors.yaml"} {
		if _, ok := tree[f]; !ok {
			t.Errorf("package tree lacks %s", f)
		}
	}
}

// copyFixture copies the upstream fixture into a temporary folder a test can
// change.
func copyFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", "upstream")
	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestOpenUpstreamErrors(t *testing.T) {
	cases := []struct {
		name   string
		change func(root string) error
		want   string
	}{
		{"not a clone", func(root string) error { return os.Remove(filepath.Join(root, "pyproject.toml")) }, "not an AgentDojo clone"},
		{"another package", func(root string) error {
			return os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname = \"other\"\nversion = \"1\"\n"), 0o644)
		}, "not the agentdojo package"},
		{"no version", func(root string) error {
			return os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname = \"agentdojo\"\n"), 0o644)
		}, "names no version"},
		{"no readme", func(root string) error { return os.Remove(filepath.Join(root, "README.md")) }, "missing README.md"},
		{"other license", func(root string) error {
			return os.WriteFile(filepath.Join(root, "LICENSE"), []byte("Apache License\n"), 0o644)
		}, "not the MIT license"},
		{"symlink in sources", func(root string) error {
			return os.Symlink("/etc/passwd", filepath.Join(root, "src", "agentdojo", "leak.py"))
		}, "symlink"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := copyFixture(t)
			if err := c.change(root); err != nil {
				t.Fatal(err)
			}
			up, err := openUpstream(root, "banking")
			if err == nil {
				_, err = up.packageTree()
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestGitHead(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
		err   bool
	}{
		{"not a clone", nil, "", false},
		{"loose ref", map[string]string{"HEAD": "ref: refs/heads/main\n", "refs/heads/main": testCommit + "\n"}, testCommit, false},
		{"packed ref", map[string]string{"HEAD": "ref: refs/heads/main\n", "packed-refs": "# pack-refs with: peeled\n" + testCommit + " refs/heads/main\n"}, testCommit, false},
		{"detached", map[string]string{"HEAD": testCommit + "\n"}, testCommit, false},
		{"garbage", map[string]string{"HEAD": "nonsense\n"}, "", true},
		{"dangling ref", map[string]string{"HEAD": "ref: refs/heads/gone\n"}, "", true},
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
			if (err != nil) != c.err || got != c.want {
				t.Fatalf("got %q %v, want %q (error %v)", got, err, c.want, c.err)
			}
		})
	}
}
