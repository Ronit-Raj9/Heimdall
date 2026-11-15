package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskhealth"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// heldOutMarkers must never appear in a response while held-out tasks are
// hidden: the held-out task's title, instruction text and a visible file.
var heldOutMarkers = []string{"HELDOUT_TITLE", "HELDOUT_INSTRUCTION", "heldout_file.py", "HELDOUT_PROJECT"}

// healthSuites is one pack with a public task (quarantined) and a held-out
// task, and a canary.
func healthSuites(t *testing.T) *fakeSuites {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "hs")
	pub := newTestTask(t, dir, "pub", "repo", map[string]string{"instruction.md": "public instruction\n", "repo/app.py": "x = 1\n"})
	sec := newTestTask(t, dir, "sec", "repo", map[string]string{"instruction.md": "HELDOUT_INSTRUCTION\n", "repo/heldout_file.py": "y = 2\n"})
	sec.Title, sec.Project, sec.Split = "HELDOUT_TITLE", "HELDOUT_PROJECT", schemas.SplitHeldOut
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s := &schemas.Suite{ID: "hs", Version: "1.0.0", Root: dir, TaskIDs: []string{"pub", "sec"}, Tasks: []*schemas.Task{pub, sec},
		Canary: "0f8fad5b-d9cb-469f-a165-70867728950e",
		Lock:   &schemas.SuiteLock{Suite: "hs", Version: "1.0.0", Tasks: map[string]schemas.TaskLock{"pub": admittedLock(at), "sec": admittedLock(at)}}}
	f := schemas.SuiteHealthFile{Suite: "hs", Quarantined: map[string]schemas.TaskQuarantine{
		"pub": {Reason: "reference flips", At: at, Source: schemas.QuarantineManual}}}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, schemas.SuiteHealthFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return &fakeSuites{ids: []string{"hs"}, suites: map[string]*schemas.Suite{"hs": s}}
}

// healthRecord is a taskhealth.Source with one experiment of four runs.
type healthRecord struct{}

func (healthRecord) Experiments(context.Context) ([]string, error) { return []string{"e1"}, nil }

func (healthRecord) Manifest(context.Context, string) (*schemas.Manifest, error) {
	return &schemas.Manifest{ExpID: "e1", SuiteID: "hs", Arms: map[string]schemas.ArmManifest{
		"A": {Config: schemas.AgentConfig{ID: "alpha", Adapter: "command"}},
		"B": {Config: schemas.AgentConfig{ID: "beta", Adapter: "command"}},
	}, Slots: []schemas.Slot{{SlotID: "s1", Task: "pub", Arm: "A"}}}, nil
}

func (healthRecord) SealedRuns(context.Context, string, string) ([]schemas.SealedRun, error) {
	var out []schemas.SealedRun
	for i, passed := range []bool{true, false, true, true} {
		arm, cfg := "A", "alpha"
		if i%2 == 1 {
			arm, cfg = "B", "beta"
		}
		outcome := schemas.OutcomeFail
		if passed {
			outcome = schemas.OutcomePass
		}
		out = append(out, schemas.SealedRun{
			Slot:  schemas.Slot{SlotID: fmt.Sprintf("s%d", i), Task: "pub", TaskVersion: "1.0.0", Arm: arm},
			Run:   schemas.Run{RunID: fmt.Sprintf("r%d", i), Outcome: outcome, Agent: schemas.RunAgentInfo{ConfigID: cfg, ConfigHash: "h-" + cfg}},
			Grade: &schemas.Grade{Passed: passed, DiffSHA256: fmt.Sprintf("d%d", i), GraderVersion: "g", SuiteVersion: "1.0.0"},
		})
	}
	return out, nil
}

func (healthRecord) SlotAttempts(context.Context, string, string) ([]schemas.RunRecord, error) {
	return []schemas.RunRecord{{Run: schemas.Run{Outcome: schemas.OutcomeAbortedInfra}, Sealed: true}, {Run: schemas.Run{Outcome: schemas.OutcomePass}, Sealed: true}}, nil
}

func (healthRecord) Regrades(context.Context, string) ([]taskhealth.Regrade, error) { return nil, nil }

