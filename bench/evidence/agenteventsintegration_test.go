//go:build integration

package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Integration test of agent events with real Docker: the fresh binary as a
// tool proxy (collector included) in front of a tiny MCP server, and a tiny
// agent with no model in a third container that (a) posts native events,
// (b) sends OTLP JSON spans and an OTLP protobuf body is covered by unit
// tests, (c) appends to the events file in an aux volume, (d) prints a
// prefixed stdout line, calls a tool through the proxy, posts an oversized
// body and plants secrets. The ledgers are then read out the way the
// environment kernel copies them, and the agent_events source and the step
// timeline run on them. Run with:
//
//	GOWORK=off go test -tags integration -run AgentEventsDocker ./evidence/
//
// It needs Docker, the bench image (BENCH_IMAGE, default
// bench/bifrost-bench:dev, the base the fresh binary is copied onto) and a
// Python image (BENCH_PYTHON_IMAGE, default python:3.12-slim). Every object
// is named hd-ev-<random> and removed at the end.

const eventsTinyMCP = `
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        req = json.loads(self.rfile.read(n))
        if "id" not in req:
            self.send_response(202); self.end_headers(); return
        m = req.get("method")
        if m == "initialize":
            res = {"protocolVersion": "2025-06-18", "capabilities": {"tools": {}}, "serverInfo": {"name": "tiny", "version": "1"}}
        elif m == "tools/list":
            res = {"tools": [{"name": "echo", "inputSchema": {"type": "object"}}]}
        else:
            res = {"content": [{"type": "text", "text": "echo"}], "isError": False}
        body = json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": res}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a):
        pass
HTTPServer(("", 8000), H).serve_forever()
`

// eventsAgent is the tiny agent: no model, only reports and one tool call.
const eventsAgent = `
import json, os, sys, time, urllib.request, urllib.error
base = "http://toolproxy:9200"
for _ in range(100):
    try:
        urllib.request.urlopen(base + "/healthz", timeout=1); break
    except Exception:
        time.sleep(0.1)
def post(path, body, ctype):
    req = urllib.request.Request(base + path, data=body, method="POST", headers={"Content-Type": ctype})
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status
    except urllib.error.HTTPError as e:
        return e.code
    except urllib.error.URLError:
        return "reset"  # the collector answered 413 and closed while the body was still being sent
def now(off=0.0):
    return int((time.time() + off) * 1000)
# A tool call through the proxy: the trusted witness.
t0 = now()
post("/s/tiny/mcp", json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "echo", "arguments": {}}}).encode(), "application/json")
t1 = now()
# (a) native events over HTTP, with a planted secret.
lines = [
    {"type": "subagent_start", "id": "sa", "name": "researcher", "start_time": now(-0.5)},
    {"type": "plan", "id": "p1", "parent_id": "sa", "output": "call echo; api_key=hunter2-planted", "start_time": now(-0.4)},
    {"type": "tool_call", "id": "tc", "parent_id": "sa", "name": "echo", "start_time": t0, "end_time": t1, "status": "ok"},
    "not an event",
]
print("native", post("/events", "\n".join(json.dumps(l) if isinstance(l, dict) else l for l in lines).encode(), "application/x-ndjson"))
# (b) OTLP JSON spans: an agent span with a nested subagent and a tool.
ns = lambda ms: str(ms * 1000000)
otlp = {"resourceSpans": [{"resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "tiny"}}]},
  "scopeSpans": [{"spans": [
    {"traceId": "0af7651916cd43dd8448eb211c80319c", "spanId": "00000000000000a1", "name": "invoke_agent root", "startTimeUnixNano": ns(now(-1)), "endTimeUnixNano": ns(now()),
     "attributes": [{"key": "gen_ai.operation.name", "value": {"stringValue": "invoke_agent"}}]},
    {"traceId": "0af7651916cd43dd8448eb211c80319c", "spanId": "00000000000000a2", "parentSpanId": "00000000000000a1", "name": "invoke_agent helper", "startTimeUnixNano": ns(now(-0.9)), "endTimeUnixNano": ns(now(-0.2)),
     "attributes": [{"key": "gen_ai.operation.name", "value": {"stringValue": "invoke_agent"}}, {"key": "gen_ai.agent.name", "value": {"stringValue": "helper"}}]},
    {"traceId": "0af7651916cd43dd8448eb211c80319c", "spanId": "00000000000000a3", "parentSpanId": "00000000000000a2", "name": "execute_tool lookup", "startTimeUnixNano": ns(now(-0.8)), "endTimeUnixNano": ns(now(-0.7)),
     "attributes": [{"key": "gen_ai.operation.name", "value": {"stringValue": "execute_tool"}}, {"key": "gen_ai.tool.name", "value": {"stringValue": "internal_lookup"}}, {"key": "gen_ai.tool.call.arguments", "value": {"stringValue": "token: sk-planted-0123456789abcdef"}}]}
  ]}]}]}
print("otlp", post("/otlp/v1/traces", json.dumps(otlp).encode(), "application/json"))
# Over the request cap: dropped and counted.
print("big", post("/events", b"x" * (4 << 20 + 10), "application/x-ndjson"))
# (c) the events file.
with open(os.environ["BENCH_EVENTS_FILE"], "a") as f:
    f.write(json.dumps({"type": "memory_write", "name": "notes", "start_time": now()}) + "\n")
    f.write("{torn\n")
# (d) a prefixed stdout line.
print("@@heimdall-event " + json.dumps({"type": "handoff", "name": "to-writer", "start_time": now()}))
sys.stdout.flush()
`

