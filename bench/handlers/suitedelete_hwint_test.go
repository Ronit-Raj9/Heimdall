package handlers

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// hwintStampedSuites is the disk suite source with the list cache side, so
// GET /suites and DELETE /suites/{id} share the process-wide caches.
type hwintStampedSuites struct{ hw02Suites }

func (s hwintStampedSuites) SuiteStamp(id string) (string, string) {
	return filepath.Join(s.root, id), "stamp-1"
}

func (s hwintStampedSuites) SuiteHead(id string) (*schemas.Suite, error) {
	return s.LoadSuite(context.Background(), id)
}

// Fix wave 3 final integration (H02 + H17): DELETE /suites/{id} drops the
// deleted benchmark from the suite list caches at once, so nothing of it is
// served again and a pack created later under the same id starts fresh.
func TestDeleteSuiteEvictsSuiteCaches(t *testing.T) {
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	src := hwintStampedSuites{hw02Suites{diskSuites: diskSuites{root: filepath.Join(root, "benchmarks")}}}
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root}, Mode: testMode,
		Services: Services{Projects: ps, Index: &fakeIndex{manifests: map[string]*schemas.Manifest{}}, Suites: src},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
		Upload:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxUploadBytes)},
	}
	orgAreasMu.Lock()
	area := orgAreas["suiteauthoring"]
	orgAreasMu.Unlock()
	area(env, r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	c := &authoringClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, root: root}

	createTestSuite(t, c, "zz-evict")
	list := &SuitesHandler{suites: src}
	dir, stamp, _ := list.summaryKey("zz-evict")
	if _, ok := list.cachedSummary("zz-evict", 5*time.Second); !ok {
		t.Fatal("summary not cached before the delete")
	}
	if _, _, known := list.readyHead("zz-evict"); !known {
		t.Fatal("head not cached before the delete")
	}
	if code, body := c.do(t, "DELETE", "/suites/zz-evict", nil, nil); code != fasthttp.StatusNoContent {
		t.Fatalf("delete: %d %s", code, body)
	}
	if suiteSummaries.has(dir, stamp) {
		t.Error("the deleted benchmark's summary is still cached")
	}
	suiteHeadCache.mu.Lock()
	_, hit := suiteHeadCache.m[dir]
	suiteHeadCache.mu.Unlock()
	if hit {
		t.Error("the deleted benchmark's head entry is still cached")
	}
	if ids, _ := src.SuiteIDs(context.Background()); len(ids) != 0 {
		t.Errorf("list still has %v", ids)
	}
}
