package traceexport

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// stepsInput is the synthetic run with a step timeline: commands and file
// changes before the first response, under each of the two model turns and
// under a turn with no model span, plus rows of other kinds.
func stepsInput() Input {
	in := syntheticInput()
	t0 := in.Run.Timing.Started
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	dur := func(ms int64) *int64 { return &ms }
	code := func(c int) *int { return &c }
	in.Evidence.Steps = []schemas.StepRecord{
		{Seq: 1, TS: at(150), Kind: schemas.StepCommand, Source: schemas.StepSourceProcess, Proof: schemas.EvidenceVerified, Turn: 0, Session: 1,
			Summary: "bash -lc setup", PID: 10, PPID: 1, ExitCode: code(0), DurationMS: dur(20), Counted: true},
		{Seq: 2, TS: at(200), Kind: schemas.StepModelTurn, Source: schemas.StepSourceGateway, Turn: 1, Summary: "chat model-a"},
		{Seq: 3, TS: at(400), Kind: schemas.StepCommand, Source: schemas.StepSourceProcess, Proof: schemas.EvidenceVerified, Turn: 1, Session: 1,
			Summary: "/usr/bin/python3 -m pytest token=hunter2-synthetic " + strings.Repeat("y", 600), PID: 11, PPID: 10, ExitCode: code(2), DurationMS: dur(100), Counted: true},
		{Seq: 4, TS: at(450), Kind: schemas.StepFileChange, Source: schemas.StepSourceFS, Proof: schemas.EvidenceVerified, Turn: 1, Session: 1,
			Summary: "modify a.txt", Path: "a.txt", Op: schemas.FileModify, Counted: true},
		{Seq: 5, TS: at(460), Kind: schemas.StepToolExecution, Source: schemas.StepSourceToolProxy, Turn: 1, Summary: "read_file"},
		{Seq: 6, TS: at(3500), Kind: schemas.StepFileChange, Source: schemas.StepSourceFS, Proof: schemas.EvidenceVerified, Turn: 2, Session: 2,
			Summary: "create b.txt", Path: "b.txt", Op: schemas.FileCreate, WitnessOf: 5},
		{Seq: 7, TS: at(3600), Kind: schemas.StepCommand, Source: schemas.StepSourceProcess, Turn: 7, Session: 2, Summary: "ls", PID: 12},
	}
	return in
}

