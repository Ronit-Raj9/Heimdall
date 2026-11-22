package clusters

import (
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// sealed returns a sealed run of one outcome on task t01, arm A, clean.
func sealed(id string, outcome schemas.Outcome) schemas.SealedRun {
	return schemas.SealedRun{
		Slot:    schemas.Slot{SlotID: "s_" + id, ExpID: "e_1", Suite: "coding-core", Task: "t01", Arm: "A", Condition: schemas.ConditionClean, Counted: true},
		Run:     schemas.Run{RunID: id, ExpID: "e_1", Outcome: outcome, Agent: schemas.RunAgentInfo{ConfigID: "codex", ConfigHash: "h1"}},
		Metrics: map[string]schemas.Value{},
	}
}

// failing returns a fail run whose grade lists the tests.
func failing(id string, tests ...string) schemas.SealedRun {
	r := sealed(id, schemas.OutcomeFail)
	r.Grade = &schemas.Grade{FailedTests: tests}
	return r
}

func ev(src schemas.EventSource, seq int, typ schemas.EventType, tool, summary string, isErr bool) schemas.Event {
	return schemas.Event{EventID: string(src) + ":" + string(rune('a'+seq)), Source: src, Seq: seq, Type: typ, ToolName: tool, Summary: summary, IsError: isErr}
}

func TestExtractFailedSet(t *testing.T) {
	stateRun := failing("r", "check_refund_issued")
	stateRun.Grade.Mode = "state"
	dimRun := sealed("r", schemas.OutcomeFail)
	dimRun.Grade = &schemas.Grade{Dimensions: map[string]float64{"correctness": 1, "safety": 0.4, "performance": 0.2}}
	catRun := sealed("r", schemas.OutcomeFail)
	catRun.Grade = &schemas.Grade{Mode: "answer", Category: "numeric"}
	missing := failing("r", "tests.t::b")
	missing.Grade.MissingRequired = []string{"tests.t::a", "tests.t::b"}
	timeout := failing("r", "tests.t::a")
	timeout.Run.Outcome = schemas.OutcomeTimeout
	tests := []struct {
		name string
		run  schemas.SealedRun
		want *FailedSet
	}{
		{name: "hidden tests sorted", run: failing("r", "tests.t::z", "tests.t::a"), want: &FailedSet{Kind: LayerFailedTests, IDs: []string{"tests.t::a", "tests.t::z"}}},
		{name: "missing required joins", run: missing, want: &FailedSet{Kind: LayerFailedTests, IDs: []string{"tests.t::a", "tests.t::b"}}},
		{name: "state checks", run: stateRun, want: &FailedSet{Kind: LayerStateChecks, IDs: []string{"check_refund_issued"}}},
		{name: "dimensions below 1", run: dimRun, want: &FailedSet{Kind: LayerFailedDimensions, IDs: []string{"performance", "safety"}}},
		{name: "answer category", run: catRun, want: &FailedSet{Kind: LayerAnswerCategory, IDs: []string{"numeric"}}},
		{name: "no grade", run: sealed("r", schemas.OutcomeFail), want: nil},
		{name: "only for fail", run: timeout, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Extract(Input{Run: tt.run}).Failed
			if (got == nil) != (tt.want == nil) || (got != nil && (got.Kind != tt.want.Kind || !slices.Equal(got.IDs, tt.want.IDs))) {
				t.Fatalf("failed set %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExtractFingerprint(t *testing.T) {
	crash := sealed("r", schemas.OutcomeCrash)
	partFail := sealed("r", schemas.OutcomeInfraError)
	partFail.Run.Failures = []schemas.PartFailure{{Stage: schemas.StageGrade, Part: "cleanroom", Error: "container 3f2504e0aa exited 137"}}
	refusal := sealed("r", schemas.OutcomeRefusal)
	refusal.Run.Claim = &schemas.Claim{Text: "I can't help with deleting production data."}
	invalid := sealed("r", schemas.OutcomeInvalidEvaluator)
	invalid.Grade = &schemas.Grade{Reason: "required test tests/test_x.py::test_y is not in the report"}
	tests := []struct {
		name string
		in   Input
		want *Fingerprint
	}{
		{name: "part failure first", in: Input{Run: partFail, Stderr: "fatal: other"},
			want: &Fingerprint{Source: SourcePartFailure, Tool: "cleanroom", Text: "container <hex> exited 137"}},
		{name: "tool proxy error", in: Input{Run: crash, Tools: []schemas.ToolCall{
			{Seq: 1, Server: "desk", Tool: "lookup", Status: 200},
			{Seq: 2, Server: "desk", Tool: "refund", Status: 500, IsError: true, Result: "Internal error id 99"},
			{Seq: 3, Server: "desk", Tool: "lookup", Status: 200}}},
			want: &Fingerprint{Source: SourceToolProxy, Tool: "desk.refund", Text: "500 internal error id <n>"}},
		{name: "terminal agent error", in: Input{Run: crash, Events: []schemas.Event{
			ev(schemas.SourceAgentStream, 1, schemas.EventError, "", "error: Model metadata not found", true),
			ev(schemas.SourceAgentStream, 2, schemas.EventToolCall, "bash", "", false),
			ev(schemas.SourceAgentStream, 3, schemas.EventError, "", "turn failed: unexpected status 503 ref nonce_5l7m7njw691pcbwiqhszkmqf", true)}},
			want: &Fingerprint{Source: SourceAgent, Text: "turn failed: unexpected status 503 ref <id>"}},
		{name: "agent error carried past is ignored", in: Input{Run: crash, Events: []schemas.Event{
			ev(schemas.SourceAgentStream, 1, schemas.EventError, "", "error: Model metadata not found", true),
			ev(schemas.SourceAgentStream, 2, schemas.EventToolCall, "bash", "", false)}},
			want: nil},
		{name: "gateway model error", in: Input{Run: crash, Events: []schemas.Event{
			ev(schemas.SourceGateway, 1, schemas.EventModelResponse, "", "ok", false),
			ev(schemas.SourceGateway, 2, schemas.EventModelResponse, "", "error 503: overloaded call 3", true)}},
			want: &Fingerprint{Source: SourceModel, Text: "error 503: overloaded call <n>"}},
		{name: "gateway incomplete marker is not an error", in: Input{Run: crash, Events: []schemas.Event{
			ev(schemas.SourceAgentStream, 1, schemas.EventError, "", "gateway request log incomplete: 3 rows", true)}},
			want: nil},
		{name: "stderr needs an error word", in: Input{Run: crash, Stderr: "Reading additional input from stdin...\nError: ENOSPC at /tmp/x.log\n"},
			want: &Fingerprint{Source: SourceStderr, Text: "error: enospc at <path>"}},
		{name: "crash falls back to the final message", in: Input{Run: withClaim(crash, "Segfault in worker 3")},
			want: &Fingerprint{Source: SourceFinalMessage, Text: "segfault in worker <n>"}},
		{name: "refusal reads the final message", in: Input{Run: refusal, Stderr: "fatal: x"},
			want: &Fingerprint{Source: SourceFinalMessage, Text: "i can't help with deleting production data."}},
		{name: "invalid evaluator reads the grader reason", in: Input{Run: invalid},
			want: &Fingerprint{Source: SourceGrader, Text: "required test <path>::test_y is not in the report"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Extract(tt.in).Error
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Fatalf("fingerprint %+v, want %+v", got, tt.want)
			}
		})
	}
}

func withClaim(r schemas.SealedRun, text string) schemas.SealedRun {
	r.Run.Claim = &schemas.Claim{Text: text}
	return r
}

func TestExtractSignals(t *testing.T) {
	t.Run("loop from pattern_loop", func(t *testing.T) {
		r := sealed("r", schemas.OutcomeTimeout)
		r.Metrics["pattern_loop"] = schemas.Value{State: schemas.ValueObserved, V: ptr(1),
			Reason: `events: r:gateway:3,r:gateway:4; Bash "pytest -q tests/test_7.py" repeated 3 times in a row`}
		if got := Extract(Input{Run: r}).Loop; got != `bash "pytest -q <path>"` {
			t.Fatalf("loop %q", got)
		}
		r.Metrics["pattern_loop"] = schemas.Value{State: schemas.ValueObserved, V: ptr(0), Reason: "no repeated tool call in 4 calls"}
		if got := Extract(Input{Run: r}).Loop; got != "" {
			t.Fatalf("loop %q, want none", got)
		}
	})
	t.Run("provider status skips injected faults", func(t *testing.T) {
		ledger := []schemas.LedgerEntry{{Kind: "message", Action: "pass", Status: 200}, {Kind: "message", Action: "inject", Status: 503}}
		if got := Extract(Input{Run: sealed("r", schemas.OutcomeCrash), Ledger: ledger}).Provider; got != "" {
			t.Fatalf("provider %q, want none (the 503 was injected)", got)
		}
		ledger = append(ledger, schemas.LedgerEntry{Kind: "message", Action: "pass", Status: 529}, schemas.LedgerEntry{Kind: "models", Status: 200})
		if got := Extract(Input{Run: sealed("r", schemas.OutcomeCrash), Ledger: ledger}).Provider; got != "529" {
			t.Fatalf("provider %q, want 529", got)
		}
		ledger = append(ledger, schemas.LedgerEntry{Kind: "message", Action: "pass", UpstreamError: "dial tcp: timeout"})
		if got := Extract(Input{Run: sealed("r", schemas.OutcomeCrash), Ledger: ledger}).Provider; got != "transport error" {
			t.Fatalf("provider %q", got)
		}
	})
	t.Run("last tool", func(t *testing.T) {
		events := []schemas.Event{
			ev(schemas.SourceGateway, 1, schemas.EventToolCall, "Bash", "", false),
			ev(schemas.SourceAgentStream, 1, schemas.EventToolCall, "Bash", "", false),
			ev(schemas.SourceAgentStream, 2, schemas.EventToolResult, "Bash", "exit 1: 2 failed", false),
		}
		lt := Extract(Input{Run: sealed("r", schemas.OutcomeFail), Events: events}).LastTool
		if lt == nil || lt.Name != "Bash" || lt.Errored == nil || !*lt.Errored {
			t.Fatalf("last tool %+v, want Bash errored", lt)
		}
		lt = Extract(Input{Run: sealed("r", schemas.OutcomeFail), Events: events[:1]}).LastTool
		if lt == nil || lt.Errored != nil {
			t.Fatalf("gateway last tool %+v, want unknown outcome", lt)
		}
		lt = Extract(Input{Run: sealed("r", schemas.OutcomeFail), Events: events, Tools: []schemas.ToolCall{{Server: "desk", Tool: "refund", Status: 200}}}).LastTool
		if lt == nil || lt.Name != "desk.refund" || *lt.Errored {
			t.Fatalf("tool proxy last tool %+v", lt)
		}
	})
	t.Run("gates fired, sorted", func(t *testing.T) {
		r := sealed("r", schemas.OutcomeIntegrityViolation)
		r.Gates = []schemas.GateResult{{Gate: "z_gate", Fired: true}, {Gate: "quiet", Fired: false}, {Gate: "impossible_pass", Fired: true}}
		if got := Extract(Input{Run: r}).Gates; !slices.Equal(got, []string{"impossible_pass", "z_gate"}) {
			t.Fatalf("gates %v", got)
		}
	})
}

func TestLayersAndTitle(t *testing.T) {
	yes := true
	tests := []struct {
		name  string
		f     Features
		kinds []string
		title string
	}{
		{name: "fail on one test", f: Features{Outcome: "fail", Failed: &FailedSet{Kind: LayerFailedTests, IDs: []string{"tests.test_limiter::test_limiter_window"}}},
			kinds: []string{LayerOutcome, LayerFailedTests}, title: "fails test_limiter_window"},
		{name: "fail with error", f: Features{Outcome: "fail", Failed: &FailedSet{Kind: LayerFailedTests, IDs: []string{"a.py::test_a", "a.py::test_b"}},
			Error: &Fingerprint{Source: SourceModel, Text: "error 503"}},
			kinds: []string{LayerOutcome, LayerFailedTests, LayerError}, title: "fails test_a and 1 more; model error: error 503"},
		{name: "timeout after a loop", f: Features{Outcome: "timeout", Loop: `bash "pytest -q"`, Error: &Fingerprint{Source: SourceStderr, Text: "segmentation fault"}},
			kinds: []string{LayerOutcome, LayerLoop, LayerError}, title: `timeout after repeated bash "pytest -q": stderr: segmentation fault`},
		{name: "integrity gate alone", f: Features{Outcome: "integrity_violation", Gates: []string{"impossible_pass"}, Error: &Fingerprint{Source: SourceAgent, Text: "x"}},
			kinds: []string{LayerOutcome, LayerGates}, title: "impossible_pass gate"},
		{name: "tool error", f: Features{Outcome: "crash", Error: &Fingerprint{Source: SourceToolProxy, Tool: "desk.refund", Text: "500"}},
			kinds: []string{LayerOutcome, LayerError}, title: "crash: tool error: desk.refund 500"},
		{name: "infra error with provider", f: Features{Outcome: "infra_error", Provider: "503"},
			kinds: []string{LayerOutcome, LayerProvider}, title: "infra_error: provider 503"},
		{name: "fallback to last tool errored", f: Features{Outcome: "fail", LastTool: &LastTool{Name: "Bash", Errored: &yes}, Condition: "test-crash"},
			kinds: []string{LayerOutcome, LayerLastTool}, title: "fail: tool Bash errored"},
		{name: "fallback to condition", f: Features{Outcome: "budget", Condition: "model-outage"},
			kinds: []string{LayerOutcome, LayerCondition}, title: "budget: under model-outage"},
		{name: "clean with nothing", f: Features{Outcome: "fail", Condition: "clean"}, kinds: []string{LayerOutcome}, title: "Failed with no error recorded"},
		{name: "refusal ignores non-message errors", f: Features{Outcome: "refusal", Error: &Fingerprint{Source: SourceStderr, Text: "x"}},
			kinds: []string{LayerOutcome}, title: "Refused the task"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layers := Layers(tt.f)
			var kinds []string
			for _, l := range layers {
				kinds = append(kinds, l.Kind)
			}
			if !slices.Equal(kinds, tt.kinds) {
				t.Fatalf("layers %v, want %v", kinds, tt.kinds)
			}
			if got := Title(layers); got != tt.title {
				t.Fatalf("title %q, want %q", got, tt.title)
			}
		})
	}
}

// TestTitleOutcomeOnly (J13.groups): a signature with only its outcome
// layer reads as a sentence, never the bare outcome id, and says where its
// runs happened when they share one benchmark or a last tool.
func TestTitleOutcomeOnly(t *testing.T) {
	outcome := func(o string) []Layer { return Layers(Features{Outcome: o}) }
	for _, tt := range []struct {
		layers          []Layer
		benchmark, tool string
		want            string
	}{
		{outcome("fail"), "", "", "Failed with no error recorded"},
		{outcome("timeout"), "", "", "Timed out"},
		{outcome("crash"), "", "", "Crashed"},
		{outcome("fail"), "swe-lite", "", "Failed with no error recorded on swe-lite"},
		{outcome("timeout"), "swe-lite", "Bash", "Timed out on swe-lite in Bash"},
		{outcome("crash"), "", "Bash", "Crashed in Bash"},
		{outcome("unknown"), "", "", "unknown"},
		// A signature that names what failed keeps its title.
		{Layers(Features{Outcome: "crash", Error: &Fingerprint{Source: SourceToolProxy, Tool: "desk.refund", Text: "500"}}), "swe-lite", "Bash",
			"crash: tool error: desk.refund 500"},
	} {
		if got := TitleIn(tt.layers, tt.benchmark, tt.tool); got != tt.want {
			t.Errorf("TitleIn(%v, %q, %q) = %q, want %q", tt.layers, tt.benchmark, tt.tool, got, tt.want)
		}
	}
}

func TestSignatureStability(t *testing.T) {
	sig := func(in Input) string { return Signature(Layers(Extract(in))) }
	a := failing("r_a", "tests.t::b", "tests.t::a")
	a.Run.Agent.ExitCode = ptrInt(1)
	b := failing("r_b", "tests.t::a", "tests.t::b")
	b.Slot.Task, b.Slot.Arm, b.Run.Agent.ConfigHash = "t09", "B", "h2"
	errA := []schemas.Event{ev(schemas.SourceAgentStream, 1, schemas.EventError, "", "panic at 2026-11-14T01:00:00Z: index 3 out of range [0x1f]", true)}
	errB := []schemas.Event{ev(schemas.SourceAgentStream, 1, schemas.EventError, "", "panic at 2026-11-15T09:12:44Z: index 17 out of range [0xff]", true)}
	t.Run("same cause, other run, order and volatile text", func(t *testing.T) {
		if x, y := sig(Input{Run: a, Events: errA}), sig(Input{Run: b, Events: errB}); x != y {
			t.Fatalf("signatures differ: %s, %s", x, y)
		}
	})
	t.Run("pinned value", func(t *testing.T) {
		// The signature is part of the API (/clusters/{signature}); a change
		// here breaks every saved link and must be deliberate.
		if got := sig(Input{Run: failing("r", "tests.t::a")}); got != "c_a5e4c40a251f8068" {
			t.Fatalf("signature %s", got)
		}
	})
	t.Run("different failed set", func(t *testing.T) {
		if sig(Input{Run: a}) == sig(Input{Run: failing("r", "tests.t::a")}) {
			t.Fatal("different failed sets share a signature")
		}
	})
	t.Run("different error", func(t *testing.T) {
		other := []schemas.Event{ev(schemas.SourceAgentStream, 1, schemas.EventError, "", "connection refused", true)}
		if sig(Input{Run: a, Events: errA}) == sig(Input{Run: a, Events: other}) {
			t.Fatal("different errors share a signature")
		}
	})
}

func ptr(f float64) *float64 { return &f }

func ptrInt(n int) *int { return &n }
