package metrics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// trace builds the events of one source for the pattern tests. Event ids are
// "r:<source>:<seq>".
type trace struct {
	src    schemas.EventSource
	seq    int
	events []schemas.Event
	call   string // id of the last tool call
}

func gwTrace() *trace     { return &trace{src: schemas.SourceGateway} }
func streamTrace() *trace { return &trace{src: schemas.SourceAgentStream} }

func (b *trace) add(e schemas.Event) *trace {
	b.seq++
	e.Seq = b.seq
	e.Source = b.src
	e.EventID = fmt.Sprintf("r:%s:%d", b.src, b.seq)
	e.Evidence = schemas.EvidenceGateway
	if b.src == schemas.SourceAgentStream {
		e.Evidence = schemas.EvidenceNative
	}
	b.events = append(b.events, e)
	return b
}

// tool appends a tool call whose arguments hash is derived from name and target.
func (b *trace) tool(name, target string) *trace {
	return b.toolArgs(name, target, name+"|"+target)
}

func (b *trace) toolArgs(name, target, args string) *trace {
	b.add(schemas.Event{Type: schemas.EventToolCall, ToolName: name, Target: target, ArgsHash: "h:" + args, ToolCallID: fmt.Sprintf("tc%d", b.seq+1)})
	b.call = b.events[len(b.events)-1].EventID
	return b
}

// result appends the result of the last tool call, carrying its name and target.
func (b *trace) result(failed bool, summary string) *trace {
	c := b.events[len(b.events)-1]
	for i := len(b.events) - 1; i >= 0; i-- {
		if b.events[i].EventID == b.call {
			c = b.events[i]
			break
		}
	}
	return b.add(schemas.Event{Type: schemas.EventToolResult, ParentEventID: c.EventID, ToolCallID: c.ToolCallID,
		ToolName: c.ToolName, Target: c.Target, IsError: failed, Summary: summary})
}

func (b *trace) model() *trace { return b.add(schemas.Event{Type: schemas.EventModelResponse}) }

func (b *trace) incomplete() *trace {
	return b.add(schemas.Event{Type: schemas.EventError, IsError: true, Summary: gatewayIncomplete + ": 2 rows missing"})
}

func (b *trace) ev() []schemas.Event { return b.events }

func join(parts ...[]schemas.Event) []schemas.Event {
	var out []schemas.Event
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func seen(level schemas.EvidenceLevel, reason string) schemas.Value {
	v := schemas.Observed(1, level)
	v.Reason = reason
	return v
}

func absent(level schemas.EvidenceLevel) schemas.Value { return schemas.Observed(0, level) }

func TestPickTrace(t *testing.T) {
	gwCalls := gwTrace().model().tool("Bash", "ls").ev()
	gwNoCalls := gwTrace().model().model().ev()
	stream := streamTrace().tool("Bash", "ls").result(false, "ok").ev()
	tests := []struct {
		name   string
		events []schemas.Event
		ok     bool
		level  schemas.EvidenceLevel
		calls  int
	}{
		{name: "nothing", events: nil, ok: false},
		{name: "only the proxy ledger", events: []schemas.Event{{Source: schemas.SourceProxyLedger, Type: schemas.EventFault}}, ok: false},
		{name: "gateway wins", events: join(gwCalls, stream), ok: true, level: schemas.EvidenceGateway, calls: 1},
		{name: "gateway alone", events: gwNoCalls, ok: true, level: schemas.EvidenceGateway, calls: 0},
		{name: "stream when gateway logged no tool calls", events: join(gwNoCalls, stream), ok: true, level: schemas.EvidenceNative, calls: 1},
		{name: "stream when gateway incomplete", events: join(gwTrace().tool("Bash", "ls").incomplete().ev(), stream), ok: true, level: schemas.EvidenceNative, calls: 1},
		{name: "incomplete gateway when alone", events: gwTrace().tool("Bash", "ls").incomplete().ev(), ok: true, level: schemas.EvidenceGateway, calls: 1},
		{name: "stream alone", events: stream, ok: true, level: schemas.EvidenceNative, calls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, ok := pickTrace(tt.events)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && (tr.level != tt.level || len(tr.calls) != tt.calls) {
				t.Errorf("level %q calls %d, want %q %d", tr.level, len(tr.calls), tt.level, tt.calls)
			}
		})
	}
}

