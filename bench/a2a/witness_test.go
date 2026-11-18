package a2a

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func ms(v float64) *float64 { return &v }

// teamRun is a run with an entry agent and one worker on its own key.
func teamRun() *schemas.A2ARun {
	return &schemas.A2ARun{
		Entry:     "hb-r-1-lead",
		Owned:     []string{"hb-r-1-lead", "hb-r-1-writer"},
		Members:   map[string]string{"hb-r-1-lead": Lead, "hb-r-1-writer": "writer"},
		KeyMember: map[string]string{"vk-run": Lead, "vk-writer": "writer"},
	}
}

const completed = `{"task":{"id":"t-lead","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"artifactId":"answer","parts":[{"text":"Leeds; Parcelwing"}]}]}}`

func TestBuildWitnessesTeam(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	rows := []schemas.A2ALogRow{
		{ID: "op1", Timestamp: t0, RecordKind: "request", Operation: "SendMessage", Status: "success", AgentName: "hb-r-1-lead", VirtualKeyID: "vk-run", TaskID: "t-lead", Latency: ms(40), ResponseBody: completed},
		// The lead delegates to the writer with the run key it was forwarded.
		{ID: "op2", Timestamp: t0.Add(10 * time.Millisecond), RecordKind: "request", Operation: "SendMessage", Status: "success", AgentName: "hb-r-1-writer", VirtualKeyID: "vk-run", TaskID: "t-w", Latency: ms(5),
			RequestBody: `{"message":{"parts":[{"text":"who carries parcels?"}]}}`, ResponseBody: `{"task":{"id":"t-w","status":{"state":"TASK_STATE_COMPLETED"}}}`},
		{ID: "ev1", Timestamp: t0.Add(time.Millisecond), RecordKind: "event", Operation: "SendStreamingMessage", AgentName: "hb-r-1-lead", TaskID: "t-lead", TaskState: "TASK_STATE_WORKING"},
		{ID: "other", Timestamp: t0, RecordKind: "request", Operation: "SendMessage", AgentName: "someone-else", VirtualKeyID: "vk-run"},
		// Another caller of the same registration is not the run's.
		{ID: "stranger", Timestamp: t0, RecordKind: "request", Operation: "SendMessage", Status: "error", AgentName: "hb-r-1-lead", VirtualKeyID: "vk-stranger", TaskID: "t-x"},
		{ID: "ev-x", Timestamp: t0, RecordKind: "event", AgentName: "hb-r-1-lead", TaskID: "t-x", TaskState: "TASK_STATE_FAILED"},
	}
	rep := &schemas.A2ALauncherReport{TaskID: "t-lead", FinalState: "TASK_STATE_COMPLETED", AnswerSHA256: hashText("Leeds; Parcelwing"),
		Operations: []schemas.A2ALaunchOperation{{Method: "SendMessage", RPCID: "heimdall-1", TaskID: "t-lead", Status: 200}}}
	linked := []schemas.A2ALinkedRow{{Kind: "llm", ID: "l1", TaskID: "t-w", VirtualKeyID: "vk-writer"}, {Kind: "llm", ID: "l2", TaskID: "t-lead", VirtualKeyID: "vk-stranger"}}
	ev := Build(Input{Run: teamRun(), RunKeyID: "vk-run", Launcher: rep, Rows: rows, Linked: linked})
	if !ev.Complete || ev.Strength != StrengthWitnessed {
		t.Fatalf("want a complete witness, got %+v", ev)
	}
	if len(ev.Operations) != 2 || ev.StreamEvents != 1 || ev.Foreign != 1 || ev.TaskStates["t-x"] != "" {
		t.Fatalf("operations %d events %d foreign %d, want 2, 1 and 1 (rows of other registrations and other keys dropped)", len(ev.Operations), ev.StreamEvents, ev.Foreign)
	}
	if ev.Operations[0].Caller != CallerBench || ev.Operations[1].Caller != Lead {
		t.Fatalf("callers %q %q, want bench and lead", ev.Operations[0].Caller, ev.Operations[1].Caller)
	}
	if len(ev.Hops) != 1 || ev.Hops[0].From != Lead || ev.Hops[0].To != "writer" || ev.Hops[0].Source != "gateway_key" {
		t.Fatalf("hops %+v", ev.Hops)
	}
	if ev.Agreement == nil || ev.Agreement.AnswerMatches == nil || !*ev.Agreement.AnswerMatches || ev.Agreement.GatewayState != "TASK_STATE_COMPLETED" {
		t.Fatalf("agreement %+v", ev.Agreement)
	}
	if !ev.Linked[0].OnRunKey || ev.Linked[0].Member != "writer" || ev.Linked[1].OnRunKey {
		t.Fatalf("linked %+v", ev.Linked)
	}
	msgs := Messages(ev, rows)
	if len(msgs) != 1 || msgs[0].From != Lead || msgs[0].To != "writer" || msgs[0].Body != "who carries parcels?" || msgs[0].Fields["source"] != "agent_gateway" {
		t.Fatalf("messages %+v", msgs)
	}
}

