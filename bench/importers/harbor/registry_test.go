package harbor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testCommit = "6bedd7878dc5d6f3456b4d80b781eb3c2d84f262"

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	b := fmt.Sprintf(`[
	 {"name":"bfcl","version":"1.0","description":"function calling","tasks":[
	   {"name":"bfcl-simple-0","git_url":"https://github.com/laude-institute/harbor-datasets.git","git_commit_id":%[1]q,"path":"datasets/bfcl/bfcl-simple-0"},
	   {"name":"bfcl-simple-1","git_url":"https://github.com/laude-institute/harbor-datasets.git","git_commit_id":%[1]q,"path":"datasets/bfcl/bfcl-simple-1"}]},
	 {"name":"kumo","version":"1.0","tasks":[]},
	 {"name":"kumo","version":"hard","tasks":[]},
	 {"name":"aime","version":"1.0","tasks":[]},
	 {"name":"spider2-dbt","version":"1.0","tasks":[
	   {"name":"activity001","git_url":"https://huggingface.co/datasets/harborframework/harbor-datasets","git_commit_id":%[1]q,"path":"datasets/spider2-dbt/activity001"}]}
	]`, testCommit)
	r, err := ParseRegistry([]byte(b))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRegistryResolve(t *testing.T) {
	r := testRegistry(t)
	d, err := r.Resolve("bfcl@1.0")
	if err != nil || len(d.Tasks) != 2 || d.Ref() != "bfcl@1.0" {
		t.Fatalf("bfcl@1.0: %v %+v", err, d)
	}
	if d, err := r.Resolve("bfcl"); err != nil || d.Version != "1.0" {
		t.Fatalf("bare name with one version: %v", err)
	}
	if _, err := r.Resolve("kumo"); err == nil || !strings.Contains(err.Error(), "1.0, hard") {
		t.Fatalf("ambiguous name: %v", err)
	}
	if _, err := r.Resolve("kumo@2"); err == nil || !strings.Contains(err.Error(), "no version") {
		t.Fatalf("missing version: %v", err)
	}
	if _, err := r.Resolve("nope@1"); err == nil {
		t.Fatal("unknown name resolved")
	}
	if d.Kind() != KindAgent || d.License() != "Apache-2.0" {
		t.Fatalf("bfcl kind/licence: %s %s", d.Kind(), d.License())
	}
	sorted := r.Sorted()
	if last := sorted[len(sorted)-1]; last.Kind() != KindModel {
		t.Fatalf("model tests should sort last, got %s", last.Ref())
	}
	if aime, _ := r.Resolve("aime@1.0"); aime.Kind() != KindModel || aime.License() != "NOASSERTION" {
		t.Fatalf("aime: %s %s", aime.Kind(), aime.License())
	}
}

func TestLooksLikeRef(t *testing.T) {
	for s, want := range map[string]bool{
		"bfcl@1.0": true, "terminal-bench@2.0": true, "swe-lancer-diamond@ic": true,
		"./bfcl@1.0": false, "/data/x@1": false, "bfcl": false, "a@b/c": false, "@1.0": false,
	} {
		if got := LooksLikeRef(s); got != want {
			t.Errorf("LooksLikeRef(%q) = %v", s, got)
		}
	}
}

