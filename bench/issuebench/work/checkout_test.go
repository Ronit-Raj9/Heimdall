package work

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const checkoutToken = "ghp_FAKEtokenForCheckoutTests0123456789"

type gitCall struct {
	env  []string
	args []string
}

// served is a Git seam over ExecGit that serves owner/name from a local bare
// repository by rewriting the GitHub URL to file://. It records every call
// as the code made it (before the rewrite).
type served struct {
	mu    sync.Mutex
	repo  string
	bare  string
	calls []gitCall
}

func (s *served) git(ctx context.Context, env []string, args ...string) ([]byte, error) {
	s.mu.Lock()
	s.calls = append(s.calls, gitCall{env: append([]string(nil), env...), args: append([]string(nil), args...)})
	s.mu.Unlock()
	real := make([]string, len(args))
	for i, a := range args {
		if a == "https://github.com/"+s.repo+".git" {
			a = "file://" + s.bare
		}
		real[i] = a
	}
	return ExecGit(ctx, env, real...)
}

func mustGit(t *testing.T, args ...string) string {
	t.Helper()
	out, err := ExecGit(context.Background(), nil, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newServed makes a bare repository with two commits and returns the seam
// plus both commit ids (first, tip).
func newServed(t *testing.T) (*served, string, string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	bare := filepath.Join(root, "bare.git")
	mustGit(t, "init", "-q", src)
	id := []string{"-C", src, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, "-C", src, "add", "a.txt")
	mustGit(t, append(id, "commit", "-q", "-m", "one")...)
	first := mustGit(t, "-C", src, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, append(id, "commit", "-q", "-am", "two")...)
	tip := mustGit(t, "-C", src, "rev-parse", "HEAD")
	mustGit(t, "clone", "-q", "--bare", src, bare)
	mustGit(t, "-C", bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	return &served{repo: "acme/widget", bare: bare}, first, tip
}

func TestCheckoutAtSHA(t *testing.T) {
	s, first, tip := newServed(t)
	for _, sha := range []string{first, tip} {
		dir := filepath.Join(t.TempDir(), "repo")
		if err := (Checkouter{Git: s.git}).Checkout(context.Background(), s.repo, sha, dir, ""); err != nil {
			t.Fatalf("checkout %s: %v", sha, err)
		}
		if got := mustGit(t, "-C", dir, "rev-parse", "HEAD"); got != sha {
			t.Fatalf("HEAD %s, want %s", got, sha)
		}
		b, err := os.ReadFile(filepath.Join(dir, "a.txt"))
		if err != nil {
			t.Fatal(err)
		}
		want := "two\n"
		if sha == first {
			want = "one\n"
		}
		if string(b) != want {
			t.Fatalf("a.txt at %s = %q, want %q", sha, b, want)
		}
		if out, _ := ExecGit(context.Background(), nil, "-C", dir, "remote"); strings.TrimSpace(string(out)) != "" {
			t.Fatalf("a remote is configured: %q", out)
		}
	}
	// A public fetch passes no token env.
	for _, c := range s.calls {
		if len(c.env) != 0 {
			t.Fatalf("call %v got env %v without a token", c.args, c.env)
		}
	}
}

func TestCheckoutTokenOnlyInEnv(t *testing.T) {
	s, first, _ := newServed(t)
	dir := filepath.Join(t.TempDir(), "repo")
	if err := (Checkouter{Git: s.git}).Checkout(context.Background(), s.repo, first, dir, checkoutToken); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	header := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + checkoutToken))
	sawEnv := false
	for _, c := range s.calls {
		for _, a := range c.args {
			if strings.Contains(a, checkoutToken) || strings.Contains(a, header) {
				t.Fatalf("token in argv: %v", c.args)
			}
		}
		for _, e := range c.env {
			if strings.Contains(e, header) {
				sawEnv = true
				if !contains(c.args, "fetch") {
					t.Fatalf("token env on a non-fetch call: %v", c.args)
				}
			}
		}
	}
	if !sawEnv {
		t.Fatal("fetch did not get the token env")
	}
	for _, f := range []string{".git/config", ".git/FETCH_HEAD"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if strings.Contains(string(b), checkoutToken) || strings.Contains(string(b), header) || strings.Contains(string(b), "extraheader") {
			t.Fatalf("%s holds the token:\n%s", f, b)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestCheckoutRefusesBadInput(t *testing.T) {
	called := false
	git := func(context.Context, []string, ...string) ([]byte, error) { called = true; return nil, nil }
	c := Checkouter{Git: git}
	good := strings.Repeat("a", 40)
	cases := []struct{ repo, sha, dir string }{
		{"acme/widget", "abc123", "d"},
		{"acme/widget", strings.Repeat("A", 40), "d"},
		{"acme/widget", "main", "d"},
		{"acme/widget", "--upload-pack=x" + strings.Repeat("a", 25), "d"},
		{"acme", good, "d"},
		{"acme/../x", good, "d"},
		{"-x/y", good + "", ""},
	}
	for _, tc := range cases {
		if err := c.Checkout(context.Background(), tc.repo, tc.sha, tc.dir, ""); err == nil {
			t.Fatalf("Checkout(%q, %q) accepted", tc.repo, tc.sha)
		}
	}
	if called {
		t.Fatal("git ran for a refused request")
	}
}

func TestCheckoutMissingCommit(t *testing.T) {
	s, _, _ := newServed(t)
	missing := strings.Repeat("0", 39) + "1"
	dir := filepath.Join(t.TempDir(), "repo")
	err := (Checkouter{Git: s.git}).Checkout(context.Background(), s.repo, missing, dir, checkoutToken)
	if err == nil {
		t.Fatal("missing commit checked out")
	}
	msg := err.Error()
	if !strings.Contains(msg, "could not fetch commit "+missing+" of acme/widget") {
		t.Fatalf("not a plain error: %s", msg)
	}
	header := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + checkoutToken))
	if strings.Contains(msg, checkoutToken) || strings.Contains(msg, header) {
		t.Fatalf("error holds the token: %s", msg)
	}
}

func TestCheckoutErrorRedactsToken(t *testing.T) {
	header := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + checkoutToken))
	git := func(_ context.Context, _ []string, args ...string) ([]byte, error) {
		if contains(args, "fetch") {
			return []byte("fatal: bad header Authorization: Basic " + header + " token " + checkoutToken), errors.New("exit status 128")
		}
		return nil, nil
	}
	err := (Checkouter{Git: git}).Checkout(context.Background(), "acme/widget", strings.Repeat("b", 40), t.TempDir(), checkoutToken)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), checkoutToken) || strings.Contains(err.Error(), header) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("token not redacted: %s", err)
	}
}

