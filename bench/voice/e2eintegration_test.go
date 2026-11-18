package voice

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestEndToEndThroughGateway is one real call through a gateway: the voice
// caller and the helpdesk run on this host, the agent (the cascaded agent
// or the Realtime bridge) calls the gateway on a virtual key, and the call
// must end with the refund done and the caller hanging up after its last
// line. It never runs by default. Point it at a test stack whose openai
// provider is the scripted fake (docs/voice-agents.md); against a real
// provider every call is paid.
//
//	BENCH_VOICE_E2E_GATEWAY=http://127.0.0.1:28500  the gateway, host side
//	BENCH_VOICE_E2E_KEY=<virtual key>               allowed gpt-4o-mini, whisper-1, gpt-4o-mini-tts, gpt-realtime
//	BENCH_VOICE_E2E_AGENT=cascaded|realtime         default cascaded
func TestEndToEndThroughGateway(t *testing.T) {
	gw, key := os.Getenv("BENCH_VOICE_E2E_GATEWAY"), os.Getenv("BENCH_VOICE_E2E_KEY")
	if gw == "" || key == "" {
		t.Skip("BENCH_VOICE_E2E_GATEWAY and BENCH_VOICE_E2E_KEY are not set")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	task := filepath.Join("..", "examples", "acme-voice-desk", "benchmarks", "acme-voice-desk", "tasks", "refund-in-policy")
	server := filepath.Join(task, "..", "..", "services", "helpdesk", "server.py")
	dir := t.TempDir()
	deskPort, callerPort := freePort(t), freePort(t)
	start := func(env []string, args ...string) *exec.Cmd {
		cmd := exec.Command(py, append([]string{"-B"}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	start([]string{"SEED_PATH=" + filepath.Join(task, "seed", "db.json"), "STATE_PATH=" + filepath.Join(dir, "db.json"), fmt.Sprintf("PORT=%d", deskPort)}, server)
	state := filepath.Join(dir, "caller")
	start([]string{"BENCH_VOICE_TTS=tone", "BENCH_VOICE_STT=tone"}, "voicecaller.py", "serve", "--config", filepath.Join(task, "user.json"), "--state", state, "--port", fmt.Sprint(callerPort))
	waitPort(t, deskPort)
	waitPort(t, callerPort)
	agent := "cascadedagent.py"
	if os.Getenv("BENCH_VOICE_E2E_AGENT") == "realtime" {
		agent = "realtimebridge.py"
	}
	cmd := exec.Command(py, "-B", agent, "Answer the call.")
	cmd.Env = append(os.Environ(),
		fmt.Sprintf(`BENCH_MCP_SERVERS={"helpdesk":{"url":"http://127.0.0.1:%d/mcp"}}`, deskPort),
		fmt.Sprintf("BENCH_VOICE_URL=ws://127.0.0.1:%d/call", callerPort),
		"VOICE_GATEWAY_URL="+gw, "VOICE_GATEWAY_KEY="+key, "VOICE_REALTIME_MODEL=openai/gpt-realtime")
	done := make(chan error, 1)
	var out []byte
	go func() { out, err = cmd.CombinedOutput(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v\n%s", agent, err, out)
		}
	case <-time.After(3 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatalf("%s did not finish:\n%s", agent, out)
	}
	var db struct {
		Refunds []struct {
			OrderID     string `json:"order_id"`
			AmountPence int    `json:"amount_pence"`
		} `json:"refunds"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "db.json"))
	if err != nil || json.Unmarshal(raw, &db) != nil {
		t.Fatalf("helpdesk state: %v", err)
	}
	if len(db.Refunds) != 1 || db.Refunds[0].AmountPence != 4500 || db.Refunds[0].OrderID != "B-3001" {
		t.Fatalf("refunds %+v", db.Refunds)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		ev, err := ReadTimeline(filepath.Join(state, TimelineExport))
		if err == nil && len(ev) > 0 && ev[len(ev)-1].Ev == "call_end" {
			a, err := Analyze(ev, nil)
			if err != nil || a.EndedBy != "caller_done" || a.FirstAudioMS == nil || len(a.Latencies()) == 0 {
				t.Fatalf("call: %v %+v", err, a)
			}
			t.Logf("%s: first audio %.0f ms, latencies %v, dead air %.0f ms", agent, *a.FirstAudioMS, a.Latencies(), a.DeadAirMS)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no call_end in the timeline (%v)", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitPort(t *testing.T, port int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("port %d never opened", port)
}
