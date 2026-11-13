package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// This file holds the fake dependencies and the in-memory test server every
// handler test uses.

const (
	testMode  = schemas.ModeRecorded
	testToken = "test-write-token"
	testExp   = "e_test"
)

// fakeIndex is an in-memory ReadIndex.
type fakeIndex struct {
	manifests map[string]*schemas.Manifest
	runs      []RunView
	files     map[string]map[string][]byte // run id -> name -> bytes
	asked     []string                     // every RunFile name requested
	mu        sync.Mutex
}

func (f *fakeIndex) Experiments() []string {
	var ids []string
	for id := range f.manifests {
		ids = append(ids, id)
	}
	return ids
}

func (f *fakeIndex) Manifest(expID string) (*schemas.Manifest, bool) {
	m, ok := f.manifests[expID]
	return m, ok
}

func (f *fakeIndex) Runs(expID string) []RunView {
	var out []RunView
	for _, rv := range f.runs {
		if rv.Slot.ExpID == expID {
			out = append(out, rv)
		}
	}
	return out
}

func (f *fakeIndex) Run(runID string) (*RunView, bool) {
	for _, rv := range f.runs {
		if rv.Run.RunID == runID {
			rv := rv
			return &rv, true
		}
	}
	return nil, false
}

func (f *fakeIndex) Pair(pairID string) []RunView {
	var out []RunView
	for _, rv := range f.runs {
		if rv.Slot.PairID == pairID {
			out = append(out, rv)
		}
	}
	return out
}

func (f *fakeIndex) RunFile(runID, name string) ([]byte, error) {
	f.mu.Lock()
	f.asked = append(f.asked, name)
	f.mu.Unlock()
	if data, ok := f.files[runID][name]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("run %s file %s: %w", runID, name, fs.ErrNotExist)
}

// fakeAnalyzer returns a fixed analysis.
type fakeAnalyzer struct{ err error }

func (f *fakeAnalyzer) Analyze(_ context.Context, expID, gradeSource string) (*schemas.Analysis, error) {
	if f.err != nil {
		return nil, f.err
	}
	p := 0.0156
	return &schemas.Analysis{
		AnalysisID: "a_1", ExpID: expID, GradeSource: gradeSource, Mode: testMode,
		Verdict: schemas.Verdict{
			Primary:  schemas.Endpoint{Condition: "clean", Metric: "verified_success"},
			Decision: schemas.DecisionImproved, Action: schemas.ActionShip,
			Effect: &schemas.Effect{DBar: 0.5, PValue: &p, NTasks: 2, NNonzero: 1},
		},
		Naive: map[string]schemas.Ratio{"A": {Num: 1, Den: 2}, "B": {Num: 2, Den: 2}},
	}, nil
}

// fakeLauncher records calls and serves one event channel per subscription.
type fakeLauncher struct {
	mu      sync.Mutex
	planned *schemas.Experiment
	created *schemas.Manifest
	started string
	events  []schemas.LiveEvent // sent on every subscription, then closed
}

func (f *fakeLauncher) Plan(_ context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if exp.Name == "bad" {
		return nil, fmt.Errorf("experiment bad: no arms")
	}
	f.planned = exp
	m := testManifest()
	m.Experiment = *exp
	return m, nil
}

func (f *fakeLauncher) Create(_ context.Context, m *schemas.Manifest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = m
	return nil
}

func (f *fakeLauncher) Start(_ context.Context, expID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if expID != testExp {
		return fmt.Errorf("start %s: %w", expID, schemas.ErrNotFound)
	}
	f.started = expID
	return nil
}

func (f *fakeLauncher) Events(string) (<-chan schemas.LiveEvent, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan schemas.LiveEvent, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, func() {}
}

// fakeCatalog returns a small catalog.
type fakeCatalog struct{}

func (fakeCatalog) Catalog() *schemas.Catalog {
	return &schemas.Catalog{Gates: []schemas.CatalogGate{{Name: "protected_files", Baseline: true}}}
}

// fakeAuditor answers a fixed verdict for both inputs.
type fakeAuditor struct{ gotCSV, gotJSON bool }

func (f *fakeAuditor) AuditCSV(_ context.Context, csv []byte) (*schemas.Verdict, error) {
	if !strings.HasPrefix(string(csv), "task,") {
		return nil, fmt.Errorf("missing header")
	}
	f.gotCSV = true
	return &schemas.Verdict{Decision: schemas.DecisionInconclusive}, nil
}

func (f *fakeAuditor) Audit(_ context.Context, req *schemas.AuditRequest) (*schemas.Verdict, error) {
	f.gotJSON = len(req.Rows) > 0
	return &schemas.Verdict{Decision: schemas.DecisionInconclusive}, nil
}

// testManifest is a two-arm, two-task, one-trial experiment.
func testManifest() *schemas.Manifest {
	m := &schemas.Manifest{
		ExpID: testExp, Hash: "sha256:abc", CreatedAt: time.Date(2026, 11, 14, 23, 41, 0, 0, time.UTC),
		Experiment: schemas.Experiment{Name: "demo", Suite: "coding-core@1.0.0",
			Primary:    schemas.Endpoint{Condition: "clean", Metric: "verified_success"},
			Conditions: []schemas.ConditionSpec{{ID: "clean", Trials: 1}},
			Arms:       map[string]schemas.ArmSpec{"A": {Label: "baseline"}, "B": {Label: "notes", Set: map[string]any{"notes.user_file": "x.md"}}},
		},
		Arms: map[string]schemas.ArmManifest{"A": {Label: "baseline"}, "B": {Label: "notes"}},
	}
	for i, task := range []string{"t01", "t02"} {
		for j, arm := range []string{"A", "B"} {
			m.Slots = append(m.Slots, schemas.Slot{
				SlotID: fmt.Sprintf("s_%s_%s", task, arm), PairID: "p_" + task, ExpID: testExp, Suite: "coding-core",
				Task: task, Arm: arm, Condition: "clean", Trial: 1, OrderIndex: i*2 + j, Counted: true,
			})
		}
	}
	return m
}

