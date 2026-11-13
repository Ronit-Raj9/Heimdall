package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// world holds the fake parts' shared switches and call counters. A test sets
// fail["<op>"] to make the part behind that op return an error.
type world struct {
	mu        sync.Mutex
	fail      map[string]bool
	calls     map[string]int
	timedOut  bool
	gradePass bool
	invalid   bool
	veto      bool
	cost      float64
	execs     map[string]int // slot id -> Exec calls
	agents    []schemas.AgentConfig
	seq       []string // every op in call order
	plans     []schemas.FaultPlan
}

func newWorld() *world {
	return &world{fail: map[string]bool{}, calls: map[string]int{}, gradePass: true, execs: map[string]int{}}
}

func (w *world) hit(op string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls[op]++
	w.seq = append(w.seq, op)
	if w.fail[op] {
		return fmt.Errorf("injected %s failure", op)
	}
	return nil
}

func (w *world) count(op string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls[op]
}

type part struct{ name string }

func (p part) GetName() string    { return p.name }
func (p part) GetVersion() string { return "1.0.0-test" }

type fakeEnv struct {
	part
	w *world
}

func (e fakeEnv) Create(_ context.Context, rc *schemas.RunContext) (*schemas.EnvironmentHandle, error) {
	if err := e.w.hit("create"); err != nil {
		return nil, err
	}
	e.w.mu.Lock()
	e.w.agents = append(e.w.agents, *rc.Agent)
	e.w.mu.Unlock()
	return &schemas.EnvironmentHandle{Volume: "vol-" + rc.Run.RunID, Workdir: "/workspace"}, nil
}

func (e fakeEnv) Exec(_ context.Context, rc *schemas.RunContext, _ *schemas.RunPlan) (*schemas.ExecResult, error) {
	e.w.mu.Lock()
	e.w.execs[rc.Slot.SlotID]++
	e.w.mu.Unlock()
	if err := e.w.hit("exec"); err != nil {
		return nil, err
	}
	now := time.Now()
	return &schemas.ExecResult{TimedOut: e.w.timedOut, Started: now, Ended: now.Add(time.Second),
		StdoutPath: filepath.Join(rc.Dir, "agent.stream.jsonl")}, nil
}

func (e fakeEnv) ExportDiff(_ context.Context, rc *schemas.RunContext) (*schemas.Diff, error) {
	if err := e.w.hit("export"); err != nil {
		return nil, err
	}
	return &schemas.Diff{Path: filepath.Join(rc.Dir, "workspace.diff"), SHA256: "d1"}, nil
}

func (e fakeEnv) Destroy(context.Context, *schemas.RunContext) error { return e.w.hit("destroy") }

type fakeAdapter struct {
	part
	w *world
}

func (a fakeAdapter) Plan(_ context.Context, rc *schemas.RunContext) (*schemas.RunPlan, error) {
	if err := a.w.hit("plan"); err != nil {
		return nil, err
	}
	return &schemas.RunPlan{Image: rc.Agent.Image, Argv: []string{"agent"},
		Env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "sk-secret", "OPENAI_API_KEY": "sk-2", "PLAIN": "visible"}}, nil
}

type fakeParser struct{ part }

func (fakeParser) Parse(context.Context, *schemas.RunContext, []byte) (*schemas.ParseResult, error) {
	return &schemas.ParseResult{}, nil
}

type fakeLayer struct {
	part
	w *world
}

func (l fakeLayer) Steps() []schemas.ReceiptStep {
	return []schemas.ReceiptStep{schemas.StepAssigned, schemas.StepArmed, schemas.StepVisible, schemas.StepEffect}
}

func (l fakeLayer) Arm(_ context.Context, rc *schemas.RunContext) (*schemas.Receipt, error) {
	if err := l.w.hit("arm"); err != nil {
		return nil, err
	}
	return &schemas.Receipt{ProbeID: rc.Probe.ID, Steps: map[schemas.ReceiptStep]schemas.ReceiptEntry{
		schemas.StepArmed: {State: schemas.StepYes}}}, nil
}

func (l fakeLayer) Disarm(context.Context, *schemas.RunContext) error { return l.w.hit("disarm") }

