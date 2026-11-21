package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

type bridgeTestLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *bridgeTestLog) add(f string, a ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
	l.mu.Unlock()
}
func (l *bridgeTestLog) Printf(f string, a ...any)   { l.add(f, a...) }
func (l *bridgeTestLog) Warnf(f string, a ...any)    { l.add(f, a...) }
func (l *bridgeTestLog) Errorf(f string, a ...any)   { l.add(f, a...) }
func (l *bridgeTestLog) Write(p []byte) (int, error) { l.add("%s", p); return len(p), nil }

// fakeBridgeLauncher reports its experiment active for activeFor polls.
type fakeBridgeLauncher struct {
	mu        sync.Mutex
	activeFor int
	polls     int
	cancelled []string
	events    chan schemas.LiveEvent
}

func (f *fakeBridgeLauncher) Plan(context.Context, *schemas.Experiment) (*schemas.Manifest, error) {
	return nil, errors.New("not used")
}
func (f *fakeBridgeLauncher) Create(context.Context, *schemas.Manifest) error { return nil }
func (f *fakeBridgeLauncher) Start(context.Context, string) error             { return nil }
func (f *fakeBridgeLauncher) Events(string) (<-chan schemas.LiveEvent, func()) {
	return f.events, func() {}
}
func (f *fakeBridgeLauncher) Active(string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	return f.polls <= f.activeFor
}
func (f *fakeBridgeLauncher) Cancel(_ context.Context, expID string) error {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, expID)
	f.mu.Unlock()
	return nil
}

func TestBridgeWaitExperimentEnds(t *testing.T) {
	f := &fakeBridgeLauncher{activeFor: 3, events: make(chan schemas.LiveEvent, 4)}
	f.events <- schemas.LiveEvent{Kind: "slot_sealed", SlotID: "s1", Outcome: schemas.OutcomeFail}
	log := &bridgeTestLog{}
	if err := waitExperiment(context.Background(), f, "e_x", time.Minute, 5*time.Millisecond, log); err != nil {
		t.Fatal(err)
	}
	if f.polls < 3+bridgeIdlePolls {
		t.Fatalf("returned after %d polls, want at least %d", f.polls, 3+bridgeIdlePolls)
	}
	if !strings.Contains(strings.Join(log.lines, "\n"), "slot s1 sealed: fail") {
		t.Fatalf("event not logged: %v", log.lines)
	}
	if len(f.cancelled) != 0 {
		t.Fatalf("cancelled %v", f.cancelled)
	}
}

func TestBridgeWaitExperimentTimeoutCancels(t *testing.T) {
	f := &fakeBridgeLauncher{activeFor: 1 << 30}
	err := waitExperiment(context.Background(), f, "e_x", 30*time.Millisecond, 5*time.Millisecond, &bridgeTestLog{})
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("err = %v", err)
	}
	if len(f.cancelled) != 1 || f.cancelled[0] != "e_x" {
		t.Fatalf("cancelled = %v", f.cancelled)
	}
}

func TestBridgeCheckVerdict(t *testing.T) {
	cases := []struct {
		runs []bridgeRun
		want string
	}{
		{[]bridgeRun{{Outcome: "fail"}, {Outcome: "pass"}}, checkPass},
		{[]bridgeRun{{Outcome: "fail"}, {Outcome: "timeout"}}, checkWarn},
		{[]bridgeRun{{Outcome: "crash"}, {Outcome: "timeout"}}, checkFail},
		{[]bridgeRun{{Outcome: "infra_error"}, {Outcome: "crash"}}, checkError},
		{[]bridgeRun{{Outcome: "pass"}}, checkError}, // one of two sealed
	}
	for i, c := range cases {
		if got, d := checkVerdict(c.runs, 2); got != c.want {
			t.Errorf("case %d: %s (%s), want %s", i, got, d, c.want)
		}
	}
}

func TestBridgeAdmitVerdict(t *testing.T) {
	runs := []bridgeRun{
		{Task: "t01", Arm: "A", Outcome: "pass"}, {Task: "t01", Arm: "B", Outcome: "fail"},
		{Task: "t02", Arm: "A", Outcome: "fail"}, {Task: "t02", Arm: "B", Outcome: "fail"},
		{Task: "t03", Arm: "A", Outcome: "pass"}, {Task: "t03", Arm: "B", Outcome: "pass"},
	}
	got := admitVerdict(runs, []string{"t01", "t02", "t03", "t04"})
	want := map[string]bool{"t01": true, "t02": false, "t03": false, "t04": false}
	if len(got) != 4 {
		t.Fatalf("got %d tasks", len(got))
	}
	for _, g := range got {
		if g.Admitted != want[g.Task] {
			t.Errorf("%s admitted=%v (%s)", g.Task, g.Admitted, g.Reason)
		}
		if !g.Admitted && g.Reason == "" {
			t.Errorf("%s refused without a reason", g.Task)
		}
	}
}

