package scaffold

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testRepo is a throwaway git repository built inside a test.
type testRepo struct {
	t   *testing.T
	dir string
}

// newTestRepo makes an empty repository, isolated from the user's git
// configuration so signing, hooks or templates never interfere.
func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	r := &testRepo{t: t, dir: filepath.Join(t.TempDir(), "src")}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *testRepo) write(rel, content string) {
	r.t.Helper()
	p := filepath.Join(r.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) remove(rel string) {
	r.t.Helper()
	if err := os.Remove(filepath.Join(r.dir, filepath.FromSlash(rel))); err != nil {
		r.t.Fatal(err)
	}
}

// commit stages everything and returns the new commit id.
func (r *testRepo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--no-verify", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func TestOpenRepoAndTree(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "top\n")
	r.write("services/api/main.py", "print('hi')\n")
	r.write("services/api/run.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(r.dir, "services/api/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	c1 := r.commit("first")
	ctx := context.Background()

	whole, err := openRepo(ctx, r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if whole.prefix != "" {
		t.Fatalf("prefix = %q", whole.prefix)
	}
	sub, err := openRepo(ctx, filepath.Join(r.dir, "services", "api"))
	if err != nil {
		t.Fatal(err)
	}
	if sub.prefix != "services/api/" {
		t.Fatalf("prefix = %q", sub.prefix)
	}
	entries, err := sub.tree(ctx, c1)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range entries {
		paths = append(paths, e.Path+":"+e.Mode)
	}
	if want := []string{"main.py:100644", "run.sh:100755"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("tree = %v, want %v", paths, want)
	}

	r.write("README.md", "changed\n")
	r.write("services/api/main.py", "print('bye')\n")
	c2 := r.commit("second")
	inside, outside, err := sub.changes(ctx, c1, c2)
	if err != nil {
		t.Fatal(err)
	}
	if len(inside) != 1 || inside[0] != (change{Status: "M", Path: "main.py"}) || !reflect.DeepEqual(outside, []string{"README.md"}) {
		t.Fatalf("inside %v outside %v", inside, outside)
	}
	msg, err := sub.message(ctx, c2)
	if err != nil || strings.TrimSpace(msg) != "second" {
		t.Fatalf("message = %q, %v", msg, err)
	}
	if _, err := openRepo(ctx, t.TempDir()); err == nil {
		t.Fatal("want error outside a work tree")
	}
	if _, err := whole.resolve(ctx, "--output=/tmp/x"); err == nil {
		t.Fatal("want error for an option-like revision")
	}
}

func TestCatBlobs(t *testing.T) {
	r := newTestRepo(t)
	r.write("a.txt", "alpha\n")
	r.write("b.bin", "be\x00ta")
	c := r.commit("blobs")
	ctx := context.Background()
	repo, err := openRepo(ctx, r.dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := repo.tree(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	err = repo.catBlobs(ctx, shasOf(entries), func(sha string, data []byte) error {
		got[sha] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		want := map[string]string{"a.txt": "alpha\n", "b.bin": "be\x00ta"}[e.Path]
		if got[e.SHA] != want {
			t.Errorf("%s = %q, want %q", e.Path, got[e.SHA], want)
		}
	}
}

func TestReadBatch(t *testing.T) {
	stream := "aaa blob 5\nhello\nbbb blob 0\n\n"
	var got []string
	err := readBatch(bufio.NewReader(strings.NewReader(stream)), []string{"aaa", "bbb"}, func(sha string, data []byte) error {
		got = append(got, sha+"="+string(data))
		return nil
	})
	if err != nil || !reflect.DeepEqual(got, []string{"aaa=hello", "bbb="}) {
		t.Fatalf("got %v, %v", got, err)
	}
	err = readBatch(bufio.NewReader(strings.NewReader("ccc missing\n")), []string{"ccc"}, func(string, []byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("want missing object error, got %v", err)
	}
}
