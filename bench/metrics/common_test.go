package metrics

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Test helpers shared by every metric test in the package.

func compute(t *testing.T, m schemas.Metric, rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
	t.Helper()
	got, err := m.Compute(context.Background(), rc, ev)
	if err != nil {
		t.Fatalf("%s: compute: %v", m.GetName(), err)
	}
	return got
}

// checkValue compares state, number, evidence level and bounds exactly, and
// the reason as a substring when want has one. Every unknown must carry a
// reason.
func checkValue(t *testing.T, got, want schemas.Value) {
	t.Helper()
	if got.State != want.State {
		t.Fatalf("state = %q (%+v), want %q", got.State, got, want.State)
	}
	if got.Evidence != want.Evidence {
		t.Errorf("evidence = %q, want %q", got.Evidence, want.Evidence)
	}
	switch {
	case want.V == nil && got.V != nil:
		t.Errorf("v = %v, want nil", *got.V)
	case want.V != nil && got.V == nil:
		t.Errorf("v = nil, want %v", *want.V)
	case want.V != nil && math.Abs(*got.V-*want.V) > 1e-9:
		t.Errorf("v = %v, want %v", *got.V, *want.V)
	}
	switch {
	case want.Bounds == nil && got.Bounds != nil:
		t.Errorf("bounds = %v, want nil", *got.Bounds)
	case want.Bounds != nil && got.Bounds == nil:
		t.Errorf("bounds = nil, want %v", *want.Bounds)
	case want.Bounds != nil && (math.Abs(got.Bounds[0]-want.Bounds[0]) > 1e-9 || math.Abs(got.Bounds[1]-want.Bounds[1]) > 1e-9):
		t.Errorf("bounds = %v, want %v", *got.Bounds, *want.Bounds)
	}
	if got.State == schemas.ValueUnknown && got.Reason == "" {
		t.Errorf("unknown value has no reason")
	}
	if want.Reason != "" && !strings.Contains(got.Reason, want.Reason) {
		t.Errorf("reason = %q, want it to contain %q", got.Reason, want.Reason)
	}
}

func tok(fresh, cacheRead, cacheWrite, out int64) *schemas.Tokens {
	return &schemas.Tokens{Fresh: fresh, CacheRead: cacheRead, CacheWrite: cacheWrite, Out: out}
}

func attempts(n int) []schemas.Attempt {
	out := make([]schemas.Attempt, n)
	for i := range out {
		out[i] = schemas.Attempt{N: i + 1}
	}
	return out
}

func calls(ids ...string) []schemas.ToolCallRef {
	out := make([]schemas.ToolCallRef, len(ids))
	for i, id := range ids {
		out[i] = schemas.ToolCallRef{ID: id, Name: "bash"}
	}
	return out
}

// okMsg is a successful message row with one attempt and usage.
func okMsg(id string) schemas.GatewayRequest {
	return schemas.GatewayRequest{ID: id, Kind: kindMessage, Status: statusSuccess, Tokens: tok(10, 0, 0, 5), Attempts: attempts(1)}
}

func completeLog(reqs ...schemas.GatewayRequest) *schemas.RequestLog {
	return &schemas.RequestLog{Requests: reqs, Complete: true}
}

func passed() *schemas.Grade { return &schemas.Grade{Passed: true} }
func failed() *schemas.Grade { return &schemas.Grade{Passed: false} }

func claim(made bool, source schemas.EvidenceLevel) *schemas.Claim {
	return &schemas.Claim{Made: made, Source: source}
}

func veto(fired bool) schemas.GateResult {
	return schemas.GateResult{Gate: "g1_protected_paths", Effect: schemas.GateVeto, Fired: fired}
}

