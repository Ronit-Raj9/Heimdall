package metrics

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func stepRow(kind, source string, counted bool) schemas.StepRecord {
	return schemas.StepRecord{Kind: kind, Source: source, Counted: counted, TS: time.Unix(1, 0)}
}

func observedAll() []schemas.ObserverStatus {
	return []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverOn}, {Observer: schemas.ObserverFS, State: schemas.ObserverOn, Dropped: 2}}
}

func fullLog(rows ...schemas.GatewayRequest) *schemas.RequestLog {
	return &schemas.RequestLog{Complete: true, Requests: rows}
}

func TestAgentSteps(t *testing.T) {
	turn := stepRow(schemas.StepModelTurn, schemas.StepSourceGateway, true)
	cmd := stepRow(schemas.StepCommand, schemas.StepSourceProcess, true)
	req := stepRow(schemas.StepToolRequest, schemas.StepSourceGateway, true)
	req.Execution = schemas.ExecutedWithProof
	witness := stepRow(schemas.StepCommand, schemas.StepSourceProcess, false)
	witness.WitnessOf = 1
	reported := stepRow(schemas.StepToolRequest, schemas.StepSourceGateway, true)
	reported.Execution = schemas.ExecutedAgentReported
	tests := []struct {
		name  string
		ev    *schemas.Evidence
		state schemas.ValueState
		v     float64
		level schemas.EvidenceLevel
	}{
		{"no gateway log", &schemas.Evidence{Steps: []schemas.StepRecord{turn}}, schemas.ValueUnknown, 0, ""},
		{"old run", &schemas.Evidence{Requests: fullLog()}, schemas.ValueUnknown, 0, ""},
		{"only model turns, observers off", &schemas.Evidence{Requests: fullLog(), Steps: []schemas.StepRecord{turn, turn}}, schemas.ValueUnknown, 0, ""},
		{"only model turns, observers on and saw nothing", &schemas.Evidence{Requests: fullLog(), Steps: []schemas.StepRecord{turn}, Observers: observedAll()}, schemas.ValueObserved, 1, schemas.EvidenceBoundary},
		{"trusted witnesses", &schemas.Evidence{Requests: fullLog(), Steps: []schemas.StepRecord{turn, req, witness, cmd}, Observers: observedAll()}, schemas.ValueObserved, 3, schemas.EvidenceBoundary},
		{"agent-reported lowers the level", &schemas.Evidence{Requests: fullLog(), Steps: []schemas.StepRecord{turn, reported}}, schemas.ValueObserved, 2, schemas.EvidenceNative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := newAgentStepsMetric().Compute(t.Context(), nil, tt.ev)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != tt.state {
				t.Fatalf("value %+v", v)
			}
			if tt.state == schemas.ValueObserved {
				if got, _ := v.Float(); got != tt.v || v.Evidence != tt.level {
					t.Fatalf("value %+v", v)
				}
			} else if v.Reason == "" {
				t.Fatal("unknown without reason")
			}
		})
	}
}

func TestCommandsAndFiles(t *testing.T) {
	root := stepRow(schemas.StepCommand, schemas.StepSourceProcess, true)
	root.PID = 20
	child := stepRow(schemas.StepCommand, schemas.StepSourceProcess, false)
	child.PID, child.PPID, child.WitnessOf, child.TS = 21, 20, 3, time.Unix(2, 0)
	linked := stepRow(schemas.StepCommand, schemas.StepSourceProcess, false)
	linked.PID, linked.PPID, linked.WitnessOf = 40, 7, 9
	runtime := stepRow(schemas.StepCommand, schemas.StepSourceProcess, false)
	runtime.PID, runtime.Op = 7, "runtime"
	ev := &schemas.Evidence{Steps: []schemas.StepRecord{runtime, root, child, linked}, FileEvents: make([]schemas.FileEvent, 3), Observers: observedAll()}
	v, _ := newCommandsExecutedMetric().Compute(t.Context(), nil, ev)
	if got, ok := v.Float(); !ok || got != 2 || v.Evidence != schemas.EvidenceBoundary {
		t.Fatalf("commands %+v", v)
	}
	v, _ = newFilesChangedMetric().Compute(t.Context(), nil, ev)
	if got, ok := v.Float(); !ok || got != 5 {
		t.Fatalf("files %+v (3 events + 2 dropped)", v)
	}
	for _, tt := range []struct {
		name     string
		statuses []schemas.ObserverStatus
		state    schemas.ValueState
	}{
		{"no record", nil, schemas.ValueUnknown},
		{"missing", []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverFailed, Reason: "boom"}}, schemas.ValueUnknown},
		{"one session missing", []schemas.ObserverStatus{{Observer: schemas.ObserverProc, Session: 1, State: schemas.ObserverOn}, {Observer: schemas.ObserverProc, Session: 2, State: schemas.ObserverFailed}}, schemas.ValueUnknown},
		{"remote agent", []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverOff, Reason: "remote agent: x"}}, schemas.ValueNotApplicable},
		{"off", []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverOff, Reason: "switched off"}}, schemas.ValueUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v, _ := newCommandsExecutedMetric().Compute(t.Context(), nil, &schemas.Evidence{Observers: tt.statuses})
			if v.State != tt.state {
				t.Fatalf("value %+v", v)
			}
		})
	}
}

