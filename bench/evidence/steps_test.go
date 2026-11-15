package evidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// timelineEvidence is a run with three model turns (the first retried once),
// a shell tool call the process observer saw, an MCP tool call the tool
// proxy saw, a file tool call nothing saw, a file change inside the shell
// command, a file change no command explains and an agent-reported result.
func timelineEvidence() *schemas.Evidence {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	return &schemas.Evidence{
		Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
			{ID: "q1", Timestamp: at(0), Status: "success", Kind: "message", Provider: "p", ModelServed: "m", LatencyMS: 1000,
				Attempts:  []schemas.Attempt{{N: 1, StatusCode: 503, FailReason: "overloaded"}, {N: 2, StatusCode: 200}},
				ToolCalls: []schemas.ToolCallRef{{ID: "c1", Name: "Bash", Target: "pytest -q"}}},
			{ID: "q2", Timestamp: at(2500), Status: "success", Kind: "message", ModelServed: "m", LatencyMS: 500,
				ToolCalls: []schemas.ToolCallRef{{ID: "c2", Name: "mcp__docs__search", ArgsHash: "h1"}}},
			{ID: "q3", Timestamp: at(4000), Status: "success", Kind: "message", ModelServed: "m", LatencyMS: 500,
				ToolCalls: []schemas.ToolCallRef{{ID: "c3", Name: "Read", Target: "/workspace/missing.txt"}}},
		}},
		RequestedTools: []schemas.ToolCallRecord{{ToolCallID: "c1", Requested: true}, {ToolCallID: "c2", Requested: true}, {ToolCallID: "c3", Requested: true}},
		ToolCalls: []schemas.ToolCall{
			{Seq: 1, Server: "docs", Tool: "tools/list", Protocol: "mcp", Started: at(3100)},
			{Seq: 2, Server: "docs", Tool: "search", Protocol: "mcp", ArgsSHA256: "h1", Started: at(3200), LatencyMS: 40},
		},
		Events: []schemas.Event{
			{Type: schemas.EventToolResult, Source: schemas.SourceAgentStream, ToolCallID: "c1", ToolName: "Bash", TS: at(2100)},
			{Type: schemas.EventToolCall, Source: schemas.SourceAgentStream, ToolCallID: "ghost", ToolName: "Bash", TS: at(4600)},
		},
		Processes: []schemas.ProcessRecord{
			{PID: 1, Start: at(-800), End: at(5000), RunningAtEnd: true, Argv: []string{"sh", "-c", "agent"}},
			{PID: 7, PPID: 1, Start: at(-500), End: at(5000), RunningAtEnd: true, Argv: []string{"node", "agent.js"}},
			{PID: 20, PPID: 7, Start: at(1200), End: at(2000), Argv: []string{"bash", "-c", "pytest -q"}},
			{PID: 21, PPID: 20, Start: at(1250), End: at(1950), Argv: []string{"python", "-m", "pytest", "-q"}},
			{PID: 30, PPID: 7, Start: at(4700), End: at(4800), Argv: []string{"git", "status"}},
		},
		FileEvents: []schemas.FileEvent{
			{TS: at(1800), Op: schemas.FileModify, Path: "junit.xml"},
			{TS: at(4550), Op: schemas.FileCreate, Path: "notes.md"},
			{TS: at(4560), Op: schemas.FileModify, Path: "notes.md"},
		},
		Observers: []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverOn}, {Observer: schemas.ObserverFS, State: schemas.ObserverOn}},
	}
}

