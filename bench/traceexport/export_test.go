package traceexport

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var update = flag.Bool("update", false, "rewrite the golden traces in testdata/golden")

// recordedRuns are sealed runs of the read-only recorded bundle with a golden
// trace each: a clean pass, and a model-outage run whose third model request
// the fault proxy answered with an injected 503.
var recordedRuns = []string{
	"e_smoke_sdoj/slots/s_wskttklrzb/runs/r_aunhv28sxj",
	"e_smoke_sdoj/slots/s_glz13mrnep/runs/r_ioap8m36je",
}

const recordedRoot = "../testdata/recorded"

func TestBuildGoldenRecorded(t *testing.T) {
	for _, rel := range recordedRuns {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			in, err := LoadRun(filepath.Join(recordedRoot, rel))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			td, err := Build(in, Options{ServiceVersion: "test"})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			checkTrace(t, td)
			got, err := Marshal(td)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "golden", filepath.Base(rel)+".otlp.json")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create it): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("trace differs from %s; run go test ./traceexport -run TestBuildGoldenRecorded -update and review the diff", golden)
			}
			again, err := Build(in, Options{ServiceVersion: "test"})
			if err != nil {
				t.Fatal(err)
			}
			b2, err := Marshal(again)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, b2) {
				t.Fatal("two exports of the same run differ")
			}
		})
	}
}

func TestBuildRecordedOutageSpans(t *testing.T) {
	in, err := LoadRun(filepath.Join(recordedRoot, recordedRuns[1]))
	if err != nil {
		t.Fatal(err)
	}
	td, err := Build(in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	spans := td.ResourceSpans[0].ScopeSpans[0].Spans
	root := spans[0]
	if root.SpanID != SpanID("r_ioap8m36je", "run") || root.TraceID != TraceID("r_ioap8m36je") {
		t.Fatalf("root ids: %s %s", root.TraceID, root.SpanID)
	}
	wantAttr(t, root.Attributes, "heimdall.outcome", "fail")
	wantAttr(t, root.Attributes, "heimdall.condition", "model-outage")
	wantAttr(t, root.Attributes, "heimdall.verified_success", "false")
	wantAttr(t, root.Attributes, "heimdall.seal_hash", in.SealHash)
	models := spansNamed(spans, "chat ")
	if len(models) != 3 {
		t.Fatalf("model spans: got %d, want 3", len(models))
	}
	last := models[2]
	if last.Status == nil || last.Status.Code != StatusError {
		t.Fatalf("injected 503 not an error span: %+v", last.Status)
	}
	wantAttr(t, last.Attributes, "error.type", "503")
	if _, ok := Attr(last.Attributes, "gen_ai.usage.input_tokens"); ok {
		t.Fatal("a request without usage exported token counts")
	}
	// The injected 503 is priced as an observed zero: the provider never billed it.
	wantAttr(t, last.Attributes, "heimdall.cost_usd", "0")
	wantAttr(t, last.Attributes, "heimdall.fault_injected", "true")
	wantAttr(t, models[0].Attributes, "gen_ai.usage.input_tokens", "535")
	wantAttr(t, models[0].Attributes, "gen_ai.usage.output_tokens", "32")
	wantAttr(t, models[0].Attributes, "gen_ai.provider.name", "anthropic")
	wantAttr(t, models[0].Attributes, "heimdall.cost_usd", "0.002085")
	faults := 0
	for _, e := range last.Events {
		if e.Name == "heimdall.fault" {
			faults++
		}
	}
	if faults != 1 {
		t.Fatalf("fault events on the faulted request: got %d, want 1", faults)
	}
	calls := spansNamed(spans, "tool_call ")
	if len(calls) != 1 || calls[0].ParentSpanID != models[0].SpanID {
		t.Fatalf("requested tool call span: %+v", calls)
	}
	wantAttr(t, calls[0].Attributes, "heimdall.tool.result_returned", "true")
	if len(spansNamed(spans, "heimdall.gate ")) != 5 || len(spansNamed(spans, "heimdall.grade")) != 1 {
		t.Fatal("want five gate spans and one grade span")
	}
	b, err := Marshal(td)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "nonce_lhhsoxhj3xnw9gjm078u") {
		t.Fatal("the probe nonce reached the trace")
	}
}