func TestModelAttempts(t *testing.T) {
	ev := &schemas.Evidence{Requests: fullLog(
		schemas.GatewayRequest{ID: "a", Kind: "message", Status: "success", Attempts: []schemas.Attempt{{N: 1}, {N: 2}, {N: 3}}},
		schemas.GatewayRequest{ID: "b", ParentID: "a", Kind: "message", Status: "success", FallbackIndex: 1},
		schemas.GatewayRequest{ID: "c", Kind: "models"},
	)}
	v, _ := newModelAttemptsMetric().Compute(t.Context(), nil, ev)
	if got, ok := v.Float(); !ok || got != 4 {
		t.Fatalf("attempts %+v", v)
	}
	if v, _ := newModelAttemptsMetric().Compute(t.Context(), nil, &schemas.Evidence{}); v.State != schemas.ValueUnknown {
		t.Fatalf("no log %+v", v)
	}
}

func TestToolCallsUnwitnessedAndExecuted(t *testing.T) {
	proven := stepRow(schemas.StepToolRequest, schemas.StepSourceGateway, true)
	proven.Execution = schemas.ExecutedWithProof
	none := stepRow(schemas.StepToolRequest, schemas.StepSourceGateway, false)
	none.Execution = schemas.ExecutionNoWitness
	ledger := stepRow(schemas.StepToolExecution, schemas.StepSourceToolProxy, true)
	ev := &schemas.Evidence{Requests: fullLog(), Steps: []schemas.StepRecord{proven, none, ledger}, Observers: observedAll()}
	v, _ := newToolCallsUnwitnessedMetric().Compute(t.Context(), nil, ev)
	if got, ok := v.Float(); !ok || got != 1 {
		t.Fatalf("unwitnessed %+v", v)
	}
	v, _ = newToolCallsExecutedMetric().Compute(t.Context(), nil, ev)
	if got, ok := v.Float(); !ok || got != 2 || v.Evidence != schemas.EvidenceBoundary {
		t.Fatalf("executed %+v (proven request + unrequested ledger row)", v)
	}
	noWitness := &schemas.Evidence{Requests: fullLog(), Steps: []schemas.StepRecord{none}}
	if v, _ := newToolCallsUnwitnessedMetric().Compute(t.Context(), nil, noWitness); v.State != schemas.ValueUnknown {
		t.Fatalf("no witness source %+v", v)
	}
	if v, _ := newToolCallsExecutedMetric().Compute(t.Context(), nil, noWitness); v.State != schemas.ValueUnknown {
		t.Fatalf("executed with nothing witnessed %+v", v)
	}
	reported := stepRow(schemas.StepToolRequest, schemas.StepSourceGateway, true)
	reported.Execution = schemas.ExecutedAgentReported
	v, _ = newToolCallsExecutedMetric().Compute(t.Context(), nil, &schemas.Evidence{Steps: []schemas.StepRecord{reported}})
	if v.Evidence != schemas.EvidenceNative {
		t.Fatalf("agent-reported only %+v", v)
	}
}
