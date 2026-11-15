package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/traceexport"
	"github.com/valyala/fasthttp"
)

// recordedRunsRoot is the read-only recorded bundle. Tests that change a run
// copy it into a temp folder first.
const recordedRunsRoot = "../../testdata/recorded"

// outageRun is a sealed model-outage run of e_smoke_sdoj.
const (
	smokeExp   = "e_smoke_sdoj"
	outageRun  = "r_ioap8m36je"
	outageDir  = "e_smoke_sdoj/slots/s_glz13mrnep/runs/r_ioap8m36je"
	cleanRunID = "r_aunhv28sxj"
)

func runExportCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runExportOTLP(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func readTrace(t *testing.T, b []byte) *traceexport.TracesData {
	t.Helper()
	var td traceexport.TracesData
	if err := json.Unmarshal(b, &td); err != nil {
		t.Fatalf("parse trace: %v", err)
	}
	if err := traceexport.Validate(&td); err != nil {
		t.Fatalf("validate trace: %v", err)
	}
	return &td
}

func TestExportOTLPRecorded(t *testing.T) {
	out := filepath.Join(t.TempDir(), "trace.json")
	code, _, stderr := runExportCmd(t, "-runs", recordedRunsRoot, "-exp", smokeExp, "-run", outageRun, "-out", out)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	td := readTrace(t, b)
	if len(td.ResourceSpans) != 1 || td.ResourceSpans[0].ScopeSpans[0].Spans[0].TraceID != traceexport.TraceID(outageRun) {
		t.Fatal("want one trace, the run's")
	}

	code, stdout, stderr := runExportCmd(t, "-runs", recordedRunsRoot, "-exp", smokeExp)
	if code != exitOK {
		t.Fatalf("whole experiment: exit %d: %s", code, stderr)
	}
	if got := len(readTrace(t, []byte(stdout)).ResourceSpans); got != 8 {
		t.Fatalf("whole experiment: %d traces, want 8", got)
	}

	dir := t.TempDir() + "/"
	if code, _, stderr := runExportCmd(t, "-runs", recordedRunsRoot, "-exp", smokeExp, "-out", dir); code != exitOK {
		t.Fatalf("folder: exit %d: %s", code, stderr)
	}
	b, err = os.ReadFile(filepath.Join(dir, cleanRunID+".otlp.json"))
	if err != nil {
		t.Fatal(err)
	}
	readTrace(t, b)
}

func TestExportOTLPRefusesTamperedAndUnsealed(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join(recordedRunsRoot, smokeExp), filepath.Join(root, smokeExp))
	grade := filepath.Join(root, outageDir, "grade.json")
	b, err := os.ReadFile(grade)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grade, append(b, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "trace.json")
	for _, args := range [][]string{
		{"-runs", root, "-exp", smokeExp, "-run", outageRun, "-out", out},
		{"-runs", root, "-exp", smokeExp, "-out", out},
	} {
		code, _, stderr := runExportCmd(t, args...)
		if code != exitInfra || !strings.Contains(stderr, "seal does not verify") {
			t.Fatalf("tampered run: exit %d: %s", code, stderr)
		}
		if _, err := os.Stat(out); err == nil {
			t.Fatal("a refused export wrote its output")
		}
	}

	if err := os.Remove(filepath.Join(root, outageDir, "seal.json")); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runExportCmd(t, "-runs", root, "-exp", smokeExp, "-run", outageRun)
	if code != exitInfra || !strings.Contains(stderr, "not sealed") {
		t.Fatalf("unsealed run: exit %d: %s", code, stderr)
	}
	code, stdout, stderr := runExportCmd(t, "-runs", root, "-exp", smokeExp)
	if code != exitOK || !strings.Contains(stderr, "skipped run "+outageRun+": not sealed") {
		t.Fatalf("experiment with an unsealed run: exit %d: %s", code, stderr)
	}
	if got := len(readTrace(t, []byte(stdout)).ResourceSpans); got != 7 {
		t.Fatalf("exported %d runs, want the 7 sealed ones", got)
	}
}