func TestBuildSyntheticRun(t *testing.T) {
	for _, payloads := range []bool{false, true} {
		t.Run(map[bool]string{false: "hashes", true: "payloads"}[payloads], func(t *testing.T) {
			in := syntheticInput()
			td, err := Build(in, Options{Payloads: payloads, PayloadCap: 64})
			if err != nil {
				t.Fatal(err)
			}
			checkTrace(t, td)
			b, err := Marshal(td)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range syntheticSecrets {
				if strings.Contains(string(b), secret) {
					t.Fatalf("secret %q reached the trace", secret)
				}
			}
			spans := td.ResourceSpans[0].ScopeSpans[0].Spans
			sessions := spansNamed(spans, "invoke_agent ")
			if len(sessions) != 2 {
				t.Fatalf("session spans: got %d, want 2", len(sessions))
			}
			tools := spansNamed(spans, "execute_tool ")
			if len(tools) != 2 {
				t.Fatalf("execute_tool spans: got %d, want 2", len(tools))
			}
			if tools[0].ParentSpanID != sessions[0].SpanID || tools[1].ParentSpanID != sessions[1].SpanID {
				t.Fatal("tool spans are not under their sessions")
			}
			wantAttr(t, tools[1].Attributes, "heimdall.tool.fault", "error")
			wantAttr(t, tools[0].Attributes, "heimdall.tool.args_bytes", "46")
			wantAttr(t, tools[0].Attributes, "heimdall.tool.result_clipped", "true")
			args, hasArgs := Attr(tools[0].Attributes, "gen_ai.tool.call.arguments")
			if hasArgs != payloads {
				t.Fatalf("arguments exported: %v, payloads: %v", hasArgs, payloads)
			}
			if payloads && !strings.Contains(args.String(), "[redacted]") {
				t.Fatalf("arguments not redacted: %s", args.String())
			}
			res, _ := Attr(tools[0].Attributes, "gen_ai.tool.call.result")
			if payloads && !strings.HasSuffix(res.String(), "[truncated]") {
				t.Fatalf("result not capped: %q", res.String())
			}
			models := spansNamed(spans, "chat ")
			if len(models) != 2 {
				t.Fatalf("model spans: got %d, want 2", len(models))
			}
			wantAttr(t, models[0].Attributes, "heimdall.logical_step", "1")
			wantAttr(t, models[0].Attributes, "heimdall.attempts", "2")
			wantAttr(t, models[0].Attributes, "heimdall.retries", "1")
			wantAttr(t, models[0].Attributes, "heimdall.fallbacks", "1")
			wantAttr(t, models[0].Attributes, "gen_ai.usage.input_tokens", "120")
			if _, ok := Attr(models[1].Attributes, "heimdall.cost_usd"); ok {
				t.Fatal("a request without a cost row exported a cost")
			}
			wantAttr(t, models[0].Attributes, "gen_ai.usage.cache_read.input_tokens", "20")
			wantAttr(t, models[0].Attributes, "gen_ai.response.model", "model-b")
			if len(models[0].Events) != 2 {
				t.Fatalf("retry and fallback events: got %d, want 2", len(models[0].Events))
			}
			calls := spansNamed(spans, "tool_call ")
			if len(calls) != 1 || calls[0].ParentSpanID != models[0].SpanID {
				t.Fatalf("requested tool span: %+v", calls)
			}
			wantAttr(t, calls[0].Attributes, "heimdall.tool.executed", "true")
			gate := spansNamed(spans, "heimdall.gate g1_protected_paths")
			if len(gate) != 1 || gate[0].Status == nil || gate[0].Status.Code != StatusError {
				t.Fatal("a fired veto gate is not an error span")
			}
			if _, ok := Attr(spans[0].Attributes, "heimdall.cost_usd"); ok {
				t.Fatal("an unknown cost was exported")
			}
			_, claim := Attr(spans[0].Attributes, "heimdall.claim.text")
			if claim != payloads {
				t.Fatalf("claim text exported: %v, payloads: %v", claim, payloads)
			}
		})
	}
}