func TestBuildStepSpans(t *testing.T) {
	td, err := Build(stepsInput(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	checkTrace(t, td)
	b, err := Marshal(td)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hunter2-synthetic") {
		t.Fatal("a secret in a step summary reached the trace")
	}
	spans := td.ResourceSpans[0].ScopeSpans[0].Spans
	sessions := spansNamed(spans, "invoke_agent ")
	models := spansNamed(spans, "chat ")
	if len(sessions) != 2 || len(models) != 2 {
		t.Fatalf("sessions %d, models %d", len(sessions), len(models))
	}
	bySeq := map[string]Span{}
	for _, sp := range spans {
		if v, ok := Attr(sp.Attributes, "heimdall.step.seq"); ok {
			bySeq[v.String()] = sp
		}
	}
	if len(bySeq) != 5 {
		t.Fatalf("timeline spans: got %d, want 5 (commands and file changes only)", len(bySeq))
	}
	cases := []struct {
		seq, name, parent string
		start, end        int // ms after the run start
	}{
		{"1", "command bash", sessions[0].SpanID, 150, 170},
		{"3", "command python3", models[0].SpanID, 400, 500},
		{"4", "file_change modify", models[0].SpanID, 450, 450},
		{"6", "file_change create", models[1].SpanID, 3500, 3500},
		{"7", "command ls", sessions[1].SpanID, 3600, 3600},
	}
	t0 := syntheticInput().Run.Timing.Started
	for _, c := range cases {
		sp, ok := bySeq[c.seq]
		if !ok {
			t.Fatalf("no span for timeline row %s", c.seq)
		}
		if sp.Name != c.name || sp.ParentSpanID != c.parent {
			t.Errorf("row %s: name %q parent %s, want %q under %s", c.seq, sp.Name, sp.ParentSpanID, c.name, c.parent)
		}
		if sp.SpanID != SpanID("r_syn", "step:"+c.seq) {
			t.Errorf("row %s: span id %s is not the step key's", c.seq, sp.SpanID)
		}
		start, end := nanos(t0.Add(time.Duration(c.start)*time.Millisecond)), nanos(t0.Add(time.Duration(c.end)*time.Millisecond))
		if sp.StartTimeUnixNano != start || sp.EndTimeUnixNano != end {
			t.Errorf("row %s: %d..%d, want %d..%d", c.seq, sp.StartTimeUnixNano, sp.EndTimeUnixNano, start, end)
		}
	}
	py := bySeq["3"]
	wantAttr(t, py.Attributes, "process.pid", "11")
	wantAttr(t, py.Attributes, "process.parent_pid", "10")
	wantAttr(t, py.Attributes, "process.exit.code", "2")
	wantAttr(t, py.Attributes, "heimdall.step.source", schemas.StepSourceProcess)
	wantAttr(t, py.Attributes, "heimdall.step.proof", string(schemas.EvidenceVerified))
	wantAttr(t, py.Attributes, "heimdall.step.turn", "1")
	wantAttr(t, py.Attributes, "heimdall.step.counted", "true")
	if py.Status == nil || py.Status.Code != StatusError || py.Status.Message != "exit code 2" {
		t.Errorf("a failed command is not an error span: %+v", py.Status)
	}
	sum, _ := Attr(py.Attributes, "heimdall.step.summary")
	if !strings.HasSuffix(sum.String(), "[truncated]") || len(sum.String()) > textCap+len(" [truncated]") {
		t.Errorf("summary not clipped: %d bytes", len(sum.String()))
	}
	if bySeq["1"].Status != nil {
		t.Error("a command that exited 0 is an error span")
	}
	fc := bySeq["6"]
	wantAttr(t, fc.Attributes, "heimdall.file.op", schemas.FileCreate)
	wantAttr(t, fc.Attributes, "file.path", "b.txt")
	wantAttr(t, fc.Attributes, "heimdall.step.source", schemas.StepSourceFS)
	wantAttr(t, fc.Attributes, "heimdall.session.index", "2")
	wantAttr(t, fc.Attributes, "heimdall.step.witness_of", "5")
	if _, ok := Attr(bySeq["7"].Attributes, "process.exit.code"); ok {
		t.Error("an unknown exit code was exported")
	}

	again, err := Build(stepsInput(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, b2) {
		t.Fatal("exporting the same timeline twice gave different bytes")
	}
}

// TestBuildStepSpansTurnZeroNoSession checks that a turn 0 row of a run
// without session spans goes under the run span, and that rows repeating a
// seq still get distinct span ids.
func TestBuildStepSpansTurnZeroNoSession(t *testing.T) {
	in := stepsInput()
	in.Evidence.Sessions = nil
	t0 := in.Run.Timing.Started
	in.Evidence.Steps = []schemas.StepRecord{
		{Seq: 1, TS: t0.Add(50 * time.Millisecond), Kind: schemas.StepCommand, Turn: 0, Summary: "sh"},
		{Seq: 1, TS: t0.Add(60 * time.Millisecond), Kind: schemas.StepFileChange, Turn: 0, Op: schemas.FileDelete, Path: "c.txt"},
	}
	td, err := Build(in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	checkTrace(t, td)
	spans := td.ResourceSpans[0].ScopeSpans[0].Spans
	root := spans[0].SpanID
	for _, name := range []string{"command sh", "file_change delete"} {
		got := spansNamed(spans, name)
		if len(got) != 1 || got[0].ParentSpanID != root {
			t.Fatalf("%s: %+v, want one span under the run", name, got)
		}
	}
}

// TestBuildWithoutStepsUnchanged checks that an empty timeline adds nothing:
// the trace equals the one built before timelines existed.
func TestBuildWithoutStepsUnchanged(t *testing.T) {
	in := stepsInput()
	in.Evidence.Steps = []schemas.StepRecord{{Seq: 1, TS: in.Run.Timing.Started, Kind: schemas.StepModelTurn, Turn: 1}}
	a, err := Build(in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(syntheticInput(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := Marshal(a)
	jb, _ := Marshal(b)
	if !bytes.Equal(ja, jb) {
		t.Fatal("a timeline without commands or file changes changed the trace")
	}
}

func TestCommandName(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "command",
		"   ":                    "command",
		"/usr/bin/git status":    "command git",
		"pytest -q":              "command pytest",
		strings.Repeat("x", 100): "command " + strings.Repeat("x", commandNameCap) + " [truncated]",
	} {
		if got := commandName(in); got != want {
			t.Errorf("commandName(%q) = %q, want %q", in, got, want)
		}
	}
}