// TestExportOTLPPostsToFakeCollector posts to a local fake OTLP/HTTP
// collector, never a real backend, with headers from an environment variable.
func TestExportOTLPPostsToFakeCollector(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		keys = append(keys, r.URL.Path+" "+r.Header.Get("Content-Type")+" "+r.Header.Get("X-Maxim-Api-Key")+" "+r.Header.Get("X-Maxim-Repo-Id"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("HD_OTLP_TEST_HEADERS", "x-maxim-api-key=test-key-value,x-maxim-repo-id=repo%201")
	code, stdout, stderr := runExportCmd(t, "-runs", recordedRunsRoot, "-exp", smokeExp, "-run", outageRun,
		"-endpoint", srv.URL, "-header-env", "HD_OTLP_TEST_HEADERS")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatal("with -endpoint and no -out nothing goes to stdout")
	}
	if strings.Contains(stderr, "test-key-value") {
		t.Fatal("a header value was printed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || keys[0] != "/v1/traces application/json test-key-value repo 1" {
		t.Fatalf("collector saw %d posts: %v", len(bodies), keys)
	}
	readTrace(t, bodies[0])
}

func TestExportOTLPUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"no exp", []string{"-runs", recordedRunsRoot}, exitUsage},
		{"header env without endpoint", []string{"-runs", recordedRunsRoot, "-exp", smokeExp, "-header-env", "X"}, exitUsage},
		{"stray argument", []string{"-runs", recordedRunsRoot, "-exp", smokeExp, "extra"}, exitUsage},
		{"unset header variable", []string{"-runs", recordedRunsRoot, "-exp", smokeExp, "-endpoint", "http://127.0.0.1:1", "-header-env", "HD_OTLP_UNSET_VAR"}, exitInfra},
		{"bad endpoint", []string{"-runs", recordedRunsRoot, "-exp", smokeExp, "-endpoint", "ftp://x"}, exitInfra},
		{"unknown experiment", []string{"-runs", recordedRunsRoot, "-exp", "e_none"}, exitInfra},
		{"unknown run", []string{"-runs", recordedRunsRoot, "-exp", smokeExp, "-run", "r_none"}, exitInfra},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, _, stderr := runExportCmd(t, c.args...); code != c.code {
				t.Fatalf("exit %d, want %d: %s", code, c.code, stderr)
			}
		})
	}
	if runExport(nil) != exitUsage || runExport([]string{"csv"}) != exitUsage {
		t.Fatal("export without the otlp subcommand must be a usage error")
	}
}

// TestRunOTLPRouteOnDisk serves the trace of a real sealed run through the
// store's index, then refuses it once a file changes after the seal.
func TestRunOTLPRouteOnDisk(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join(recordedRunsRoot, smokeExp), filepath.Join(root, smokeExp))
	st, err := store.NewFSStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	rt := router.New()
	handlers.NewRunsHandler(schemas.ModeRecorded, indexAdapter{ix: ix, root: st.Root()}).RegisterRoutes(rt, lib.SecurityHeaders())
	get := func() *fasthttp.RequestCtx {
		var c fasthttp.RequestCtx
		c.Request.SetRequestURI(handlers.APIPrefix + "/runs/" + outageRun + "/otlp")
		c.Request.Header.SetMethod("GET")
		rt.Handler(&c)
		return &c
	}
	c := get()
	if c.Response.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", c.Response.StatusCode(), c.Response.Body())
	}
	readTrace(t, c.Response.Body())
	if err := os.WriteFile(filepath.Join(root, outageDir, "metrics.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := get(); c.Response.StatusCode() != 409 {
		t.Fatalf("tampered run: status = %d: %s", c.Response.StatusCode(), c.Response.Body())
	}
}