func TestBuildIncompleteWhenLauncherOperationMissing(t *testing.T) {
	rep := &schemas.A2ALauncherReport{TaskID: "t1", Operations: []schemas.A2ALaunchOperation{{Method: "SendMessage", TaskID: "t1"}, {Method: "GetTask", TaskID: "t1"}}}
	rows := []schemas.A2ALogRow{{ID: "op1", RecordKind: "request", Operation: "SendMessage", Status: "success", AgentName: "hb-r-1-lead", VirtualKeyID: "vk-run", TaskID: "t1"}}
	ev := Build(Input{Run: teamRun(), RunKeyID: "vk-run", Launcher: rep, Rows: rows})
	if ev.Complete || ev.Strength != StrengthPartial || len(ev.Unmatched) != 1 || ev.Unmatched[0].Method != "GetTask" {
		t.Fatalf("want a partial witness with GetTask unmatched, got %+v", ev)
	}
}

func TestBuildDisagreement(t *testing.T) {
	rep := &schemas.A2ALauncherReport{TaskID: "t-lead", FinalState: "TASK_STATE_COMPLETED", AnswerSHA256: hashText("something else"),
		Operations: []schemas.A2ALaunchOperation{{Method: "SendMessage", TaskID: "t-lead"}}}
	rows := []schemas.A2ALogRow{{ID: "op1", RecordKind: "request", Operation: "SendMessage", Status: "success", AgentName: "hb-r-1-lead", VirtualKeyID: "vk-run", TaskID: "t-lead", ResponseBody: completed}}
	ev := Build(Input{Run: teamRun(), RunKeyID: "vk-run", Launcher: rep, Rows: rows})
	if ev.Agreement == nil || ev.Agreement.AnswerMatches == nil || *ev.Agreement.AnswerMatches || ev.Agreement.Note == "" {
		t.Fatalf("want a disagreement, got %+v", ev.Agreement)
	}
}

func TestFaultRecovery(t *testing.T) {
	rep := &schemas.A2ALauncherReport{TaskID: "t1", FinalState: "TASK_STATE_COMPLETED", Fault: &schemas.A2AFaultFired{Stream: "drop", AfterEvents: 1, Fired: true},
		Operations: []schemas.A2ALaunchOperation{{Method: "SendStreamingMessage", TaskID: "t1"}, {Method: "GetTask", TaskID: "t1"}}}
	rows := []schemas.A2ALogRow{
		{ID: "op1", RecordKind: "request", Operation: "SendStreamingMessage", Status: "success", AgentName: "hb-r-1-lead", VirtualKeyID: "vk-run", TaskID: "t1"},
		{ID: "op2", RecordKind: "request", Operation: "GetTask", Status: "success", AgentName: "hb-r-1-lead", VirtualKeyID: "vk-run", TaskID: "t1", ResponseBody: `{"id":"t1","status":{"state":"TASK_STATE_COMPLETED"}}`},
	}
	ev := Build(Input{Run: teamRun(), RunKeyID: "vk-run", Launcher: rep, Rows: rows, Fault: &schemas.A2AFault{Stream: "drop", AfterEvents: 1}})
	if ev.Fault == nil || !ev.Fault.Observed || !ev.Fault.Recovered {
		t.Fatalf("want an observed, recovered fault, got %+v", ev.Fault)
	}
	ev = Build(Input{Run: teamRun(), RunKeyID: "vk-run", Launcher: rep, Rows: rows[:1], Fault: &schemas.A2AFault{Stream: "drop"}})
	if ev.Fault.Recovered {
		t.Fatalf("no GetTask in the agent log must not count as recovered: %+v", ev.Fault)
	}
}

func TestNoRegistration(t *testing.T) {
	ev := Build(Input{})
	if ev.Complete || ev.Strength != StrengthNone || ev.Reason == "" {
		t.Fatalf("got %+v", ev)
	}
}

func TestResponseTask(t *testing.T) {
	cases := []struct{ body, state, answer string }{
		{completed, "TASK_STATE_COMPLETED", "Leeds; Parcelwing"},
		{`{"id":"t","status":{"state":"TASK_STATE_FAILED","message":{"parts":[{"text":"no"}]}}}`, "TASK_STATE_FAILED", "no"},
		{`{"message":{"role":"ROLE_AGENT","parts":[{"text":"hi"}]}}`, "message", "hi"},
		{`{"task":{"status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"parts":[{"text":"a,b","filename":"r.csv"}]}]}}`, "TASK_STATE_COMPLETED", ""},
		{``, "", ""},
		{`not json`, "", ""},
	}
	for _, c := range cases {
		st, ans := responseTask(c.body)
		if st != c.state || ans != hashText(c.answer) {
			t.Errorf("responseTask(%q) = %q %q, want %q %q", c.body, st, ans, c.state, hashText(c.answer))
		}
	}
}

func TestReadLauncherReportTakesTheLast(t *testing.T) {
	p := filepath.Join(t.TempDir(), "stdout")
	data := `{"type":"result"}
{"type":"a2a_report","task_id":"first"}
not json "a2a_report"
{"type":"a2a_report","task_id":"second","final_state":"TASK_STATE_COMPLETED"}
`
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := ReadLauncherReport(p)
	if rep == nil || rep.TaskID != "second" {
		t.Fatalf("got %+v", rep)
	}
	if ReadLauncherReport(filepath.Join(t.TempDir(), "missing")) != nil {
		t.Fatal("a missing file has no report")
	}
}
