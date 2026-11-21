package handlers

import (
	"bytes"
	"net"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func TestProjectBlobsLargeFile(t *testing.T) {
	store, err := projectfs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	env := OrgEnv{
		Mode:     testMode,
		Services: Services{Projects: store},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
		Upload:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxUploadBytes)},
	}
	orgAreasMu.Lock()
	area := orgAreas["projectblobs"]
	orgAreasMu.Unlock()
	area(env, r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: lib.MaxUploadBytes}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	c := &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}

	p := ProjectBlobsPath + "/experiments/runs/big.bin"
	big := bytes.Repeat([]byte{7, 0, 255}, 1<<20) // 3 MiB
	if resp := c.do(t, "PUT", p, big, nil); resp.StatusCode() != fasthttp.StatusUnauthorized {
		t.Fatalf("PUT without token = %d", resp.StatusCode())
	}
	resp := c.do(t, "PUT", p, big, authed("application/octet-stream"))
	if resp.StatusCode() != 200 {
		t.Fatalf("PUT = %d %s", resp.StatusCode(), resp.Body())
	}
	etag := strings.Trim(string(resp.Header.Peek("ETag")), `"`)
	h := authed("application/octet-stream")
	h["If-Match"] = `"0000"`
	if resp := c.do(t, "PUT", p, big[:2<<20], h); resp.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("stale If-Match = %d", resp.StatusCode())
	}
	h["If-Match"] = `"` + etag + `"`
	if resp := c.do(t, "PUT", p, big[:2<<20], h); resp.StatusCode() != 200 {
		t.Fatalf("matching If-Match = %d %s", resp.StatusCode(), resp.Body())
	}
	get := c.do(t, "GET", p, nil, nil)
	if get.StatusCode() != 200 || !bytes.Equal(get.Body(), big[:2<<20]) {
		t.Fatalf("GET = %d, %d bytes", get.StatusCode(), len(get.Body()))
	}
	// Per-area cap.
	old := projectfs.LargeFileCaps[projectfs.AreaPolicies]
	projectfs.LargeFileCaps[projectfs.AreaPolicies] = 2 << 20
	t.Cleanup(func() { projectfs.LargeFileCaps[projectfs.AreaPolicies] = old })
	if resp := c.do(t, "PUT", ProjectBlobsPath+"/policies/big.bin", big, authed("application/octet-stream")); resp.StatusCode() != fasthttp.StatusRequestEntityTooLarge {
		t.Fatalf("over area cap = %d", resp.StatusCode())
	}
	if resp := c.do(t, "PUT", ProjectBlobsPath+"/uploads/../x", []byte("a"), authed("application/octet-stream")); resp.StatusCode()/100 != 4 {
		t.Fatalf("escape = %d", resp.StatusCode())
	}
}