func (l fakeLayer) Receipts(_ context.Context, rc *schemas.RunContext, _ *schemas.Evidence) (*schemas.Receipt, error) {
	if err := l.w.hit("receipts"); err != nil {
		return nil, err
	}
	return &schemas.Receipt{ProbeID: rc.Probe.ID, Steps: map[schemas.ReceiptStep]schemas.ReceiptEntry{
		schemas.StepEffect: {State: schemas.StepYes}}}, nil
}

type fakeSource struct {
	part
	w *world
}

func (s fakeSource) Collect(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) error {
	if err := s.w.hit("collect"); err != nil {
		return err
	}
	ev.Claim = &schemas.Claim{Made: true, Text: "done", Source: schemas.EvidenceNative}
	ev.Requests = &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{{ID: "row1", Raw: json.RawMessage(`{"big":true}`)}}}
	return nil
}

type fakeGrader struct {
	part
	w *world
}

func (g fakeGrader) Grade(context.Context, *schemas.RunContext, *schemas.Diff) (*schemas.Grade, error) {
	if err := g.w.hit("grade"); err != nil {
		return nil, err
	}
	return &schemas.Grade{Passed: g.w.gradePass, InvalidEvaluator: g.w.invalid, TestsRan: 3}, nil
}

type fakeGate struct {
	part
	w        *world
	baseline bool
}

func (g fakeGate) Effect() schemas.GateEffect { return schemas.GateVeto }
func (g fakeGate) Baseline() bool             { return g.baseline }

func (g fakeGate) Check(context.Context, *schemas.RunContext, *schemas.Evidence) (*schemas.GateResult, error) {
	if err := g.w.hit("gate"); err != nil {
		return nil, err
	}
	return &schemas.GateResult{Fired: g.w.veto && g.baseline}, nil
}

type fakeMetric struct {
	part
	w *world
}

func (m fakeMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{Name: m.name, Kind: schemas.KindCount, MinEvidence: schemas.EvidenceOutcome}
}

func (m fakeMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if err := m.w.hit("measure"); err != nil {
		return schemas.Value{}, err
	}
	if m.name == costMetric {
		return schemas.Observed(m.w.cost, schemas.EvidenceGateway), nil
	}
	if ev.Grade != nil && ev.Grade.Passed {
		return schemas.Observed(1, schemas.EvidenceOutcome), nil
	}
	return schemas.Observed(0, schemas.EvidenceOutcome), nil
}

type fakeGateway struct {
	part
	w      *world
	active map[string]bool
}

func (g *fakeGateway) Preflight(context.Context) (*schemas.GatewayManifest, error) {
	if err := g.w.hit("preflight"); err != nil {
		return nil, err
	}
	return &schemas.GatewayManifest{Name: "fake", Version: "1.0.0", ConfigSHA256: "cfg"}, nil
}

func (g *fakeGateway) MintKey(_ context.Context, rc *schemas.RunContext) (*schemas.RunKey, error) {
	if err := g.w.hit("mint"); err != nil {
		return nil, err
	}
	return &schemas.RunKey{ID: "vk-" + rc.Run.RunID, Value: "sk-virtual", KeySlot: "fp-slot-1"}, nil
}

func (g *fakeGateway) RevokeKey(context.Context, *schemas.RunKey) error { return g.w.hit("revoke") }

func (g *fakeGateway) RevokeStale(_ context.Context, active map[string]bool) (int, error) {
	g.active = active
	return 2, g.w.hit("revoke_stale")
}

func (g *fakeGateway) FetchRequests(context.Context, *schemas.RunKey, schemas.FetchExpectation) (*schemas.RequestLog, error) {
	return &schemas.RequestLog{Complete: true}, nil
}

type fakeFault struct{ w *world }

func (f fakeFault) SetPlan(_ context.Context, _ string, plan schemas.FaultPlan) error {
	f.w.mu.Lock()
	f.w.plans = append(f.w.plans, plan)
	f.w.mu.Unlock()
	return f.w.hit("fault_set")
}

func (f fakeFault) ClearPlan(context.Context, string) error { return f.w.hit("fault_clear") }

func (f fakeFault) Ledger(context.Context, string) ([]schemas.LedgerEntry, error) { return nil, nil }

type fakeLoader struct {
	part
	suite *schemas.Suite
}

func (l fakeLoader) Load(_ context.Context, ref string) (*schemas.Suite, error) {
	if ref != l.suite.ID+"@"+l.suite.Version && ref != l.suite.ID {
		return nil, fmt.Errorf("unknown suite %s", ref)
	}
	return l.suite, nil
}

