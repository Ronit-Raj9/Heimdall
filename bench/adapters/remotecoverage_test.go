package adapters

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const testSignSecret = "sign-secret-0123456789abcdef"

func exposedAgent() *schemas.AgentConfig {
	a := remoteAgent(RemoteTrafficGateway)
	a.Remote.Tools = schemas.RemoteToolsExpose
	a.Remote.SignEnv = "HD_REMOTE_TEST_SIGN"
	return a
}

func TestRemoteCoveragePlan(t *testing.T) {
	rc, cfg := remoteFixture(t, exposedAgent())
	t.Setenv("HD_REMOTE_TEST_SIGN", testSignSecret)
	cfg.GatewayPublicURL = "https://gw.example.com/"
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
	rc.RemoteTools = &schemas.RemoteToolsAccess{
		BaseURL: "https://tools.example.com", RunID: rc.Run.RunID, Token: "tok-0123456789abcdef",
		Services: []string{"api", "notes"}, MCP: []string{"notes"}, MCPPaths: map[string]string{"notes": "/mcp"}, External: []string{"weather"},
	}
	plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	var tools map[string]utils.RemoteToolEntry
	if err := json.Unmarshal([]byte(plan.Env["BENCH_REMOTE_TOOLS"]), &tools); err != nil {
		t.Fatal(err)
	}
	base := "https://tools.example.com/r/" + rc.Run.RunID
	want := map[string]string{"notes": base + "/s/notes/mcp", "api": base + "/s/api", "weather": base + "/x/weather"}
	for id, u := range want {
		if tools[id].URL != u {
			t.Errorf("tool %s url %q, want %q", id, tools[id].URL, u)
		}
	}
	if strings.Contains(plan.Env["BENCH_REMOTE_TOOLS"], "tok-") {
		t.Fatal("the token is in the unredacted tools env")
	}
	if plan.Env["BENCH_REMOTE_TOOLS_TOKEN"] != "tok-0123456789abcdef" || plan.Env["BENCH_REMOTE_SIGN_SECRET"] != testSignSecret {
		t.Fatalf("env %v", plan.Env)
	}
	if plan.Env["BENCH_REMOTE_GATEWAY_URL"] != "https://gw.example.com" || plan.Env["BENCH_REMOTE_OUTPUT"] != schemas.OutputState {
		t.Fatalf("gateway url %q output %q", plan.Env["BENCH_REMOTE_GATEWAY_URL"], plan.Env["BENCH_REMOTE_OUTPUT"])
	}
	for k, v := range plan.Env {
		secret := v == "tok-0123456789abcdef" || v == testSignSecret
		named := strings.Contains(k, "TOKEN") || strings.Contains(k, "SECRET")
		if secret && !named {
			t.Fatalf("env %s holds a secret under a name plan redaction does not match", k)
		}
	}

	t.Run("state task without a published proxy", func(t *testing.T) {
		rc.RemoteTools = nil
		if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "no tool proxy was published") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("artifact task needs inline", func(t *testing.T) {
		rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "report.csv"}}}
		if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "remote.artifacts inline") {
			t.Fatalf("err %v", err)
		}
		rc.Agent.Remote.Artifacts = schemas.RemoteArtifactsInline
		plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
		if err != nil || plan.Env["BENCH_REMOTE_ARTIFACTS"] != "inline" {
			t.Fatalf("plan env %v err %v", plan, err)
		}
	})
	t.Run("short sign secret", func(t *testing.T) {
		t.Setenv("HD_REMOTE_TEST_SIGN", "short")
		rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
		if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "remote.sign_env") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("unknown tools value", func(t *testing.T) {
		t.Setenv("HD_REMOTE_TEST_SIGN", testSignSecret)
		rc.Agent.Remote.Tools = "yes"
		if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "remote.tools") {
			t.Fatalf("err %v", err)
		}
	})
}

// telemetryAgent is a deployed agent that checks the request signature,
// answers 202 first and then the final document with a self-report.
type telemetryAgent struct {
	mu        sync.Mutex
	sigOK     bool
	tools     map[string]any
	polls     int
	final     string
	asyncMode bool
}

func (f *telemetryAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/jobs/") {
		f.polls++
		w.Header().Set("Content-Type", "application/json")
		if f.polls < 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"status":"running"}`)
			return
		}
		_, _ = io.WriteString(w, f.final)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	mac := hmac.New(sha256.New, []byte(testSignSecret))
	mac.Write([]byte(r.Header.Get("X-Heimdall-Timestamp") + "."))
	mac.Write(raw)
	f.sigOK = r.Header.Get("X-Heimdall-Signature") == "sha256="+hex.EncodeToString(mac.Sum(nil))
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.tools, _ = body["tools"].(map[string]any)
	w.Header().Set("Content-Type", "application/json")
	if f.asyncMode {
		w.Header().Set("Location", "/jobs/7")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"id":"7","status":"queued"}`)
		return
	}
	_, _ = io.WriteString(w, f.final)
}