func TestToolClassifiers(t *testing.T) {
	tests := []struct {
		name                       string
		tool, target               string
		command, edit, read, tests bool
	}{
		{name: "claude bash pytest", tool: "Bash", target: "pytest -q", command: true, tests: true},
		{name: "codex go test", tool: "exec_command", target: "go test ./...", command: true, tests: true},
		{name: "python -m unittest", tool: "run_shell_command", target: "python -m unittest discover", command: true, tests: true},
		{name: "make test", tool: "shell", target: "make test", command: true, tests: true},
		{name: "npm test", tool: "Bash", target: "npm test -- --watch=false", command: true, tests: true},
		{name: "plain command", tool: "Bash", target: "pip install -e .", command: true},
		{name: "cat is a read", tool: "Bash", target: "cat src/x.py", command: true, read: true},
		{name: "sed -n is a read", tool: "Bash", target: "sed -n 1,20p src/x.py", command: true, read: true},
		{name: "edit", tool: "Edit", target: "src/x.py", edit: true},
		{name: "multi edit", tool: "MultiEdit", target: "src/x.py", edit: true},
		{name: "write", tool: "Write", target: "src/x.py", edit: true},
		{name: "codex apply_patch", tool: "apply_patch", target: "src/x.py", edit: true},
		{name: "todo write is not an edit", tool: "TodoWrite"},
		{name: "read", tool: "Read", target: "src/x.py", read: true},
		{name: "read_file", tool: "read_file", read: true},
		{name: "grep", tool: "Grep", target: "parse_date", read: true},
		{name: "test tool by name", tool: "run_tests", tests: true},
		{name: "read of a test file is not a test run", tool: "Read", target: "tests/test_pytest.py", read: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := schemas.Event{Type: schemas.EventToolCall, ToolName: tt.tool, Target: tt.target}
			if got := isCommand(e); got != tt.command {
				t.Errorf("isCommand = %v", got)
			}
			if got := isEdit(e); got != tt.edit {
				t.Errorf("isEdit = %v", got)
			}
			if got := isRead(e); got != tt.read {
				t.Errorf("isRead = %v", got)
			}
			if got := runsTests(e); got != tt.tests {
				t.Errorf("runsTests = %v", got)
			}
		})
	}
}

func TestCallKey(t *testing.T) {
	tests := []struct {
		name string
		a, b schemas.Event
		same bool
	}{
		{name: "command by text, not arguments", a: schemas.Event{ToolName: "Bash", Target: "pytest  -q", ArgsHash: "1"}, b: schemas.Event{ToolName: "Bash", Target: "pytest -q", ArgsHash: "2"}, same: true},
		{name: "other tools by arguments", a: schemas.Event{ToolName: "Read", Target: "x.py", ArgsHash: "1"}, b: schemas.Event{ToolName: "Read", Target: "x.py", ArgsHash: "2"}, same: false},
		{name: "same tool and arguments", a: schemas.Event{ToolName: "Read", ArgsHash: "1"}, b: schemas.Event{ToolName: "Read", ArgsHash: "1"}, same: true},
		{name: "no hash falls back to target", a: schemas.Event{ToolName: "Read", Target: "x.py"}, b: schemas.Event{ToolName: "Read", Target: "y.py"}, same: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := callKey(tt.a) == callKey(tt.b); got != tt.same {
				t.Errorf("same = %v, want %v", got, tt.same)
			}
		})
	}
}

