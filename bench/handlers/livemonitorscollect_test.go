package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livemon"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

// degradingAgent is a deployed agent endpoint whose behaviour the test
// changes: good answers, wrong answers, or no answer at all.
type degradingAgent struct {
	mode atomic.Value // "good" | "wrong" | "down"
	srv  *httptest.Server
}

func newDegradingAgent(t *testing.T) *degradingAgent {
	a := &degradingAgent{}
	a.mode.Store("good")
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch a.mode.Load().(string) {
		case "down":
			hj, ok := w.(http.Hijacker)
			if !ok {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			c, _, _ := hj.Hijack()
			_ = c.Close()
			return
		case "wrong":
			_, _ = io.WriteString(w, `{"answer":"I am not sure"}`)
			return
		}
		task := r.URL.Query().Get("task")
		_, _ = io.WriteString(w, `{"answer":"ok-`+task+`"}`)
	}))
	t.Cleanup(a.srv.Close)
	return a
}

// endpointLauncher stands in for the run pipeline: Start sends each planned
// task to the agent's endpoint and seals one run per slot in the index,
// graded on the answer, with infra_error when the endpoint did not answer.
type endpointLauncher struct {
	t     *testing.T
	url   string
	index *fakeIndex
	mu    sync.Mutex
	n     int
}

func (l *endpointLauncher) Plan(_ context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	id := fmt.Sprintf("e_mon_%d", l.n)
	m := &schemas.Manifest{ExpID: id, Hash: "sha256:" + id, CreatedAt: time.Now().UTC(), Experiment: *exp,
		Arms: map[string]schemas.ArmManifest{}}
	for k, a := range exp.Arms {
		m.Arms[k] = schemas.ArmManifest{Label: a.Label}
	}
	for _, c := range exp.Conditions {
		for i, task := range c.Tasks.IDs {
			m.Slots = append(m.Slots, schemas.Slot{SlotID: fmt.Sprintf("s_%s_%s", id, task), PairID: "p_" + task, ExpID: id,
				Suite: "svc-bench", Task: task, Arm: "A", Condition: c.ID, Trial: 1, OrderIndex: i, Counted: true})
		}
	}
	return m, nil
}

func (l *endpointLauncher) Create(_ context.Context, m *schemas.Manifest) error {
	l.index.mu.Lock()
	l.index.manifests[m.ExpID] = m
	l.index.mu.Unlock()
	return nil
}

func (l *endpointLauncher) Start(_ context.Context, id string) error {
	m := l.index.manifests[id]
	for _, s := range m.Slots {
		start := time.Now().UTC()
		run := schemas.Run{RunID: "r_" + strings.TrimPrefix(s.SlotID, "s_"), SlotID: s.SlotID, PairID: s.PairID, ExpID: id, Attempt: 1}
		resp, err := http.Get(l.url + "?task=" + s.Task)
		switch {
		case err != nil:
			run.Outcome = schemas.OutcomeInfraError
			run.Failures = []schemas.PartFailure{{Stage: schemas.StageRun, Part: "agent", Error: err.Error()}}
		default:
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			run.Outcome = schemas.OutcomeFail
			if strings.Contains(string(b), "ok-"+s.Task) {
				run.Outcome = schemas.OutcomePass
			}
		}
		agentStart, agentEnd, sealed := start, start.Add(1200*time.Millisecond), start.Add(2*time.Second)
		run.Timing = schemas.RunTiming{Started: start, AgentStarted: &agentStart, AgentExited: &agentEnd, Sealed: &sealed}
		y, _ := run.Outcome.Score()
		run.Y = schemas.Observed(y, schemas.EvidenceOutcome)
		l.index.mu.Lock()
		l.index.runs = append(l.index.runs, RunView{Slot: s, Run: run, Sealed: true, SealHash: "sha256:seal"})
		l.index.files[run.RunID] = map[string][]byte{FileMetrics: []byte(`{"cost_usd":{"state":"observed","v":0.004}}`)}
		l.index.mu.Unlock()
	}
	return nil
}

func (l *endpointLauncher) Events(string) (<-chan schemas.LiveEvent, func()) {
	ch := make(chan schemas.LiveEvent)
	close(ch)
	return ch, func() {}
}

type monitorWorld struct {
	t       *testing.T
	agent   *degradingAgent
	env     OrgEnv
	r       *router.Router
	root    string
	notices []string
	got     []livemon.Notice
	mu      sync.Mutex
}