func TestCheckTaskRefusesUnsafeEntries(t *testing.T) {
	ok := RegistryTask{Name: "a", GitURL: "https://github.com/x/y.git", Commit: testCommit, Path: "datasets/a"}
	if err := checkTask(ok); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]RegistryTask{
		"short commit": {Name: "a", GitURL: ok.GitURL, Commit: "abc123", Path: "datasets/a"},
		"dot dot":      {Name: "a", GitURL: ok.GitURL, Commit: testCommit, Path: "../etc"},
		"absolute":     {Name: "a", GitURL: ok.GitURL, Commit: testCommit, Path: "/etc"},
		"http":         {Name: "a", GitURL: "http://github.com/x/y.git", Commit: testCommit, Path: "datasets/a"},
		"option":       {Name: "a", GitURL: "--upload-pack=x", Commit: testCommit, Path: "datasets/a"},
	} {
		if err := checkTask(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// fakeGit makes the checkout step create the sparse folders with a task.
func fakeGit(calls *[][]string) func(ctx context.Context, dir string, args ...string) error {
	var patterns []string
	return func(_ context.Context, dir string, args ...string) error {
		*calls = append(*calls, args)
		switch args[0] {
		case "sparse-checkout":
			patterns = args[4:]
		case "checkout":
			for _, p := range patterns {
				d := filepath.Join(dir, filepath.FromSlash(p))
				if err := os.MkdirAll(d, 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(d, "task.toml"), []byte("version = \"1.0\"\n"), 0o644); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func TestFetchGitWritesTasksAndManifest(t *testing.T) {
	r := testRegistry(t)
	d, _ := r.Resolve("bfcl@1.0")
	dest := t.TempDir()
	var calls [][]string
	m, err := Fetch(context.Background(), d, dest, FetchOptions{Git: fakeGit(&calls), Registry: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Tasks != 2 || m.Benchmark != "bfcl@1.0" || m.Revision != testCommit || m.License != "Apache-2.0" ||
		m.Source != "https://github.com/laude-institute/harbor-datasets" {
		t.Fatalf("manifest %+v", m)
	}
	for _, n := range []string{"bfcl-simple-0", "bfcl-simple-1"} {
		if !isFile(filepath.Join(dest, "tasks", n, "task.toml")) {
			t.Fatalf("missing task %s", n)
		}
	}
	var fetch []string
	for _, c := range calls {
		if c[0] == "fetch" {
			fetch = c
		}
	}
	if strings.Join(fetch, " ") != "fetch -q --depth 1 --filter=blob:none origin "+testCommit {
		t.Fatalf("fetch args %v", fetch)
	}
	if got := readManifest(dest); got == nil || got.Benchmark != "bfcl@1.0" {
		t.Fatalf("readManifest %+v", got)
	}
	up := describeUpstream(dest, settings{})
	if up.Benchmark != "bfcl@1.0" || up.Revision != testCommit || up.License != "Apache-2.0" {
		t.Fatalf("describeUpstream %+v", up)
	}
	// A second fetch keeps the folders and runs no git.
	calls = nil
	if _, err := Fetch(context.Background(), d, dest, FetchOptions{Git: fakeGit(&calls)}); err != nil || len(calls) != 0 {
		t.Fatalf("refetch: %v, %d git calls", err, len(calls))
	}
	// ids select and an unknown id is an error.
	if _, err := Fetch(context.Background(), d, t.TempDir(), FetchOptions{Git: fakeGit(&calls), IDs: []string{"nope"}}); err == nil {
		t.Fatal("unknown id accepted")
	}
}

func TestFetchHuggingFace(t *testing.T) {
	root := "datasets/spider2-dbt/activity001"
	files := map[string]string{
		root + "/task.toml":              "version = \"1.0\"\n",
		root + "/instruction.md":         "Fix the dbt models.\n",
		root + "/environment/Dockerfile": "FROM python:3.11-slim\n",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/datasets/harborframework/harbor-datasets/tree/"+testCommit+"/"+root):
			var out []hfEntry
			out = append(out, hfEntry{Type: "directory", Path: root + "/environment"})
			for p := range files {
				out = append(out, hfEntry{Type: "file", Path: p})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(r.URL.Path, "/datasets/harborframework/harbor-datasets/resolve/"+testCommit+"/"):
			p := strings.TrimPrefix(r.URL.Path, "/datasets/harborframework/harbor-datasets/resolve/"+testCommit+"/")
			body, ok := files[p]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := hfBase
	hfBase = srv.URL
	defer func() { hfBase = old }()
	d, _ := testRegistry(t).Resolve("spider2-dbt@1.0")
	dest := t.TempDir()
	m, err := Fetch(context.Background(), d, dest, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.License != "MIT" || m.Tasks != 1 {
		t.Fatalf("manifest %+v", m)
	}
	b, err := os.ReadFile(filepath.Join(dest, "tasks", "activity001", "environment", "Dockerfile"))
	if err != nil || string(b) != files[root+"/environment/Dockerfile"] {
		t.Fatalf("Dockerfile %q %v", b, err)
	}
	if detectLayout(filepath.Join(dest, "tasks", "activity001")) != LayoutHarbor {
		t.Fatal("fetched folder is not a Harbor task")
	}
}

func TestNextLink(t *testing.T) {
	if got := nextLink(`<https://huggingface.co/api/x?cursor=abc>; rel="next"`); got != "https://huggingface.co/api/x?cursor=abc" {
		t.Fatalf("got %q", got)
	}
	if nextLink("") != "" {
		t.Fatal("empty header")
	}
}

func TestDownloadRetriesRateLimit(t *testing.T) {
	old := retryBase
	retryBase = time.Millisecond
	defer func() { retryBase = old }()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "f")
	if err := download(context.Background(), nil, srv.URL, "", out); err != nil || n != 3 {
		t.Fatalf("err %v after %d calls", err, n)
	}
	if b, _ := os.ReadFile(out); string(b) != "ok" {
		t.Fatalf("body %q", b)
	}
	if w := retryWait(&statusError{code: 429, retryAfter: 7 * time.Second}, 0); w != 7*time.Second {
		t.Fatalf("Retry-After ignored: %v", w)
	}
	if retryable(&statusError{code: 404}) {
		t.Fatal("404 retried")
	}
}