func TestReadsFileOf(t *testing.T) {
	tests := []struct {
		name   string
		read   schemas.Event
		target string
		want   bool
	}{
		{name: "read of the failing test file", read: schemas.Event{ToolName: "Read", Target: "tests/test_x.py"}, target: "pytest tests/test_x.py::test_y -q", want: true},
		{name: "read by absolute path", read: schemas.Event{ToolName: "Read", Target: "/workspace/src/x.py"}, target: "src/x.py", want: true},
		{name: "cat of the file", read: schemas.Event{ToolName: "Bash", Target: "cat src/x.py"}, target: "python src/x.py --check", want: true},
		{name: "another file", read: schemas.Event{ToolName: "Read", Target: "README.md"}, target: "pytest tests/test_x.py", want: false},
		{name: "no target", read: schemas.Event{ToolName: "Read"}, target: "src/x.py", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readsFileOf(tt.read, tt.target); got != tt.want {
				t.Errorf("readsFileOf = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFailedResult(t *testing.T) {
	tests := []struct {
		name string
		e    schemas.Event
		want bool
	}{
		{name: "error flag", e: schemas.Event{IsError: true}, want: true},
		{name: "codex non-zero exit", e: schemas.Event{Summary: "exit 1: FAILED tests/test_x.py"}, want: true},
		{name: "claude code exit code", e: schemas.Event{Summary: "Exit code 2 no such file"}, want: true},
		{name: "exit 0", e: schemas.Event{Summary: "exit 0: 11 passed"}, want: false},
		{name: "plain output", e: schemas.Event{Summary: "11 passed"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := failedResult(tt.e); got != tt.want {
				t.Errorf("failedResult = %v, want %v", got, tt.want)
			}
		})
	}
}

// smokeRunDir is the sealed smoke experiment. Run folders are not committed,
// so the test skips where the folder is absent.
const smokeRunDir = "../runs/e_smoke_sdoj"

// TestPatternsOnSealedSmokeRun runs the detectors over every sealed run of the
// smoke experiment, read-only. The scripted agent reads /workspace, gets
// "Is a directory", and ends: claimed success on clean runs, an honest
// failure under model-outage. So no run loops, and a run shows an
// unverified finish and an ignored error exactly when it claimed success.
func TestPatternsOnSealedSmokeRun(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(smokeRunDir, "slots", "*", "runs", "*", "events.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(paths) == 0 {
		t.Skipf("no sealed runs under %s", smokeRunDir)
	}
	for _, p := range paths {
		dir := filepath.Dir(p)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			ev := &schemas.Evidence{Events: readEventsFile(t, p)}
			var run schemas.Run
			readJSON(t, filepath.Join(dir, "run.json"), &run)
			ev.Claim = run.Claim
			if ev.Claim == nil {
				t.Fatal("sealed run has no claim")
			}
			want := 0.0
			if ev.Claim.Made {
				want = 1
			}
			checks := []struct {
				m     schemas.Metric
				v     float64
				level schemas.EvidenceLevel
			}{
				{m: newPatternLoopMetric(), v: 0, level: schemas.EvidenceGateway},
				{m: newPatternUnverifiedMetric(), v: want, level: schemas.EvidenceGateway},
				{m: newPatternIgnoredErrorMetric(), v: want, level: schemas.EvidenceNative},
			}
			for _, c := range checks {
				got, err := c.m.Compute(context.Background(), nil, ev)
				if err != nil {
					t.Fatalf("%s: %v", c.m.GetName(), err)
				}
				t.Logf("%s = %s %v (%s): %s", c.m.GetName(), got.State, c.v, got.Evidence, got.Reason)
				v, ok := got.Float()
				if !ok || v != c.v || got.Evidence != c.level {
					t.Errorf("%s = %+v, want observed %v at %s", c.m.GetName(), got, c.v, c.level)
				}
				if v == 1 && !strings.HasPrefix(got.Reason, "events: "+run.RunID+":") {
					t.Errorf("%s reason %q does not start with this run's event ids", c.m.GetName(), got.Reason)
				}
			}
		})
	}
}

func readEventsFile(t *testing.T, path string) []schemas.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []schemas.Event
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var e schemas.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

func readJSON(t *testing.T, path string, dst any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}
