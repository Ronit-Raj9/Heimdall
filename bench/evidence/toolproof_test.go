package evidence

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var proofT0 = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func proofAt(ms int) time.Time { return proofT0.Add(time.Duration(ms) * time.Millisecond) }

// proofEvidence is one model turn requesting seven calls, each seen by a
// different set of witnesses, and a final answer that carries two results.
func proofEvidence() *schemas.Evidence {
	return &schemas.Evidence{
		Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
			{ID: "q1", Timestamp: proofAt(0), Status: "success", Kind: "message", LatencyMS: 1000, ToolCalls: []schemas.ToolCallRef{
				{ID: "c-proxy", Name: "mcp__docs__search", ArgsHash: "h1"},
				{ID: "c-proc", Name: "exec_command", Command: "pytest -q"},
				{ID: "c-file", Name: "edit", Paths: "/workspace/src/app.py"},
				{ID: "c-shim", Name: "Bash", Target: "make test"},
				{ID: "c-agent", Name: "Read", Target: "/workspace/README.md"},
				{ID: "c-returned", Name: "Grep", Target: "src"},
				{ID: "c-none", Name: "Glob", Target: "*.py"},
			}},
			{ID: "q2", Timestamp: proofAt(9000), Status: "success", Kind: "message", LatencyMS: 500, ToolResultIDs: []string{"c-returned", "c-proxy"}},
		}},
		RequestedTools: []schemas.ToolCallRecord{{ToolCallID: "c-proxy"}, {ToolCallID: "c-proc"}, {ToolCallID: "c-none"}},
		ToolCalls: []schemas.ToolCall{
			{Seq: 1, Server: "docs", Tool: "search", Protocol: "mcp", ArgsSHA256: "h1", Started: proofAt(1200), LatencyMS: 30},
		},
		ShimLog: []schemas.ShimCall{{Tool: "make", Action: "pass", At: proofAt(1300).Format(time.RFC3339Nano), ToolCallID: "c-shim"}},
		Events: []schemas.Event{
			{Type: schemas.EventToolResult, Source: schemas.SourceAgentStream, ToolCallID: "c-agent", ToolName: "Read", TS: proofAt(1400)},
			{Type: schemas.EventToolResult, Source: schemas.SourceAgentStream, ToolCallID: "c-proxy", ToolName: "search", TS: proofAt(1500)},
		},
		Processes: []schemas.ProcessRecord{
			{PID: 1, Start: proofAt(-900), End: proofAt(9600), RunningAtEnd: true, Argv: []string{"codex"}},
			{PID: 40, PPID: 1, Start: proofAt(1100), End: proofAt(1900), Argv: []string{"/bin/bash", "-lc", "pytest  -q"}},
		},
		FileEvents: []schemas.FileEvent{{TS: proofAt(2000), Op: schemas.FileModify, Path: "src/app.py"}},
		Observers:  []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverOn}, {Observer: schemas.ObserverFS, State: schemas.ObserverOn}},
	}
}

func requestClasses(steps []schemas.StepRecord) map[string]string {
	out := map[string]string{}
	for _, s := range steps {
		if s.Kind == schemas.StepToolRequest {
			out[s.ToolCallID] = s.ProofClass
		}
	}
	return out
}

func TestProofClassPrecedence(t *testing.T) {
	steps, tools := BuildTimeline(proofEvidence())
	want := map[string]string{
		"c-proxy":    schemas.ProofProxy, // ledger beats the agent's result and the returned result
		"c-proc":     schemas.ProofProcess,
		"c-file":     schemas.ProofFileEffect,
		"c-shim":     schemas.ProofShim,
		"c-agent":    schemas.ProofResultReturned,
		"c-returned": schemas.ProofResultReturned,
		"c-none":     schemas.ProofNone,
	}
	if got := requestClasses(steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("classes = %v, want %v", got, want)
	}
	// Classes never change the execution states: a returned result alone is
	// still no witness of execution.
	for _, s := range steps {
		if s.Kind != schemas.StepToolRequest {
			continue
		}
		wantExec := schemas.ExecutedWithProof
		switch s.ToolCallID {
		case "c-agent":
			wantExec = schemas.ExecutedAgentReported
		case "c-returned", "c-none":
			wantExec = schemas.ExecutionNoWitness
		}
		if s.Execution != wantExec {
			t.Errorf("%s execution %s, want %s", s.ToolCallID, s.Execution, wantExec)
		}
	}
	gotTools := map[string]string{}
	for _, r := range tools {
		gotTools[r.ToolCallID] = r.ProofClass
	}
	if !reflect.DeepEqual(gotTools, map[string]string{"c-proxy": schemas.ProofProxy, "c-proc": schemas.ProofProcess, "c-none": schemas.ProofNone}) {
		t.Fatalf("requested tool records = %v", gotTools)
	}
}

