package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestQueueReasonsInvalidateGate checks that a run a safety check threw out
// (a fired gate with effect invalidate) is queued in Needs review (N257),
// and that a gate that did not fire, or a report-only gate, queues nothing.
func TestQueueReasonsInvalidateGate(t *testing.T) {
	pass := schemas.Run{Outcome: schemas.OutcomePass}
	grade := &schemas.Grade{Passed: true}
	tests := []struct {
		name  string
		gates []schemas.GateResult
		want  string
	}{
		{"fired invalidate gate", []schemas.GateResult{{Gate: "model_fidelity", Effect: schemas.GateInvalidate, Fired: true}}, "invalidated"},
		{"invalidate gate not fired", []schemas.GateResult{{Gate: "model_fidelity", Effect: schemas.GateInvalidate}}, ""},
		{"report gate fired", []schemas.GateResult{{Gate: "canary_echoed", Effect: schemas.GateReport, Fired: true}}, ""},
		{"veto and invalidate", []schemas.GateResult{{Gate: "egress", Effect: schemas.GateVeto, Fired: true}, {Gate: "fault_delivery", Effect: schemas.GateInvalidate, Fired: true}}, "veto,invalidated"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sr := &schemas.SealedRun{Run: pass, Grade: grade, Gates: tc.gates}
			if got := strings.Join(QueueReasons(sr, nil), ","); got != tc.want {
				t.Fatalf("reasons = %q, want %q", got, tc.want)
			}
		})
	}
	found := false
	for _, r := range schemas.ReviewReasons {
		found = found || r == schemas.ReviewReasonInvalidated
	}
	if !found {
		t.Fatal("invalidated is not a listed queue reason, so the Needs review filter refuses it")
	}
}

// TestThrownOutByPlainDetail checks the plain words the queue shows next to
// "Thrown out by a safety check".
func TestThrownOutByPlainDetail(t *testing.T) {
	gates := []schemas.GateResult{
		{Gate: "model_fidelity", Effect: schemas.GateInvalidate, Fired: true, Detail: "7 request(s): 7 served by another model, 0 undeclared fallback, 0 cache hit"},
		{Gate: "fault_delivery", Effect: schemas.GateInvalidate, Fired: true, Detail: "the fault plan never reached the gateway: the agent did not send the heimdall headers"},
		{Gate: "egress", Effect: schemas.GateVeto, Fired: true, Detail: "x"},
		{Gate: "other_gate", Effect: schemas.GateInvalidate, Fired: false, Detail: "y"},
		{Gate: "custom_check", Effect: schemas.GateInvalidate, Fired: true, Detail: "something odd"},
	}
	want := []string{
		"model check: served by another model",
		"fault delivery check: the fault plan never reached the gateway",
		"custom_check: something odd",
	}
	if got := ThrownOutBy(gates); !reflect.DeepEqual(got, want) {
		t.Fatalf("ThrownOutBy = %q, want %q", got, want)
	}
}
