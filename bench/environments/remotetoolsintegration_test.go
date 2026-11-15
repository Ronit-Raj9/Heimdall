//go:build integration

package environments

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// remoteMCPServer is a trivial MCP server speaking streamable HTTP with JSON
// replies: initialize, tools/list with one tool, tools/call.
const remoteMCPServer = `
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def send(self, code, obj=None):
        body = b"" if obj is None else json.dumps(obj).encode()
        self.send_response(code)
        if obj is not None:
            self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_GET(self):
        if self.path == "/healthz":
            return self.send(200, {})
        self.send(405)
    def do_POST(self):
        msg = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")
        if "id" not in msg:
            return self.send(202)
        m = msg.get("method")
        if m == "initialize":
            res = {"protocolVersion": "2025-06-18", "capabilities": {"tools": {}}, "serverInfo": {"name": "hd-rem", "version": "1"}}
        elif m == "tools/list":
            res = {"tools": [{"name": "add_note", "description": "Add a note.", "inputSchema": {"type": "object"}}]}
        elif m == "tools/call":
            res = {"content": [{"type": "text", "text": "noted " + json.dumps(msg.get("params", {}).get("arguments"))}]}
        else:
            return self.send(200, {"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32601, "message": "no method"}})
        self.send(200, {"jsonrpc": "2.0", "id": msg["id"], "result": res})
HTTPServer(("0.0.0.0", 8080), H).serve_forever()
`

// remoteAgentScript is the fake remote agent: a process outside the run's
// networks (host network, as if on another machine reaching the published
// address) that calls one MCP tool through the gate with the run's bearer
// token, and checks the gate refuses the call without it and for another
// run id.
const remoteAgentScript = `
import json, sys, urllib.request, urllib.error
url, token = sys.argv[1], sys.argv[2]
def call(u, tok):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "add_note", "arguments": {"text": "hello"}}}).encode()
    req = urllib.request.Request(u, data=body, method="POST", headers={"Content-Type": "application/json", "Accept": "application/json, text/event-stream"})
    if tok:
        req.add_header("Authorization", "Bearer " + tok)
    try:
        r = urllib.request.urlopen(req, timeout=20)
        return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()
code, text = call(url, None)
assert code == 401, ("no token", code, text)
code, text = call(url, "wrong-" + token)
assert code == 401, ("wrong token", code, text)
other = url.replace("/r/", "/r/x", 1)
code, text = call(other, token)
assert code == 404, ("other run", code, text)
code, text = call(url, token)
assert code == 200 and "noted" in text, ("with token", code, text)
print("remote agent ok")
`

// toolGateImage builds the bench image with this tree's binary, so the
// gate subcommand exists, and removes it after the test.
func toolGateImage(t *testing.T) string {
	t.Helper()
	const tag = "hd-rem-toolproxy:test"
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "bifrost-bench"), "./cmd/bifrost-bench")
	build.Dir = ".."
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	writeTree(t, dir, map[string]string{"Dockerfile": "FROM " + DefaultToolProxyImage + "\nCOPY bifrost-bench /usr/local/bin/bifrost-bench\n"})
	if out, err := exec.Command("docker", "build", "--network", "none", "-t", tag, dir).CombinedOutput(); err != nil {
		t.Fatalf("build tool gate image: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "image", "rm", "-f", tag).CombinedOutput(); err != nil {
			t.Errorf("remove %s: %v %s", tag, err, out)
		}
	})
	return tag
}

// TestRemoteToolsThroughPublishedGate runs a state task's services for a
// remote agent with remote.tools expose: the real tool proxy and the gate
// start, a fake remote agent in its own container calls the MCP tool through
// the published gate with the bearer token (and is refused without it), and
// the call lands in the tool ledger like a local agent's, without the token.
func TestRemoteToolsThroughPublishedGate(t *testing.T) {
	if exec.Command("docker", "image", "inspect", DefaultToolProxyImage).Run() != nil {
		t.Skip(DefaultToolProxyImage + " is not built")
	}
	ctx := context.Background()
	e, rc := scenarioRun(t, DockerWorkspaceConfig{ToolProxyImage: toolGateImage(t)}, map[string]string{"repo/README.md": "task\n"})
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
	rc.Agent = &schemas.AgentConfig{Budgets: schemas.Budgets{WallS: 120},
		Remote: &schemas.RemoteAgent{URL: "https://agent.example.com/run", Tools: schemas.RemoteToolsExpose}}
	rc.Task.Services = []schemas.ServiceSpec{
		{ID: "notes", Role: schemas.ServiceRoleMCP, Image: "python:3.12-slim", Argv: []string{"python3", "-c", remoteMCPServer}, Port: 8080,
			Ready: &schemas.ReadyCheck{HTTP: "/healthz", TimeoutS: 30}},
		{ID: "grader", Role: schemas.ServiceRoleData, Image: "python:3.12-slim", Argv: []string{"python3", "-m", "http.server", "8082"}, Port: 8082, Hidden: true},
	}
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	handles, results, err := e.StartServices(ctx, rc)
	if err != nil {
		t.Fatalf("start services: %v (results %+v)", err, results)
	}
	rc.Services = handles
	a := rc.RemoteTools
	if a == nil || strings.Join(a.Services, ",") != "notes" || len(a.Token) != 64 {
		t.Fatalf("remote tools %+v", a)
	}
	urls := utils.RemoteToolURLs(a)
	if _, ok := urls["grader"]; ok {
		t.Fatal("a hidden service was published")
	}
	url := urls["notes"].URL
	agent := "hd-rem-agent-" + strings.TrimPrefix(rc.Run.RunID, "hd-env-")
	out, err := exec.Command("docker", "run", "--rm", "--name", agent, "--network", "host", "--cap-drop", "ALL",
		"python:3.12-slim", "python3", "-c", remoteAgentScript, url, a.Token).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "remote agent ok") {
		t.Fatalf("fake remote agent: %v\n%s\n%s", err, out, e.logTail(ctx, remoteGateName(rc.Run.RunID)))
	}
	if _, err := e.ExportState(ctx, rc); err != nil {
		t.Fatalf("export state: %v", err)
	}
	ledger := string(readFile(t, filepath.Join(rc.Dir, ToolsFile)))
	if !strings.Contains(ledger, `"tool":"add_note"`) || !strings.Contains(ledger, `"server":"notes"`) {
		t.Fatalf("ledger has no add_note call:\n%s", ledger)
	}
	if strings.Contains(ledger, a.Token) {
		t.Fatal("the token reached the ledger")
	}
	if rec := string(readFile(t, filepath.Join(rc.Dir, schemas.RemoteToolsFile))); strings.Contains(rec, a.Token) || !strings.Contains(rec, "bench-rt-") {
		t.Fatalf("record %s", rec)
	}
}
