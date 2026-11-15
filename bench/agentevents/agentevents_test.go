package agentevents

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var t0 = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func opts() Options {
	return Options{RunID: "r1", Secrets: []string{"sk-bench-run-key-0123456789"}, Now: func() time.Time { return t0 }}
}

func TestNativeLinesNestingAndCaps(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"subagent_start","id":"a1","name":"researcher","start_time":"2026-11-15T12:00:01Z"}`,
		`{"type":"plan","id":"p1","parent_id":"a1","output":"1. search 2. write","start_time":1794830402000}`,
		`{"type":"tool_call","id":"t1","parent_id":"a1","name":"search","tool_call_id":"call_9","input":{"q":"x","api_key":"hunter2-secret"},"start_time":"2026-11-15T12:00:03Z","end_time":"2026-11-15T12:00:04Z","status":"ok"}`,
		`not json`,
		`{"name":"no type"}`,
		`{"type":"weird_kind","id":"w"}`,
		`{"type":"message","output":"token sk-bench-run-key-0123456789 leaked"}`,
	}, "\n")
	evs, bad := FromNativeJSONL([]byte(in), schemas.ReportChannelFile, opts())
	if bad != 2 || len(evs) != 5 {
		t.Fatalf("events %d bad %d", len(evs), bad)
	}
	byType := map[string]schemas.Event{}
	for _, e := range evs {
		byType[e.ReportedType] = e
		if e.Source != schemas.SourceAgentReported || e.EvidenceSource != schemas.EvidenceSourceAgentReported || e.EvidenceStrength != schemas.EvidencePartial || e.Channel != "file" {
			t.Fatalf("origin: %+v", e)
		}
	}
	sub, tc := byType["subagent_start"], byType["tool_call"]
	if tc.ParentEventID != sub.EventID || byType["plan"].ParentEventID != sub.EventID {
		t.Fatalf("nesting: %q %q want %q", tc.ParentEventID, byType["plan"].ParentEventID, sub.EventID)
	}
	if tc.Type != schemas.EventAgentStep || tc.ToolName != "search" || tc.ToolCallID != "call_9" || tc.DurationMS == nil || *tc.DurationMS != 1000 {
		t.Fatalf("tool call: %+v", tc)
	}
	if strings.Contains(tc.Target, "hunter2") || strings.Contains(tc.Summary, "hunter2") {
		t.Fatalf("secret kept: %q %q", tc.Target, tc.Summary)
	}
	if strings.Contains(byType["message"].Summary, "sk-bench") {
		t.Fatalf("run key kept: %q", byType["message"].Summary)
	}
	if _, ok := byType["custom"]; !ok {
		t.Fatal("unknown type not kept as custom")
	}
	// Event cap.
	o := opts()
	o.Limits.MaxEvents = 2
	evs, bad = FromNativeJSONL([]byte(in), schemas.ReportChannelFile, o)
	if len(evs) != 2 || bad != 2+3 {
		t.Fatalf("cap: %d events %d dropped", len(evs), bad)
	}
}

func TestNativeStdoutPrefixAndArray(t *testing.T) {
	out := "hello\n@@heimdall-event {\"type\":\"handoff\",\"name\":\"to-writer\"}\n\x1b[0m@@heimdall-event {\"type\":\"memory_write\",\"name\":\"k\"}\nplain {\"type\":\"plan\"}\n"
	evs, bad := FromNativeJSONL([]byte(out), schemas.ReportChannelStdout, opts())
	if len(evs) != 2 || bad != 0 || evs[0].ReportedType != "handoff" {
		t.Fatalf("stdout: %+v bad %d", evs, bad)
	}
	evs, bad = FromNativeJSONL([]byte(`[{"type":"plan"},{"type":"retrieval","name":"docs"}]`), schemas.ReportChannelNative, opts())
	if len(evs) != 2 || bad != 0 {
		t.Fatalf("array: %d %d", len(evs), bad)
	}
}

func TestFieldCapsAndControlChars(t *testing.T) {
	long := strings.Repeat("é", 5000)
	line, _ := json.Marshal(map[string]any{"type": "tool_result", "name": long, "output": long + "\x00\x1b[31m", "id": long})
	recs, _ := ParseNative(line, schemas.ReportChannelNative, Limits{}, t0)
	r := recs[0]
	if len(r.Name) > maxName+3 || len(r.Output) > DefaultMaxText+3 || len(r.ID) > maxID+3 {
		t.Fatalf("caps: %d %d %d", len(r.Name), len(r.Output), len(r.ID))
	}
	if strings.ContainsAny(r.Output, "\x00\x1b") {
		t.Fatal("control characters kept")
	}
}

const otlpJSON = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"my-agent"}}]},
"scopeSpans":[{"scope":{"name":"x"},"spans":[
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"aaaaaaaaaaaaaaa1","name":"invoke_agent planner","startTimeUnixNano":"1794830400000000000","endTimeUnixNano":"1794830410000000000","attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"invoke_agent"}},{"key":"gen_ai.agent.name","value":{"stringValue":"planner"}}],"status":{"code":1}},
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"aaaaaaaaaaaaaaa2","parentSpanId":"aaaaaaaaaaaaaaa1","name":"invoke_agent coder","startTimeUnixNano":"1794830401000000000","endTimeUnixNano":"1794830405000000000","attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"invoke_agent"}},{"key":"gen_ai.agent.name","value":{"stringValue":"coder"}}]},
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"aaaaaaaaaaaaaaa3","parentSpanId":"aaaaaaaaaaaaaaa2","name":"execute_tool read_file","startTimeUnixNano":1794830402000000000,"endTimeUnixNano":"1794830402500000000","attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"execute_tool"}},{"key":"gen_ai.tool.name","value":{"stringValue":"read_file"}},{"key":"gen_ai.tool.call.id","value":{"stringValue":"call_1"}},{"key":"gen_ai.tool.call.arguments","value":{"stringValue":"{\"path\":\"a.py\"}"}}],"status":{"code":"STATUS_CODE_ERROR","message":"boom"}},
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"aaaaaaaaaaaaaaa4","parentSpanId":"aaaaaaaaaaaaaaa2","name":"chat gpt","startTimeUnixNano":"1794830403000000000","attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"chat"}},{"key":"gen_ai.request.model","value":{"stringValue":"m1"}}]},
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"aaaaaaaaaaaaaaa5","parentSpanId":"aaaaaaaaaaaaaaa2","name":"search","startTimeUnixNano":"1794830404000000000","attributes":[{"key":"openinference.span.kind","value":{"stringValue":"TOOL"}},{"key":"tool.name","value":{"stringValue":"web_search"}},{"key":"input.value","value":{"stringValue":"q"}}]}
]}]}]}`

