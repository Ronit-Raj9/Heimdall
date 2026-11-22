package adapters

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const testRemoteToken = "remote-bearer-0123456789"

func remoteAgent(traffic string) *schemas.AgentConfig {
	return &schemas.AgentConfig{
		ID: "deployed", Adapter: AdapterRemoteHTTP, Image: "bench/helper:1", Model: "claude-test",
		Budgets: schemas.Budgets{WallS: 300, CPUs: 1, MemGB: 1},
		Remote:  &schemas.RemoteAgent{URL: "https://agent.example.com/v1/run", AuthEnv: "HD_REMOTE_TEST_TOKEN", TimeoutS: 120, ModelTraffic: traffic},
	}
}

// remoteFixture is the command fixture with an answer task.
func remoteFixture(t *testing.T, agent *schemas.AgentConfig) (*schemas.RunContext, CommandConfig) {
	t.Helper()
	rc, cfg := fixture(t, agent)
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
	t.Setenv("HD_REMOTE_TEST_TOKEN", testRemoteToken)
	return rc, cfg
}

func TestRemoteHTTPPlan(t *testing.T) {
	for _, traffic := range []string{"", RemoteTrafficExternal, RemoteTrafficGateway} {
		t.Run("traffic "+traffic, func(t *testing.T) {
			rc, cfg := remoteFixture(t, remoteAgent(traffic))
			if traffic == RemoteTrafficGateway {
				cfg.GatewayPublicURL = testGateway // Y03: an internet endpoint needs the published gateway
			}
			plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(plan.Argv, " ") != "python3 -I "+remoteLauncherFile || plan.Network != testNetwork {
				t.Fatalf("argv %q network %q", plan.Argv, plan.Network)
			}
			if len(plan.Egress) != 1 || plan.Egress[0] != "agent.example.com:443" {
				t.Fatalf("egress %v", plan.Egress)
			}
			want := map[string]string{
				"BENCH_REMOTE_URL": "https://agent.example.com/v1/run", utils.EnvRemoteConnect: "bench-eg-r_p20fj3s8wa:9300",
				"BENCH_REMOTE_TIMEOUT_S": "120", "BENCH_REMOTE_TOKEN": testRemoteToken, "BENCH_REMOTE_SESSION_ID": "r_p20fj3s8wa",
				"BENCH_REMOTE_SESSION_INDEX": "1", utils.EnvAnswerPath: "/workspace/answer.txt",
			}
			for k, v := range want {
				if plan.Env[k] != v {
					t.Errorf("env %s = %q, want %q", k, plan.Env[k], v)
				}
			}
			gateway := traffic == RemoteTrafficGateway
			if (plan.Env["BENCH_REMOTE_RUN_KEY"] == testKey) != gateway || (plan.Env["BENCH_REMOTE_GATEWAY_URL"] == testGateway) != gateway {
				t.Fatalf("gateway env with traffic %q: %v", traffic, plan.Env)
			}
			for _, a := range plan.Argv {
				if strings.Contains(a, testRemoteToken) || strings.Contains(a, testKey) {
					t.Fatal("a secret is in argv")
				}
			}
			for p, content := range plan.Files {
				if strings.Contains(content, testRemoteToken) || strings.Contains(content, testKey) {
					t.Fatalf("a secret is in file %s", p)
				}
			}
			for k, v := range plan.Env {
				secret := v == testRemoteToken || v == testKey
				named := strings.Contains(k, "TOKEN") || strings.Contains(k, "KEY")
				if secret && !named {
					t.Fatalf("env %s holds a secret under a name plan redaction does not match", k)
				}
			}
		})
	}
}

func TestRemoteHTTPPlanSession(t *testing.T) {
	rc, cfg := remoteFixture(t, remoteAgent(""))
	rc.Session, rc.SessionIndex = &schemas.SessionSpec{ID: "s2", Instruction: "instruction.md"}, 1
	plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env["BENCH_REMOTE_SESSION_ID"] != "s2" || plan.Env["BENCH_REMOTE_SESSION_INDEX"] != "2" {
		t.Fatalf("session env %v", plan.Env)
	}
	rc.Agent.Remote.TimeoutS = 0
	if plan, err = newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err != nil || plan.Env["BENCH_REMOTE_TIMEOUT_S"] != "295" {
		t.Fatalf("default timeout %q, %v", plan.Env["BENCH_REMOTE_TIMEOUT_S"], err)
	}
}