func newMonitorWorld(t *testing.T) *monitorWorld {
	t.Helper()
	oldCompat := evalCompat
	evalCompat = func(*schemas.AgentConfig, *schemas.Suite) (bool, []schemas.Capability, string) { return true, nil, "" }
	t.Cleanup(func() { evalCompat = oldCompat })
	w := &monitorWorld{t: t, agent: newDegradingAgent(t), root: t.TempDir()}
	writeFiles(t, w.root, map[string]string{
		"agents/support-bot.yaml": "id: support-bot\nadapter: remote_http\nremote:\n  url: " + w.agent.srv.URL + "\n",
	})
	ps, err := projectfs.Open(w.root)
	if err != nil {
		t.Fatal(err)
	}
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{}, files: map[string]map[string][]byte{}}
	tasks := []string{"t01", "t02", "t03", "t04", "t05", "t06", "t07", "t08"}
	suites := &fakeSuites{ids: []string{"svc-bench"}, suites: map[string]*schemas.Suite{
		"svc-bench": {ID: "svc-bench", Version: "1.0.0", TaskIDs: tasks}}}
	w.env = OrgEnv{Cfg: &lib.Config{Root: w.root}, Mode: schemas.ModeRecorded, Services: Services{
		Projects: ps, Index: idx, Suites: suites, Launcher: &endpointLauncher{t: t, url: w.agent.srv.URL, index: idx}}}
	w.r = router.New()
	for _, a := range OrgAreas() {
		a(w.env, w.r)
	}
	old := MonitorNotifier
	MonitorNotifier = func(_ OrgEnv, _ livemon.Monitor, n livemon.Notice) {
		w.mu.Lock()
		w.notices = append(w.notices, n.Incident.Kind+":"+n.Event)
		w.got = append(w.got, n)
		w.mu.Unlock()
	}
	t.Cleanup(func() { MonitorNotifier = old })
	return w
}

// fire does what the ticker and schedule.fire do for one check: load the
// monitor's schedule from the extras, start its evaluation, then collect.
func (w *monitorWorld) fire(m livemon.Monitor) {
	w.t.Helper()
	f := &schedule.File{}
	MonitorSchedules(w.root, f)
	s, ok := f.Find(m.ScheduleName())
	if !ok {
		w.t.Fatalf("schedule %s missing from extras", m.ScheduleName())
	}
	if err := s.Validate(w.root, nil); err != nil {
		w.t.Fatalf("schedule invalid: %v", err)
	}
	spec, err := schedule.LoadEvaluationSpec(w.root, s.Evaluation)
	if err != nil {
		w.t.Fatal(err)
	}
	ev, err := LaunchEvaluation(context.Background(), w.env, *spec, "schedule:"+s.Name, s.Name)
	if err != nil {
		w.t.Fatalf("launch: %v", err)
	}
	if len(ev.Children) != 1 || ev.Children[0].ExperimentID == "" {
		w.t.Fatalf("children: %+v", ev.Children)
	}
	if _, err := CollectMonitors(context.Background(), w.env, time.Now()); err != nil {
		w.t.Fatalf("collect: %v", err)
	}
}

