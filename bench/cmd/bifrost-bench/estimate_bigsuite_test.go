package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/valyala/fasthttp"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/suite"
)

// bigSuiteFixture is a project whose built-in library holds a coding-core
// pack of n tasks: the committed t01 to t08 and copies of t01 (its repo
// trimmed to a few files), every task admitted in a legacy lock, as the
// hosted library's large packs are. The recorded 10-run experiment
// e_pilot_mixed_nc93 on coding-core@1.0.0 is in the run store.
type bigSuiteFixture struct {
	app     *app
	suites  serveSuites
	agents  map[string]*schemas.AgentConfig
	libDir  string // the pack folder
	project string
}

func newBigSuiteFixture(t testing.TB, n int) *bigSuiteFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	lib := filepath.Join(dir, "library")
	t.Setenv(suite.LibraryEnv, lib)
	pack := filepath.Join(suite.LibraryBenchmarks(lib), "coding-core")
	writeBigSuite(t, pack, n)
	if err := os.CopyFS(suite.LibraryAgents(lib), os.DirFS(filepath.Join("..", "..", "agents"))); err != nil {
		t.Fatal(err)
	}
	a, err := newApp(ctx, filepath.Join(dir, "deploy", "bench.yaml"), appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.logger = schemas.NoopLogger{}
	if err := os.CopyFS(filepath.Join(a.cfg.Path(a.cfg.RunsRoot), bigSuiteExp), os.DirFS(filepath.Join("..", "..", "testdata", "recorded", bigSuiteExp))); err != nil {
		t.Fatal(err)
	}
	a.deferGateway = true // as the hosted server: no gateway preflight
	agents := map[string]*schemas.AgentConfig{}
	files, err := serveAgents{root: a.cfg.Root, library: a.library}.AgentConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Config != nil {
			agents[f.Path] = f.Config
		}
	}
	return &bigSuiteFixture{
		app:     a,
		suites:  serveSuites{root: a.cfg.Path(a.cfg.BenchmarksRoot), cfg: a.cfg, reg: a.reg, library: suite.LibraryBenchmarks(a.library)},
		agents:  agents,
		libDir:  pack,
		project: a.cfg.Root,
	}
}

// bigSuiteExp is the recorded experiment of the fixture.
const bigSuiteExp = "e_pilot_mixed_nc93"