func TestRemoteHTTPPlanErrors(t *testing.T) {
	cases := []struct {
		name   string
		change func(rc *schemas.RunContext)
		want   string
	}{
		{"no remote", func(rc *schemas.RunContext) { rc.Agent.Remote = nil }, "no remote block"},
		{"diff task", func(rc *schemas.RunContext) { rc.Task.Output = nil }, "a coding task needs remote.repo tarball"},
		{"state task", func(rc *schemas.RunContext) { rc.Task.Output.Kind = schemas.OutputState }, "needs remote.tools expose"},
		{"bad url", func(rc *schemas.RunContext) { rc.Agent.Remote.URL = "ftp://x" }, "want http(s)"},
		{"user info", func(rc *schemas.RunContext) { rc.Agent.Remote.URL = "https://u:p@x/" }, "no user info"},
		{"bad traffic", func(rc *schemas.RunContext) { rc.Agent.Remote.ModelTraffic = "both" }, "want gateway or external"},
		{"auth env unset", func(rc *schemas.RunContext) { rc.Agent.Remote.AuthEnv = "HD_REMOTE_UNSET_VAR" }, "is not set"},
		{"auth env name", func(rc *schemas.RunContext) { rc.Agent.Remote.AuthEnv = "A-B" }, "not an environment variable name"},
		{"gateway no key", func(rc *schemas.RunContext) { rc.Agent.Remote.ModelTraffic = RemoteTrafficGateway; rc.Key = nil }, "needs the run key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc, cfg := remoteFixture(t, remoteAgent(""))
			tc.change(rc)
			if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// fakeAgent is a deployed agent that records the last request.
type fakeAgent struct {
	auth, host, contentType string
	body                    map[string]any
	status                  int
	reply                   string
}

func (f *fakeAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.auth, f.host, f.body = r.Header.Get("Authorization"), r.Host, nil
	raw, err := io.ReadAll(r.Body)
	if err == nil {
		err = json.Unmarshal(raw, &f.body)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if f.contentType != "" {
		w.Header().Set("Content-Type", f.contentType)
	}
	w.WriteHeader(f.status)
	if _, err := io.WriteString(w, f.reply); err != nil {
		return
	}
}

// runLauncher runs remotehttp.py on the host with the plan's environment,
// pointed at a local fake agent.
func runLauncher(t *testing.T, env map[string]string) (string, string, error) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "remotehttp.py")
	instruction := filepath.Join(dir, "instruction.md")
	ws := filepath.Join(dir, "ws")
	for p, content := range map[string]string{script: remoteLauncherSource, instruction: testInstruction,
		filepath.Join(ws, "notes/chat.txt"): "we met on tuesday", filepath.Join(ws, ".git/config"): "hidden"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	answer := filepath.Join(ws, "answer.txt")
	cmd := exec.Command(py, "-I", script)
	cmd.Env = []string{"BENCH_REMOTE_INSTRUCTION=" + instruction, "BENCH_ANSWER_PATH=" + answer, "BENCH_REMOTE_FILES_DIR=" + ws}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	b, rerr := os.ReadFile(answer)
	if rerr != nil {
		b = nil
	}
	return string(b), string(out), err
}

func TestRemoteHTTPLauncher(t *testing.T) {
	agent := &fakeAgent{status: 200, reply: `{"answer": "Tuesday"}`, contentType: "application/json"}
	srv := httptest.NewServer(agent)
	defer srv.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	// The URL names a host that does not resolve; the launcher must connect
	// to BENCH_REMOTE_CONNECT (the forwarder) and still send the URL's host.
	env := map[string]string{
		"BENCH_REMOTE_URL": "http://agent.invalid:" + port + "/run?v=1", utils.EnvRemoteConnect: "127.0.0.1:" + port,
		"BENCH_REMOTE_TOKEN": testRemoteToken, "BENCH_REMOTE_SESSION_ID": "s1", "BENCH_REMOTE_SESSION_INDEX": "2",
		"BENCH_REMOTE_TIMEOUT_S": "10", "BENCH_REMOTE_TRAFFIC": RemoteTrafficGateway,
		"BENCH_REMOTE_GATEWAY_URL": testGateway, "BENCH_REMOTE_RUN_KEY": testKey, "BENCH_REMOTE_MODEL": "claude-test",
	}
	answer, out, err := runLauncher(t, env)
	if err != nil || answer != "Tuesday" {
		t.Fatalf("answer %q, err %v\n%s", answer, err, out)
	}
	if agent.auth != "Bearer "+testRemoteToken || agent.host != "agent.invalid:"+port {
		t.Fatalf("auth %q host %q", agent.auth, agent.host)
	}
	if agent.body["instruction"] != testInstruction || agent.body["session_id"] != "s1" || agent.body["session_index"] != float64(2) {
		t.Fatalf("body %v", agent.body)
	}
	files, ok := agent.body["files"].(map[string]any)
	if !ok || files["notes/chat.txt"] != "we met on tuesday" || len(files) != 1 {
		t.Fatalf("files %v", agent.body["files"])
	}
	gw, ok := agent.body["gateway"].(map[string]any)
	if !ok || gw["key"] != testKey || gw["url"] != testGateway {
		t.Fatalf("gateway %v", agent.body["gateway"])
	}
	if strings.Contains(out, testRemoteToken) || strings.Contains(out, testKey) {
		t.Fatal("the launcher printed a secret")
	}

	delete(env, "BENCH_REMOTE_TOKEN")
	env["BENCH_REMOTE_TRAFFIC"] = RemoteTrafficExternal
	cases := []struct {
		name, contentType, reply string
		status                   int
		answer                   string
		fails                    bool
	}{
		{"text answer", "text/plain; charset=utf-8", "Tuesday\n", 200, "Tuesday\n", false},
		{"structured answer", "application/json", `{"answer": {"day": "Tuesday"}}`, 200, `{"day": "Tuesday"}`, false},
		{"server error", "text/plain", "boom", 500, "", true},
		{"no answer field", "application/json", `{"result": "x"}`, 200, "", true},
		{"not json", "application/json", `<html>`, 200, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent.contentType, agent.reply, agent.status = tc.contentType, tc.reply, tc.status
			answer, out, err := runLauncher(t, env)
			if tc.fails != (err != nil) || answer != tc.answer {
				t.Fatalf("answer %q, err %v\n%s", answer, err, out)
			}
			if agent.auth != "" {
				t.Fatal("authorization sent without a token")
			}
			if _, ok := agent.body["gateway"]; ok {
				t.Fatal("gateway block sent with external traffic")
			}
		})
	}
}

// TestRemoteHTTPLauncherTLS checks that through the forwarder address the
// launcher still verifies TLS against the URL's host name.
func TestRemoteHTTPLauncherTLS(t *testing.T) {
	agent := &fakeAgent{status: 200, reply: `{"answer": "ok"}`, contentType: "application/json"}
	srv := httptest.NewTLSServer(agent)
	defer srv.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SSL_CERT_FILE": ca, "BENCH_REMOTE_URL": "https://example.com:" + port + "/run",
		utils.EnvRemoteConnect: "127.0.0.1:" + port, "BENCH_REMOTE_TIMEOUT_S": "10"}
	if answer, out, err := runLauncher(t, env); err != nil || answer != "ok" {
		t.Fatalf("answer %q, err %v\n%s", answer, err, out)
	}
	// A URL host the certificate does not name fails verification, even
	// though the connection reaches the same server.
	env["BENCH_REMOTE_URL"] = "https://other.invalid:" + port + "/run"
	if answer, out, err := runLauncher(t, env); err == nil || answer != "" || !strings.Contains(out, "CERTIFICATE_VERIFY_FAILED") {
		t.Fatalf("wrong host name accepted: answer %q, err %v\n%s", answer, err, out)
	}
}

// TestRemoteHTTPNoPaidNeedsGateway is QC2-15-08: a no-paid run never goes to
// a remote agent whose model traffic is outside the bench.
func TestRemoteHTTPNoPaidNeedsGateway(t *testing.T) {
	for _, traffic := range []string{"", RemoteTrafficExternal} {
		rc, cfg := remoteFixture(t, remoteAgent(traffic))
		rc.NoPaidTraffic = true
		if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "model_traffic gateway") {
			t.Fatalf("traffic %q: err %v", traffic, err)
		}
	}
	rc, cfg := remoteFixture(t, remoteAgent(RemoteTrafficGateway))
	rc.NoPaidTraffic = true
	cfg.GatewayPublicURL = testGateway
	if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
}

// TestRemoteHTTPGatewayUnreachable is Y03 (G1 P0-1): an internet endpoint
// with model_traffic gateway on a worker whose gateway is not published is
// refused with the reason, instead of being told an internal address.
func TestRemoteHTTPGatewayUnreachable(t *testing.T) {
	rc, cfg := remoteFixture(t, remoteAgent(RemoteTrafficGateway))
	cfg.GatewayPublicURL = ""
	if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "gateway.public_url") {
		t.Fatalf("err %v", err)
	}
	// An endpoint on the worker's own network reaches the agent URL.
	rc.Agent.Remote.URL = "http://acme-agent:8000/run"
	if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
}