func (w *monitorWorld) detail(id string) MonitorDetail {
	w.t.Helper()
	ctx := evalCall(w.r, "GET", LiveMonitorsPath+"/"+id, "")
	if ctx.Response.StatusCode() != 200 {
		w.t.Fatalf("get: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var d MonitorDetail
	if err := json.Unmarshal(ctx.Response.Body(), &d); err != nil {
		w.t.Fatal(err)
	}
	return d
}

// TestMonitorAgainstDegradingEndpoint runs a monitor end to end against a
// real HTTP endpoint that answers well, then wrongly, recovers, goes down
// and comes back: one quality incident and one infra incident, each opened
// and resolved once, with links to the failing runs.
func TestMonitorAgainstDegradingEndpoint(t *testing.T) {
	w := newMonitorWorld(t)
	ctx := evalCall(w.r, "POST", LiveMonitorsPath, `{"agent":"support-bot","benchmark":"svc-bench","tasks":5,"interval_min":60,"budget_usd_per_day":12}`)
	if ctx.Response.StatusCode() != 201 {
		t.Fatalf("create: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var created MonitorDetail
	if err := json.Unmarshal(ctx.Response.Body(), &created); err != nil {
		t.Fatal(err)
	}
	m := created.Monitor
	if len(created.Tasks) != 5 || created.CheckBudgetUSD != 0.5 || created.Status != "no_data" {
		t.Fatalf("created: tasks %v budget %v status %s", created.Tasks, created.CheckBudgetUSD, created.Status)
	}
	for i := 0; i < 6; i++ {
		w.fire(m)
	}
	if d := w.detail(m.ID); d.Status != "healthy" || len(d.Samples) != 6 || *d.Day.PassRate != 1 {
		t.Fatalf("healthy: status %s samples %d", d.Status, len(d.Samples))
	}
	w.agent.mode.Store("wrong")
	w.fire(m)
	if len(w.notices) != 0 {
		t.Fatalf("one bad check alerted: %v", w.notices)
	}
	w.fire(m)
	w.fire(m)
	if strings.Join(w.notices, ",") != "quality:opened" {
		t.Fatalf("after degrading: %v", w.notices)
	}
	q := w.got[0].Incident
	if len(q.FailingRuns) == 0 || q.FailingRuns[0].RunID == "" || len(q.Evaluations) == 0 {
		t.Fatalf("incident links: %+v", q)
	}
	if d := w.detail(m.ID); d.Status != "degraded" || len(d.Open) != 1 {
		t.Fatalf("degraded: %s %d", d.Status, len(d.Open))
	}
	w.agent.mode.Store("good")
	for i := 0; i < 3; i++ {
		w.fire(m)
	}
	if strings.Join(w.notices, ",") != "quality:opened,quality:resolved" {
		t.Fatalf("after recovery: %v", w.notices)
	}
	w.agent.mode.Store("down")
	w.fire(m)
	w.fire(m)
	if strings.Join(w.notices, ",") != "quality:opened,quality:resolved,infra:opened" {
		t.Fatalf("after outage: %v", w.notices)
	}
	infra := w.got[2].Incident
	if infra.Cause != "unreachable" {
		t.Fatalf("outage cause %q", infra.Cause)
	}
	d := w.detail(m.ID)
	if d.Status != "down" || d.Last == nil || d.Last.Reachable == nil || *d.Last.Reachable || d.Last.Status != livemon.SampleInfra {
		t.Fatalf("down: %s %+v", d.Status, d.Last)
	}
	w.agent.mode.Store("good")
	w.fire(m)
	if strings.Join(w.notices, ",") != "quality:opened,quality:resolved,infra:opened,infra:resolved" {
		t.Fatalf("after restore: %v", w.notices)
	}
	d = w.detail(m.ID)
	if d.Status != "healthy" || len(d.Incidents) != 2 || d.Day.Checks != 15 || d.Day.InfraChecks != 2 {
		t.Fatalf("final: %s incidents %d checks %d infra %d", d.Status, len(d.Incidents), d.Day.Checks, d.Day.InfraChecks)
	}
	if d.Day.P50MS == nil || *d.Day.P50MS != 1200 || d.Day.CostUSD <= 0 {
		t.Fatalf("latency/cost: %+v", d.Day)
	}
	// Collecting again adds nothing: one sample per evaluation.
	if n, err := CollectMonitors(context.Background(), w.env, time.Now()); err != nil || n != 0 {
		t.Fatalf("second collect: %d %v", n, err)
	}
}

func TestRecordSample(t *testing.T) {
	m := livemon.Monitor{BudgetUSDPerDay: 3}
	at := time.Now()
	tests := []struct {
		rec    schedule.Record
		ok     bool
		status string
	}{
		{schedule.Record{At: at, Status: schedule.StatusRefused, Reason: "daily cap: spent 3"}, true, livemon.SampleSkipped},
		{schedule.Record{At: at, Status: schedule.StatusError, Reason: "evaluation .heimdall/x does not exist"}, true, livemon.SampleError},
		{schedule.Record{At: at, Status: schedule.StatusError, Result: &schedule.Result{EvaluationID: "ev_x"}}, false, ""},
		{schedule.Record{At: at, Status: schedule.StatusOK}, false, ""},
	}
	for i, tc := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s, ok := recordSample(tc.rec, m)
			if ok != tc.ok || s.Status != tc.status {
				t.Fatalf("got %v %q", ok, s.Status)
			}
		})
	}
}

func TestMonitorRunInfra(t *testing.T) {
	rv := RunView{Slot: schemas.Slot{Task: "t1"}, Run: schemas.Run{RunID: "r1", Outcome: schemas.OutcomeCrash,
		Failures: []schemas.PartFailure{{Error: "POST https://agent: dial tcp: connection refused"}}}}
	r := monitorRun(rv, nil)
	if !r.Infra || r.Pass != nil {
		t.Fatalf("crash on a refused connection is infra: %+v", r)
	}
	rv.Run.Outcome, rv.Run.Failures = schemas.OutcomeFail, nil
	r = monitorRun(rv, nil)
	if r.Infra || r.Pass == nil || *r.Pass {
		t.Fatalf("a wrong answer is a failure: %+v", r)
	}
}
