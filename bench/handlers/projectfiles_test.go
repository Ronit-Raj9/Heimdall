package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func newProjectFilesClient(t *testing.T, store projectfs.Store) *testClient {
	t.Helper()
	r := router.New()
	env := OrgEnv{
		Mode:     testMode,
		Services: Services{Projects: store},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
	}
	orgAreasMu.Lock()
	area := orgAreas["projectfiles"]
	orgAreasMu.Unlock()
	area(env, r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: 4 * lib.MaxBodyBytes}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestProjectFilesRoundTrip(t *testing.T) {
	store, err := projectfs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := newProjectFilesClient(t, store)
	base := APIPrefix + "/project/files/uploads/notes/a.txt"

	// Writes need the write token.
	if resp := c.do(t, "PUT", base, []byte("one"), nil); resp.StatusCode() != fasthttp.StatusUnauthorized {
		t.Fatalf("PUT without token = %d", resp.StatusCode())
	}
	resp := c.do(t, "PUT", base, []byte("one"), authed("text/plain"))
	if resp.StatusCode() != 200 {
		t.Fatalf("PUT = %d %s", resp.StatusCode(), resp.Body())
	}
	etag := strings.Trim(string(resp.Header.Peek("ETag")), `"`)
	// Stale If-Match: 409.
	h := authed("text/plain")
	h["If-Match"] = `"0000"`
	if resp := c.do(t, "PUT", base, []byte("two"), h); resp.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("stale If-Match = %d %s", resp.StatusCode(), resp.Body())
	}
	h["If-Match"] = `"` + etag + `"`
	if resp := c.do(t, "PUT", base, []byte("two"), h); resp.StatusCode() != 200 {
		t.Fatalf("PUT with If-Match = %d %s", resp.StatusCode(), resp.Body())
	}
	// Read back.
	var f ProjectFile
	resp = c.do(t, "GET", base, nil, nil)
	if err := json.Unmarshal(resp.Body(), &f); err != nil || f.Content != "two" || f.Encoding != "utf-8" {
		t.Fatalf("GET = %s (%v)", resp.Body(), err)
	}
	// Two versions, newest first, old content readable.
	var vs ProjectFileVersions
	resp = c.do(t, "GET", APIPrefix+"/project/versions/uploads/notes/a.txt", nil, nil)
	if err := json.Unmarshal(resp.Body(), &vs); err != nil || len(vs.Versions) != 2 {
		t.Fatalf("versions = %s (%v)", resp.Body(), err)
	}
	resp = c.do(t, "GET", APIPrefix+"/project/versions/uploads/notes/a.txt?id="+vs.Versions[1].ID, nil, nil)
	if err := json.Unmarshal(resp.Body(), &f); err != nil || f.Content != "one" {
		t.Fatalf("old version = %s", resp.Body())
	}
	// Folder listing.
	var l ProjectFileList
	resp = c.do(t, "GET", APIPrefix+"/project/files?area=uploads&recursive=true", nil, nil)
	if err := json.Unmarshal(resp.Body(), &l); err != nil || len(l.Entries) != 2 {
		t.Fatalf("list = %s", resp.Body())
	}
	// Export holds the file under its area.
	resp = c.do(t, "GET", APIPrefix+"/project/export.zip", nil, nil)
	zr, err := zip.NewReader(bytes.NewReader(resp.Body()), int64(len(resp.Body())))
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != "uploads/notes/a.txt" {
		t.Fatalf("export = %v %v", zr, err)
	}
	// Delete.
	if resp := c.do(t, "DELETE", base, nil, authed("")); resp.StatusCode() != 200 {
		t.Fatalf("DELETE = %d", resp.StatusCode())
	}
	if resp := c.do(t, "GET", base, nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("GET after delete = %d", resp.StatusCode())
	}
}

func TestProjectFilesRefusals(t *testing.T) {
	store, _ := projectfs.Open(t.TempDir())
	c := newProjectFilesClient(t, store)
	cases := []struct {
		method, path string
		body         []byte
		want         int
	}{
		{"GET", "/project/files/secrets/x", nil, 400},
		{"GET", "/project/files?area=nope", nil, 400},
		{"PUT", "/project/files/agents/..%2F..%2Fevil.yaml", []byte("x"), 0}, // router may 404 first; any refusal
		{"PUT", "/project/files/agents/.git/config", []byte("x"), 400},
		{"PUT", "/project/files/agents/a/.heimdall/x", []byte("x"), 400},
		{"PUT", "/project/files/agents/bad.yaml", []byte("id: x\n"), 422},
		{"PUT", "/project/files/uploads/big.bin", make([]byte, projectfs.MaxFileBytes+1), 413},
		{"GET", "/project/files/uploads/missing.txt", nil, 404},
	}
	for _, tc := range cases {
		resp := c.do(t, tc.method, APIPrefix+tc.path, tc.body, authed("application/octet-stream"))
		if tc.want == 0 && resp.StatusCode() >= 400 && resp.StatusCode() < 500 {
			continue
		}
		if resp.StatusCode() != tc.want {
			t.Errorf("%s %s = %d %s, want %d", tc.method, tc.path, resp.StatusCode(), resp.Body(), tc.want)
		}
	}
	// No store: 503 with a next step.
	c = newProjectFilesClient(t, nil)
	if resp := c.do(t, "GET", APIPrefix+"/project/files", nil, nil); resp.StatusCode() != 503 {
		t.Errorf("nil store = %d", resp.StatusCode())
	}
}