func TestRequestLogProblem(t *testing.T) {
	tests := []struct {
		name   string
		log    *schemas.RequestLog
		bad    bool
		reason string
	}{
		{name: "nil log", log: nil, bad: true, reason: reasonNoLog},
		{name: "incomplete with reason", log: &schemas.RequestLog{Reason: "2 rows missing"}, bad: true, reason: "2 rows missing"},
		{name: "incomplete without reason", log: &schemas.RequestLog{}, bad: true, reason: reasonIncomplete},
		{name: "complete", log: completeLog(), bad: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, bad := requestLogProblem(tt.log)
			if bad != tt.bad {
				t.Fatalf("bad = %v, want %v", bad, tt.bad)
			}
			if bad {
				checkValue(t, v, schemas.Value{State: schemas.ValueUnknown, Evidence: schemas.EvidenceGateway, Reason: tt.reason})
			}
		})
	}
}

func TestLogicalMessageRequests(t *testing.T) {
	tests := []struct {
		name  string
		reqs  []schemas.GatewayRequest
		roots []string
		ok    []bool
	}{
		{
			name:  "one row per request",
			reqs:  []schemas.GatewayRequest{okMsg("a"), okMsg("b")},
			roots: []string{"a", "b"},
			ok:    []bool{true, true},
		},
		{
			name: "fallback child joins parent",
			reqs: []schemas.GatewayRequest{
				{ID: "a", Kind: kindMessage, Status: "error"},
				{ID: "a-fb", ParentID: "a", FallbackIndex: 1, Kind: kindMessage, Status: statusSuccess},
			},
			roots: []string{"a"},
			ok:    []bool{true},
		},
		{
			name: "chain resolves to first row",
			reqs: []schemas.GatewayRequest{
				{ID: "c", ParentID: "b", Kind: kindMessage, Status: statusSuccess},
				{ID: "b", ParentID: "a", Kind: kindMessage, Status: "error"},
				{ID: "a", Kind: kindMessage, Status: "error"},
			},
			roots: []string{"a"},
			ok:    []bool{true},
		},
		{
			name: "self parent is a root",
			reqs: []schemas.GatewayRequest{
				{ID: "a", ParentID: "a", Kind: kindMessage, Status: statusSuccess},
			},
			roots: []string{"a"},
			ok:    []bool{true},
		},
		{
			name: "missing parent names the request",
			reqs: []schemas.GatewayRequest{
				{ID: "x1", ParentID: "gone", Kind: kindMessage, Status: "error"},
				{ID: "x2", ParentID: "gone", Kind: kindMessage, Status: statusSuccess},
			},
			roots: []string{"gone"},
			ok:    []bool{true},
		},
		{
			name: "cycle terminates",
			reqs: []schemas.GatewayRequest{
				{ID: "p", ParentID: "q", Kind: kindMessage, Status: statusSuccess},
				{ID: "q", ParentID: "p", Kind: kindMessage, Status: statusSuccess},
			},
			roots: []string{"q", "p"},
			ok:    []bool{true, true},
		},
		{
			name: "other kinds skipped",
			reqs: []schemas.GatewayRequest{
				{ID: "ct", Kind: "count_tokens", Status: statusSuccess},
				{ID: "m", Kind: "models", Status: statusSuccess},
				okMsg("a"),
			},
			roots: []string{"a"},
			ok:    []bool{true},
		},
		{
			name: "rows without ids stay separate",
			reqs: []schemas.GatewayRequest{
				{Kind: kindMessage, Status: statusSuccess},
				{Kind: kindMessage, Status: statusProcessing},
			},
			roots: []string{"#0", "#1"},
			ok:    []bool{true, false},
		},
		{name: "empty", reqs: nil, roots: nil, ok: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := logicalMessageRequests(tt.reqs)
			var roots []string
			var ok []bool
			for _, lr := range got {
				roots = append(roots, lr.root)
				ok = append(ok, lr.succeeded)
			}
			if !reflect.DeepEqual(roots, tt.roots) {
				t.Errorf("roots = %v, want %v", roots, tt.roots)
			}
			if !reflect.DeepEqual(ok, tt.ok) {
				t.Errorf("succeeded = %v, want %v", ok, tt.ok)
			}
		})
	}
}