func TestBuildRefuses(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want error
	}{
		{"unsealed", Input{Run: schemas.Run{RunID: "r_x", Timing: schemas.RunTiming{Started: time.Now()}}}, ErrUnsealed},
		{"no run id", Input{SealHash: "abc"}, nil},
		{"no times", Input{Run: schemas.Run{RunID: "r_x"}, SealHash: "abc"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Build(c.in, Options{})
			if err == nil {
				t.Fatal("want an error")
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestIDsStableAndDistinct(t *testing.T) {
	if TraceID("r_a") != TraceID("r_a") || TraceID("r_a") == TraceID("r_b") {
		t.Fatal("trace ids must be stable per run and differ between runs")
	}
	if SpanID("r_a", "run") == SpanID("r_a", "grade") || SpanID("r_a", "run") == SpanID("r_b", "run") {
		t.Fatal("span ids must differ per key and per run")
	}
	if len(TraceID("r_a")) != 32 || len(SpanID("r_a", "run")) != 16 {
		t.Fatal("id lengths")
	}
}

func TestClean(t *testing.T) {
	cases := []struct{ in, notWant string }{
		{"Authorization: Bearer sk-live-abcdefghijkl", "sk-live-abcdefghijkl"},
		{`{"api_key":"hunter2hunter2"}`, "hunter2hunter2"},
		{"token=abc123secret", "abc123secret"},
		{"call with bearer abcdefghijklmnop", "abcdefghijklmnop"},
		{"key sk-proj-ABCDEFGH12345678 in text", "ABCDEFGH12345678"},
		{"ref nonce_lhhsoxhj3xnw9gjm078u9dcw call 3", "lhhsoxhj3xnw9gjm078u9dcw"},
	}
	for _, c := range cases {
		if got := clean(c.in, 1000); strings.Contains(got, c.notWant) {
			t.Fatalf("clean(%q) = %q", c.in, got)
		}
	}
	if got := clean(strings.Repeat("é", 10), 5); got != "éé [truncated]" {
		t.Fatalf("cap on a rune boundary: %q", got)
	}
}

// checkTrace validates a built trace: Validate, one root per trace, every
// span inside the root's window, every event inside its span.
func checkTrace(t *testing.T, td *TracesData) {
	t.Helper()
	if err := Validate(td); err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, rs := range td.ResourceSpans {
		var roots []Span
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				if sp.ParentSpanID == "" {
					roots = append(roots, sp)
				}
			}
		}
		if len(roots) != 1 {
			t.Fatalf("roots: got %d, want 1", len(roots))
		}
		root := roots[0]
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				if sp.StartTimeUnixNano < root.StartTimeUnixNano || sp.EndTimeUnixNano > root.EndTimeUnixNano {
					t.Fatalf("span %s (%s) outside the run span", sp.SpanID, sp.Name)
				}
				for _, e := range sp.Events {
					if e.TimeUnixNano < sp.StartTimeUnixNano || e.TimeUnixNano > sp.EndTimeUnixNano {
						t.Fatalf("event %s outside span %s (%s)", e.Name, sp.SpanID, sp.Name)
					}
				}
			}
		}
	}
}

func spansNamed(spans []Span, prefix string) []Span {
	var out []Span
	for _, sp := range spans {
		if strings.HasPrefix(sp.Name, prefix) {
			out = append(out, sp)
		}
	}
	return out
}