func TestAgentEventsDocker(t *testing.T) {
	ctx := context.Background()
	d := utils.Docker{}
	benchImage := envOr("BENCH_IMAGE", "bench/bifrost-bench:dev")
	pyImage := envOr("BENCH_PYTHON_IMAGE", "python:3.12-slim")
	for _, img := range []string{benchImage, pyImage} {
		if _, err := d.MustRun(ctx, "image", "inspect", img); err != nil {
			t.Skipf("image %s not available: %v", img, err)
		}
	}
	id := strings.ToLower(utils.NewID("hd-ev", 8))
	image, network := id+":test", id+"-net"
	tp, mcp, cfg, ledger, aux := id+"-tp", id+"-tiny", id+"-cfg", id+"-ledger", id+"-aux"
	t.Cleanup(func() {
		for _, args := range [][]string{
			{"rm", "-f", tp, mcp}, {"network", "rm", network}, {"volume", "rm", cfg, ledger, aux}, {"image", "rm", image},
		} {
			_, _ = d.Run(context.Background(), args...)
		}
	})
	build := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(build, "bifrost-bench"), "../cmd/bifrost-bench")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	df := "FROM " + benchImage + "\nCOPY bifrost-bench /usr/local/bin/bifrost-bench\n"
	if err := os.WriteFile(filepath.Join(build, "Dockerfile"), []byte(df), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) *utils.DockerResult {
		t.Helper()
		res, err := d.Run(ctx, args...)
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("docker %v: %v %s", args, err, res.Stderr)
		}
		return res
	}
	run("build", "-q", "-t", image, build)
	run("network", "create", "--internal", network)
	for _, v := range []string{cfg, ledger, aux} {
		run("volume", "create", v)
	}
	run("run", "--rm", "--user", "0", "--entrypoint", "sh", "-v", ledger+":/v", "-v", aux+":/a", image, "-c", "chown 10001:10001 /v && mkdir -p /a/events && chmod 1777 /a/events")
	run("run", "-d", "--name", mcp, "--network", network, "--network-alias", "tiny", pyImage, "python", "-c", eventsTinyMCP)
	run("create", "--name", tp, "--network", network, "--network-alias", "toolproxy",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only", "--tmpfs", "/tmp",
		"-v", ledger+":/ledger", "-v", cfg+":/cfg", "--entrypoint", "bifrost-bench", image,
		"toolproxy", "-listen", ":9200", "-routes", "/cfg/routes.json", "-fault", "/cfg/fault.json", "-ledger", "/ledger/tools.jsonl")
	routes := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(routes, []byte(`{"run_id":"`+id+`","routes":[{"service":"tiny","upstream":"http://tiny:8000","protocol":"mcp","mcp_path":"/mcp"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run("cp", routes, tp+":/cfg/routes.json")
	run("start", tp)
	agent := run("run", "--rm", "--network", network, "--user", "1000:1000", "-v", aux+":/opt/bench",
		"-e", "BENCH_EVENTS_FILE=/opt/bench/events/events.jsonl", pyImage, "python", "-c", eventsAgent)
	out := string(agent.Stdout)
	if !strings.Contains(out, "native 202") || !strings.Contains(out, "otlp 200") || !(strings.Contains(out, "big 413") || strings.Contains(out, "big reset")) {
		t.Fatalf("agent output:\n%s\n%s", out, agent.Stderr)
	}

	// Copy what the environment kernel copies into a run folder.
	rc := testRC(t)
	rc.Key = &schemas.RunKey{Value: "sk-bench-integration-key-0001"}
	rc.Handle = &schemas.EnvironmentHandle{AuxVolume: aux}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(rc.Dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(collectorLedgerFile, run("exec", tp, "cat", "/ledger/agent-events.jsonl").Stdout)
	write(collectorCountsFile, run("exec", tp, "cat", "/ledger/agent-events.counts.json").Stdout)
	write(collectorStateFile, []byte(`{"collector":"on"}`))
	write("agent.stream.jsonl", agent.Stdout)
	rc.Exec = &schemas.ExecResult{StdoutPath: filepath.Join(rc.Dir, "agent.stream.jsonl"), Ended: time.Now()}
	ev := &schemas.Evidence{}
	for _, line := range bytes.Split(run("exec", tp, "cat", "/ledger/tools.jsonl").Stdout, []byte("\n")) {
		var c schemas.ToolCall
		if json.Unmarshal(line, &c) == nil && c.Tool != "" {
			ev.ToolCalls = append(ev.ToolCalls, c)
		}
	}
	if len(ev.ToolCalls) == 0 {
		t.Fatal("no tool proxy row")
	}
	src := newAgentEventsSource(Deps{Docker: d, HelperImage: pyImage})
	if err := src.Collect(ctx, rc, ev); err != nil {
		t.Fatal(err)
	}
	st := ev.AgentEvents
	t.Logf("status %+v", st)
	if st == nil || st.Channels["native"] != 3 || st.Channels["otlp"] != 3 || st.Channels["file"] != 1 || st.Channels["stdout"] != 1 {
		t.Fatalf("channels: %+v", st)
	}
	// One bad native line, one oversized body (collector), one torn file line.
	if st.Dropped != 3 {
		t.Fatalf("dropped %d, want 3", st.Dropped)
	}
	raw, _ := os.ReadFile(filepath.Join(rc.Dir, schemas.AgentEventsFile))
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "sk-planted") {
		t.Fatalf("planted secret kept:\n%s", raw)
	}
	steps, _ := BuildTimeline(ev)
	var echo, lookup *schemas.StepRecord
	reported := 0
	for i, s := range steps {
		if s.Source != schemas.StepSourceReported {
			continue
		}
		reported++
		if s.Counted {
			t.Fatalf("reported row counted: %+v", s)
		}
		switch s.ToolName {
		case "echo":
			echo = &steps[i]
		case "internal_lookup":
			lookup = &steps[i]
		}
	}
	if reported != 8 || echo == nil || lookup == nil {
		t.Fatalf("%d reported rows; echo %v lookup %v", reported, echo, lookup)
	}
	if echo.Corroboration != schemas.Corroborated || steps[echo.WitnessOf-1].Source != schemas.StepSourceToolProxy {
		t.Fatalf("echo not corroborated by the proxy row: %+v", echo)
	}
	if lookup.Corroboration != schemas.ReportedOnly || lookup.ParentSeq == 0 || steps[lookup.ParentSeq-1].EventType != "subagent_start" {
		t.Fatalf("lookup: %+v", lookup)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// otelAgent is an agent that knows nothing of the bench: it creates GenAI
// spans through the OpenTelemetry API only, and is started under
// opentelemetry-instrument, so the SDK, exporter, endpoint and protocol come
// from the environment the bench sets.
const otelAgent = `
from opentelemetry import trace
tr = trace.get_tracer("tiny-framework")
with tr.start_as_current_span("invoke_agent planner", attributes={"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "planner"}):
    with tr.start_as_current_span("invoke_agent coder", attributes={"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "coder"}):
        with tr.start_as_current_span("execute_tool read_file", attributes={"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "read_file", "gen_ai.tool.call.id": "call_1", "gen_ai.tool.call.arguments": "{\"path\": \"a.py\"}"}):
            pass
print("spans made")
`

// TestAgentEventsOTelSDKDocker exports spans with the real Python
// OpenTelemetry SDK (http/protobuf, the bench's setting) to the collector
// running alone (bifrost-bench eventcollector), as for a run with no
// proxied services. It installs the SDK into a throwaway image with pip; with
// no network it is skipped.
func TestAgentEventsOTelSDKDocker(t *testing.T) {
	ctx := context.Background()
	d := utils.Docker{}
	benchImage := envOr("BENCH_IMAGE", "bench/bifrost-bench:dev")
	pyImage := envOr("BENCH_PYTHON_IMAGE", "python:3.12-slim")
	for _, img := range []string{benchImage, pyImage} {
		if _, err := d.MustRun(ctx, "image", "inspect", img); err != nil {
			t.Skipf("image %s not available: %v", img, err)
		}
	}
	id := strings.ToLower(utils.NewID("hd-ev", 8))
	image, otelImage, network, ec, ledger := id+":test", id+"-otel:test", id+"-net", id+"-ec", id+"-ledger"
	t.Cleanup(func() {
		for _, args := range [][]string{{"rm", "-f", ec}, {"network", "rm", network}, {"volume", "rm", ledger}, {"image", "rm", image, otelImage}} {
			_, _ = d.Run(context.Background(), args...)
		}
	})
	run := func(args ...string) *utils.DockerResult {
		t.Helper()
		res, err := d.Run(ctx, args...)
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("docker %v: %v %s", args, err, res.Stderr)
		}
		return res
	}
	sdk := t.TempDir()
	if err := os.WriteFile(filepath.Join(sdk, "Dockerfile"), []byte("FROM "+pyImage+"\nRUN pip install -q --no-cache-dir setuptools==75.8.0 opentelemetry-distro==0.48b0 opentelemetry-sdk==1.27.0 opentelemetry-exporter-otlp-proto-http==1.27.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := d.Run(ctx, "build", "-q", "-t", otelImage, sdk); err != nil || res.ExitCode != 0 {
		t.Skipf("could not install the OpenTelemetry SDK (no network?): %v %s", err, res.Stderr)
	}
	build := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(build, "bifrost-bench"), "../cmd/bifrost-bench")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(build, "Dockerfile"), []byte("FROM "+benchImage+"\nCOPY bifrost-bench /usr/local/bin/bifrost-bench\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("build", "-q", "-t", image, build)
	run("network", "create", "--internal", network)
	run("volume", "create", ledger)
	run("run", "--rm", "--user", "0", "--entrypoint", "sh", "-v", ledger+":/v", image, "-c", "chown 10001:10001 /v")
	run("run", "-d", "--name", ec, "--network", network, "--network-alias", "toolproxy", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--read-only", "-v", ledger+":/ledger", "--entrypoint", "bifrost-bench", image,
		"eventcollector", "-listen", ":9200", "-ledger", "/ledger/agent-events.jsonl")
	// The environment the bench gives the agent (environments/eventcollector.go).
	env := []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://toolproxy:9200/otlp",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://toolproxy:9200/otlp/v1/traces",
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf", "OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=none", "OTEL_LOGS_EXPORTER=none",
		"OTEL_SERVICE_NAME=tiny", "OTEL_RESOURCE_ATTRIBUTES=heimdall.run_id=" + id,
	}
	args := []string{"run", "--rm", "--network", network, "--user", "1000:1000"}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	time.Sleep(time.Second) // the collector's first second
	agent := run(append(args, otelImage, "opentelemetry-instrument", "python", "-c", otelAgent)...)
	if !strings.Contains(string(agent.Stdout), "spans made") {
		t.Fatalf("agent: %s %s", agent.Stdout, agent.Stderr)
	}
	rc := testRC(t)
	if err := os.WriteFile(filepath.Join(rc.Dir, collectorLedgerFile), run("exec", ec, "cat", "/ledger/agent-events.jsonl").Stdout, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rc.Dir, collectorStateFile), []byte(`{"collector":"on"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := &schemas.Evidence{}
	if err := newAgentEventsSource(Deps{}).Collect(ctx, rc, ev); err != nil {
		t.Fatal(err)
	}
	t.Logf("status %+v; stderr %s", ev.AgentEvents, agent.Stderr)
	if st := ev.AgentEvents; st == nil || st.Channels["otlp"] != 3 || st.Kinds["agent"] != 1 || st.Kinds["subagent_start"] != 1 || st.Kinds["tool_call"] != 1 {
		t.Fatalf("status %+v", st)
	}
	steps, _ := BuildTimeline(ev)
	var tool *schemas.StepRecord
	for i, s := range steps {
		if s.ToolName == "read_file" {
			tool = &steps[i]
		}
	}
	if tool == nil || tool.ToolCallID != "call_1" || tool.ParentSeq == 0 || steps[tool.ParentSeq-1].EventType != "subagent_start" {
		t.Fatalf("tool row %+v in %+v", tool, steps)
	}
}