func TestFiredVeto(t *testing.T) {
	tests := []struct {
		name  string
		gates []schemas.GateResult
		want  string
		fired bool
	}{
		{name: "none", gates: nil},
		{name: "veto not fired", gates: []schemas.GateResult{veto(false)}},
		{name: "report fired", gates: []schemas.GateResult{{Gate: "g8", Effect: schemas.GateReport, Fired: true}}},
		{name: "invalidate fired", gates: []schemas.GateResult{{Gate: "egress", Effect: schemas.GateInvalidate, Fired: true}}},
		{name: "veto fired", gates: []schemas.GateResult{veto(false), {Gate: "g2", Effect: schemas.GateVeto, Fired: true}}, want: "g2", fired: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fired := firedVeto(tt.gates)
			if got != tt.want || fired != tt.fired {
				t.Errorf("firedVeto = (%q, %v), want (%q, %v)", got, fired, tt.want, tt.fired)
			}
		})
	}
}

func TestGradeProblem(t *testing.T) {
	tests := []struct {
		name   string
		grade  *schemas.Grade
		bad    bool
		reason string
	}{
		{name: "nil", grade: nil, bad: true, reason: reasonNoGrade},
		{name: "invalid evaluator", grade: &schemas.Grade{InvalidEvaluator: true, Reason: "tests did not run"}, bad: true, reason: "tests did not run"},
		{name: "passed", grade: passed()},
		{name: "failed", grade: failed()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, bad := gradeProblem(tt.grade)
			if bad != tt.bad {
				t.Fatalf("bad = %v, want %v", bad, tt.bad)
			}
			if bad {
				checkValue(t, v, schemas.Value{State: schemas.ValueUnknown, Evidence: schemas.EvidenceOutcome, Reason: tt.reason})
			}
		})
	}
}

func TestClaimLevel(t *testing.T) {
	tests := []struct {
		name  string
		claim *schemas.Claim
		want  schemas.EvidenceLevel
	}{
		{name: "nil", claim: nil, want: schemas.EvidenceOutcome},
		{name: "stdout tail", claim: claim(true, schemas.EvidenceOutcome), want: schemas.EvidenceOutcome},
		{name: "native", claim: claim(true, schemas.EvidenceNative), want: schemas.EvidenceNative},
		{name: "empty source", claim: claim(true, ""), want: schemas.EvidenceOutcome},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claimLevel(tt.claim); got != tt.want {
				t.Errorf("claimLevel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLevelRank(t *testing.T) {
	order := []schemas.EvidenceLevel{"", schemas.EvidenceOutcome, schemas.EvidenceGateway, schemas.EvidenceNative, schemas.EvidenceBoundary}
	for i := 1; i < len(order); i++ {
		t.Run(string(order[i]), func(t *testing.T) {
			if levelRank(order[i-1]) >= levelRank(order[i]) {
				t.Errorf("rank(%q) >= rank(%q)", order[i-1], order[i])
			}
		})
	}
}

func TestOrEmpty(t *testing.T) {
	tests := []struct {
		name string
		ev   *schemas.Evidence
	}{
		{name: "nil", ev: nil},
		{name: "set", ev: &schemas.Evidence{Grade: passed()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := orEmpty(tt.ev)
			if got == nil {
				t.Fatal("orEmpty returned nil")
			}
			if tt.ev != nil && got != tt.ev {
				t.Error("orEmpty replaced non-nil evidence")
			}
		})
	}
}

func TestMessageRows(t *testing.T) {
	tests := []struct {
		name string
		reqs []schemas.GatewayRequest
		want []string
	}{
		{name: "empty", reqs: nil, want: nil},
		{name: "keeps order, drops other kinds", reqs: []schemas.GatewayRequest{okMsg("a"), {ID: "ct", Kind: "count_tokens"}, okMsg("b")}, want: []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, r := range messageRows(tt.reqs) {
				got = append(got, r.ID)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("messageRows ids = %v, want %v", got, tt.want)
			}
		})
	}
}
