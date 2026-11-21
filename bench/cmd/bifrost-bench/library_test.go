package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/taskhealth"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// libraryFixture is a library root holding the mini pack as hd-lib and an
// agent config, plus two org projects: org A with its own pack, org B empty.
func libraryFixture(t *testing.T) (libRoot, orgA, orgB string) {
	t.Helper()
	libRoot, orgA, orgB = t.TempDir(), t.TempDir(), t.TempDir()
	mini := filepath.Join("..", "..", "suite", "testdata", "valid", "mini")
	copyTree(t, mini, filepath.Join(libRoot, "benchmarks", "mini"))
	copyTree(t, mini, filepath.Join(orgA, "benchmarks", "a-own"))
	raw, err := os.ReadFile(filepath.Join(orgA, "benchmarks", "a-own", "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orgA, "benchmarks", "a-own", "suite.yaml"), []byte(strings.Replace(string(raw), "id: mini", "id: a-own", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(libRoot, "agents"), filepath.Join(orgB, "benchmarks")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(libRoot, "agents", "lib-agent.yaml"), []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return libRoot, orgA, orgB
}

// GET /suites' source lists library packs for every org, marked built in,
// and never another org's own packs.
func TestServeSuitesLibraryLayer(t *testing.T) {
	ctx := context.Background()
	libRoot, orgA, orgB := libraryFixture(t)
	a := serveSuites{root: filepath.Join(orgA, "benchmarks"), library: suite.LibraryBenchmarks(libRoot)}
	b := serveSuites{root: filepath.Join(orgB, "benchmarks"), library: suite.LibraryBenchmarks(libRoot)}

	idsA, err := a.SuiteIDs(ctx)
	if err != nil || strings.Join(idsA, ",") != "a-own,mini" {
		t.Fatalf("org A ids = %v, %v", idsA, err)
	}
	idsB, err := b.SuiteIDs(ctx)
	if err != nil || strings.Join(idsB, ",") != "mini" {
		t.Fatalf("org B ids = %v, %v; want only the library pack", idsB, err)
	}
	if !a.SuiteBuiltin("mini") || a.SuiteBuiltin("a-own") {
		t.Errorf("builtin flags wrong: mini=%v a-own=%v", a.SuiteBuiltin("mini"), a.SuiteBuiltin("a-own"))
	}
	s, err := b.LoadSuite(ctx, "mini")
	if err != nil || !strings.HasPrefix(s.Root, libRoot) {
		t.Fatalf("org B load mini: %v (root %v)", err, s)
	}
	if _, err := b.LoadSuite(ctx, "a-own"); err == nil {
		t.Fatal("org B loaded org A's pack")
	}

	ag, err := (serveAgents{root: orgB, library: libRoot}).AgentConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ag) != 1 || ag[0].Path != "agents/lib-agent.yaml" {
		t.Fatalf("agents = %+v; want the library config as agents/lib-agent.yaml", ag)
	}
}

// A hosted org's app: the planner's loader and experiment guard accept a
// library suite and a library agent, and the org's own paths stay confined.
func TestForOrgLibrary(t *testing.T) {
	libRoot, _, orgB := libraryFixture(t)
	t.Setenv(suite.LibraryEnv, libRoot)
	base := &app{cfg: &lib.Config{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), reg: &schemas.Registry{}}
	oa, err := base.forOrg(tenancy.Org{ID: "org_b"}, tenancy.OrgPaths{ProjectRoot: orgB, StoreRoot: t.TempDir(), ReleasesRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if oa.library != libRoot {
		t.Fatalf("library = %q", oa.library)
	}
	if _, err := engine.LoadSuite(context.Background(), oa.reg, "mini"); err != nil {
		t.Fatalf("org registry cannot load the library suite: %v", err)
	}
	exp := &schemas.Experiment{Suite: "mini@1.0.0", Arms: map[string]schemas.ArmSpec{"a": {Agent: "agents/lib-agent.yaml"}, "b": {}}}
	if err := oa.planGuard(exp); err != nil {
		t.Fatalf("plan referencing the library refused: %v", err)
	}
	exp.Arms["b"] = schemas.ArmSpec{Agent: "../" + filepath.Base(libRoot) + "/agents/lib-agent.yaml"}
	if err := oa.planGuard(exp); err == nil {
		t.Fatal("path out of the project accepted")
	}
}

// The shared runner's per-org registry loads library suites, with and
// without the org's downloaded project.
func TestSharedOrgProjectLibrary(t *testing.T) {
	libRoot, orgA, _ := libraryFixture(t)
	projects := t.TempDir()
	copyTree(t, orgA, filepath.Join(projects, "org_a"))
	t.Setenv(suite.LibraryEnv, libRoot)
	a := &app{cfg: &lib.Config{Root: t.TempDir(), BenchmarksRoot: "benchmarks"}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), reg: &schemas.Registry{}}
	for _, org := range []string{"org_a", "org_missing"} {
		reg, _ := sharedOrgProject(a, filepath.Join(projects, org))
		if _, err := engine.LoadSuite(context.Background(), reg, "mini"); err != nil {
			t.Errorf("%s: runner loader misses the library suite: %v", org, err)
		}
	}
	reg, _ := sharedOrgProject(a, filepath.Join(projects, "org_a"))
	if _, err := engine.LoadSuite(context.Background(), reg, "a-own"); err != nil {
		t.Errorf("org_a own suite lost: %v", err)
	}
}

// emptyRuns is a run store with no experiments: health of a fresh suite.
type emptyRuns struct{}

func (emptyRuns) Experiments(context.Context) ([]string, error) { return nil, nil }
func (emptyRuns) Manifest(context.Context, string) (*schemas.Manifest, error) {
	return nil, schemas.ErrNotFound
}
func (emptyRuns) SealedRuns(context.Context, string, string) ([]schemas.SealedRun, error) {
	return nil, nil
}
func (emptyRuns) SlotAttempts(context.Context, string, string) ([]schemas.RunRecord, error) {
	return nil, nil
}
func (emptyRuns) Regrades(context.Context, string) ([]taskhealth.Regrade, error) { return nil, nil }

// Read paths of a library suite: task detail and health over the org's
// suite source; write paths: the suite jobs and the judge refuse it.
func TestLibrarySuiteReadPathsAndWritesRefused(t *testing.T) {
	libRoot, _, orgB := libraryFixture(t)
	t.Setenv(suite.LibraryEnv, libRoot)
	src := serveSuites{root: filepath.Join(orgB, "benchmarks"), library: suite.LibraryBenchmarks(libRoot)}

	r := router.New()
	handlers.NewSuitesHandler(schemas.ModeLive, src).RegisterRoutes(r)
	handlers.NewSuiteHealthHandler(schemas.ModeLive, src, emptyRuns{}).RegisterRoutes(r)
	get := func(path string) (int, string) {
		var ctx fasthttp.RequestCtx
		ctx.Request.SetRequestURI(path)
		ctx.Request.Header.SetMethod("GET")
		r.Handler(&ctx)
		return ctx.Response.StatusCode(), string(ctx.Response.Body())
	}
	if code, body := get(handlers.APIPrefix + "/suites/mini/tasks/m01"); code != 200 {
		t.Fatalf("task detail = %d %s", code, body)
	}
	if code, body := get(handlers.APIPrefix + "/suites/mini/health"); code != 200 || !strings.Contains(body, "m01") {
		t.Fatalf("health = %d %s", code, body)
	}
	if code, body := get(handlers.APIPrefix + "/suites"); code != 200 || !strings.Contains(body, `"builtin":true`) {
		t.Fatalf("list = %d %s", code, body)
	}

	a := &app{cfg: &lib.Config{Root: orgB, BenchmarksRoot: "benchmarks"}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), reg: &schemas.Registry{}}
	l := &launcher{a: a}
	if err := validateSuite(l, "mini", true); err == nil || !strings.Contains(err.Error(), schemas.ErrReadOnly.Error()) {
		t.Errorf("lock/admit of a library suite: %v", err)
	}
	if err := validateSuite(l, "mini", false); err != nil {
		t.Errorf("lint of a library suite refused: %v", err)
	}
	if _, err := suiteOpsCheckSuite(jobEnv{App: a}, "mini"); err == nil || !strings.Contains(err.Error(), schemas.ErrReadOnly.Error()) {
		t.Errorf("suite ops on a library suite: %v", err)
	}
	if _, err := (judgeSvc{a: a}).AddCalibrationLabel(context.Background(), "mini", &schemas.CalibrationLabelRequest{Task: "m01"}); !errors.Is(err, schemas.ErrReadOnly) {
		t.Errorf("judge label on a library suite: %v", err)
	}
	if _, err := snapshotSuite(context.Background(), a, "mini"); !errors.Is(err, schemas.ErrReadOnly) {
		t.Errorf("hosted admission of a library suite: %v", err)
	}
}
