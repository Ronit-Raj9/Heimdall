//go:build integration

package toolproxy

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Integration test: the real binary in a proxy container in front of a tiny
// MCP server, on an internal network, called by a raw JSON-RPC client in a
// third container; then the ledger is read out of the proxy. Run with:
//
//	GOWORK=off go test -tags integration ./toolproxy/...
//
// It needs Docker, the bench image (BENCH_IMAGE, default
// bench/bifrost-bench:dev, used as the base the fresh binary is copied onto)
// and a Python image (BENCH_PYTHON_IMAGE, default python:3.12-slim). Every
// object it creates is named hd-tool-<random> and removed at the end.

const tinyMCP = `
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(405); self.end_headers()
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
            res = {"content": [{"type": "text", "text": "echo: " + req["params"]["arguments"].get("text", "")}], "isError": False}
        body = json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": res}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Mcp-Session-Id", "s1")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a):
        pass
HTTPServer(("", 8000), H).serve_forever()
`

// rawClient posts one JSON-RPC body (argv[1]) to the proxy and prints the
// status, the session header and the body; with argv[1] "health" it polls
// /healthz until it answers.
const rawClient = `
import sys, time, urllib.request
base = "http://toolproxy:9200"
if sys.argv[1] == "health":
    for _ in range(100):
        try:
            print(urllib.request.urlopen(base + "/healthz", timeout=1).status); sys.exit(0)
        except Exception:
            time.sleep(0.1)
    sys.exit(1)
req = urllib.request.Request(base + "/s/tiny/mcp", data=sys.argv[1].encode(), method="POST",
    headers={"Content-Type": "application/json", "Accept": "application/json, text/event-stream", "Mcp-Session-Id": "s1"})
with urllib.request.urlopen(req, timeout=30) as r:
    print(r.status, r.headers.get("Mcp-Session-Id"), r.read().decode())
`