// fakeStore is a minimal run-folder store: files on disk under a temp root,
// transitions and leases in memory.
type fakeStore struct {
	part
	w         *world
	root      string
	mu        sync.Mutex
	manifests map[string]*schemas.Manifest
	runs      map[string]*fakeRun
	bySlot    map[string][]string
	leases    map[string]string
}

type fakeRun struct {
	run    schemas.Run
	dir    string
	trans  []schemas.Transition
	sealed bool
}

func newFakeStore(w *world, root string) *fakeStore {
	return &fakeStore{part: part{"fake"}, w: w, root: root, manifests: map[string]*schemas.Manifest{},
		runs: map[string]*fakeRun{}, bySlot: map[string][]string{}, leases: map[string]string{}}
}

func (s *fakeStore) Root() string { return s.root }

func (s *fakeStore) CreateExperiment(_ context.Context, m *schemas.Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.manifests[m.ExpID]; ok {
		return schemas.ErrExists
	}
	s.manifests[m.ExpID] = m
	return nil
}

func (s *fakeStore) LoadManifest(_ context.Context, expID string) (*schemas.Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.manifests[expID]
	if !ok {
		return nil, schemas.ErrNotFound
	}
	return m, nil
}

func (s *fakeStore) ListExperiments(context.Context) ([]string, error) { return nil, nil }

func (s *fakeStore) NewRun(_ context.Context, slot *schemas.Slot, run *schemas.Run) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.root, slot.ExpID, "slots", slot.SlotID, "runs", run.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	s.runs[run.RunID] = &fakeRun{run: *run, dir: dir}
	s.bySlot[slot.SlotID] = append(s.bySlot[slot.SlotID], run.RunID)
	return dir, nil
}

func (s *fakeStore) get(runID string) (*fakeRun, error) {
	r, ok := s.runs[runID]
	if !ok {
		return nil, schemas.ErrNotFound
	}
	if r.sealed {
		return nil, schemas.ErrSealed
	}
	return r, nil
}

func (s *fakeStore) AppendTransition(_ context.Context, run *schemas.Run, t schemas.Transition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(run.RunID)
	if err != nil {
		return err
	}
	r.trans = append(r.trans, t)
	return nil
}

func (s *fakeStore) WriteRunFile(_ context.Context, run *schemas.Run, name string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(run.RunID)
	if err != nil {
		return err
	}
	if name == "run.json" {
		if err := json.Unmarshal(data, &r.run); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(r.dir, name), data, 0o644)
}

func (s *fakeStore) Lease(_ context.Context, slotID, owner string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.leases[slotID]; ok && o != owner {
		return false, nil
	}
	s.leases[slotID] = owner
	return true, nil
}

func (s *fakeStore) Heartbeat(context.Context, string, string) error { return nil }

func (s *fakeStore) Release(_ context.Context, slotID, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[slotID] == owner {
		delete(s.leases, slotID)
	}
	return nil
}

func (s *fakeStore) Seal(_ context.Context, run *schemas.Run) (*schemas.SealRecord, error) {
	if err := s.w.hit("seal"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(run.RunID)
	if err != nil {
		return nil, err
	}
	r.sealed = true
	return &schemas.SealRecord{RunID: run.RunID, ManifestHash: run.ManifestHash, SealedAt: time.Now()}, nil
}

func (s *fakeStore) Verify(context.Context, string, string) error { return nil }

func (s *fakeStore) SlotRuns(_ context.Context, _ string, slotID string) ([]schemas.RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []schemas.RunRecord
	for _, id := range s.bySlot[slotID] {
		r := s.runs[id]
		out = append(out, schemas.RunRecord{Run: r.run, Dir: r.dir, Sealed: r.sealed,
			Transitions: append([]schemas.Transition(nil), r.trans...)})
	}
	return out, nil
}

func (s *fakeStore) SealedRuns(context.Context, string, string) ([]schemas.SealedRun, error) {
	return nil, nil
}

func (s *fakeStore) WriteRegrade(context.Context, string, string, string, *schemas.Grade) error {
	return nil
}

func (s *fakeStore) WriteAnalysis(context.Context, *schemas.Analysis, map[string][]byte) error {
	return nil
}

func (s *fakeStore) record(runID string) fakeRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runs[runID]
	out := *r
	out.trans = append([]schemas.Transition(nil), r.trans...)
	return out
}