func TestCommandFits(t *testing.T) {
	tests := []struct {
		argv, want string
		fits       bool
	}{
		{"bash -lc pytest -q", "pytest -q", true},
		{"bash -lc pytest   -q", "pytest -q", true},
		// Claude Code wraps the command in eval '...' and re-quotes it.
		{`/bin/bash -c -l source /tmp/snap.sh && eval 'grep -R "foo bar" .' < /dev/null`, `grep -R "foo bar" .`, true},
		{`bash -c eval 'echo '"'"'hi'"'"''`, `echo 'hi'`, true},
		{"bash -lc ls -la", "pytest", false},
		{"bash -lc ls", "", false},
	}
	for _, tt := range tests {
		if got := commandFits(tt.argv, tt.want); got != tt.fits {
			t.Errorf("commandFits(%q, %q) = %v, want %v", tt.argv, tt.want, got, tt.fits)
		}
	}
}

func TestPathFits(t *testing.T) {
	if !pathFits("src/app.py", []string{"/workspace/src/app.py"}) || !pathFits("src/app.py", []string{"./src/app.py"}) {
		t.Error("workspace path not matched")
	}
	if !pathFits("app.py", []string{"/repo/app.py"}) {
		t.Error("absolute path outside /workspace not matched by suffix")
	}
	if !pathFits("b.py", []string{"a.py", "b.py"}) {
		t.Error("second path of a patch not matched")
	}
	if pathFits("src/app.py", []string{"pytest -q"}) || pathFits("x", []string{""}) || pathFits("x", nil) {
		t.Error("matched a command or an empty path")
	}
}

// TestCodexStreamIDs: Codex's exec stream numbers its calls item_N while the
// model's are call_...; the stream's results pair with the gateway's calls by
// what they name, then in order, and no false conflict is raised.
func TestCodexStreamIDs(t *testing.T) {
	ev := &schemas.Evidence{
		Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
			{ID: "q1", Timestamp: proofAt(0), Status: "success", Kind: "message", LatencyMS: 500, ToolCalls: []schemas.ToolCallRef{{ID: "call_A", Name: "exec_command", Command: "sed -n 1,5p a.py"}}},
			{ID: "q2", Timestamp: proofAt(1000), Status: "success", Kind: "message", LatencyMS: 500, ToolCalls: []schemas.ToolCallRef{{ID: "call_B", Name: "exec_command"}}, ToolResultIDs: []string{"call_A"}},
			{ID: "q3", Timestamp: proofAt(2000), Status: "success", Kind: "message", LatencyMS: 500, ToolResultIDs: []string{"call_A", "call_B"}},
		}},
		Events: []schemas.Event{
			{Type: schemas.EventToolCall, Source: schemas.SourceAgentStream, ToolCallID: "item_1", ToolName: "exec_command", Target: "/bin/sh -c \"sed -n 1,5p a.py\"", TS: proofAt(3000)},
			{Type: schemas.EventToolResult, Source: schemas.SourceAgentStream, ToolCallID: "item_1", ToolName: "exec_command", TS: proofAt(3000)},
			{Type: schemas.EventToolCall, Source: schemas.SourceAgentStream, ToolCallID: "item_2", ToolName: "apply_patch", Target: "/workspace/b.py", TS: proofAt(3000)},
			{Type: schemas.EventToolResult, Source: schemas.SourceAgentStream, ToolCallID: "item_2", ToolName: "apply_patch", TS: proofAt(3000)},
		},
	}
	if got := streamIDAliases(ev); !reflect.DeepEqual(got, map[string]string{"item_1": "call_A", "item_2": "call_B"}) {
		t.Fatalf("aliases = %v", got)
	}
	steps, _ := BuildTimeline(ev)
	for _, s := range steps {
		if s.Conflict != "" {
			t.Errorf("false conflict on %s %s: %s", s.Kind, s.ToolCallID, s.Conflict)
		}
		if s.Kind == schemas.StepToolRequest && (s.Execution != schemas.ExecutedAgentReported || s.ProofClass != schemas.ProofResultReturned) {
			t.Errorf("%s: execution %s class %s", s.ToolCallID, s.Execution, s.ProofClass)
		}
		if s.Kind == schemas.StepToolExecution && s.Counted {
			t.Errorf("stream result %s counted on its own, so twice", s.ToolCallID)
		}
	}
	// One namespace: ids that match are matched by id, no aliases.
	ev.Events[0].ToolCallID = "call_A"
	if got := streamIDAliases(ev); got != nil {
		t.Fatalf("aliases with a shared namespace = %v", got)
	}
}

