package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// syncServer serves the real GET /project/manifest (L26) plus minimal
// stand-ins for the L01 file routes and export zip and the L24 project upload.
func syncServer(t *testing.T, root, token string) string {
	t.Helper()
	// The Phase 0 store stub refuses a missing area folder; L01 fixes that.
	for _, a := range handlers.ProjectAreas {
		_ = os.MkdirAll(filepath.Join(root, string(a)), 0o755)
	}
	st, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	mode := schemas.ModeRecorded
	write := lib.RequireWriteToken(mode, token)
	env := handlers.OrgEnv{Cfg: &lib.Config{Root: root}, Mode: mode, Services: handlers.Services{Projects: st}, Write: write}
	r := router.New()
	for _, a := range handlers.OrgAreas() {
		a(env, r)
	}
	opts := func(ctx *fasthttp.RequestCtx) projectfs.WriteOpts {
		return projectfs.WriteOpts{IfMatch: string(ctx.Request.Header.Peek("If-Match"))}
	}
	// fasthttp/router matches the raw path, so the catch-all is still escaped.
	rel := func(ctx *fasthttp.RequestCtx) string {
		p, _ := url.PathUnescape(ctx.UserValue("path").(string))
		return p
	}
	status := func(ctx *fasthttp.RequestCtx, err error) {
		switch {
		case errors.Is(err, projectfs.ErrConflict):
			ctx.SetStatusCode(409)
		case errors.Is(err, projectfs.ErrNotFound):
			ctx.SetStatusCode(404)
		case err != nil:
			ctx.SetStatusCode(400)
			ctx.SetBodyString(err.Error())
		}
	}
	r.PUT(handlers.APIPrefix+"/project/files/{area}/{path:*}", write(func(ctx *fasthttp.RequestCtx) {
		_, err := st.Write(ctx, projectfs.Area(ctx.UserValue("area").(string)), rel(ctx), ctx.PostBody(), opts(ctx))
		status(ctx, err)
	}))
	r.DELETE(handlers.APIPrefix+"/project/files/{area}/{path:*}", write(func(ctx *fasthttp.RequestCtx) {
		status(ctx, st.Delete(ctx, projectfs.Area(ctx.UserValue("area").(string)), rel(ctx), opts(ctx)))
	}))
	r.POST(handlers.APIPrefix+"/uploads/project", write(func(ctx *fasthttp.RequestCtx) {
		fh, err := ctx.FormFile("file")
		if err != nil {
			status(ctx, err)
			return
		}
		f, _ := fh.Open()
		defer f.Close()
		zr, err := zip.NewReader(f, fh.Size)
		if err != nil {
			status(ctx, err)
			return
		}
		area := string(ctx.QueryArgs().Peek("area"))
		for _, zf := range zr.File {
			full, err := st.Resolve(projectfs.Area(area), zf.Name)
			if err != nil {
				status(ctx, err)
				return
			}
			rc, _ := zf.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			_ = os.WriteFile(full, data, 0o644)
		}
	}))
	r.GET(handlers.APIPrefix+"/project/export.zip", func(ctx *fasthttp.RequestCtx) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		m, _ := handlers.BuildProjectManifest(context.Background(), st, handlers.ProjectAreas)
		for _, f := range m.Files {
			full, _ := st.Resolve(projectfs.Area(f.Area), f.Path)
			data, _ := os.ReadFile(full)
			w, _ := zw.Create(f.Area + "/" + f.Path)
			_, _ = w.Write(data)
		}
		_ = zw.Close()
		ctx.SetContentType("application/zip")
		ctx.SetBody(buf.Bytes())
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: lib.MaxUploadBytes}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Shutdown() })
	return "http://" + ln.Addr().String()
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPushPullRoundTrip(t *testing.T) {
	srvRoot, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
	const token = "tok-test"
	base := syncServer(t, srvRoot, token)
	big := strings.Repeat("x", projectfs.MaxFileBytes+10)
	writeTree(t, src, map[string]string{
		"agents/a.yaml":                      "name: a\n",
		"benchmarks/core/tasks/t1/task.yaml": "id: t1\n",
		"benchmarks/core/data.bin":           big,
		"policies/p.yaml":                    "rules: []\n",
		"uploads/my file.txt":                "hello",
		"notes/ignored.txt":                  "not an area",
	})
	ctx := context.Background()
	var out bytes.Buffer
	o := &syncOpts{server: base, token: token, dir: src}
	st, err := pushProject(ctx, o, &out)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out.String())
	}
	if st.put != 4 || st.zipped != 1 {
		t.Fatalf("push stats %+v\n%s", st, out.String())
	}
	// Second push: nothing changed.
	if st, err = pushProject(ctx, o, io.Discard); err != nil || st.unchanged != 5 || st.put != 0 {
		t.Fatalf("second push %+v %v", st, err)
	}
	if _, err := pullProject(ctx, &syncOpts{server: base, dir: dst}, &out); err != nil {
		t.Fatalf("pull: %v\n%s", err, out.String())
	}
	a, _ := localManifest(src)
	b, _ := localManifest(dst)
	if len(a) != 5 || len(a) != len(b) {
		t.Fatalf("trees differ: %d vs %d", len(a), len(b))
	}
	for k, f := range a {
		if b[k].SHA256 != f.SHA256 {
			t.Errorf("%s differs after round trip", k)
		}
	}
	// Delete propagates with -delete, both ways.
	os.Remove(filepath.Join(src, "policies", "p.yaml"))
	if st, err = pushProject(ctx, &syncOpts{server: base, token: token, dir: src, del: true}, io.Discard); err != nil || st.deleted != 1 {
		t.Fatalf("push -delete %+v %v", st, err)
	}
	if st, err = pullProject(ctx, &syncOpts{server: base, dir: dst, del: true}, io.Discard); err != nil || st.deleted != 1 {
		t.Fatalf("pull -delete %+v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "policies", "p.yaml")); !os.IsNotExist(err) {
		t.Fatal("pull -delete kept a file the server no longer has")
	}
}