func TestOTLPJSONGenAIAndOpenInference(t *testing.T) {
	evs, bad, err := FromOTLP([]byte(otlpJSON), "application/json", opts())
	if err != nil || bad != 0 || len(evs) != 5 {
		t.Fatalf("err %v bad %d n %d", err, bad, len(evs))
	}
	checkOTLP(t, evs)
}

func checkOTLP(t *testing.T, evs []schemas.Event) {
	t.Helper()
	types := []string{}
	for _, e := range evs {
		types = append(types, e.ReportedType)
	}
	want := []string{"agent", "subagent_start", "tool_call", "llm", "tool_call"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("types %v want %v", types, want)
	}
	if evs[1].ParentEventID != evs[0].EventID || evs[2].ParentEventID != evs[1].EventID {
		t.Fatalf("parents: %q %q", evs[1].ParentEventID, evs[2].ParentEventID)
	}
	tc := evs[2]
	if tc.ToolName != "read_file" || tc.ToolCallID != "call_1" || !tc.IsError || tc.Channel != "otlp" || tc.DurationMS == nil || *tc.DurationMS != 500 {
		t.Fatalf("tool span: %+v", tc)
	}
	if evs[4].ToolName != "web_search" || evs[3].ModelServed != "m1" {
		t.Fatalf("openinference/model: %+v %+v", evs[4], evs[3])
	}
}

// Minimal protobuf encoder for tests: the inverse of pbwire.go.
type pb struct{ b []byte }

func (p *pb) key(n, wire int) { p.b = binary.AppendUvarint(p.b, uint64(n<<3|wire)) }
func (p *pb) bytes(n int, v []byte) *pb {
	p.key(n, wireBytes)
	p.b = binary.AppendUvarint(p.b, uint64(len(v)))
	p.b = append(p.b, v...)
	return p
}
func (p *pb) str(n int, s string) *pb { return p.bytes(n, []byte(s)) }
func (p *pb) fixed64(n int, v uint64) *pb {
	p.key(n, wireFixed64)
	p.b = binary.LittleEndian.AppendUint64(p.b, v)
	return p
}
func (p *pb) varint(n int, v uint64) *pb {
	p.key(n, wireVarint)
	p.b = binary.AppendUvarint(p.b, v)
	return p
}
func kvStr(k, v string) []byte {
	return (&pb{}).str(1, k).bytes(2, (&pb{}).str(1, v).b).b
}
func hexb(s string) []byte { b, _ := hex.DecodeString(s); return b }

