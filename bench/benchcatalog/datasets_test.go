package benchcatalog

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/projectfs"
)

func TestFetchRefusesPrivateHosts(t *testing.T) {
	f := NewFetcher()
	for _, u := range []string{
		"http://example.com/data.jsonl",
		"https://localhost/data.jsonl",
		"https://127.0.0.1/data.jsonl",
		"https://10.0.0.5/x",
		"https://192.168.1.1/x",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/x",
		"https://metadata.google.internal/x",
		"https://user:pw@example.com/x",
		"https://example.com:8443/x",
		"file:///etc/passwd",
		"",
	} {
		if _, _, err := f.Fetch(context.Background(), u); !errors.Is(err, ErrFetchRefused) {
			t.Errorf("%q: err = %v, want refused", u, err)
		}
	}
	if _, err := f.CheckURL("https://huggingface.co/datasets/x/resolve/main/a.json"); err != nil {
		t.Fatalf("public https refused: %v", err)
	}
}

func TestFetchDialGuard(t *testing.T) {
	// Names are checked again at dial time: an address that resolves inside is refused.
	f := NewFetcher()
	c := f.httpClient()
	for _, addr := range []string{"127.0.0.1:443", "10.1.2.3:443", "[::1]:443", "169.254.169.254:443"} {
		if _, err := c.Get("https://" + addr + "/"); err == nil || !errors.Is(err, ErrFetchRefused) {
			t.Errorf("dial %s: %v, want refused", addr, err)
		}
	}
}

func TestFetchSizeCapAndSave(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big.jsonl":
			w.Header().Set("Content-Length", "2048")
			w.Write(big)
		case "/chunked.jsonl":
			w.(http.Flusher).Flush() // no Content-Length: the read limit must stop it
			w.Write(big)
		case "/rows.jsonl":
			w.Write([]byte("{\"id\":1}\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := &Fetcher{MaxBytes: 1024, allowPrivate: true}
	for _, p := range []string{"/big.jsonl", "/chunked.jsonl"} {
		if _, _, err := f.Fetch(context.Background(), srv.URL+p); !errors.Is(err, projectfs.ErrTooLarge) {
			t.Fatalf("%s: err = %v, want too large", p, err)
		}
	}
	if _, _, err := f.Fetch(context.Background(), srv.URL+"/missing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing: %v", err)
	}
	project := t.TempDir()
	store, err := projectfs.Open(project)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := FetchDataset(context.Background(), f, store, "import-x", srv.URL+"/rows.jsonl", "u")
	if err != nil || rel != "uploads/datasets/import-x/rows.jsonl" {
		t.Fatalf("fetch dataset: %q %v", rel, err)
	}
	if b, _ := os.ReadFile(filepath.Join(project, rel)); string(b) != "{\"id\":1}\n" {
		t.Fatalf("saved = %q", b)
	}
}

func TestSaveDatasetZipAndLargeFile(t *testing.T) {
	project := t.TempDir()
	store, _ := projectfs.Open(project)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{"repo-main/a/task.toml", "repo-main/b/task.toml"} {
		w, _ := zw.Create(n)
		w.Write([]byte("x = 1\n"))
	}
	zw.Close()
	rel, err := SaveDataset(context.Background(), store, "terminal-bench-2", "main.zip", buf.Bytes(), "u")
	if err != nil || rel != "uploads/datasets/terminal-bench-2/repo-main" {
		t.Fatalf("zip: %q %v", rel, err)
	}
	if _, err := os.Stat(filepath.Join(project, rel, "a", "task.toml")); err != nil {
		t.Fatal(err)
	}
	// A file above the 1 MiB area cap is fine in the uploads area and replaces the old data.
	large := bytes.Repeat([]byte("{}\n"), 1<<20)
	rel, err = SaveDataset(context.Background(), store, "terminal-bench-2", "../../rows.jsonl", large, "u")
	if err != nil || rel != "uploads/datasets/terminal-bench-2/rows.jsonl" {
		t.Fatalf("large: %q %v", rel, err)
	}
	if _, err := os.Stat(filepath.Join(project, "uploads/datasets/terminal-bench-2/repo-main")); !os.IsNotExist(err) {
		t.Fatal("old dataset not replaced")
	}
	if _, err := SaveDataset(context.Background(), store, "x", "a.jsonl", make([]byte, projectfs.MaxUploadFileBytes+1), "u"); !errors.Is(err, projectfs.ErrTooLarge) {
		t.Fatalf("over cap: %v", err)
	}
}

func TestNormalizeURL(t *testing.T) {
	src, project := fixture(t)
	p := ImportParams{Entry: "livecodebench", URL: "https://127.0.0.1/x.jsonl"}
	if _, _, err := src.Normalize(project, &p, true); err == nil || !errors.Is(err, ErrFetchRefused) {
		// the importer may be missing from this test binary; then the error names it
		if err == nil || !strings.Contains(err.Error(), "importer") {
			t.Fatalf("private url: %v", err)
		}
	}
}