func TestRemoteCoverageLauncher(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	final := `{"answer": "done", "version": "v1.2.3", "build": "abc\u0007def",
 "usage": {"input_tokens": 120, "output_tokens": 30, "model": "m-1", "cost_usd": 0.004, "note": "sk-abcdefghijklmnop"},
 "events": [{"type": "tool_call", "name": "notes.add", "input": {"api_key": "sk-abcdefghijklmnopq"}}, "not an object"],
 "artifacts": {"out/report.csv": "aWQsbgox", "../escape.txt": "eA==", "bad.bin": "!!"}}`
	for _, async := range []bool{false, true} {
		name := "sync"
		if async {
			name = "async"
		}
		t.Run(name, func(t *testing.T) {
			agent := &telemetryAgent{final: final, asyncMode: async}
			srv := httptest.NewServer(agent)
			defer srv.Close()
			_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
			dir := t.TempDir()
			script, instr, ws := filepath.Join(dir, "l.py"), filepath.Join(dir, "i.md"), filepath.Join(dir, "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(script, []byte(remoteLauncherSource), 0o644)
			_ = os.WriteFile(instr, []byte("do it"), 0o644)
			cmd := exec.Command(py, "-I", "-B", script)
			cmd.Env = []string{
				"BENCH_REMOTE_URL=http://agent.invalid:" + port + "/run", utils.EnvRemoteConnect + "=127.0.0.1:" + port,
				"BENCH_REMOTE_INSTRUCTION=" + instr, "BENCH_ANSWER_PATH=" + filepath.Join(ws, "answer.txt"), "BENCH_REMOTE_FILES_DIR=" + ws,
				"BENCH_REMOTE_TIMEOUT_S=20", "BENCH_REMOTE_SIGN_SECRET=" + testSignSecret, "BENCH_REMOTE_ARTIFACTS=inline",
				"BENCH_REMOTE_OUTPUT=artifact",
				`BENCH_REMOTE_TOOLS={"notes":{"url":"https://tools.example.com/r/r1/s/notes/mcp","protocol":"mcp"}}`,
				"BENCH_REMOTE_TOOLS_TOKEN=tok-0123456789abcdef",
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("launcher: %v\n%s", err, out)
			}
			if !agent.sigOK {
				t.Fatal("signature did not verify")
			}
			notes, _ := agent.tools["notes"].(map[string]any)
			if notes["token"] != "tok-0123456789abcdef" || notes["url"] != "https://tools.example.com/r/r1/s/notes/mcp" {
				t.Fatalf("tools block %v", agent.tools)
			}
			if strings.Contains(string(out), "tok-0123456789abcdef") || strings.Contains(string(out), testSignSecret) || strings.Contains(string(out), "sk-abcdefghijklmnop") {
				t.Fatalf("a secret reached stdout:\n%s", out)
			}
			var report map[string]any
			events := 0
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "@@heimdall-event ") {
					events++
					continue
				}
				var m map[string]any
				if json.Unmarshal([]byte(line), &m) == nil && m["type"] == schemas.RemoteReportType {
					report = m
				}
			}
			if events != 1 || report == nil {
				t.Fatalf("events %d report %v\n%s", events, report, out)
			}
			usage, _ := report["usage"].(map[string]any)
			if usage["input_tokens"] != float64(120) || usage["cost_usd"] != 0.004 || usage["model"] != "m-1" || report["version"] != "v1.2.3" || report["build"] != "abcdef" {
				t.Fatalf("report %v", report)
			}
			if report["events_dropped"] != float64(1) || (report["async"] == true) != async {
				t.Fatalf("report %v", report)
			}
			arts, _ := report["artifacts"].([]any)
			if len(arts) != 1 || arts[0] != "out/report.csv" {
				t.Fatalf("artifacts %v", report)
			}
			if b, err := os.ReadFile(filepath.Join(ws, "out/report.csv")); err != nil || string(b) != "id,n\n1" {
				t.Fatalf("artifact file %q %v", b, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
				t.Fatal("an artifact escaped the workspace")
			}
			if b, _ := os.ReadFile(filepath.Join(ws, "answer.txt")); string(b) != "done" {
				t.Fatalf("answer %q", b)
			}
		})
	}
}

func TestRemoteCoverageLauncherAsyncOtherHost(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://elsewhere.example.com/jobs/1")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	dir := t.TempDir()
	script, instr := filepath.Join(dir, "l.py"), filepath.Join(dir, "i.md")
	_ = os.WriteFile(script, []byte(remoteLauncherSource), 0o644)
	_ = os.WriteFile(instr, []byte("x"), 0o644)
	cmd := exec.Command(py, "-I", "-B", script)
	cmd.Env = []string{"BENCH_REMOTE_URL=" + srv.URL + "/run", "BENCH_REMOTE_INSTRUCTION=" + instr,
		"BENCH_ANSWER_PATH=" + filepath.Join(dir, "a.txt"), "BENCH_REMOTE_TIMEOUT_S=5"}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "status URL on its own host") {
		t.Fatalf("err %v\n%s", err, out)
	}
}
