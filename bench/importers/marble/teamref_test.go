package marble

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// freePort asks the kernel for a free loopback port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startPython runs a python3 program until the test ends and waits for its
// health URL.
func startPython(t *testing.T, env []string, health string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "python3", append([]string{"-B", "-I"}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	for i := 0; i < 100; i++ {
		if r, err := http.Get(health); err == nil {
			r.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not come up", health)
}

// fakeTool answers every POST /query with one row, like the database tool.
const fakeTool = `import json, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b"{}")
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        b = json.dumps({"status": "success", "columns": ["count"], "rows": [["42"]]}).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
`

// TestReferenceTeam runs the generated reference control of a chain-graph
// task (agent1 - agent2 - agent3) as a three-member team against the real
// mailbox server (environments/mailbox.py) and a fake database tool, with no
// Docker: agent3's finding must reach the lead through agent2, the lead must
// write the diagnosis, and the verifier must pass it with no route
// violation. Then it runs the same control alone.
func TestReferenceTeam(t *testing.T) {
	needPython(t)
	clone, root := fixtureClone(t), t.TempDir()
	if _, err := runImport(t, root, clone, "marble-db", []string{"2"}, nil); err != nil {
		t.Fatal(err)
	}
	taskDir := filepath.Join(root, "benchmarks", "marble-db", "tasks", "database-2")
	ref := filepath.Join(taskDir, "solution", "reference.sh")

	dir := t.TempDir()
	toolPortN, mailPort := freePort(t), freePort(t)
	toolScript := filepath.Join(dir, "tool.py")
	if err := os.WriteFile(toolScript, []byte(fakeTool), 0o644); err != nil {
		t.Fatal(err)
	}
	startPython(t, nil, fmt.Sprintf("http://127.0.0.1:%d/", toolPortN), toolScript, fmt.Sprint(toolPortN))
	record := filepath.Join(dir, "state", "mailbox", "messages.jsonl")
	mailbox, err := filepath.Abs(filepath.Join("..", "..", "environments", "mailbox.py"))
	if err != nil {
		t.Fatal(err)
	}
	startPython(t, []string{fmt.Sprintf("MAILBOX_PORT=%d", mailPort), "MAILBOX_RECORD=" + record, "MAILBOX_TEAM=agent1,agent2,agent3"},
		fmt.Sprintf("http://127.0.0.1:%d/healthz", mailPort), mailbox)

	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outs := map[string]string{}
	var mu sync.Mutex
	for _, id := range []string{"agent1", "agent2", "agent3"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			cmd := exec.Command("sh", ref)
			cmd.Dir = ws
			cmd.Env = append(os.Environ(), "HEIMDALL_AGENT_ID="+id, "HEIMDALL_TEAM=agent1,agent2,agent3", "BENCH_WORKSPACE="+ws,
				fmt.Sprintf("HEIMDALL_MAILBOX_URL=http://127.0.0.1:%d/", mailPort), fmt.Sprintf("MARBLE_TOOL_URL=http://127.0.0.1:%d", toolPortN))
			out, err := cmd.CombinedOutput()
			mu.Lock()
			defer mu.Unlock()
			outs[id] = string(out)
			if err != nil {
				outs[id] += " (" + err.Error() + ")"
			}
		}(id)
	}
	wg.Wait()
	if !strings.Contains(outs["agent1"], "lead agent1: wrote diagnosis.md from 3 findings") || !strings.Contains(outs["agent2"], "worker agent2: done") || !strings.Contains(outs["agent3"], "worker agent3: done") {
		t.Fatalf("member outputs: %v", outs)
	}
	diag, err := os.ReadFile(filepath.Join(ws, artifactName))
	if err != nil || !strings.Contains(string(diag), "ROOT_CAUSES: VACUUM, LOCK_CONTENTION") || !strings.Contains(string(diag), "agent3 checked") {
		t.Fatalf("diagnosis: %v\n%s", err, diag)
	}
	// Every message kept to the chain: agent3 never wrote to agent1.
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	pairs := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m struct{ From, To string }
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		pairs[m.From+">"+m.To]++
	}
	if pairs["agent3>agent1"] != 0 || pairs["agent1>agent3"] != 0 || pairs["agent3>agent2"] != 1 || pairs["agent2>agent1"] != 2 || pairs["agent2>agent3"] != 1 {
		t.Errorf("messages by route: %v", pairs)
	}
	expected := map[string]any{}
	eb, _ := os.ReadFile(filepath.Join(taskDir, "verifier", "tests", "expected.json"))
	if err := json.Unmarshal(eb, &expected); err != nil {
		t.Fatal(err)
	}
	rf, out := runVerifyWithState(t, expected, string(diag), filepath.Join(dir, "state"))
	if rf.Reward != 1 || rf.Dimensions["route_violations"] != 0 || rf.Dimensions["senders"] != 3 {
		t.Errorf("verify: %+v %s", rf, out)
	}

	// Alone (admission, a single-agent baseline): no mailbox, one query.
	solo := t.TempDir()
	cmd := exec.Command("sh", ref)
	cmd.Dir = solo
	cmd.Env = append(os.Environ(), "BENCH_WORKSPACE="+solo, fmt.Sprintf("MARBLE_TOOL_URL=http://127.0.0.1:%d", toolPortN))
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "reference (alone)") {
		t.Fatalf("solo: %v %s", err, out)
	}
	if d, _ := os.ReadFile(filepath.Join(solo, artifactName)); !strings.Contains(string(d), "ROOT_CAUSES: VACUUM, LOCK_CONTENTION") {
		t.Errorf("solo diagnosis: %s", d)
	}
}

// runVerifyWithState runs the verifier with a mailbox state folder.
func runVerifyWithState(t *testing.T, expected map[string]any, diagnosis, state string) (rewardFile, string) {
	t.Helper()
	var msgs []map[string]any
	b, err := os.ReadFile(filepath.Join(state, "mailbox", "messages.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		m := map[string]any{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	return runVerify(t, expected, &diagnosis, msgs)
}
