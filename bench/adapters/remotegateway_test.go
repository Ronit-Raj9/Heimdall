package adapters

import (
	"bytes"
	"context"
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

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestRemoteGatewayTrafficEndToEnd plans a gateway-traffic run with
// gateway.public_url set, runs the launcher with the plan's env against a
// fake remote agent that really calls the gateway URL it was sent (a fake
// gateway standing in for Bifrost) with the run key and model, and checks
// that the self-reported usage in its answer lands in reported_tokens and
// reported_cost_usd only: with external traffic the trusted tokens and
// cost_usd stay unknown with the external traffic reason.
func TestRemoteGatewayTrafficEndToEnd(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	var mu sync.Mutex
	var gwAuth, gwModel string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		gwAuth = r.Header.Get("Authorization")
		gwModel, _ = body["model"].(string)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Tuesday"}}],"usage":{"prompt_tokens":11,"completion_tokens":2}}`)
	}))
	defer gateway.Close()
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Gateway struct{ URL, Key, Model string } `json:"gateway"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		call, _ := json.Marshal(map[string]any{"model": req.Gateway.Model, "messages": []any{map[string]string{"role": "user", "content": "day?"}}})
		greq, _ := http.NewRequest("POST", req.Gateway.URL+"/v1/chat/completions", bytes.NewReader(call))
		greq.Header.Set("Authorization", "Bearer "+req.Gateway.Key)
		resp, err := http.DefaultClient.Do(greq)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"answer":"Tuesday","usage":{"input_tokens":11,"output_tokens":2,"cost_usd":0.0001},"version":"agent-7"}`)
	}))
	defer agent.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(agent.URL, "http://"))

	rc, cfg := remoteFixture(t, remoteAgent(RemoteTrafficGateway))
	cfg.GatewayPublicURL = gateway.URL
	plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env["BENCH_REMOTE_GATEWAY_URL"] != gateway.URL {
		t.Fatalf("gateway url %q, want the public URL", plan.Env["BENCH_REMOTE_GATEWAY_URL"])
	}
	dir := t.TempDir()
	script, instr, ws := filepath.Join(dir, "l.py"), filepath.Join(dir, "i.md"), filepath.Join(dir, "ws")
	_ = os.MkdirAll(ws, 0o755)
	_ = os.WriteFile(script, []byte(remoteLauncherSource), 0o644)
	_ = os.WriteFile(instr, []byte(testInstruction), 0o644)
	cmd := exec.Command(py, "-I", "-B", script)
	for k, v := range plan.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, "BENCH_REMOTE_URL=http://agent.invalid:"+port+"/run", utils.EnvRemoteConnect+"=127.0.0.1:"+port,
		"BENCH_REMOTE_INSTRUCTION="+instr, "BENCH_ANSWER_PATH="+filepath.Join(ws, "answer.txt"), "BENCH_REMOTE_FILES_DIR="+ws)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("launcher: %v\n%s", err, out)
	}
	mu.Lock()
	if gwAuth != "Bearer "+testKey || gwModel != "claude-test" {
		t.Fatalf("gateway saw auth %q model %q", gwAuth, gwModel)
	}
	mu.Unlock()
	stream := filepath.Join(dir, "agent.stream.jsonl")
	if err := os.WriteFile(stream, out, 0o644); err != nil {
		t.Fatal(err)
	}

	// External traffic: the self-report is all there is, and it stays apart.
	rc.Agent.Remote.ModelTraffic = RemoteTrafficExternal
	rc.Exec = &schemas.ExecResult{StdoutPath: stream}
	rc.Dir = dir
	ev := &schemas.Evidence{Requests: &schemas.RequestLog{Complete: true}}
	evidence.CollectRemote(rc, ev)
	utils.MarkExternalTraffic(rc, ev.Requests)
	if ev.Remote == nil || ev.Remote.Reported == nil || ev.Remote.Reported.Version != "agent-7" || ev.Remote.Channels.SelfReport != "on" {
		t.Fatalf("remote evidence %+v", ev.Remote)
	}
	values := map[string]schemas.Value{}
	for _, m := range metrics.Builtin() {
		switch m.GetName() {
		case metrics.ReportedTokens, metrics.ReportedCostUSD, "tokens", "cost_usd":
			v, err := m.Compute(context.Background(), rc, ev)
			if err != nil {
				t.Fatal(err)
			}
			values[m.GetName()] = v
		}
	}
	if v := values[metrics.ReportedTokens]; !v.IsObserved() || *v.V != 13 || v.Source != "agent_reported" {
		t.Fatalf("reported_tokens %+v", v)
	}
	if v := values[metrics.ReportedCostUSD]; !v.IsObserved() || *v.V != 0.0001 {
		t.Fatalf("reported_cost_usd %+v", v)
	}
	for _, name := range []string{"tokens", "cost_usd"} {
		if v := values[name]; v.IsObserved() || v.State != schemas.ValueUnknown || !strings.Contains(v.Reason, "remote agent") {
			t.Fatalf("%s %+v: want unknown with the external traffic reason", name, v)
		}
	}
}
