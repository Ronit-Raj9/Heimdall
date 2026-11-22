package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// A voice platform agent's text run: the launcher queues each turn with the
// run token that carries the voice_platform block, and this server answers
// it through the platform's chat API, one chat per session across turns.
func TestPullVoiceTurns(t *testing.T) {
	var mu sync.Mutex
	var inputs, prev []string
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer vk-secret" {
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"Invalid key"}`))
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if fail {
			w.WriteHeader(503)
			w.Write([]byte(`{"message":"down"}`))
			return
		}
		var req struct {
			Input          string `json:"input"`
			PreviousChatID string `json:"previousChatId"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		inputs, prev = append(inputs, req.Input), append(prev, req.PreviousChatID)
		json.NewEncoder(w).Encode(map[string]any{"id": "chat_" + string(rune('0'+len(inputs))), "output": []any{
			map[string]any{"role": "assistant", "content": "You said: " + req.Input}}})
	}))
	defer srv.Close()

	signer, _ := pullagent.NewSigner([]byte("test-pull-signing-key-0123"))
	h := NewPullAgentHandler(schemas.ModeLive, pullagent.NewBroker(), signer, false, "", nil)
	reg := func(r *router.Router) { h.RegisterRunRoutes(r) }
	spec := &schemas.VoicePlatformAgent{Platform: "vapi", AgentID: "asst_1", APIKeySecret: "K", URL: srv.URL}
	tok, err := signer.RunToken(pullagent.Claims{Agent: "voice-platform", Run: "run_1", Exp: time.Now().Add(time.Hour).Unix(), Voice: spec})
	if err != nil {
		t.Fatal(err)
	}
	turn := func(payload, key string) pullagent.Result {
		t.Helper()
		hdr := []string{"Authorization", "Bearer " + tok, "Content-Type", "application/json"}
		if key != "" {
			hdr = append(hdr, VoiceKeyHeader, key)
		}
		resp := call(t, reg, nil, "POST", PullRunTurnsPath, `{"timeout_s":30,"payload":`+payload+`}`, hdr...)
		if resp.StatusCode() != 201 {
			t.Fatalf("submit: %d %s", resp.StatusCode(), resp.Body())
		}
		var created PullRunTurnCreated
		json.Unmarshal(resp.Body(), &created)
		var res pullagent.Result
		for range 200 { // the test request has no server context: poll
			resp = call(t, reg, nil, "GET", strings.Replace(PullRunTurnPath, "{id}", created.ID, 1)+"?wait=0", "", hdr...)
			if resp.StatusCode() != 202 {
				json.Unmarshal(resp.Body(), &res)
				return res
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("the turn was never answered")
		return res
	}
	text := func(res pullagent.Result) string {
		var a VoiceAnswer
		json.Unmarshal(res.Answer, &a)
		return a.Text
	}
	r := turn(`{"kind":"conversation","session_id":"run_1","message":"my order is late"}`, "vk-secret")
	if r.State != pullagent.StateAnswered || text(r) != "You said: my order is late" {
		t.Fatalf("first turn: %+v", r)
	}
	r = turn(`{"kind":"conversation","session_id":"run_1","message":"order 42"}`, "vk-secret")
	if text(r) != "You said: order 42" || len(prev) != 2 || prev[0] != "" || prev[1] != "chat_1" {
		t.Fatalf("second turn %+v, chain %v", r, prev)
	}
	// A turn kind the agent cannot answer fails the turn plainly.
	if r := turn(`{"kind":"tool_results","session_id":"run_1"}`, "vk-secret"); r.State != pullagent.StateFailed || !strings.Contains(r.Error, "no bench tools") {
		t.Fatalf("tool results: %+v", r)
	}
	// The platform down is infrastructure, answered as infra_error.
	mu.Lock()
	fail = true
	mu.Unlock()
	r = turn(`{"kind":"conversation","session_id":"run_1","message":"hello?"}`, "vk-secret")
	var a VoiceAnswer
	json.Unmarshal(r.Answer, &a)
	if r.State != pullagent.StateAnswered || !strings.Contains(a.InfraError, "503") {
		t.Fatalf("platform down: %+v", r)
	}
	// A wrong key is the agent's setup, not infrastructure: the turn fails.
	tok2, _ := signer.RunToken(pullagent.Claims{Agent: "voice-platform", Run: "run_2", Exp: time.Now().Add(time.Hour).Unix(), Voice: spec})
	tok = tok2
	if r := turn(`{"kind":"task","session_id":"run_2","instruction":"hi"}`, "wrong"); r.State != pullagent.StateFailed || !strings.Contains(r.Error, "401") {
		t.Fatalf("wrong key: %+v", r)
	}
}

// The real launcher (adapters/pullagent.py) against a real local server and
// a fake Vapi: the key goes with the turn, the reply is the answer, and the
// platform down exits as an unreachable endpoint (75), never a wrong answer.
func TestPullVoiceLauncher(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	var down atomic.Bool
	vapi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(503)
			return
		}
		if r.Header.Get("Authorization") != "Bearer vk-secret" {
			w.WriteHeader(401)
			return
		}
		var req struct {
			Input string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(map[string]any{"id": "chat_1", "output": []any{map[string]any{"role": "assistant", "content": "Refund issued for: " + req.Input}}})
	}))
	defer vapi.Close()
	signer, _ := pullagent.NewSigner([]byte("e2e-pull-signing-key-0123"))
	h := NewPullAgentHandler(schemas.ModeLive, pullagent.NewBroker(), signer, false, "", nil)
	r := router.New()
	h.RegisterRunRoutes(r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fasthttp.Server{Handler: r.Handler}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Shutdown() })
	base := "http://" + ln.Addr().String()
	dir := t.TempDir()
	for name, c := range map[string]string{"pullagent.py": adapters.LauncherSource("pullagent.py"), "heimdall_conv.py": adapters.LauncherSource("heimdall_conv.py"), "instruction.md": "order 42"} {
		os.WriteFile(filepath.Join(dir, name), []byte(c), 0o644)
	}
	spec := &schemas.VoicePlatformAgent{Platform: "vapi", AgentID: "asst_1", APIKeySecret: "K", URL: vapi.URL}
	run := func(runID string) (string, int) {
		tok, _ := signer.RunToken(pullagent.Claims{Agent: "voice-platform", Run: runID, Exp: time.Now().Add(time.Hour).Unix(), Voice: spec})
		cmd := exec.Command(py, "-I", filepath.Join(dir, "pullagent.py"))
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "BENCH_PULL_URL=" + base + strings.TrimSuffix(PullRunTurnsPath, "/turns"),
			"BENCH_PULL_TOKEN=" + tok, "BENCH_PULL_AGENT=voice-platform", "BENCH_PULL_TIMEOUT_S=30", "BENCH_PULL_TURN_S=20",
			"BENCH_PULL_INSTRUCTION=" + filepath.Join(dir, "instruction.md"), "BENCH_PULL_OUTPUT=answer", "BENCH_PULL_SESSION_ID=" + runID,
			"BENCH_ANSWER_PATH=" + filepath.Join(dir, runID+".answer"), "BENCH_VOICE_PLATFORM_KEY=vk-secret"}
		out, _ := cmd.CombinedOutput()
		if strings.Contains(string(out), "vk-secret") {
			t.Fatalf("the platform key leaked into the launcher's output:\n%s", out)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}
	out, code := run("run_ok")
	if b, _ := os.ReadFile(filepath.Join(dir, "run_ok.answer")); code != 0 || string(b) != "Refund issued for: order 42" {
		t.Fatalf("exit %d answer %q\n%s", code, b, out)
	}
	down.Store(true)
	out, code = run("run_down")
	if code != 75 || !strings.Contains(out, `"endpoint_unreachable"`) {
		t.Fatalf("platform down: exit %d\n%s", code, out)
	}
}
