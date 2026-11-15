//go:build integration

package utils

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeAgentServer answers every POST with {"answer": "through-forwarder"}.
const fakeAgentServer = `import http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        body = json.dumps({"answer": "through-forwarder"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
http.server.HTTPServer(("0.0.0.0", 8080), H).serve_forever()
`

// TestRemoteEgressDocker starts a real forwarder: a launcher on an internal
// network reaches the remote agent only through it, and cannot reach the
// agent directly.
func TestRemoteEgressDocker(t *testing.T) {
	const image = "python:3.12-slim"
	d := Docker{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if res, err := d.Run(ctx, "image", "inspect", image); err != nil || res.ExitCode != 0 {
		t.Skip(image + " not present locally")
	}
	suffix := NewID("t", 6)
	internal, agent := "hd-remote-int-"+suffix, "hd-remote-agent-"+suffix
	runID := "hd_remote_" + suffix
	t.Cleanup(func() {
		c := context.Background()
		for _, args := range [][]string{{"rm", "-f", agent}, {"network", "rm", internal}} {
			if res, err := d.Run(c, args...); err != nil || res.ExitCode != 0 {
				t.Logf("cleanup %v: %v", args, err)
			}
		}
	})
	if _, err := d.MustRun(ctx, "network", "create", "--internal", "--label", LabelBench+"=1", internal); err != nil {
		t.Fatal(err)
	}
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: runID, ExpID: "e_hd_remote"}, Dir: t.TempDir()}
	target := agent + ":8080"
	egress, err := StartRemoteEgress(ctx, d, rc, &schemas.RunPlan{Image: image, Egress: []string{target}}, internal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// The agent container sits on the forwarder's outbound network, so
		// it goes first or the network cannot be removed.
		if res, err := d.Run(context.Background(), "rm", "-f", agent); err != nil || res.ExitCode != 0 {
			t.Logf("remove agent: %v", err)
		}
		egress.Stop(context.Background())
	})
	// The remote agent lives outside the evaluation network: on the
	// forwarder's outbound network only.
	if _, err := d.MustRun(ctx, "run", "-d", "--name", agent, "--label", LabelBench+"=1", "--network", RemoteEgressName(runID),
		image, "python3", "-c", fakeAgentServer); err != nil {
		t.Fatal(err)
	}
	launcher, err := os.ReadFile(filepath.Join("..", "adapters", "remotehttp.py"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, content := range map[string]string{"remotehttp.py": string(launcher), "instruction.md": "what day?"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(script string) (*DockerResult, error) {
		return d.Run(ctx, "run", "--rm", "--label", LabelBench+"=1", "--network", internal, "-v", dir+":/in:ro",
			"-e", "BENCH_REMOTE_URL=http://"+target+"/run", "-e", EnvRemoteConnect+"="+RemoteEgressAddr(runID),
			"-e", "BENCH_REMOTE_INSTRUCTION=/in/instruction.md", "-e", "BENCH_ANSWER_PATH=/tmp/answer.txt",
			"-e", "BENCH_REMOTE_TIMEOUT_S=20", image, "sh", "-c", script)
	}
	var res *DockerResult
	for range 20 { // the fake agent may still be starting
		res, err = run("python3 -I /in/remotehttp.py && cat /tmp/answer.txt")
		if err == nil && res.ExitCode == 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil || res.ExitCode != 0 || !strings.HasSuffix(strings.TrimSpace(string(res.Stdout)), "through-forwarder") {
		t.Fatalf("launcher through the forwarder: %v\n%s\n%s", err, res.Stdout, res.Stderr)
	}
	direct := `python3 -c "import socket; socket.create_connection(('` + agent + `', 8080), 3)"`
	if res, err := run(direct); err != nil || res.ExitCode == 0 {
		t.Fatalf("launcher reached the agent without the forwarder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rc.Dir, RemoteEgressFile)); err != nil {
		t.Fatal(err)
	}
}
