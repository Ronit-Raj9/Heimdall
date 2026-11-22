package handlers

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// The whole pull path with real processes: the run's launcher
// (adapters/pullagent.py) queues turns on a real local server, and the
// Python SDK's heimdall.serve takes and answers them. The coding turn
// downloads the workspace through its signed link and returns a patch that
// the launcher applies.

const pullServeScript = `
import json, os, subprocess, sys
sys.path.insert(0, sys.argv[1])
from heimdall.serve import Answer, serve

def solve(turn):
    if turn.repo:
        dest = os.path.join(sys.argv[2], "checkout")
        files = turn.download_repo(dest)
        assert "README.md" in files, files
        subprocess.run(["git", "init", "-q", dest], check=True)
        subprocess.run(["git", "-C", dest, "add", "-A"], check=True)
        with open(os.path.join(dest, "README.md"), "a") as f:
            f.write("fixed by the agent\n")
        diff = subprocess.run(["git", "-C", dest, "diff"], check=True, capture_output=True, text=True).stdout
        return Answer(text="patched", patch=diff, usage={"input_tokens": 7, "output_tokens": 3})
    return "the answer to %s is 42" % turn.task_id

print("handled", serve("support-bot", solve, url=sys.argv[3], token="local", max_turns=int(sys.argv[4])), flush=True)
`

func TestPullEndToEnd(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	signer, _ := pullagent.NewSigner([]byte("e2e-pull-signing-key-0123"))
	h := NewPullAgentHandler(schemas.ModeLive, pullagent.NewBroker(), signer, false, "", nil)
	r := router.New()
	h.RegisterRoutes(r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fasthttp.Server{Handler: V1Alias(schemas.ModeLive, "test", false)(r.Handler)}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Shutdown() })
	base := "http://" + ln.Addr().String()

	dir := t.TempDir()
	clients, err := filepath.Abs("../clients/python")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "serve.py")
	os.WriteFile(script, []byte(pullServeScript), 0o644)
	serveCmd := exec.Command(py, script, clients, dir, base, "2")
	serveCmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
	var serveOut bytes.Buffer
	serveCmd.Stdout, serveCmd.Stderr = &serveOut, &serveOut
	if err := serveCmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serveCmd.Wait() }()
	t.Cleanup(func() { _ = serveCmd.Process.Kill() })
	for i := 0; ; i++ {
		if ok, _ := h.broker.Connected("", "support-bot"); ok {
			break
		}
		if i > 200 {
			t.Fatalf("heimdall.serve never connected:\n%s", serveOut.String())
		}
		time.Sleep(25 * time.Millisecond)
	}

	launcher := filepath.Join(dir, "launcher")
	ws := filepath.Join(dir, "ws")
	for p, c := range map[string]string{filepath.Join(launcher, "pullagent.py"): adapters.LauncherSource("pullagent.py"),
		filepath.Join(launcher, "heimdall_conv.py"): adapters.LauncherSource("heimdall_conv.py"),
		filepath.Join(launcher, "instruction.md"):   "Fix the README.", filepath.Join(ws, "README.md"): "scratch\n"} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(runID string, extra map[string]string) string {
		t.Helper()
		tok, _ := signer.RunToken(pullagent.Claims{Agent: "support-bot", Run: runID, Exp: time.Now().Add(time.Hour).Unix()})
		cmd := exec.Command(py, "-I", filepath.Join(launcher, "pullagent.py"))
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "BENCH_PULL_URL=" + base + PullRunTurnsPath[:len(PullRunTurnsPath)-len("/turns")],
			"BENCH_PULL_TOKEN=" + tok, "BENCH_PULL_AGENT=support-bot", "BENCH_PULL_TIMEOUT_S=60", "BENCH_PULL_TURN_S=30",
			"BENCH_PULL_INSTRUCTION=" + filepath.Join(launcher, "instruction.md"), "BENCH_PULL_TASK_ID=t01",
			"BENCH_PULL_SESSION_ID=" + runID, "BENCH_ANSWER_PATH=" + filepath.Join(dir, runID+".answer")}
		for k, v := range extra {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("launcher %s: %v\n%s\nserve:\n%s", runID, err, out, serveOut.String())
		}
		if strings.Contains(string(out), tok) {
			t.Fatalf("the run token leaked into the launcher's output")
		}
		return string(out)
	}

	out := run("run_answer", map[string]string{"BENCH_PULL_OUTPUT": "answer"})
	if b, _ := os.ReadFile(filepath.Join(dir, "run_answer.answer")); string(b) != "the answer to t01 is 42" {
		t.Fatalf("answer %q\n%s", b, out)
	}
	out = run("run_patch", map[string]string{"BENCH_PULL_OUTPUT": "diff", "BENCH_REPO": "tarball", "BENCH_REPO_DIR": ws})
	if b, _ := os.ReadFile(filepath.Join(ws, "README.md")); string(b) != "scratch\nfixed by the agent\n" {
		t.Fatalf("patch not applied: %q\n%s\nserve:\n%s", b, out, serveOut.String())
	}
	var report map[string]any
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, `"type": "pull_report"`) {
			json.Unmarshal([]byte(line), &report)
		}
	}
	if p, _ := report["patch"].(map[string]any); p["applied"] != true {
		t.Fatalf("report %v", report)
	}
	select {
	case err := <-done:
		if err != nil || !strings.Contains(serveOut.String(), "handled 2") {
			t.Fatalf("serve: %v\n%s", err, serveOut.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("serve did not stop after its 2 turns:\n%s", serveOut.String())
	}
}