func TestBuildTimeline(t *testing.T) {
	ev := timelineEvidence()
	steps, tools := BuildTimeline(ev)
	bySeq := map[int]schemas.StepRecord{}
	counted := 0
	for i, s := range steps {
		if s.Seq != i+1 {
			t.Fatalf("seq %d at %d", s.Seq, i)
		}
		if i > 0 && s.TS.Before(steps[i-1].TS) {
			t.Fatalf("not ordered at %d", s.Seq)
		}
		bySeq[s.Seq] = s
		if s.Counted {
			counted++
		}
	}
	find := func(kind, source string, match func(schemas.StepRecord) bool) schemas.StepRecord {
		t.Helper()
		for _, s := range steps {
			if s.Kind == kind && s.Source == source && match(s) {
				return s
			}
		}
		t.Fatalf("no %s row from %s", kind, source)
		return schemas.StepRecord{}
	}
	c1 := find(schemas.StepToolRequest, schemas.StepSourceGateway, func(s schemas.StepRecord) bool { return s.ToolCallID == "c1" })
	c2 := find(schemas.StepToolRequest, schemas.StepSourceGateway, func(s schemas.StepRecord) bool { return s.ToolCallID == "c2" })
	c3 := find(schemas.StepToolRequest, schemas.StepSourceGateway, func(s schemas.StepRecord) bool { return s.ToolCallID == "c3" })
	if c1.Execution != schemas.ExecutedWithProof || c2.Execution != schemas.ExecutedWithProof || c3.Execution != schemas.ExecutionNoWitness {
		t.Fatalf("executions %s %s %s", c1.Execution, c2.Execution, c3.Execution)
	}
	bash := find(schemas.StepCommand, schemas.StepSourceProcess, func(s schemas.StepRecord) bool { return s.PID == 20 })
	py := find(schemas.StepCommand, schemas.StepSourceProcess, func(s schemas.StepRecord) bool { return s.PID == 21 })
	git := find(schemas.StepCommand, schemas.StepSourceProcess, func(s schemas.StepRecord) bool { return s.PID == 30 })
	node := find(schemas.StepCommand, schemas.StepSourceProcess, func(s schemas.StepRecord) bool { return s.PID == 7 })
	if bash.WitnessOf != c1.Seq || py.WitnessOf != c1.Seq || bash.Counted || py.Counted {
		t.Fatalf("shell tree not linked to c1: bash %+v python %+v", bash, py)
	}
	if !git.Counted || node.Op != "runtime" || node.Counted {
		t.Fatalf("git %+v node %+v", git, node)
	}
	junit := find(schemas.StepFileChange, schemas.StepSourceFS, func(s schemas.StepRecord) bool { return s.Path == "junit.xml" })
	if junit.Counted || junit.WitnessOf != c1.Seq {
		t.Fatalf("junit %+v, c1 seq %d", junit, c1.Seq)
	}
	notes := 0
	for _, s := range steps {
		if s.Path == "notes.md" && s.Counted {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("notes.md counted %d times", notes)
	}
	ledger := find(schemas.StepToolExecution, schemas.StepSourceToolProxy, func(schemas.StepRecord) bool { return true })
	if ledger.WitnessOf != c2.Seq || ledger.Counted || ledger.Turn != 2 {
		t.Fatalf("ledger %+v", ledger)
	}
	ghost := find(schemas.StepAgentEvent, schemas.StepSourceAgent, func(schemas.StepRecord) bool { return true })
	if ghost.Conflict == "" {
		t.Fatal("ghost tool call without conflict")
	}
	retries := 0
	for _, s := range steps {
		if s.Kind == schemas.StepModelAttempt {
			retries++
		}
	}
	if retries != 1 {
		t.Fatalf("attempt rows %d", retries)
	}
	// 3 turns + c1 + c2 + git + notes.md = 7.
	if counted != 7 {
		for _, s := range steps {
			t.Logf("%+v", s)
		}
		t.Fatalf("counted %d, want 7", counted)
	}
	if tools[0].Proof != schemas.ExecutedWithProof || tools[0].ExecutionSource != schemas.EvidenceSourceProcessResult || !tools[0].Executed {
		t.Fatalf("c1 record %+v", tools[0])
	}
	if tools[1].ExecutionSource != schemas.EvidenceSourceToolRunner || tools[2].Proof != schemas.ExecutionNoWitness || tools[2].Executed {
		t.Fatalf("records %+v", tools)
	}
	if ev.RequestedTools[0].Proof != "" {
		t.Fatal("BuildTimeline changed its input")
	}
}

// TestBuildTimelineAgentReported checks a call only the agent stream
// backs, and the conflict when the process observer watched and saw nothing.
func TestBuildTimelineAgentReported(t *testing.T) {
	ev := timelineEvidence()
	ev.Processes = nil
	steps, _ := BuildTimeline(ev)
	for _, s := range steps {
		if s.ToolCallID == "c1" && s.Kind == schemas.StepToolRequest {
			if s.Execution != schemas.ExecutedAgentReported || !strings.Contains(s.Conflict, "process observer") {
				t.Fatalf("c1 %+v", s)
			}
			return
		}
	}
	t.Fatal("no c1 row")
}

// TestBuildTimelineEmptyAndOld checks that a run with no witnesses yields no
// timeline, so old runs keep encoding as before.
func TestBuildTimelineEmptyAndOld(t *testing.T) {
	if s, tools := BuildTimeline(&schemas.Evidence{}); s != nil || tools != nil {
		t.Fatalf("timeline %v %v", s, tools)
	}
	if s, _ := BuildTimeline(nil); s != nil {
		t.Fatal("nil evidence gave a timeline")
	}
}

// TestStepTimelineSourceCollect reads observer files from a run folder and
// writes steps.jsonl.
func TestStepTimelineSourceCollect(t *testing.T) {
	rc := testRC(t)
	ev := timelineEvidence()
	procs, fsevs, statuses := ev.Processes, ev.FileEvents, ev.Observers
	ev.Processes, ev.FileEvents, ev.Observers = nil, nil, nil
	writeLines := func(name string, rows any) {
		var b []byte
		switch r := rows.(type) {
		case []schemas.ProcessRecord:
			for _, x := range r {
				j, _ := json.Marshal(x)
				b = append(append(b, j...), '\n')
			}
		case []schemas.FileEvent:
			for _, x := range r {
				j, _ := json.Marshal(x)
				b = append(append(b, j...), '\n')
			}
		}
		b = append(b, []byte("torn line\n")...)
		if err := os.WriteFile(filepath.Join(rc.Dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLines(schemas.ProcessesFile, procs)
	writeLines(schemas.FSEventsFile, fsevs)
	sb, _ := json.Marshal(statuses)
	if err := os.WriteFile(filepath.Join(rc.Dir, schemas.ObserversFile), sb, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := newStepTimelineSource(Deps{}).Collect(t.Context(), rc, ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Processes) != len(procs) || len(ev.FileEvents) != len(fsevs) || len(ev.Observers) != 2 || len(ev.Steps) == 0 {
		t.Fatalf("collected %d %d %d %d", len(ev.Processes), len(ev.FileEvents), len(ev.Observers), len(ev.Steps))
	}
	b, err := os.ReadFile(filepath.Join(rc.Dir, schemas.StepsFile))
	if err != nil || strings.Count(string(b), "\n") != len(ev.Steps) {
		t.Fatalf("steps.jsonl: %v", err)
	}
	empty := &schemas.Evidence{}
	if err := newStepTimelineSource(Deps{}).Collect(t.Context(), testRC(t), empty); err != nil || empty.Steps != nil || empty.Observers != nil {
		t.Fatalf("empty run: %v %+v", err, empty)
	}
}

func TestToolNameFitsAndRelPath(t *testing.T) {
	for _, tt := range []struct {
		requested, server, tool string
		want                    bool
	}{
		{"search", "docs", "search", true},
		{"mcp__docs__search", "docs", "search", true},
		{"docs.search", "docs", "search", true},
		{"mcp__other__search", "docs", "search", false},
		{"research", "docs", "search", false},
	} {
		if got := toolNameFits(tt.requested, tt.server, tt.tool); got != tt.want {
			t.Errorf("toolNameFits(%q, %q, %q) = %v", tt.requested, tt.server, tt.tool, got)
		}
	}
	for in, want := range map[string]string{"/workspace/src/a.py": "src/a.py", "./b.txt": "b.txt", "pytest -q": "", "c.md": "c.md"} {
		if got := relPath(in); got != want {
			t.Errorf("relPath(%q) = %q, want %q", in, got, want)
		}
	}
}