func newHealthClient(t *testing.T, suites SuiteSource, src taskhealth.Source, visible bool) *testClient {
	t.Helper()
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, testMode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found")
	}
	common := lib.BodyLimit(testMode, lib.MaxBodyBytes)
	NewSuitesHandler(testMode, suites).WithHeldOutVisible(visible).RegisterRoutes(r, common)
	NewSuiteHealthHandler(testMode, suites, src).RegisterRoutes(r, common)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Shutdown(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestHeldOutHiddenFromAPI(t *testing.T) {
	c := newHealthClient(t, healthSuites(t), healthRecord{}, false)
	for _, path := range []string{"/suites", "/suites/hs/tasks/sec", "/suites/hs/health"} {
		t.Run(path, func(t *testing.T) {
			body := string(c.do(t, "GET", APIPrefix+path, nil, nil).Body())
			for _, m := range heldOutMarkers {
				if strings.Contains(body, m) {
					t.Fatalf("%s leaked held-out content %q: %s", path, m, body)
				}
			}
		})
	}
	resp := c.do(t, "GET", APIPrefix+"/suites/hs/tasks/sec", nil, nil)
	if resp.StatusCode() != 404 || !strings.Contains(string(resp.Body()), "is held out") {
		t.Fatalf("held-out detail: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "GET", APIPrefix+"/suites/hs/tasks/pub", nil, nil); resp.StatusCode() != 200 {
		t.Fatalf("public detail: %d %s", resp.StatusCode(), resp.Body())
	}
	suite := suiteByID(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil)), "hs")
	if suite["held_out_count"] != float64(1) || suite["quarantined_count"] != float64(1) || suite["has_canary"] != true {
		t.Fatalf("summary counts: %v %v %v", suite["held_out_count"], suite["quarantined_count"], suite["has_canary"])
	}
	sec := taskByID(t, suite, "sec")
	if sec["redacted"] != true || sec["split"] != "held_out" || sec["title"] != "" || sec["status"] != "admitted" {
		t.Fatalf("held-out entry: %v", sec)
	}
	pub := taskByID(t, suite, "pub")
	q, _ := pub["quarantine"].(map[string]any)
	if q["reason"] != "reference flips" || q["source"] != "manual" || pub["split"] != nil {
		t.Fatalf("public entry: %v", pub)
	}
}

func TestHeldOutVisibleByConfig(t *testing.T) {
	c := newHealthClient(t, healthSuites(t), healthRecord{}, true)
	resp := c.do(t, "GET", APIPrefix+"/suites/hs/tasks/sec", nil, nil)
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), "HELDOUT_INSTRUCTION") {
		t.Fatalf("visible held-out detail: %d %s", resp.StatusCode(), resp.Body())
	}
	sec := taskByID(t, suiteByID(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil)), "hs"), "sec")
	if sec["redacted"] != nil || sec["title"] != "HELDOUT_TITLE" || sec["split"] != "held_out" {
		t.Fatalf("visible held-out entry: %v", sec)
	}
}

func TestSuiteHealthRoute(t *testing.T) {
	c := newHealthClient(t, healthSuites(t), healthRecord{}, false)
	resp := c.do(t, "GET", APIPrefix+"/suites/hs/health", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("health: %d %s", resp.StatusCode(), resp.Body())
	}
	body := decodeBody(t, resp)
	checkKeys(t, "SuiteHealth", body, loadKeys(t, "SuiteHealth"), true)
	nested := map[string]string{
		"thresholds": "HealthThresholds", "tasks[]": "TaskHealth", "tasks[].verifier": "VerifierHealth",
		"tasks[].variance": "OutcomeVariance", "tasks[].variance.per_config[]": "ConfigOutcome",
		"tasks[].infra": "InfraHealth", "tasks[].difficulty": "DifficultyHealth", "tasks[].quarantine": "TaskQuarantine",
	}
	for path, typ := range nested {
		for i, obj := range objectsAt(t, body, path) {
			checkKeys(t, fmt.Sprintf("SuiteHealth.%s #%d", path, i), obj, loadKeys(t, typ), false)
		}
	}
	var h schemas.SuiteHealth
	if err := json.Unmarshal(resp.Body(), &h); err != nil {
		t.Fatal(err)
	}
	pub := h.Tasks[0]
	if pub.Task != "pub" || pub.Difficulty.Runs != 4 || pub.Difficulty.Configs != 2 || pub.Quarantine == nil || pub.Infra.AbortedInfra != 1 {
		t.Fatalf("pub health %+v", pub)
	}
	if h.Tasks[1].Split != schemas.SplitHeldOut {
		t.Fatalf("held-out split %+v", h.Tasks[1])
	}
}

func TestSuiteHealthRouteErrors(t *testing.T) {
	tests := []struct {
		name   string
		suites SuiteSource
		src    taskhealth.Source
		path   string
		status int
	}{
		{"no loader", nil, healthRecord{}, "/suites/hs/health", 503},
		{"no store", healthSuites(t), nil, "/suites/hs/health", 503},
		{"unknown suite", healthSuites(t), healthRecord{}, "/suites/nope/health", 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var suites SuiteSource
			if tt.suites != nil {
				suites = tt.suites
			}
			c := newHealthClient(t, suites, tt.src, false)
			if resp := c.do(t, "GET", APIPrefix+tt.path, nil, nil); resp.StatusCode() != tt.status {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
		})
	}
}

func TestRedactTask(t *testing.T) {
	at := time.Unix(0, 0)
	in := schemas.SuiteTask{ID: "x", Version: "1", Title: "secret", Project: "p", Kind: "bugfix", Status: "admitted", Admitted: true,
		AdmittedAt: &at, HiddenTestFiles: 3, Tags: []string{"t"}, Provenance: &schemas.TaskProvenance{Source: "s"}}
	out := redactTask(in)
	if out.Title != "" || out.Project != "" || out.Kind != "" || out.HiddenTestFiles != 0 || len(out.Tags) != 0 || out.Provenance != nil {
		t.Fatalf("redacted %+v", out)
	}
	if out.ID != "x" || out.Status != "admitted" || !out.Admitted || !out.Redacted || out.Split != schemas.SplitHeldOut {
		t.Fatalf("kept fields %+v", out)
	}
}