func wantAttr(t *testing.T, attrs []KeyValue, key, want string) {
	t.Helper()
	v, ok := Attr(attrs, key)
	if !ok {
		t.Fatalf("attribute %s missing", key)
	}
	if got := v.String(); got != want {
		t.Fatalf("attribute %s = %q, want %q", key, got, want)
	}
}

// syntheticSecrets appear in syntheticInput and must never reach a trace.
var syntheticSecrets = []string{
	"sk-live-SYNTHETICKEY123456",
	"hunter2-synthetic",
	"Zm9vOmJhcg-synthetic-basic",
	"nonce_abcdefghijklmnopqrstuvwx",
}

// syntheticInput is a hand-made sealed run with every kind of evidence the
// exporter reads: two sessions, two proxied tool calls (one faulted), two
// logical model requests (the first retried once and served by a fallback),
// canonical step records, a requested tool call, a grade, gates and secrets
// in every free-text field.
func syntheticInput() Input {
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	sealed := at(9000)
	exit := 0
	usd := 0.01
	tok := func(fresh, read, out int64) *schemas.Tokens {
		return &schemas.Tokens{Fresh: fresh, CacheRead: read, Out: out}
	}
	args := `{"path":"a.txt","api_key":"hunter2-synthetic"}`
	return Input{
		Slot: schemas.Slot{SlotID: "s_syn", PairID: "p_syn", ExpID: "e_syn", Suite: "suite", Task: "t01", TaskVersion: "1.0.0",
			Arm: "A", Condition: "tool-outage", Trial: 1},
		Run: schemas.Run{RunID: "r_syn", SlotID: "s_syn", ExpID: "e_syn", Attempt: 1, ManifestHash: "mh", Outcome: schemas.OutcomeFail,
			Timing: schemas.RunTiming{Started: t0, Sealed: &sealed},
			Agent:  schemas.RunAgentInfo{ConfigID: "codex", ConfigHash: "ch", ExitCode: &exit},
			Claim:  &schemas.Claim{Made: true, Text: "done, token=hunter2-synthetic"}},
		SealHash: "sealhash",
		Metrics: map[string]schemas.Value{"verified_success": schemas.Observed(0, schemas.EvidenceOutcome),
			"cost_usd": schemas.Unknown("no price", schemas.EvidenceGateway)},
		Transitions: []schemas.Transition{
			{Stage: schemas.StageGrade, Status: schemas.TransitionStarted, At: at(7000)},
			{Stage: schemas.StageGrade, Status: schemas.TransitionCompleted, At: at(7500)},
			{Stage: schemas.StageGate, Status: schemas.TransitionStarted, At: at(7600)},
			{Stage: schemas.StageGate, Status: schemas.TransitionCompleted, At: at(7700)},
		},
		Evidence: schemas.Evidence{
			Sessions: []schemas.SessionResult{
				{ID: "s1", Index: 1, Started: at(100), Ended: at(3000)},
				{ID: "s2", Index: 2, Started: at(3100), Ended: at(6000), ExitCode: 1},
			},
			Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
				{ID: "q1", Timestamp: at(200), Status: "error", Kind: "message", Provider: "openai", ModelRequested: "model-a",
					ModelServed: "model-a", LatencyMS: 50, ErrorStatus: 429, ErrorMessage: "Authorization: Bearer sk-live-SYNTHETICKEY123456",
					Attempts: []schemas.Attempt{{N: 1, StatusCode: 429, FailReason: "rate limited"}, {N: 2, StatusCode: 429}}},
				{ID: "q2", ParentID: "q1", Timestamp: at(260), Status: "success", Kind: "message", Provider: "openai", ModelRequested: "model-a",
					ModelServed: "model-b", FallbackIndex: 1, LatencyMS: 100, Tokens: tok(100, 20, 10), ResponseID: "resp-1",
					ToolCalls: []schemas.ToolCallRef{{ID: "call_1", Name: "read_file", ArgsHash: "ah", Target: "a.txt Authorization: Basic Zm9vOmJhcg-synthetic-basic"}}},
				{ID: "q3", Timestamp: at(3200), Status: "success", Kind: "message", Provider: "openai", ModelRequested: "model-a",
					ModelServed: "model-a", LatencyMS: 80, Tokens: tok(150, 0, 5), ToolResultIDs: []string{"call_1"}},
				{ID: "q4", Timestamp: at(3300), Status: "success", Kind: "count_tokens"},
			}},
			LogicalSteps: []schemas.LogicalRequest{{LogicalRequestID: "q1", LogicalStep: 1}, {LogicalRequestID: "q3", LogicalStep: 2}},
			Attempts: []schemas.AttemptRecord{{AttemptID: "q1:attempt:1", LogicalRequestID: "q1", AttemptNumber: 1},
				{AttemptID: "q1:attempt:2", LogicalRequestID: "q1", AttemptNumber: 2}, {AttemptID: "q3:attempt:1", LogicalRequestID: "q3", AttemptNumber: 1}},
			Retries:   []schemas.RetryRecord{{AttemptID: "q1:attempt:2", LogicalRequestID: "q1", AttemptNumber: 2, RetryType: "bifrost"}},
			Fallbacks: []schemas.FallbackRecord{{FallbackIndex: 1, LogicalRequestID: "q1"}},
			RequestedTools: []schemas.ToolCallRecord{{ToolCallID: "call_1", LogicalStep: 1, ToolName: "read_file", Requested: true, Executed: true,
				RequestSource: schemas.EvidenceSourceBifrostLog, ExecutionSource: schemas.EvidenceSourceAgentStream}},
			Cost: []schemas.CostRow{{RequestID: "q2", USD: schemas.Observed(usd, schemas.EvidenceGateway)}},
			ToolCalls: []schemas.ToolCall{
				{Seq: 1, Session: "s1", Server: "fs", Tool: "read_file", Protocol: "mcp", Arguments: args, ArgsSHA256: hexSHA256(args),
					Result: strings.Repeat("x", 100) + " password=hunter2-synthetic", ResultSHA256: "0000", Started: at(400), LatencyMS: 30},
				{Seq: 2, Session: "s2", Server: "fs", Tool: "write_file", Protocol: "mcp", IsError: true, Fault: "error",
					Nonce: "nonce_abcdefghijklmnopqrstuvwx", Result: "injected nonce_abcdefghijklmnopqrstuvwx", Started: at(3400), LatencyMS: 5},
			},
			Events: []schemas.Event{{EventID: "r_syn:shim_log:1", Type: schemas.EventFault, Source: schemas.SourceShimLog, TS: at(5000),
				Summary: "shim crashed pytest, secret=hunter2-synthetic"}},
			Grade: &schemas.Grade{Passed: false, TestsRan: 3, Failures: 1, Reason: "1 failed", FailedTests: []string{"test_a"}},
			Gates: []schemas.GateResult{
				{Gate: "g1_protected_paths", Version: "1.0.0", Effect: schemas.GateVeto, Fired: true, Detail: "tests/x changed"},
				{Gate: "egress", Version: "1.0.0", Effect: schemas.GateInvalidate},
			},
			Answer: &schemas.Answer{Text: "the answer", Source: "file", SHA256: "anshash"},
		},
	}
}

// TestSyntheticJSONShape checks the JSON keys the OTLP/JSON encoding needs.
func TestSyntheticJSONShape(t *testing.T) {
	td, err := Build(syntheticInput(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(td)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"resourceSpans"`, `"scopeSpans"`, `"traceId"`, `"spanId"`, `"parentSpanId"`, `"startTimeUnixNano": "`, `"intValue": "`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("output lacks %s", key)
		}
	}
}