// writeBigSuite writes the pack: suite.yaml listing n tasks, t01 to t08 as
// committed and copies of t01 with only a few repo files, and a lock that
// admits every task.
func writeBigSuite(t testing.TB, pack string, n int) {
	t.Helper()
	src := filepath.Join("..", "..", "benchmarks", "coding-core")
	if err := os.CopyFS(pack, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	t01 := filepath.Join(pack, "tasks", "t01")
	keep := map[string]bool{"repo/README.md": true, "repo/pyproject.toml": true, "repo/ledger/__init__.py": true}
	var files []string
	if err := filepath.WalkDir(t01, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(t01, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "repo/") && !keep[rel] {
			return nil
		}
		files = append(files, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(t01, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		data[f] = b
	}
	ids := []string{"t01", "t02", "t03", "t04", "t05", "t06", "t07", "t08"}
	for i := len(ids); i < n; i++ {
		id := fmt.Sprintf("t%05d", i+1)
		ids = append(ids, id)
		for _, f := range files {
			b := data[f]
			if f == "task.yaml" {
				b = []byte(strings.Replace(string(b), "id: t01\n", "id: "+id+"\n", 1))
			}
			p := filepath.Join(pack, "tasks", id, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	yml, err := os.ReadFile(filepath.Join(pack, "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	y := strings.Replace(string(yml), "tasks: [t01, t02, t03, t04, t05, t06, t07, t08]", "tasks: ["+strings.Join(ids, ", ")+"]", 1)
	if err := os.WriteFile(filepath.Join(pack, "suite.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(pack, "suite.lock")); err != nil {
		t.Fatal(err)
	}
	ldr := suite.Builtin(filepath.Dir(pack))[0]
	st, err := ldr.Load(context.Background(), "coding-core")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := suite.Lock(st, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for id, tl := range lock.Tasks {
		tl.Status = schemas.TaskAdmitted
		tl.Admission = &schemas.AdmissionRecord{Admitted: true}
		lock.Tasks[id] = tl
	}
	if _, err := suite.WriteLock(filepath.Join(pack, "suite.lock"), lock); err != nil {
		t.Fatal(err)
	}
}

// bigEstimateRequest is the live D4.slow request: one benchmark, score
// mode, 5 tasks, 1 trial, the Bifrost cache on (two arms).
func bigEstimateRequest() *handlers.EvalEstimateRequest {
	return &handlers.EvalEstimateRequest{Mode: schemas.EvaluationModeScore, Arms: []handlers.EvalEstimateArm{{Agent: "codex", Model: "claude-sonnet-4-5"}},
		Benchmarks: []string{"coding-core"}, TasksPerBenchmark: 5, Trials: 1, BifrostCache: true}
}

func (f *bigSuiteFixture) estimate(t testing.TB) *handlers.EvalEstimate {
	t.Helper()
	est, err := handlers.EstimateEvaluationWith(context.Background(), f.suites, serveForecast{a: f.app}, bigEstimateRequest(), f.agents, f.project)
	if err != nil {
		t.Fatal(err)
	}
	return est
}

// TestEstimateBigSuiteProfile times estimates on a pack of HEIMDALL_BIG_SUITE
// tasks (skipped when unset); run it with -cpuprofile to see the hot spots.
func TestEstimateBigSuiteProfile(t *testing.T) {
	n := 0
	fmt.Sscan(os.Getenv("HEIMDALL_BIG_SUITE"), &n)
	if n == 0 {
		t.Skip("set HEIMDALL_BIG_SUITE to the task count")
	}
	start := time.Now()
	f := newBigSuiteFixture(t, n)
	t.Logf("fixture: %s", time.Since(start))
	for i := range 3 {
		start = time.Now()
		est := f.estimate(t)
		t.Logf("estimate %d: %s runs=%d notes=%v per=%+v", i, time.Since(start), est.Runs, est.Notes, est.PerBenchmark[0].Notes)
	}
}

// serve starts the local server over the fixture's app on a loopback port
// and returns its base URL.
func (f *bigSuiteFixture) serve(t testing.TB) string {
	t.Helper()
	ctx := context.Background()
	deps, _, err := orgDeps(ctx, f.app)
	if err != nil {
		t.Fatal(err)
	}
	deps.Compliance = serveCompliance{a: f.app}
	applyServiceHooks(f.app, &deps, auth.LocalOrgID, false)
	srv, err := server.New(f.app.cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &fasthttp.Server{Handler: srv.Handler(), ReadTimeout: time.Minute, WriteTimeout: 5 * time.Minute}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Shutdown() })
	return "http://" + ln.Addr().String()
}

// bigSuitePages are the comparison page reads of the live D4.slow report.
var bigSuitePages = []string{"verdict", "triage", "diagnostics", "adaptive", "migration", "compliance"}

func getPage(t testing.TB, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// TestComparisonPagesBigSuiteProfile times the comparison page reads of the
// recorded experiment, one at a time and then all at once, on a pack of
// HEIMDALL_BIG_SUITE tasks (skipped when unset).
func TestComparisonPagesBigSuiteProfile(t *testing.T) {
	n := 0
	fmt.Sscan(os.Getenv("HEIMDALL_BIG_SUITE"), &n)
	if n == 0 {
		t.Skip("set HEIMDALL_BIG_SUITE to the task count")
	}
	f := newBigSuiteFixture(t, n)
	base := f.serve(t) + handlers.APIPrefix + "/experiments/" + bigSuiteExp + "/"
	for round := range 2 {
		for _, p := range bigSuitePages {
			start := time.Now()
			code, body := getPage(t, base+p)
			t.Logf("round %d %s: %d in %s (%d bytes)", round, p, code, time.Since(start), len(body))
		}
	}
	start := time.Now()
	var wg sync.WaitGroup
	for _, p := range bigSuitePages {
		wg.Go(func() {
			s := time.Now()
			code, _ := getPage(t, base+p)
			t.Logf("together %s: %d in %s", p, code, time.Since(s))
		})
	}
	wg.Wait()
	t.Logf("together: %s", time.Since(start))
}

// TestBigSuiteLoadsOnce is D4.slow on a 300-task library pack: a repeated
// estimate and repeated comparison page reads load the pack from disk no
// more (each used to load it in full, once per estimate in the planner
// and once per page in the analysis, the migration report and the
// compliance pack), and the estimate and the pages answer exactly what
// they answer without the cache.
func TestBigSuiteLoadsOnce(t *testing.T) {
	was := suite.SetLoadCache(false)
	t.Cleanup(func() { suite.SetLoadCache(was) })
	f := newBigSuiteFixture(t, 300)
	base := f.serve(t) + handlers.APIPrefix + "/experiments/" + bigSuiteExp + "/"
	pages := func() map[string]string {
		out := map[string]string{}
		for _, p := range bigSuitePages {
			code, body := getPage(t, base+p)
			if code != 200 {
				t.Fatalf("%s: %d %s", p, code, body)
			}
			out[p] = scrubAnalysisIDs(string(body))
		}
		return out
	}
	estimateJSON := func() string {
		b, err := json.Marshal(f.estimate(t))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// Without the cache: the reference answers.
	wantEst, wantPages := estimateJSON(), pages()
	before := suite.FullLoads()
	estimateJSON()
	pages()
	uncached := suite.FullLoads() - before

	suite.SetLoadCache(true)
	before = suite.FullLoads()
	if got := estimateJSON(); got != wantEst {
		t.Fatalf("estimate with the cache differs:\n got %s\nwant %s", got, wantEst)
	}
	for p, got := range pages() {
		if got != wantPages[p] {
			t.Errorf("%s with the cache differs:\n got %s\nwant %s", p, got, wantPages[p])
		}
	}
	first := suite.FullLoads() - before
	before = suite.FullLoads()
	for range 3 {
		estimateJSON()
		pages()
	}
	repeat := suite.FullLoads() - before
	t.Logf("full loads: %d per estimate and page round without the cache, %d on the first round with it, %d on three repeats", uncached, first, repeat)
	if first > 1 || repeat != 0 {
		t.Fatalf("full loads with the cache: %d on the first round (want at most 1), %d on repeats (want 0)", first, repeat)
	}
}

// volatileRe matches the fields that differ on every read: the id of a
// saved analysis and the times a report is made.
var volatileRe = regexp.MustCompile(`"(analysis_id|evaluated_at|generated_at)":"[^"]*"`)

// scrubAnalysisIDs blanks the volatile fields.
func scrubAnalysisIDs(s string) string {
	return volatileRe.ReplaceAllString(s, `"$1":""`)
}

// TestCatalogReadsNoTask checks the catalog (labels of the triage and the
// verdict) is built from suite.yaml, the probes and the lock of every pack,
// without a full load: over the 61 library packs a full load of each held
// up the comparison page for about a minute.
func TestCatalogReadsNoTask(t *testing.T) {
	was := suite.SetLoadCache(false)
	t.Cleanup(func() { suite.SetLoadCache(was) })
	f := newBigSuiteFixture(t, 50)
	before := suite.FullLoads()
	cat := (&catalogSource{a: f.app}).Catalog()
	if n := suite.FullLoads() - before; n != 0 {
		t.Fatalf("the catalog loaded %d packs in full", n)
	}
	full, err := suite.Builtin(f.suites.root)[0].Load(context.Background(), "coding-core")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range cat.Suites {
		if s.ID == "coding-core" {
			found = true
			if s.Version != full.Version || s.LockHash != full.LockHash || strings.Join(s.Tasks, ",") != strings.Join(full.TaskIDs, ",") ||
				strings.Join(s.Probes, ",") != strings.Join(full.ProbeIDs, ",") || len(s.ProbeLayers) != len(full.Probes) {
				t.Fatalf("catalog entry %+v differs from the full load", s)
			}
		}
	}
	probes := 0
	for _, p := range cat.Probes {
		if full.ProbeByID(p.ID) != nil {
			probes++
		}
	}
	if !found || probes != len(full.Probes) {
		t.Fatalf("catalog lacks coding-core or its probes: found %v, %d of %d probes", found, probes, len(full.Probes))
	}
}
