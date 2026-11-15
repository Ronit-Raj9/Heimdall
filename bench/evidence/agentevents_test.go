package evidence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/schemas"
)

// reportedEvidence is timelineEvidence plus agent-reported events: a tool
// call the tool proxy also saw, a subagent with a nested plan, an internal
// tool no trusted witness can see, a model call the gateway saw, and a
// reported tool call whose result disagrees with the ledger.
func reportedEvidence(t *testing.T) *schemas.Evidence {
	t.Helper()
	ev := timelineEvidence()
	lines := strings.Join([]string{
		`{"type":"tool_call","id":"rt1","name":"search","start_time":"2026-11-15T12:00:03.150Z","end_time":"2026-11-15T12:00:03.250Z","status":"ok"}`,
		`{"type":"subagent_start","id":"sa","name":"researcher","start_time":"2026-11-15T12:00:02.600Z"}`,
		`{"type":"plan","id":"pl","parent_id":"sa","output":"read then write","start_time":"2026-11-15T12:00:02.700Z"}`,
		`{"type":"tool_call","id":"rt2","parent_id":"sa","name":"internal_lookup","start_time":"2026-11-15T12:00:04.100Z"}`,
	}, "\n")
	evs, bad := agentevents.FromNativeJSONL([]byte(lines), schemas.ReportChannelNative, agentevents.Options{RunID: "r_1"})
	if bad != 0 {
		t.Fatalf("bad %d", bad)
	}
	ev.Events = append(ev.Events, evs...)
	return ev
}

func TestBuildTimelineJoinsReported(t *testing.T) {
	base, _ := BuildTimeline(timelineEvidence())
	steps, _ := BuildTimeline(reportedEvidence(t))
	if len(steps) != len(base)+4 {
		t.Fatalf("rows %d, want %d", len(steps), len(base)+4)
	}
	countBase, count := 0, 0
	for _, s := range base {
		if s.Counted {
			countBase++
		}
	}
	bySeq := map[int]schemas.StepRecord{}
	byReport := map[string]schemas.StepRecord{}
	for i, s := range steps {
		if s.Seq != i+1 {
			t.Fatalf("seq %d at %d", s.Seq, i)
		}
		bySeq[s.Seq] = s
		if s.Counted {
			count++
		}
		if s.Source == schemas.StepSourceReported {
			if s.Counted || s.Proof != schemas.EvidencePartial || s.Kind != schemas.StepAgentEvent {
				t.Fatalf("reported row %+v", s)
			}
			byReport[s.ReportID] = s
		}
	}
	if count != countBase {
		t.Fatalf("reported rows changed agent_steps rows: %d vs %d", count, countBase)
	}
	// Links survive renumbering: every witness link points at a row whose
	// witnessed_by lists it back (trusted rows) or at a trusted row.
	for _, s := range steps {
		if s.Source != schemas.StepSourceReported && s.WitnessOf > 0 && bySeq[s.WitnessOf].Seq == 0 {
			t.Fatalf("dangling witness_of in %+v", s)
		}
		for _, w := range s.WitnessedBy {
			if bySeq[w].WitnessOf != s.Seq {
				t.Fatalf("witnessed_by %d of row %d does not point back", w, s.Seq)
			}
		}
	}
	rt1 := byReport["rt1"]
	if rt1.Corroboration != schemas.Corroborated || bySeq[rt1.WitnessOf].Source != schemas.StepSourceToolProxy {
		t.Fatalf("rt1 not corroborated by the ledger row: %+v -> %+v", rt1, bySeq[rt1.WitnessOf])
	}
	if rt2 := byReport["rt2"]; rt2.Corroboration != schemas.ReportedOnly || rt2.WitnessOf != 0 || bySeq[rt2.ParentSeq].ReportID != "sa" {
		t.Fatalf("rt2: %+v", rt2)
	}
	if pl := byReport["pl"]; bySeq[pl.ParentSeq].EventType != "subagent_start" || pl.Turn != 1 || pl.Summary != "plan: read then write" {
		t.Fatalf("plan nesting/turn: %+v", pl)
	}
	// A run with no reported events is unchanged.
	again, _ := BuildTimeline(timelineEvidence())
	a, _ := json.Marshal(again)
	b, _ := json.Marshal(base)
	if string(a) != string(b) {
		t.Fatal("timeline without reported events changed")
	}
}

