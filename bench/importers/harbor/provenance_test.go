package harbor

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeClone writes a .git folder with the given files under a new folder.
func fakeClone(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const rev = "2fd12b88aafdd04a52c298e3940bcb189f9766d6"

func TestDescribeUpstream(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		from  string // relative to the clone
		want  upstream
	}{
		{"branch ref and token in url", map[string]string{
			".git/HEAD":            "ref: refs/heads/main\n",
			".git/refs/heads/main": rev + "\n",
			".git/config":          "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = https://user:secret@github.com/laude-institute/terminal-bench-2\n",
			"README.md":            "# Terminal-Bench 2.0 \n",
			"LICENSE":              "Apache License\nVersion 2.0, January 2004\n",
		}, ".", upstream{Benchmark: "terminal-bench@2.0", Source: "https://github.com/laude-institute/terminal-bench-2", Revision: rev, License: "Apache-2.0"}},
		{"packed ref, registry match, tasks folder", map[string]string{
			".git/HEAD":        "ref: refs/heads/v1\n",
			".git/packed-refs": "# pack-refs with: peeled\n" + rev + " refs/heads/v1\n",
			"registry.json":    `[{"name":"skillsbench","version":"1.0","tasks":[{"git_commit_id":"2fd12b88"}]}]`,
			"LICENSE":          "MIT License\n",
			"tasks/a/task.md":  "---\n---\n",
		}, "tasks", upstream{Benchmark: "skillsbench@1.0", Revision: rev, License: "MIT"}},
		{"annotated tag at head", map[string]string{
			".git/HEAD":        rev + "\n",
			".git/config":      "[remote \"origin\"]\n\turl = https://github.com/benchflow-ai/skillsbench\n",
			".git/packed-refs": "# pack-refs with: peeled fully-peeled sorted\nb6be2886a0a903885afbbb0cbfb8328356af801e refs/tags/v1.0\n^" + rev + "\n",
		}, ".", upstream{Benchmark: "skillsbench@1.0", Source: "https://github.com/benchflow-ai/skillsbench", Revision: rev, License: "NOASSERTION"}},
		{"detached head, registry without a match", map[string]string{
			".git/HEAD":     rev + "\n",
			"registry.json": `[{"name":"skillsbench","version":"1.1","tasks":[{"git_commit_id":"ffff"}]}]`,
		}, ".", upstream{Benchmark: "skillsbench@2fd12b88aafd", Revision: rev, License: "NOASSERTION"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clone := fakeClone(t, c.files)
			from := filepath.Join(clone, c.from)
			got := describeUpstream(from, settings{})
			want := c.want
			if want.Source == "" {
				want.Source = from
			}
			if got != want {
				t.Errorf("got  %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestDescribeUpstreamNoGit(t *testing.T) {
	dir := t.TempDir()
	got := describeUpstream(dir, settings{benchmark: "x@1", license: "MIT"})
	if got.Source != dir || got.Revision != "" || got.Benchmark != "x@1" || got.License != "MIT" {
		t.Errorf("got %+v", got)
	}
}

func TestStripCredentials(t *testing.T) {
	cases := map[string]string{
		"https://tok@github.com/a/b":    "https://github.com/a/b",
		"https://github.com/a/b.git":    "https://github.com/a/b.git",
		"git@github.com:a/b.git":        "git@github.com:a/b.git",
		"ssh://git@example.com/a/b.git": "ssh://example.com/a/b.git",
	}
	for in, want := range cases {
		if got := stripCredentials(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}