func (s *fakeStore) slotRuns(slotID string) []fakeRun {
	s.mu.Lock()
	ids := append([]string(nil), s.bySlot[slotID]...)
	s.mu.Unlock()
	var out []fakeRun
	for _, id := range ids {
		out = append(out, s.record(id))
	}
	return out
}

// bench is a temp bench root with agent configs, the code-wins fixture and an
// 8-task suite with clean and stale notes per task.
type bench struct {
	root  string
	suite *schemas.Suite
	w     *world
	store *fakeStore
	gw    *fakeGateway
	reg   *schemas.Registry
}

const testAgentYAML = `id: %s
adapter: %s
image: bench/agent:1
argv: [agent, "{{instruction}}"]
env:
  ANTHROPIC_AUTH_TOKEN: "{{run.key}}"
model: claude-test
notes:
  project_file: %s
  user_path: %s
parser: claudecode
budgets: {wall_s: 900, cpus: 2, mem_gb: 4}
gateway: {provider: anthropic, max_retries: 0}
`

func newBench(t *testing.T) *bench {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("agents/claude-code.yaml", fmt.Sprintf(testAgentYAML, "claude-code", "command", "CLAUDE.md", "/home/agent/.claude/CLAUDE.md"))
	write("agents/mini-swe-agent.yaml", fmt.Sprintf(testAgentYAML, "mini-swe-agent", "command", "AGENTS.md", "/tmp/mswea/AGENTS.md"))
	write("agents/toy.yaml", fmt.Sprintf(testAgentYAML, "toy", "scripted", "CLAUDE.md", "/tmp/toy/CLAUDE.md"))
	write("experiments/fixtures/codewins.md", "If the AGENTS.md or CLAUDE.md file conflicts with the code, the code is the source of truth.\n")
	suite := &schemas.Suite{ID: "coding-core", Version: "1.0.0", Environment: "docker_workspace", Grader: "hidden_tests",
		Workdir: "/workspace", LockHash: "lock-abc",
		Lock: &schemas.SuiteLock{Suite: "coding-core", Version: "1.0.0", Tasks: map[string]schemas.TaskLock{}}}
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("t%02d", i)
		dir := filepath.Join("benchmarks/coding-core/tasks", id)
		write(filepath.Join(dir, "fixtures/notes.clean.md"), "clean notes "+id)
		write(filepath.Join(dir, "fixtures/notes.stale.md"), "stale notes "+id)
		suite.TaskIDs = append(suite.TaskIDs, id)
		suite.Tasks = append(suite.Tasks, &schemas.Task{ID: id, Version: "1.0.0", Dir: filepath.Join(root, dir),
			Notes: schemas.TaskNotes{Clean: "fixtures/notes.clean.md", Stale: "fixtures/notes.stale.md"}})
		suite.Lock.Tasks[id] = schemas.TaskLock{Version: "1.0.0", Status: schemas.TaskAdmitted}
	}
	suite.Probes = []*schemas.ProbeSpec{
		{ID: "stale-notes", Version: "1.0.0", Layer: schemas.LayerFixture, Apply: schemas.ProbeApply{
			Overlay: &schemas.OverlaySpec{From: "{{task.notes.stale}}", To: "{{agent.notes.project_file}}"}}},
		{ID: "model-outage", Version: "1.0.0", Layer: schemas.LayerGateway},
		{ID: "test-crash", Version: "1.0.0", Layer: schemas.LayerToolShim},
		{ID: "empty-test-run", Version: "1.0.0", Layer: schemas.LayerToolShim},
	}
	for _, p := range suite.Probes {
		suite.ProbeIDs = append(suite.ProbeIDs, p.ID)
	}
	b := &bench{root: root, suite: suite, w: newWorld()}
	b.store = newFakeStore(b.w, filepath.Join(root, "runs"))
	b.gw = &fakeGateway{part: part{"bifrost"}, w: b.w}
	b.reg = b.registry()
	return b
}