func TestReportedConflictAndUnknownCallID(t *testing.T) {
	ev := timelineEvidence()
	ev.ToolCalls[1].IsError = true
	evs, _ := agentevents.FromNativeJSONL([]byte(
		`{"type":"tool_call","id":"x","name":"search","start_time":"2026-11-15T12:00:03.200Z","status":"ok"}`+"\n"+
			`{"type":"tool_call","id":"y","name":"Bash","tool_call_id":"never-requested","start_time":"2026-11-15T12:00:04.900Z"}`),
		schemas.ReportChannelOTLP, agentevents.Options{RunID: "r_1"})
	ev.Events = append(ev.Events, evs...)
	steps, _ := BuildTimeline(ev)
	var x, y schemas.StepRecord
	for _, s := range steps {
		switch s.ReportID {
		case "x":
			x = s
		case "y":
			y = s
		}
	}
	if x.Corroboration != schemas.ReportedConflicts || !strings.Contains(x.Conflict, "error=true") {
		t.Fatalf("conflict: %+v", x)
	}
	if y.Corroboration != schemas.ReportedOnly || !strings.Contains(y.Conflict, "gateway log does not have") {
		t.Fatalf("unknown call id: %+v", y)
	}
}

func TestAgentEventsSourceCollect(t *testing.T) {
	rc := testRC(t)
	rc.Key = &schemas.RunKey{Value: "sk-bench-run-key-abcdef123456"}
	recs, _ := agentevents.ParseNative([]byte(`{"type":"plan","output":"use sk-bench-run-key-abcdef123456 here"}`+"\n"+`{"type":"subagent_start","name":"w"}`), schemas.ReportChannelNative, agentevents.Limits{}, time.Now())
	var ledger []byte
	for _, r := range recs {
		b, _ := json.Marshal(r)
		ledger = append(append(ledger, b...), '\n')
	}
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(rc.Dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(collectorLedgerFile, string(ledger)+"{torn")
	write(collectorCountsFile, `{"received":5,"kept":2,"dropped":3}`)
	write(collectorStateFile, `{"collector":"on"}`)
	write("agent.stream.jsonl", "hello\n@@heimdall-event {\"type\":\"handoff\",\"name\":\"writer\"}\n")
	rc.Exec = &schemas.ExecResult{StdoutPath: filepath.Join(rc.Dir, "agent.stream.jsonl"), Ended: time.Now()}
	ev := &schemas.Evidence{}
	if err := newAgentEventsSource(Deps{}).Collect(context.Background(), rc, ev); err != nil {
		t.Fatal(err)
	}
	st := ev.AgentEvents
	if st == nil || st.Collector != "on" || st.Kept != 3 || st.Dropped != 4 || st.Channels["native"] != 2 || st.Channels["stdout"] != 1 || st.Kinds["handoff"] != 1 {
		t.Fatalf("status %+v", st)
	}
	raw, err := os.ReadFile(filepath.Join(rc.Dir, schemas.AgentEventsFile))
	if err != nil || strings.Count(string(raw), "\n") != 3 {
		t.Fatalf("agent_events.jsonl: %v %q", err, raw)
	}
	if strings.Contains(string(raw), "sk-bench-run-key") {
		t.Fatal("run key reached agent_events.jsonl")
	}
	if _, err := os.Stat(filepath.Join(rc.Dir, schemas.AgentEventsStatusFile)); err != nil {
		t.Fatal(err)
	}
	// Collector off and nothing reported: no status, metrics read unknown.
	rc2 := testRC(t)
	ev2 := &schemas.Evidence{}
	if err := newAgentEventsSource(Deps{}).Collect(context.Background(), rc2, ev2); err != nil || ev2.AgentEvents != nil {
		t.Fatalf("off: %v %+v", err, ev2.AgentEvents)
	}
}