// TestParallelIdenticalCalls: two identical calls in one turn and two
// identical commands link one to one; a retried call (same id on the
// fallback row) is one request.
func TestParallelIdenticalCalls(t *testing.T) {
	ev := &schemas.Evidence{
		Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
			{ID: "q1", Timestamp: proofAt(0), Status: "error", Kind: "message", LatencyMS: 100, ToolCalls: []schemas.ToolCallRef{{ID: "c1", Name: "Bash", Target: "pytest -q"}}},
			{ID: "q1f", ParentID: "q1", FallbackIndex: 1, Timestamp: proofAt(200), Status: "success", Kind: "message", LatencyMS: 800, ToolCalls: []schemas.ToolCallRef{
				{ID: "c1", Name: "Bash", Target: "pytest -q"}, {ID: "c2", Name: "Bash", Target: "pytest -q"},
			}},
		}},
		Processes: []schemas.ProcessRecord{
			{PID: 1, Start: proofAt(-900), End: proofAt(5000), RunningAtEnd: true, Argv: []string{"agent"}},
			{PID: 50, PPID: 1, Start: proofAt(1100), End: proofAt(1900), Argv: []string{"bash", "-c", "pytest -q"}},
			{PID: 51, PPID: 1, Start: proofAt(1120), End: proofAt(1950), Argv: []string{"bash", "-c", "pytest -q"}},
		},
		Observers: []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverOn}},
	}
	steps, _ := BuildTimeline(ev)
	requests, linked := 0, map[int]bool{}
	for _, s := range steps {
		if s.Kind != schemas.StepToolRequest {
			continue
		}
		requests++
		if s.ProofClass != schemas.ProofProcess || len(s.WitnessedBy) != 1 {
			t.Errorf("%s: class %s witnesses %v", s.ToolCallID, s.ProofClass, s.WitnessedBy)
		}
		for _, w := range s.WitnessedBy {
			if linked[w] {
				t.Errorf("witness %d linked twice", w)
			}
			linked[w] = true
		}
	}
	if requests != 2 {
		t.Fatalf("%d request rows, want 2 (the retried c1 once)", requests)
	}
}

// TestTimelineProperties checks, over random runs, that no witness row is
// linked to two rows, that every request has exactly one class matching its
// witnesses, and that shuffling the stored order of the witness records
// changes nothing.
func TestTimelineProperties(t *testing.T) {
	for seed := int64(1); seed <= 200; seed++ {
		r := rand.New(rand.NewSource(seed))
		ev := randomRun(r)
		want, wantTools := BuildTimeline(ev)
		checkLinks(t, seed, want)
		for k := 0; k < 3; k++ {
			sh := shuffled(ev, r)
			got, gotTools := BuildTimeline(sh)
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotTools, wantTools) {
				t.Fatalf("seed %d: timeline depends on the stored order of witness records", seed)
			}
		}
	}
}