func TestIntegrationProxyContainer(t *testing.T) {
	ctx := context.Background()
	d := utils.Docker{}
	benchImage := envOrDefault("BENCH_IMAGE", "bench/bifrost-bench:dev")
	pyImage := envOrDefault("BENCH_PYTHON_IMAGE", "python:3.12-slim")
	for _, img := range []string{benchImage, pyImage} {
		if _, err := d.MustRun(ctx, "image", "inspect", img); err != nil {
			t.Skipf("image %s not available: %v", img, err)
		}
	}
	id := strings.ToLower(utils.NewID("hd-tool", 8))
	image, network := id+":test", id+"-net"
	tp, mcp, cfg, ledger := id+"-tp", id+"-tiny", id+"-cfg", id+"-ledger"
	t.Cleanup(func() {
		for _, args := range [][]string{
			{"rm", "-f", tp, mcp}, {"network", "rm", network}, {"volume", "rm", cfg, ledger}, {"image", "rm", image},
		} {
			_, _ = d.Run(context.Background(), args...)
		}
	})

	// The image: the bench image with this tree's binary on top.
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
	mustDocker(t, d, "build", "-q", "-t", image, build)

	mustDocker(t, d, "network", "create", "--internal", network)
	mustDocker(t, d, "volume", "create", cfg)
	mustDocker(t, d, "volume", "create", ledger)
	mustDocker(t, d, "run", "--rm", "--user", "0", "--entrypoint", "sh", "-v", ledger+":/v", image, "-c", "chown 10001:10001 /v")
	mustDocker(t, d, "run", "-d", "--name", mcp, "--network", network, "--network-alias", "tiny", pyImage, "python", "-c", tinyMCP)
	mustDocker(t, d, "create", "--name", tp, "--network", network, "--network-alias", "toolproxy",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only", "--tmpfs", "/tmp",
		"-v", ledger+":/ledger", "-v", cfg+":/cfg", "--entrypoint", "bifrost-bench", image,
		"toolproxy", "-listen", ":9200", "-routes", "/cfg/routes.json", "-fault", "/cfg/fault.json", "-ledger", "/ledger/tools.jsonl", "-debug")
	routes := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(routes, []byte(`{"run_id":"`+id+`","routes":[{"service":"tiny","upstream":"http://tiny:8000","protocol":"mcp","mcp_path":"/mcp"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mustDocker(t, d, "cp", routes, tp+":/cfg/routes.json")
	mustDocker(t, d, "start", tp)

	client := func(arg string) string {
		t.Helper()
		res, err := d.Run(ctx, "run", "--rm", "--network", network, pyImage, "python", "-c", rawClient, arg)
		if err != nil || res.ExitCode != 0 {
			logs, _ := d.Run(ctx, "logs", tp)
			t.Fatalf("client %s: %v %s\nproxy logs:\n%s%s", arg, err, res.Stderr, logs.Stdout, logs.Stderr)
		}
		return strings.TrimSpace(string(res.Stdout))
	}
	client("health")
	if out := client(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`); !strings.Contains(out, "200 s1") || !strings.Contains(out, "tiny") {
		t.Fatalf("initialize through the proxy: %s", out)
	}
	client(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if out := client(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello"}}}`); !strings.Contains(out, "echo: hello") {
		t.Fatalf("tools/call: %s", out)
	}

	// Arm a fault the way the tool probe layer does, call, then disarm.
	fault := filepath.Join(t.TempDir(), "fault.json")
	if err := os.WriteFile(fault, []byte(`{"mode":"error","tool":"echo","nonce":"nonce_integration1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mustDocker(t, d, "cp", fault, tp+":/cfg/fault.json")
	if out := client(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"x"}}}`); !strings.Contains(out, `"isError": true`) && !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "nonce_integration1") {
		t.Fatalf("faulted call: %s", out)
	}
	mustDocker(t, d, "exec", "-u", "0", tp, "rm", "-f", "/cfg/fault.json")
	if out := client(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"text":"back"}}}`); !strings.Contains(out, "echo: back") {
		t.Fatalf("call after disarm: %s", out)
	}

	res := mustDocker(t, d, "exec", tp, "cat", "/ledger/tools.jsonl")
	rows, bad := ReadLedger(bufio.NewReader(bytes.NewReader(res.Stdout)))
	if bad != 0 || len(rows) != 4 {
		t.Fatalf("ledger has %d rows, %d bad:\n%s", len(rows), bad, res.Stdout)
	}
	want := []schemas.ToolCall{
		{Seq: 1, Tool: "tools/list"},
		{Seq: 2, Tool: "echo", Arguments: `{"text":"hello"}`, Status: 200},
		{Seq: 3, Tool: "echo", IsError: true, Fault: FaultError, Nonce: "nonce_integration1"},
		{Seq: 4, Tool: "echo", Status: 200},
	}
	for i, w := range want {
		r := rows[i]
		if r.Seq != w.Seq || r.Tool != w.Tool || r.IsError != w.IsError || r.Fault != w.Fault || r.Nonce != w.Nonce || r.Server != "tiny" || r.Protocol != ProtocolMCP {
			t.Errorf("row %d = %+v, want %+v", i+1, r, w)
		}
		if w.Arguments != "" && r.Arguments != w.Arguments {
			t.Errorf("row %d arguments %q", i+1, r.Arguments)
		}
		if r.ArgsSHA256 == "" || r.ResultSHA256 == "" || r.Started.Before(time.Now().Add(-10*time.Minute)) {
			t.Errorf("row %d incomplete: %+v", i+1, r)
		}
	}
	t.Logf("ledger:\n%s", res.Stdout)
}

func mustDocker(t *testing.T, d utils.Docker, args ...string) *utils.DockerResult {
	t.Helper()
	res, err := d.MustRun(context.Background(), args...)
	if err != nil {
		t.Fatalf("docker %s: %v", strings.Join(args, " "), err)
	}
	return res
}

func envOrDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
