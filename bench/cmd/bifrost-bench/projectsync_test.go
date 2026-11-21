package main

import (
	"bytes"
	"context"
	"io"
	"net"
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
// the real project file, export and upload routes.
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
		"agents/a.txt":                      "name: a\n",
		"benchmarks/core/tasks/t1/notes.md": "id: t1\n",
		"benchmarks/core/data.bin":          big,
		"policies/p.yaml":                   "rules: []\n",
		"uploads/my file.txt":               "hello",
		"notes/ignored.txt":                 "not an area",
	})
	ctx := context.Background()
	var out bytes.Buffer
	o := &syncOpts{server: base, token: token, dir: src}
	st, err := pushProject(ctx, o, &out)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out.String())
	}
	if st.put != 5 || st.skipped != 0 {
		t.Fatalf("push stats %+v\n%s", st, out.String())
	}
	// The big file went through the blob route.
	if !strings.Contains(out.String(), "put  benchmarks/core/data.bin") {
		t.Fatalf("big file not pushed:\n%s", out.String())
	}
	// Second push: nothing changed.
	if st, err = pushProject(ctx, o, io.Discard); err != nil || st.unchanged != 5 || st.skipped != 0 || st.put != 0 {
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
	writeTree(t, srvRoot, map[string]string{"agents/a.txt": "server edit\n"})
	writeTree(t, src, map[string]string{"agents/a.txt": "local edit\n", "agents/new.txt": "new\n"})
	ctx := context.Background()
	// Bad token: clear 401 error.
	if _, err := pushProject(ctx, &syncOpts{server: base, token: "nope", dir: src}, io.Discard); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad token err = %v", err)
	}
	st, err := pushProject(ctx, &syncOpts{server: base, token: "tok", dir: src}, io.Discard)
	if err != nil || st.conflicts != 1 || st.put != 1 {
		t.Fatalf("conflict push %+v %v", st, err)
	}
	if got, _ := os.ReadFile(filepath.Join(srvRoot, "agents", "a.txt")); string(got) != "server edit\n" {
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

func TestPullRefusesLocalEdits(t *testing.T) {
	srvRoot, dst := t.TempDir(), t.TempDir()
	base := syncServer(t, srvRoot, "tok")
	writeTree(t, srvRoot, map[string]string{"agents/a.txt": "v1\n", "agents/b.txt": "b1\n", "agents/gone.txt": "g\n"})
	ctx := context.Background()
	o := &syncOpts{server: base, dir: dst}
	if st, err := pullProject(ctx, o, io.Discard); err != nil || st.put != 3 || st.conflicts != 0 {
		t.Fatalf("first pull %+v %v", st, err)
	}
	// Server changes a and b and drops gone; locally a and gone are edited.
	writeTree(t, srvRoot, map[string]string{"agents/a.txt": "v2\n", "agents/b.txt": "b2\n"})
	os.Remove(filepath.Join(srvRoot, "agents", "gone.txt"))
	writeTree(t, dst, map[string]string{"agents/a.txt": "my edit\n", "agents/gone.txt": "kept\n", "agents/mine.txt": "new\n"})
	var out bytes.Buffer
	st, err := pullProject(ctx, &syncOpts{server: base, dir: dst, del: true}, &out)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	// a (edited) and gone (edited) and mine (never synced) are conflicts; b is pulled.
	if st.conflicts != 3 || st.put != 1 || st.deleted != 0 {
		t.Fatalf("conflict pull %+v\n%s", st, out.String())
	}
	for _, want := range []string{"conflict agents/a.txt", "conflict agents/gone.txt", "get  agents/b.txt"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "agents", "a.txt")); string(got) != "my edit\n" {
		t.Fatal("pull overwrote a local edit")
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "agents", "b.txt")); string(got) != "b2\n" {
		t.Fatal("pull skipped a clean file")
	}
	// Still refused on the next pull (the base was not advanced).
	if st, _ = pullProject(ctx, o, io.Discard); st.conflicts != 1 {
		t.Fatalf("second pull %+v", st)
	}
	// -force overwrites and deletes.
	if st, err = pullProject(ctx, &syncOpts{server: base, dir: dst, del: true, force: true}, io.Discard); err != nil || st.conflicts != 0 || st.put != 1 || st.deleted != 2 {
		t.Fatalf("force pull %+v %v", st, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "agents", "a.txt")); string(got) != "v2\n" {
		t.Fatal("pull -force did not overwrite")
	}
	// A clean tree pulls with no conflicts afterwards.
	writeTree(t, srvRoot, map[string]string{"agents/a.txt": "v3\n"})
	if st, err = pullProject(ctx, o, io.Discard); err != nil || st.conflicts != 0 || st.put != 1 {
		t.Fatalf("clean pull %+v %v", st, err)
	}
}

func TestBigFileRoundTripAndBlobConflict(t *testing.T) {
	srvRoot, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
	base := syncServer(t, srvRoot, "tok")
	big := bytes.Repeat([]byte{0, 1, 2, 250}, 3<<18) // 3 MiB binary
	writeTree(t, src, map[string]string{"uploads/data/big.bin": string(big)})
	ctx := context.Background()
	o := &syncOpts{server: base, token: "tok", dir: src}
	if st, err := pushProject(ctx, o, io.Discard); err != nil || st.put != 1 {
		t.Fatalf("push big %+v %v", st, err)
	}
	if got, _ := os.ReadFile(filepath.Join(srvRoot, "uploads", "data", "big.bin")); !bytes.Equal(got, big) {
		t.Fatal("server copy of the big file differs")
	}
	if _, err := pullProject(ctx, &syncOpts{server: base, dir: dst}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "uploads", "data", "big.bin")); !bytes.Equal(got, big) {
		t.Fatal("pulled big file differs")
	}
	// Server-side edit of the big file: the blob route's If-Match refuses push.
	writeTree(t, srvRoot, map[string]string{"uploads/data/big.bin": string(big[:2<<20])})
	writeTree(t, src, map[string]string{"uploads/data/big.bin": string(append(big, 9))})
	if st, err := pushProject(ctx, o, io.Discard); err != nil || st.conflicts != 1 || st.put != 0 {
		t.Fatalf("conflicting big push %+v %v", st, err)
	}
	if st, err := pushProject(ctx, &syncOpts{server: base, token: "tok", dir: src, force: true}, io.Discard); err != nil || st.put != 1 {
		t.Fatalf("force big push %+v %v", st, err)
	}
}