func checkLinks(t *testing.T, seed int64, steps []schemas.StepRecord) {
	t.Helper()
	bySeq := map[int]schemas.StepRecord{}
	for _, s := range steps {
		bySeq[s.Seq] = s
	}
	owner := map[int]int{}
	for _, s := range steps {
		if s.Kind != schemas.StepToolRequest {
			continue
		}
		if s.ProofClass == "" {
			t.Fatalf("seed %d: request %s has no class", seed, s.ToolCallID)
		}
		trusted := false
		for _, w := range s.WitnessedBy {
			if o, dup := owner[w]; dup {
				t.Fatalf("seed %d: witness %d linked to requests %d and %d", seed, w, o, s.Seq)
			}
			owner[w] = s.Seq
			if bySeq[w].WitnessOf != s.Seq {
				t.Fatalf("seed %d: witness %d points at %d, not %d", seed, w, bySeq[w].WitnessOf, s.Seq)
			}
			if bySeq[w].Counted {
				t.Fatalf("seed %d: witness %d of a request is counted again", seed, w)
			}
			switch bySeq[w].Source {
			case schemas.StepSourceToolProxy, schemas.StepSourceProcess, schemas.StepSourceFS:
				trusted = true
			}
		}
		isTrusted := s.ProofClass == schemas.ProofProxy || s.ProofClass == schemas.ProofProcess || s.ProofClass == schemas.ProofFileEffect
		if trusted != isTrusted {
			t.Fatalf("seed %d: request %s class %s, trusted witness %v", seed, s.ToolCallID, s.ProofClass, trusted)
		}
	}
}

// randomRun builds a run of a few turns whose calls name a small pool of
// commands and paths, so identical calls, identical commands and calls
// without a witness all occur.
func randomRun(r *rand.Rand) *schemas.Evidence {
	cmds := []string{"pytest -q", "ls", "make build", "git status"}
	paths := []string{"a.py", "src/b.py", "c.md"}
	ev := &schemas.Evidence{
		Requests:  &schemas.RequestLog{Complete: true},
		ToolCalls: []schemas.ToolCall{},
		Processes: []schemas.ProcessRecord{{PID: 1, Start: proofAt(-1000), End: proofAt(100000), RunningAtEnd: true, Argv: []string{"agent"}}},
		Observers: []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverOn}, {Observer: schemas.ObserverFS, State: schemas.ObserverOn}},
	}
	pid, n, ms := 100, 0, 0
	for turn := 0; turn < 1+r.Intn(4); turn++ {
		q := schemas.GatewayRequest{ID: fmt.Sprintf("q%d", turn), Timestamp: proofAt(ms), Status: "success", Kind: "message", LatencyMS: 500}
		end := ms + 500
		for c := 0; c < r.Intn(4); c++ {
			n++
			id := fmt.Sprintf("c%d", n)
			at := end + 50 + r.Intn(800)
			switch r.Intn(4) {
			case 0:
				cmd := cmds[r.Intn(len(cmds))]
				q.ToolCalls = append(q.ToolCalls, schemas.ToolCallRef{ID: id, Name: "Bash", Target: cmd})
				if r.Intn(3) > 0 {
					ev.Processes = append(ev.Processes, schemas.ProcessRecord{PID: pid, PPID: 1, Start: proofAt(at), End: proofAt(at + 100), Argv: []string{"bash", "-c", cmd}})
					pid++
				}
			case 1:
				p := paths[r.Intn(len(paths))]
				q.ToolCalls = append(q.ToolCalls, schemas.ToolCallRef{ID: id, Name: "Write", Target: "/workspace/" + p})
				if r.Intn(3) > 0 {
					ev.FileEvents = append(ev.FileEvents, schemas.FileEvent{TS: proofAt(at), Op: schemas.FileModify, Path: p})
				}
			case 2:
				q.ToolCalls = append(q.ToolCalls, schemas.ToolCallRef{ID: id, Name: "mcp__docs__search", ArgsHash: "h"})
				if r.Intn(3) > 0 {
					ev.ToolCalls = append(ev.ToolCalls, schemas.ToolCall{Seq: len(ev.ToolCalls) + 1, Server: "docs", Tool: "search", Protocol: "mcp", ArgsSHA256: "h", Started: proofAt(at), LatencyMS: 20})
				}
			default:
				q.ToolCalls = append(q.ToolCalls, schemas.ToolCallRef{ID: id, Name: "Read", Target: "/workspace/x"})
				if r.Intn(2) == 0 {
					ev.Events = append(ev.Events, schemas.Event{Type: schemas.EventToolResult, Source: schemas.SourceAgentStream, ToolCallID: id, TS: proofAt(at)})
				}
			}
		}
		ev.Requests.Requests = append(ev.Requests.Requests, q)
		ms = end + 1500
	}
	return ev
}