func TestCheckoutWrongHead(t *testing.T) {
	git := func(_ context.Context, _ []string, args ...string) ([]byte, error) {
		if contains(args, "rev-parse") {
			return []byte(strings.Repeat("c", 40) + "\n"), nil
		}
		return nil, nil
	}
	err := (Checkouter{Git: git}).Checkout(context.Background(), "acme/widget", strings.Repeat("b", 40), t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "wanted "+strings.Repeat("b", 40)) {
		t.Fatalf("want a head mismatch error, got %v", err)
	}
}

func TestTokenEnv(t *testing.T) {
	if TokenEnv("") != nil {
		t.Fatal("empty token gives env")
	}
	env := TokenEnv(checkoutToken)
	want := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+checkoutToken)),
	}
	if strings.Join(env, "\n") != strings.Join(want, "\n") {
		t.Fatalf("TokenEnv = %v", env)
	}
}

func TestExecGitCleanEnv(t *testing.T) {
	t.Setenv("GIT_DIR", "/nonexistent-ibtest")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	out, err := ExecGit(context.Background(), []string{"IBTEST_EXTRA=yes"}, "-c", "alias.e=!env", "e")
	if err != nil {
		t.Fatalf("ExecGit: %v\n%s", err, out)
	}
	env := "\n" + string(out)
	for _, bad := range []string{"\nGIT_DIR=/nonexistent-ibtest", "\nGIT_TERMINAL_PROMPT=1"} {
		if strings.Contains(env, bad) {
			t.Fatalf("inherited %q", bad)
		}
	}
	for _, good := range []string{"\nGIT_TERMINAL_PROMPT=0", "\nGIT_ASKPASS=/bin/false", "\nGIT_CONFIG_NOSYSTEM=1", "\nIBTEST_EXTRA=yes"} {
		if !strings.Contains(env, good) {
			t.Fatalf("missing %q in\n%s", good, out)
		}
	}
	if strings.Contains(env, "\nHOME="+os.Getenv("HOME")+"\n") {
		t.Fatal("HOME not replaced")
	}
}

func TestRepoPublic(t *testing.T) {
	s, _, _ := newServed(t)
	if !RepoPublic(context.Background(), s.git, s.repo) {
		t.Fatal("served repository not public")
	}
	for _, c := range s.calls {
		if len(c.env) != 0 {
			t.Fatalf("RepoPublic passed env %v", c.env)
		}
		if !contains(c.args, "ls-remote") || !contains(c.args, "--exit-code") {
			t.Fatalf("args %v", c.args)
		}
	}
	gone := &served{repo: "acme/gone", bare: filepath.Join(t.TempDir(), "missing.git")}
	if RepoPublic(context.Background(), gone.git, gone.repo) {
		t.Fatal("missing repository public")
	}
	fail := func(context.Context, []string, ...string) ([]byte, error) { return nil, errors.New("boom") }
	if RepoPublic(context.Background(), fail, "acme/widget") {
		t.Fatal("failing git public")
	}
	ok := func(context.Context, []string, ...string) ([]byte, error) { return nil, nil }
	if RepoPublic(context.Background(), ok, "not a repo") {
		t.Fatal("bad name public")
	}
}
