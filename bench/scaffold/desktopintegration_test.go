//go:build integration

package scaffold

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// desktopClient is a raw MCP client, Python standard library only, that does
// what a computer-use agent does through the desktop's MCP server: list the
// tools, take a screenshot, type a line at the end of the open file, save.
// It prints one JSON line with what it saw.
const desktopClient = `
import base64, json, struct, sys, urllib.request
URL = sys.argv[1]
session = {}
def rpc(method, params=None, notify=False):
    msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
    if not notify:
        msg["id"] = rpc.n = getattr(rpc, "n", 0) + 1
    h = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    h.update(session)
    req = urllib.request.Request(URL, data=json.dumps(msg).encode(), headers=h, method="POST")
    with urllib.request.urlopen(req, timeout=60) as r:
        if r.headers.get("Mcp-Session-Id"):
            session["Mcp-Session-Id"] = r.headers["Mcp-Session-Id"]
        body = r.read().decode()
    return None if notify else json.loads(body)["result"]
def call(name, args=None):
    res = rpc("tools/call", {"name": name, "arguments": args or {}})
    if res.get("isError"):
        sys.exit(name + ": " + res["content"][0]["text"])
    return res["content"]
rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "it", "version": "1"}})
rpc("notifications/initialized", notify=True)
tools = [t["name"] for t in rpc("tools/list")["tools"]]
png = base64.b64decode(next(c for c in call("screenshot") if c["type"] == "image")["data"])
small = base64.b64decode(next(c for c in call("screenshot", {"max_width": 640}) if c["type"] == "image")["data"])
call("key", {"combo": "ctrl+End"})
call("type", {"text": "\nadded = through the gui"})
call("key", {"combo": "ctrl+s"})
call("wait", {"ms": 1500})
bad = rpc("tools/call", {"name": "click", "arguments": {"x": 5000, "y": 1}})
print(json.dumps({"tools": tools, "magic": png[:8].hex(), "size": list(struct.unpack(">II", png[16:24])),
                  "small": list(struct.unpack(">II", small[16:24])), "bad_click_error": bad.get("isError")}))
`

// TestIntegrationDesktop starts the acme-desktop image on an internal
// network as a run would, drives it with a raw MCP client from a second
// container, and checks the screenshot, the saved change in the state dump
// and that the desktop has no route out. It needs Docker and the image
// (BENCH_DESKTOP_IMAGE, default heimdall-example/acme-desktop:1); without
// them it skips. Every object it creates is named hd-cu-it-<random> and
// removed at the end.
func TestIntegrationDesktop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	image := os.Getenv("BENCH_DESKTOP_IMAGE")
	if image == "" {
		image = "heimdall-example/acme-desktop:1"
	}
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", image).Run(); err != nil {
		t.Skipf("image %s not available: %v", image, err)
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	id := "hd-cu-it-" + hex.EncodeToString(buf)
	desk, net := id+"-desktop", id+"-net"
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", desk, id+"-client").Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
	})
	docker(t, ctx, "network", "create", "--internal", "--label", "bifrost-bench=1", net)
	docker(t, ctx, "create", "--name", desk, "--label", "bifrost-bench=1", "--network", net, "--network-alias", "desktop",
		"--security-opt", "no-new-privileges", "--pids-limit", "1024", "--memory", "2g",
		"-e", "DESKTOP_APP=editor", "-e", "DESKTOP_OPEN=/home/desk/Documents/notes.conf", image)
	// The seed lands under /seed/home, as a task's files: entries do; the
	// desktop copies it into /home/desk at start.
	seed := filepath.Join(t.TempDir(), "seed")
	if err := os.MkdirAll(filepath.Join(seed, "home", "Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "home", "Documents", "notes.conf"), []byte("[notes]\ntitle = integration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	docker(t, ctx, "cp", seed, desk+":/")
	docker(t, ctx, "start", desk)
	waitReady(t, ctx, desk)

	out := docker(t, ctx, "run", "--rm", "--name", id+"-client", "--label", "bifrost-bench=1", "--network", net,
		"--entrypoint", "python3", image, "-I", "-c", desktopClient, "http://desktop:8000/mcp")
	var seen struct {
		Tools         []string `json:"tools"`
		Magic         string   `json:"magic"`
		Size          []int    `json:"size"`
		Small         []int    `json:"small"`
		BadClickError bool     `json:"bad_click_error"`
	}
	if err := json.Unmarshal([]byte(lastLine(out)), &seen); err != nil {
		t.Fatalf("client output %q: %v", out, err)
	}
	if seen.Magic != "89504e470d0a1a0a" || len(seen.Size) != 2 || seen.Size[0] != 1280 || seen.Size[1] != 800 {
		t.Errorf("screenshot magic %s size %v, want a 1280x800 PNG", seen.Magic, seen.Size)
	}
	if len(seen.Small) != 2 || seen.Small[0] != 640 || seen.Small[1] != 400 {
		t.Errorf("downscaled screenshot size %v, want 640x400", seen.Small)
	}
	if !seen.BadClickError || strings.Join(seen.Tools, ",") != "screenshot,click,move,drag,type,key,scroll,wait,cursor_position,screen_size" {
		t.Errorf("tools %v, out-of-bounds click error %v", seen.Tools, seen.BadClickError)
	}

	var state struct {
		FS map[string]struct {
			Text string                       `json:"text"`
			INI  map[string]map[string]string `json:"ini"`
		} `json:"fs"`
		App struct {
			Running bool `json:"running"`
		} `json:"app"`
	}
	if err := json.Unmarshal([]byte(docker(t, ctx, "exec", desk, "python3", "-I", "-B", "/opt/desktop/desktop.py", "dump")), &state); err != nil {
		t.Fatal(err)
	}
	if got := state.FS["notes.conf"].INI["notes"]["added"]; got != "through the gui" || !state.App.Running {
		t.Errorf("state after the edit: notes.conf %+v, app running %v", state.FS["notes.conf"], state.App.Running)
	}

	probe := `import socket, sys
for host, port in (("1.1.1.1", 443), ("8.8.8.8", 53)):
    try:
        socket.create_connection((host, port), timeout=3)
        sys.exit("reached %s:%d" % (host, port))
    except OSError:
        pass
try:
    socket.getaddrinfo("example.com", 443)
    sys.exit("resolved example.com")
except OSError:
    print("no egress")`
	if got := docker(t, ctx, "exec", desk, "python3", "-I", "-B", "-c", probe); !strings.Contains(got, "no egress") {
		t.Errorf("egress probe: %s", got)
	}
}

func docker(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func waitReady(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	for i := 0; i < 120; i++ {
		if exec.CommandContext(ctx, "docker", "exec", name, "python3", "-I", "-B", "/opt/desktop/desktop.py", "ready").Run() == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", "--tail", "30", name).CombinedOutput()
	t.Fatalf("desktop not ready: %s", logs)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
