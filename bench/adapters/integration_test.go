//go:build integration

package adapters

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// mcpServerScript is a trivial MCP server speaking streamable HTTP with JSON
// replies on any path: initialize, tools/list with one tool, tools/call.
const mcpServerScript = `
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
    def do_GET(self): self.send(405)
    def do_DELETE(self): self.send(200)
    def do_POST(self):
        msg = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")
        if "id" not in msg:
            return self.send(202)
        m = msg.get("method")
        if m == "initialize":
            res = {"protocolVersion": msg.get("params", {}).get("protocolVersion", "2025-06-18"), "capabilities": {"tools": {}}, "serverInfo": {"name": "hd-wire", "version": "1"}}
        elif m == "tools/list":
            res = {"tools": [{"name": "ping", "description": "Answer pong.", "inputSchema": {"type": "object", "properties": {}}}]}
        elif m == "tools/call":
            res = {"content": [{"type": "text", "text": "pong"}]}
        else:
            return self.send(200, {"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32601, "message": "no method"}})
        self.send(200, {"jsonrpc": "2.0", "id": msg["id"], "result": res})
HTTPServer(("0.0.0.0", 9200), H).serve_forever()
`

// TestNativeWiringInAgentImages plans a run with skills and one proxied MCP
// server through each shipped CLI config, lays the plan's files and skill
// folders into the CLI's own image, and asks the CLI itself, with no model
// call, which MCP servers and skills it found. A trivial MCP server answers
// as the tool proxy (host toolproxy, port 9200) on a private internal
// network. An agent image that is not built locally skips its case.
func TestNativeWiringInAgentImages(t *testing.T) {
	suffix := randSuffix(t)
	network := "hd-wire-net-" + suffix
	docker(t, "network", "create", "--internal", network)
	t.Cleanup(func() { dockerQuiet(t, "network", "rm", network) })
	server := "hd-wire-mcp-" + suffix
	docker(t, "run", "-d", "--name", server, "--network", network, "--network-alias", "toolproxy",
		"--entrypoint", "python3", "bench/agent-codex:1", "-c", mcpServerScript)
	t.Cleanup(func() { dockerQuiet(t, "rm", "-f", server) })

	tests := []struct {
		id    string
		image string
		check func(t *testing.T, run func(script string) string)
	}{
		{"codex", "bench/agent-codex:1", func(t *testing.T, run func(string) string) {
			var servers []struct {
				Name      string `json:"name"`
				Transport struct {
					Type string `json:"type"`
					URL  string `json:"url"`
				} `json:"transport"`
			}
			out := run("codex mcp list --json 2>/dev/null")
			if err := json.Unmarshal([]byte(out), &servers); err != nil {
				t.Fatalf("codex mcp list --json: %v\n%s", err, out)
			}
			if len(servers) != 1 || servers[0].Name != "tickets" || servers[0].Transport.Type != "streamable_http" ||
				servers[0].Transport.URL != "http://toolproxy:9200/s/tickets/mcp" {
				t.Fatalf("codex mcp servers = %+v", servers)
			}
			prompt := run("codex debug prompt-input hello 2>/dev/null")
			for _, s := range []string{"hd-pdf", "hd-house-style", "/home/agent/.codex/skills"} {
				if !strings.Contains(prompt, s) {
					t.Errorf("codex prompt does not list %s", s)
				}
			}
		}},
		{"claude-code", "bench/agent-claude-code:1", func(t *testing.T, run func(string) string) {
			if out := run("claude mcp list 2>&1"); !strings.Contains(out, "tickets: http://toolproxy:9200/s/tickets/mcp (HTTP)") || !strings.Contains(out, "Connected") {
				t.Fatalf("claude mcp list:\n%s", out)
			}
			// The init line of stream-json lists MCP servers, skills and
			// tools before any model call; the model URL is unreachable.
			out := run(`ANTHROPIC_BASE_URL=http://127.0.0.1:9 timeout 60 claude -p --dangerously-skip-permissions --output-format stream-json --verbose --model claude-sonnet-4-5 hi 2>/dev/null | head -n 1`)
			var init struct {
				MCP    []struct{ Name, Status string } `json:"mcp_servers"`
				Skills []string                        `json:"skills"`
				Tools  []string                        `json:"tools"`
			}
			if err := json.Unmarshal([]byte(out), &init); err != nil {
				t.Fatalf("init line: %v\n%s", err, out)
			}
			if len(init.MCP) != 1 || init.MCP[0].Name != "tickets" || init.MCP[0].Status != "connected" {
				t.Errorf("mcp_servers = %+v", init.MCP)
			}
			for _, want := range []string{"hd-pdf", "hd-house-style"} {
				if !contains(init.Skills, want) {
					t.Errorf("skills %v lack %s", init.Skills, want)
				}
			}
			if !contains(init.Tools, "mcp__tickets__ping") {
				t.Errorf("tools %v lack mcp__tickets__ping", init.Tools)
			}
		}},
		{"opencode", "bench/agent-opencode:1", func(t *testing.T, run func(string) string) {
			if out := run("timeout 60 opencode --pure mcp list 2>&1"); !strings.Contains(out, "tickets") || !strings.Contains(out, "connected") {
				t.Fatalf("opencode mcp list:\n%s", out)
			}
			var skills []struct{ Name, Location string }
			out := run("timeout 60 opencode --pure debug skill 2>/dev/null")
			if err := json.Unmarshal([]byte(out), &skills); err != nil {
				t.Fatalf("opencode debug skill: %v\n%s", err, out)
			}
			got := map[string]string{}
			for _, s := range skills {
				got[s.Name] = s.Location
			}
			for _, want := range []string{"hd-pdf", "hd-house-style"} {
				if !strings.HasPrefix(got[want], "/home/agent/.config/opencode/skills/") {
					t.Errorf("skill %s location %q (skills %v)", want, got[want], got)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if exec.Command("docker", "image", "inspect", tt.image).Run() != nil {
				t.Skipf("image %s not built", tt.image)
			}
			agent := loadAgent(t, filepath.Join("..", "agents", tt.id+".yaml"))
			rc, cfg := fixture(t, agent)
			writeTree(t, rc.Task.Dir, map[string]string{"skills/hd-pdf/SKILL.md": "---\nname: hd-pdf\ndescription: Fill PDF forms. Use when a task names a PDF form.\n---\nUse pdftk.\n"})
			writeTree(t, cfg.BenchRoot, map[string]string{"lib/hd-house-style/SKILL.md": "---\nname: hd-house-style\ndescription: House code style. Use when editing Python.\n---\nFour spaces.\n"})
			rc.Task.Skills = "skills"
			agent.Skills = &schemas.AgentSkills{Extra: []string{"lib/hd-house-style"}}
			rc.Services = []schemas.ServiceHandle{{Spec: schemas.ServiceSpec{ID: "tickets", Role: schemas.ServiceRoleMCP}, URL: "http://toolproxy:9200/s/tickets"}}
			plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
			if err != nil {
				t.Fatal(err)
			}
			layout := planTar(t, plan)
			n := 0
			tt.check(t, func(script string) string {
				n++
				return runInImage(t, tt.image, network, suffix, n, plan, layout, script)
			})
		})
	}
}

// runInImage starts the agent image as the agent runs (read-only root,
// tmpfs home and /tmp, uid 1000) with the plan's env, unpacks the plan's
// files and skill folders from stdin, and runs script in /workspace.
func runInImage(t *testing.T, image, network, suffix string, n int, plan *schemas.RunPlan, layout []byte, script string) string {
	t.Helper()
	envFile := filepath.Join(t.TempDir(), "env")
	var lines []string
	for k, v := range plan.Env {
		if !strings.ContainsAny(v, "\n") {
			lines = append(lines, k+"="+v)
		}
	}
	lines = append(lines, "HOME=/home/agent")
	if err := os.WriteFile(envFile, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "hd-wire-agent-" + suffix + "-" + strings.ReplaceAll(path.Base(image), ":", "-") + "-" + string(rune('a'+n))
	t.Cleanup(func() {
		if exec.Command("docker", "container", "inspect", name).Run() == nil {
			dockerQuiet(t, "rm", "-f", name)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "-i", "--rm", "--name", name, "--network", network,
		"--read-only", "--tmpfs", "/tmp", "--tmpfs", "/home/agent:uid=1000,gid=1000", "--user", "1000",
		"--env-file", envFile, "-w", "/workspace", "--entrypoint", "sh", image,
		"-c", "tar -x -C / && "+script)
	cmd.Stdin = bytes.NewReader(layout)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil && out.Len() == 0 {
		t.Fatalf("run in %s: %v", image, err)
	}
	return strings.TrimSpace(out.String())
}

// planTar packs the plan's files under /tmp and /home/agent, and every
// skill folder at its container path, as a tar to unpack at /. Files
// outside those writable folders are skipped (the instruction file is
// under /tmp and is kept).
func planTar(t *testing.T, plan *schemas.RunPlan) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dirs := map[string]bool{}
	addDir := func(p string) {
		var parts []string
		for d := path.Dir(p); d != "/" && d != "." && !dirs[d]; d = path.Dir(d) {
			parts = append(parts, d)
		}
		sort.Strings(parts)
		for _, d := range parts {
			if d == "/tmp" || d == "/home/agent" || d == "/home" {
				continue
			}
			dirs[d] = true
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: strings.TrimPrefix(d, "/") + "/", Mode: 0o755, Uid: 1000, Gid: 1000}); err != nil {
				t.Fatal(err)
			}
		}
	}
	addFile := func(p string, b []byte) {
		if !strings.HasPrefix(p, "/tmp/") && !strings.HasPrefix(p, "/home/agent/") {
			return
		}
		addDir(p)
		if err := tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(p, "/"), Mode: 0o644, Size: int64(len(b)), Uid: 1000, Gid: 1000}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	paths := make([]string, 0, len(plan.Files))
	for p := range plan.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		addFile(p, []byte(plan.Files[p]))
	}
	for dst, src := range plan.Dirs {
		err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			addFile(path.Join(dst, filepath.ToSlash(rel)), b)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func docker(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// dockerQuiet runs a cleanup command and only logs a failure.
func dockerQuiet(t *testing.T, args ...string) {
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Logf("cleanup docker %s: %v %s", strings.Join(args, " "), err, out)
	}
}

func randSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