func protoRequest() []byte {
	trace := "0af7651916cd43dd8448eb211c80319c"
	mk := func(id, parent, name string, start, end uint64, attrs [][]byte, code uint64, msg string) []byte {
		s := (&pb{}).bytes(1, hexb(trace)).bytes(2, hexb(id))
		if parent != "" {
			s.bytes(4, hexb(parent))
		}
		s.str(5, name).varint(6, 1).fixed64(7, start)
		if end > 0 {
			s.fixed64(8, end)
		}
		for _, a := range attrs {
			s.bytes(9, a)
		}
		s.varint(10, 0)
		if code > 0 {
			s.bytes(15, (&pb{}).str(2, msg).varint(3, code).b)
		}
		return s.b
	}
	spans := [][]byte{
		mk("aaaaaaaaaaaaaaa1", "", "invoke_agent planner", 1794830400000000000, 1794830410000000000, [][]byte{kvStr("gen_ai.operation.name", "invoke_agent"), kvStr("gen_ai.agent.name", "planner")}, 1, ""),
		mk("aaaaaaaaaaaaaaa2", "aaaaaaaaaaaaaaa1", "invoke_agent coder", 1794830401000000000, 1794830405000000000, [][]byte{kvStr("gen_ai.operation.name", "invoke_agent"), kvStr("gen_ai.agent.name", "coder")}, 0, ""),
		mk("aaaaaaaaaaaaaaa3", "aaaaaaaaaaaaaaa2", "execute_tool read_file", 1794830402000000000, 1794830402500000000, [][]byte{kvStr("gen_ai.operation.name", "execute_tool"), kvStr("gen_ai.tool.name", "read_file"), kvStr("gen_ai.tool.call.id", "call_1")}, 2, "boom"),
		mk("aaaaaaaaaaaaaaa4", "aaaaaaaaaaaaaaa2", "chat gpt", 1794830403000000000, 0, [][]byte{kvStr("gen_ai.operation.name", "chat"), kvStr("gen_ai.request.model", "m1"),
			(&pb{}).str(1, "n").bytes(2, (&pb{}).fixed64(4, math.Float64bits(1.5)).b).b}, 0, ""),
		mk("aaaaaaaaaaaaaaa5", "aaaaaaaaaaaaaaa2", "search", 1794830404000000000, 0, [][]byte{kvStr("openinference.span.kind", "TOOL"), kvStr("tool.name", "web_search")}, 0, ""),
	}
	scope := (&pb{}).bytes(1, (&pb{}).str(1, "scope").b)
	for _, s := range spans {
		scope.bytes(2, s)
	}
	res := (&pb{}).bytes(1, kvStr("service.name", "my-agent"))
	rs := (&pb{}).bytes(1, res.b).bytes(2, scope.b).str(3, "https://schema")
	return (&pb{}).bytes(1, rs.b).b
}

func TestOTLPProtobuf(t *testing.T) {
	evs, bad, err := FromOTLP(protoRequest(), "application/x-protobuf", opts())
	if err != nil || bad != 0 || len(evs) != 5 {
		t.Fatalf("err %v bad %d n %d", err, bad, len(evs))
	}
	checkOTLP(t, evs)
	if _, _, err := FromOTLP([]byte{0xff, 0xff, 0xff}, "application/x-protobuf", opts()); err == nil {
		t.Fatal("malformed protobuf accepted")
	}
	if _, _, err := FromOTLP([]byte(`{"x":1}`), "application/json", opts()); err == nil {
		t.Fatal("non-OTLP JSON accepted")
	}
}

func TestCollectorCapsAndCounts(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCollector(filepath.Join(dir, LedgerName), Limits{MaxEvents: 3, MaxBody: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := httptest.NewServer(c)
	defer srv.Close()
	post := func(path, ct string, body []byte) int {
		resp, err := http.Post(srv.URL+path, ct, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if s := post(PathEvents, "application/x-ndjson", []byte("{\"type\":\"plan\"}\n{\"type\":\"handoff\"}\nbad\n")); s != http.StatusAccepted {
		t.Fatalf("events status %d", s)
	}
	if s := post(PathOTLP, "application/x-protobuf", protoRequest()); s != http.StatusOK {
		t.Fatalf("otlp status %d", s)
	}
	if s := post(PathEvents, "application/json", bytes.Repeat([]byte("x"), 5000)); s != http.StatusRequestEntityTooLarge {
		t.Fatalf("big body status %d", s)
	}
	if s := post(PathOTLP, "application/json", []byte("nope")); s != http.StatusUnsupportedMediaType {
		t.Fatalf("bad otlp status %d", s)
	}
	got := c.Counts()
	// 2 native kept + 1 span kept (cap 3) + 4 spans over cap + 1 bad line + 1 big body + 1 bad otlp.
	if got.Kept != 3 || got.Dropped != 7 || got.Received != 10 {
		t.Fatalf("counts %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, LedgerName))
	recs, bad := ReadRecords(raw)
	if len(recs) != 3 || bad != 0 {
		t.Fatalf("ledger %d bad %d", len(recs), bad)
	}
	cr, _ := os.ReadFile(filepath.Join(dir, CounterName))
	if cc, err := ReadCounts(cr); err != nil || cc != got {
		t.Fatalf("counts file %+v %v", cc, err)
	}
	resp, _ := http.Get(srv.URL + PathEvents)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestInternalStep(t *testing.T) {
	for typ, want := range map[string]bool{"plan": true, "tool_call": true, "subagent_start": true, "message": false, "llm": false, "tool_result": false, "thought_summary": false} {
		if InternalStep(typ) != want {
			t.Fatalf("%s", typ)
		}
	}
}