func TestBridgeExperimentsArePlannable(t *testing.T) {
	for _, e := range []*schemas.Experiment{
		checkExperiment("agents/my-agent.yaml", "coding-core@1.0.0", "t01"),
		admitExperiment(hostedSuiteAdmitParams{Suite: "coding-core", Oracle: bridgeOracleAgent, Noop: bridgeNoopAgent}),
	} {
		if !e.NoPaidTraffic() || !e.Smoke {
			t.Errorf("%s: must be a scripted smoke experiment", e.Name)
		}
		if _, ok := e.Arms["A"]; !ok {
			t.Errorf("%s: no arm A", e.Name)
		}
		if _, ok := e.Arms["B"]; !ok {
			t.Errorf("%s: no arm B", e.Name)
		}
	}
	if e := checkExperiment("agents/my-agent.yaml", "coding-core", "t01"); e.Name != "check-my-agent" || len(e.Conditions[0].Tasks.IDs) != 1 {
		t.Fatalf("check experiment %+v", e)
	}
}

func TestBridgeValidators(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"agents/a.yaml", "agents/oracle.yaml", "agents/noop.yaml"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("id: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := jobEnv{ProjectRoot: root, Hosted: true}
	ctx := context.Background()
	ok := func(v func(context.Context, jobEnv, json.RawMessage) (any, string, error), raw string) {
		t.Helper()
		if _, _, err := v(ctx, env, json.RawMessage(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	bad := func(v func(context.Context, jobEnv, json.RawMessage) (any, string, error), raw string) {
		t.Helper()
		if _, _, err := v(ctx, env, json.RawMessage(raw)); !errors.Is(err, jobs.ErrInvalidParams) {
			t.Errorf("%s: err = %v, want invalid params", raw, err)
		}
	}
	ok(validateHostedAgentCheck, `{"agent":"agents/a.yaml","suite":"coding-core@1.0.0"}`)
	bad(validateHostedAgentCheck, `{"agent":"../etc/a.yaml","suite":"coding-core"}`)
	bad(validateHostedAgentCheck, `{"agent":"agents/missing.yaml","suite":"coding-core"}`)
	bad(validateHostedAgentCheck, `{"agent":"agents/a.yaml","suite":"../x"}`)
	bad(validateHostedAgentCheck, `{"agent":"agents/a.yaml","suite":"s","timeout_min":100000}`)
	bad(validateHostedAgentCheck, `{"agent":"agents/a.yaml","suite":"s","extra":1}`)
	ok(validateHostedSuiteAdmit, `{"suite":"coding-core","task":"t01"}`)
	bad(validateHostedSuiteAdmit, `{"suite":"coding-core","oracle":"agents/nope.yaml"}`)
	bad(validateHostedCanaryRun, `{"name":"missing"}`)
	if err := os.MkdirAll(filepath.Join(root, "canaries"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "canaries", "c1.yaml"), []byte("name: c1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok(validateHostedCanaryRun, `{"name":"c1"}`)
	bad(validateHostedCanaryRun, `{"name":"c1","baseline":true,"inject":"model-outage"}`)
}

// TestSuiteAdmitWritesMissingControls: a new org has no oracle or noop
// agent (they are not seeded and users never see them), so the admission
// bridge writes them from the binary on first use, and keeps a file the
// org already has.
func TestSuiteAdmitWritesMissingControls(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "noop.yaml"), []byte("id: noop\n# org copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := jobEnv{ProjectRoot: root, Hosted: true}
	if _, _, err := validateHostedSuiteAdmit(context.Background(), env, json.RawMessage(`{"suite":"coding-core"}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "agents", "oracle.yaml"))
	if err != nil || !strings.Contains(string(b), "kind: oracle") {
		t.Fatalf("oracle control not written: %v %s", err, b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "agents", "noop.yaml")); !strings.Contains(string(b), "# org copy") {
		t.Fatalf("existing noop replaced: %s", b)
	}
	// A path other than the two controls is never written.
	if err := ensureControlAgent(root, "agents/toy.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "toy.yaml")); err == nil {
		t.Fatal("ensureControlAgent wrote a non-control agent")
	}
}

func TestBridgeKindsHostedOnly(t *testing.T) {
	want := map[string]bool{kindAgentCheck: true, kindSuiteAdmit: true, kindCanaryRun: true}
	jobKindsMu.Lock()
	defer jobKindsMu.Unlock()
	for _, k := range jobKinds {
		if want[k.Kind] && k.Hosted {
			if k.Local && k.Kind != kindCanaryRun { // canary.run joins L17's local variant
				t.Errorf("%s bridge must not be local", k.Kind)
			}
			delete(want, k.Kind)
		}
	}
	if len(want) != 0 {
		t.Fatalf("not registered for hosted: %v", want)
	}
}

func TestBridgeRequireRunner(t *testing.T) {
	if err := requireRunner(&fakeBridgeLauncher{}); err == nil {
		t.Fatal("a launcher with no coordinator must be refused")
	}
	if err := requireRunner(&launcher{}); err == nil || !strings.Contains(err.Error(), "runner") {
		t.Fatalf("err = %v", err)
	}
}
