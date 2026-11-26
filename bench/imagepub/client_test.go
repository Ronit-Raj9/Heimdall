package imagepub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEnsureCoalescesAndSkipsPinned(t *testing.T) {
	var posts atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Query().Get("ref") != "bench/a:1" {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		<-release
		_, _ = io.WriteString(w, `{"ref":"bench/a:1","digest":"sha256:`+hex64+`"}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	tok := filepath.Join(dir, "publish.token")
	writeFile(t, tok, "tok\n")
	m := filepath.Join(dir, "manifest")
	writeFile(t, m, "bench/p:1 sha256:"+hex64+"\n")
	c := &Client{
		URL:       srv.URL,
		TokenFile: tok,
		Docker:    fakeDocker(t, `[]`),
		Manifest:  m,
		Set:       func() (map[string]string, error) { return map[string]string{"bench/a:1": "a", "bench/p:1": "p"}, nil },
	}
	if d, err := c.Ensure(context.Background(), "bench/p:1"); err != nil || d != "sha256:"+hex64 {
		t.Fatalf("pinned: %q %v", d, err)
	}
	if posts.Load() != 0 {
		t.Fatal("a pinned ref was posted")
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.Ensure(context.Background(), "bench/a:1")
		}(i)
	}
	// Both calls are in flight before the server answers.
	for c.inflight("bench/a:1") < 2 {
		runtime.Gosched()
	}
	close(release)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := posts.Load(); n != 1 {
		t.Fatalf("posts = %d, want 1", n)
	}
}

func TestEnsureCarriesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"not a platform image"}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	tok := filepath.Join(dir, "publish.token")
	writeFile(t, tok, "tok")
	c := &Client{URL: srv.URL, TokenFile: tok, Docker: fakeDocker(t, `[]`), Manifest: filepath.Join(dir, "none"),
		Set: func() (map[string]string, error) { return map[string]string{"bench/a:1": "a"}, nil }}
	_, err := c.Ensure(context.Background(), "bench/a:1")
	if err == nil || err.Error() != "image publisher: not a platform image" {
		t.Fatalf("err = %v", err)
	}
}