// testIndex holds testManifest with sealed runs: t01 A fail, B pass; t02 both pass.
func testIndex() *fakeIndex {
	m := testManifest()
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{testExp: m}, files: map[string]map[string][]byte{}}
	outcomes := map[string]schemas.Outcome{"s_t01_A": schemas.OutcomeFail, "s_t01_B": schemas.OutcomePass,
		"s_t02_A": schemas.OutcomePass, "s_t02_B": schemas.OutcomePass}
	for _, s := range m.Slots {
		runID := "r_" + strings.TrimPrefix(s.SlotID, "s_")
		y, _ := outcomes[s.SlotID].Score()
		idx.runs = append(idx.runs, RunView{Slot: s, Sealed: true, SealHash: "sha256:seal", Run: schemas.Run{
			RunID: runID, SlotID: s.SlotID, PairID: s.PairID, ExpID: testExp, Attempt: 1, Stage: schemas.StageSeal,
			Outcome: outcomes[s.SlotID], Y: schemas.Observed(y, schemas.EvidenceOutcome),
		}})
		idx.files[runID] = map[string][]byte{
			FileEvents:           []byte(fmt.Sprintf(`{"event_id":"%s:1","type":"tool_call","target":"pytest"}`+"\n", runID)),
			FileDiff:             []byte("diff --git a/x.py b/x.py\n--- a/x.py\n+++ b/x.py\n@@ -1 +1 @@\n-a\n+b\n"),
			FileReceipts:         []byte(`[{"probe_id":"stale-notes","layer":"fixture","steps":{}}]`),
			FileMetrics:          []byte(`{"steps":{"state":"observed","v":3,"evidence":"native"}}`),
			FileGates:            []byte(`[{"gate":"protected_files","version":"1","effect":"veto","fired":false}]`),
			FileEvidence:         []byte(`{"requests":{"complete":true}}`),
			"agent.stream.jsonl": []byte("secret"),
		}
	}
	return idx
}

// testDeps bundles the fakes a test server is built from.
type testDeps struct {
	index    ReadIndex
	analyzer Analyzer
	launcher Launcher
	catalog  CatalogSource
	auditor  Auditor
	doctor   DoctorFunc
}

// fullDeps returns every fake.
func fullDeps() testDeps {
	return testDeps{
		index: testIndex(), analyzer: &fakeAnalyzer{}, launcher: &fakeLauncher{}, catalog: fakeCatalog{},
		auditor: &fakeAuditor{},
		doctor: func(context.Context) []schemas.DoctorCheck {
			return []schemas.DoctorCheck{{ID: "docker", Status: schemas.DoctorPass}}
		},
	}
}

// testClient talks to handlers over fasthttp's in-memory listener.
type testClient struct {
	client *fasthttp.Client
	exp    *ExperimentsHandler
}

// newTestClient mounts every handler the way the server does: the body limit
// on all routes, the write token on POST routes.
func newTestClient(t *testing.T, d testDeps) *testClient {
	t.Helper()
	write := lib.RequireWriteToken(testMode, testToken)
	common := []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)}
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, testMode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found")
	}
	exp := NewExperimentsHandler(testMode, d.index, d.analyzer, d.launcher, d.catalog, write)
	NewHealthHandler(testMode, "test", d.doctor).RegisterRoutes(r, common...)
	NewCatalogHandler(testMode, d.catalog).RegisterRoutes(r, common...)
	exp.RegisterRoutes(r, common...)
	NewRunsHandler(testMode, d.index).RegisterRoutes(r, common...)
	NewPairsHandler(testMode, d.index).RegisterRoutes(r, common...)
	NewAuditHandler(testMode, d.auditor, write).RegisterRoutes(r, common...)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: 4 * lib.MaxBodyBytes}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		exp.Close()
		if err := srv.Shutdown(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return &testClient{exp: exp, client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

// do sends one request. headers may be nil.
func (c *testClient) do(t *testing.T, method, path string, body []byte, headers map[string]string) *fasthttp.Response {
	t.Helper()
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://127.0.0.1" + path)
	req.Header.SetMethod(method)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.SetBody(body)
	}
	resp := &fasthttp.Response{}
	if err := c.client.DoTimeout(req, resp, 5*time.Second); err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// authed returns headers with the write token and a content type.
func authed(contentType string) map[string]string {
	h := map[string]string{"Authorization": "Bearer " + testToken}
	if contentType != "" {
		h["Content-Type"] = contentType
	}
	return h
}

// decodeBody parses a JSON response into a generic map.
func decodeBody(t *testing.T, resp *fasthttp.Response) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		t.Fatalf("decode %q: %v", resp.Body(), err)
	}
	return out
}

// readSSE reads a streamed body into lines.
func readSSE(body []byte) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func TestFakesSatisfyInterfaces(t *testing.T) {
	var _ ReadIndex = &fakeIndex{}
	var _ Analyzer = &fakeAnalyzer{}
	var _ Launcher = &fakeLauncher{}
	var _ CatalogSource = fakeCatalog{}
	var _ Auditor = &fakeAuditor{}
	if testManifest().Slots[0].SlotID != "s_t01_A" {
		t.Fatal("unexpected test manifest")
	}
}