func (b *bench) registry() *schemas.Registry {
	w := b.w
	return &schemas.Registry{
		Loaders:      []schemas.SuiteLoader{fakeLoader{part{"local"}, b.suite}},
		Environments: []schemas.Environment{fakeEnv{part{"docker_workspace"}, w}},
		Adapters:     []schemas.AgentAdapter{fakeAdapter{part{"command"}, w}, fakeAdapter{part{"scripted"}, w}},
		Parsers:      []schemas.Parser{fakeParser{part{"claudecode"}}},
		ProbeLayers: []schemas.ProbeLayer{fakeLayer{part{"fixture"}, w}, fakeLayer{part{"gateway"}, w},
			fakeLayer{part{"tool_shim"}, w}},
		Evidence: []schemas.EvidenceSource{fakeSource{part{"agent_stream"}, w}},
		Graders:  []schemas.Grader{fakeGrader{part{"hidden_tests"}, w}},
		Gates:    []schemas.Gate{fakeGate{part{"g1"}, w, true}, fakeGate{part{"g9"}, w, false}},
		Metrics:  []schemas.Metric{fakeMetric{part{"verified_success"}, w}, fakeMetric{part{costMetric}, w}},
		Gateway:  b.gw,
		Fault:    fakeFault{w},
		Store:    b.store,
	}
}

func loadExperiment(t *testing.T, path string) *schemas.Experiment {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var e schemas.Experiment
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("%s does not match schemas.Experiment: %v", path, err)
	}
	return &e
}

// testExperiment is a small two-arm experiment on two tasks.
func testExperiment(conds ...schemas.ConditionSpec) *schemas.Experiment {
	if len(conds) == 0 {
		conds = []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 1}}
	}
	return &schemas.Experiment{
		Name: "unit", Suite: "coding-core@1.0.0", Base: "agents/claude-code.yaml",
		Arms: map[string]schemas.ArmSpec{
			"A": {Label: "baseline"},
			"B": {Label: "note", Set: map[string]any{"notes.user_file": "experiments/fixtures/codewins.md"}},
		},
		Primary:    schemas.Endpoint{Condition: conds[0].ID, Metric: "verified_success"},
		Conditions: conds,
		Seed:       7,
	}
}

func (b *bench) plan(t *testing.T, e *schemas.Experiment) *schemas.Manifest {
	t.Helper()
	m, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return m
}

func TestPartVersions(t *testing.T) {
	b := newBench(t)
	got := PartVersions(b.reg)
	for _, k := range []string{"loader/local", "environment/docker_workspace", "adapter/command", "parser/claudecode",
		"probe_layer/fixture", "evidence/agent_stream", "grader/hidden_tests", "gate/g1", "metric/cost_usd",
		"gateway/bifrost", "store/fake"} {
		if got[k] != "1.0.0-test" {
			t.Errorf("parts[%s] = %q", k, got[k])
		}
	}
	if len(PartVersions(nil)) != 0 {
		t.Error("nil registry should give no parts")
	}
}

func TestSelectGatesAndMetrics(t *testing.T) {
	b := newBench(t)
	tests := []struct {
		name  string
		extra []string
		want  []string
	}{
		{"baseline only", nil, []string{"g1"}},
		{"extra added", []string{"g9"}, []string{"g1", "g9"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, g := range selectGates(b.reg, tt.extra) {
				got = append(got, g.GetName())
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	if n := len(selectMetrics(b.reg, []string{costMetric})); n != 1 {
		t.Fatalf("metric subset: got %d metrics", n)
	}
	if n := len(selectMetrics(b.reg, nil)); n != 2 {
		t.Fatalf("all metrics: got %d", n)
	}
}

func TestLoadSuite(t *testing.T) {
	b := newBench(t)
	if _, err := LoadSuite(context.Background(), b.reg, "coding-core@1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSuite(context.Background(), b.reg, "nope@1"); err == nil {
		t.Fatal("unknown suite loaded")
	}
	if _, err := LoadSuite(context.Background(), &schemas.Registry{}, "x"); err == nil {
		t.Fatal("no loader should fail")
	}
}

func transitionsOf(r fakeRun) []string {
	var out []string
	for _, t := range r.trans {
		out = append(out, string(t.Stage)+":"+string(t.Status))
	}
	return out
}

func hasTransition(r fakeRun, stage schemas.Stage, status schemas.TransitionStatus) bool {
	for _, t := range r.trans {
		if t.Stage == stage && t.Status == status {
			return true
		}
	}
	return false
}

func slotsWhere(m *schemas.Manifest, f func(schemas.Slot) bool) []schemas.Slot {
	var out []schemas.Slot
	for _, s := range m.Slots {
		if f(s) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrderIndex < out[j].OrderIndex })
	return out
}