// shuffled returns ev with its witness records in a random stored order.
func shuffled(ev *schemas.Evidence, r *rand.Rand) *schemas.Evidence {
	out := *ev
	out.ToolCalls = append([]schemas.ToolCall{}, ev.ToolCalls...)
	out.Processes = append([]schemas.ProcessRecord(nil), ev.Processes...)
	out.FileEvents = append([]schemas.FileEvent(nil), ev.FileEvents...)
	r.Shuffle(len(out.ToolCalls), func(i, j int) { out.ToolCalls[i], out.ToolCalls[j] = out.ToolCalls[j], out.ToolCalls[i] })
	r.Shuffle(len(out.Processes), func(i, j int) { out.Processes[i], out.Processes[j] = out.Processes[j], out.Processes[i] })
	r.Shuffle(len(out.FileEvents), func(i, j int) { out.FileEvents[i], out.FileEvents[j] = out.FileEvents[j], out.FileEvents[i] })
	return &out
}

// TestRecordedRunProofClasses classifies two real recorded runs (read only):
// a Codex run whose stream numbers calls on its own and a custom-python run
// without a native stream. Neither recorded an observer or a ledger, so
// every call's strongest witness is the result returned to the model.
func TestRecordedRunProofClasses(t *testing.T) {
	tests := []struct {
		run                  string
		classes              map[string]int
		executed, agentSteps int
	}{
		{run: "r_hcxe5gwzej", classes: map[string]int{schemas.ProofResultReturned: 7}, executed: 7, agentSteps: 17},
		{run: "r_q8eug9pgkv", classes: map[string]int{schemas.ProofResultReturned: 7}, executed: 0, agentSteps: 9},
	}
	for _, tt := range tests {
		t.Run(tt.run, func(t *testing.T) {
			files, _ := filepath.Glob(filepath.Join("..", "testdata", "recorded", "*", "slots", "*", "runs", tt.run, "evidence.json"))
			if len(files) != 1 {
				t.Skipf("recorded run %s not found", tt.run)
			}
			b, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var ev schemas.Evidence
			if err := json.Unmarshal(b, &ev); err != nil {
				t.Fatal(err)
			}
			steps, _ := BuildTimeline(&ev)
			classes, executed, counted := map[string]int{}, 0, 0
			for _, s := range steps {
				if s.Kind == schemas.StepToolRequest {
					classes[s.ProofClass]++
					if s.Execution != schemas.ExecutionNoWitness {
						executed++
					}
				}
				if s.Conflict != "" {
					t.Errorf("conflict on %s %s: %s", s.Kind, s.ToolCallID, s.Conflict)
				}
				if s.Counted {
					counted++
				}
			}
			if !reflect.DeepEqual(classes, tt.classes) || executed != tt.executed || counted != tt.agentSteps {
				t.Fatalf("classes %v executed %d counted %d, want %v %d %d", classes, executed, counted, tt.classes, tt.executed, tt.agentSteps)
			}
			again, _ := BuildTimeline(&ev)
			if !reflect.DeepEqual(again, steps) {
				t.Fatal("classification of the same run is not stable")
			}
		})
	}
}