func TestPushConflictAndAuth(t *testing.T) {
	srvRoot, src := t.TempDir(), t.TempDir()
	base := syncServer(t, srvRoot, "tok")
	writeTree(t, srvRoot, map[string]string{"agents/a.yaml": "server edit\n"})
	writeTree(t, src, map[string]string{"agents/a.yaml": "local edit\n", "agents/new.yaml": "new\n"})
	ctx := context.Background()
	// Bad token: clear 401 error.
	if _, err := pushProject(ctx, &syncOpts{server: base, token: "nope", dir: src}, io.Discard); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad token err = %v", err)
	}
	st, err := pushProject(ctx, &syncOpts{server: base, token: "tok", dir: src}, io.Discard)
	if err != nil || st.conflicts != 1 || st.put != 1 {
		t.Fatalf("conflict push %+v %v", st, err)
	}
	if got, _ := os.ReadFile(filepath.Join(srvRoot, "agents", "a.yaml")); string(got) != "server edit\n" {
		t.Fatal("conflicting push overwrote the server file")
	}
	if st, err = pushProject(ctx, &syncOpts{server: base, token: "tok", dir: src, force: true}, io.Discard); err != nil || st.put != 1 || st.unchanged != 1 {
		t.Fatalf("force push %+v %v", st, err)
	}
}

func TestExportKeyRejectsUnsafe(t *testing.T) {
	for name, want := range map[string]string{
		"agents/a.yaml":           "agents/a.yaml",
		"project/agents/a.yaml":   "agents/a.yaml",
		"../agents/a.yaml":        "",
		"agents/../../etc/passwd": "",
		"/agents/a.yaml":          "",
		"agents/.git/config":      "",
		"other/a.yaml":            "",
	} {
		if got := exportKey(name); got != want {
			t.Errorf("exportKey(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSyncFlagsRequireServerAndToken(t *testing.T) {
	t.Setenv(syncServerEnv, "")
	t.Setenv(syncTokenEnv, "")
	t.Setenv("BENCH_WRITE_TOKEN", "")
	if _, code := syncFlags("pull", nil); code != exitUsage {
		t.Errorf("pull without -server: code %d", code)
	}
	if _, code := syncFlags("push", []string{"-server", "http://x"}); code != exitUsage {
		t.Errorf("push without token: code %d", code)
	}
	if o, _ := syncFlags("pull", []string{"-server", "http://x"}); o == nil {
		t.Error("pull without token should be allowed")
	}
}
