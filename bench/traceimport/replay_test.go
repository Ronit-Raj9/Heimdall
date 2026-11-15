package traceimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startReplay runs replay/server.py with python3 on a free local port,
// seeded with the session's observed calls, and returns its base URL and
// the state file. The test is skipped when python3 is not installed.
func startReplay(t *testing.T, s *Session) (string, string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	seed, err := observedTools(s, Source{File: "t.json", SHA256: "0", Format: FormatChat})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"server.py": replayServer, "seed.json": seed} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state", "calls.json")
	cmd := exec.Command(py, "-I", filepath.Join(dir, "server.py"))
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port), "SEED_PATH="+filepath.Join(dir, "seed.json"), "STATE_PATH="+state)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if resp, err := http.Get(base + "/healthz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("replay server did not come up")
		}
	}
	return base, state
}

func post(t *testing.T, url, body string) (int, map[string]any, http.Header) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("%s: %v: %s", url, err, b)
		}
	}
	return resp.StatusCode, out, resp.Header
}

func replaySession(t *testing.T) *Session {
	t.Helper()
	tr, err := Parse(fixture(t, "chat-transcript.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	s.ToolCalls = append(s.ToolCalls, ToolCall{Seq: 3, Name: "refund", Arguments: json.RawMessage(`{}`), IsError: true, Error: "gateway timeout"})
	return s
}

func TestReplayServer(t *testing.T) {
	base, state := startReplay(t, replaySession(t))
	mcp := base + "/mcp"
	code, init, hdr := post(t, mcp, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if code != 200 || hdr.Get("Mcp-Session-Id") == "" || init["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %d %v", code, init)
	}
	if code, _, _ := post(t, mcp, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Errorf("notification: %d", code)
	}
	_, list, _ := post(t, mcp, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if tools := list["result"].(map[string]any)["tools"].([]any); len(tools) != 3 || tools[0].(map[string]any)["name"] != "get_item" {
		t.Errorf("tools/list = %v", list)
	}
	call := func(id int, name, args string) map[string]any {
		_, out, _ := post(t, mcp, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, name, args))
		return out
	}
	if r := call(3, "get_item", `{"sku":"W-100"}`)["result"].(map[string]any); r["isError"] != false || !strings.Contains(fmt.Sprint(r["content"]), "Steel water bottle") {
		t.Errorf("get_item = %v", r)
	}
	if r := call(4, "refund", `{"x":1}`)["result"].(map[string]any); r["isError"] != true || !strings.Contains(fmt.Sprint(r["content"]), "gateway timeout") {
		t.Errorf("refund = %v (a recorded error is replayed as an error)", r)
	}
	if e := call(5, "delete_everything", `{}`)["error"]; e == nil {
		t.Error("an unknown tool was answered")
	}
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if code, out, _ := post(t, base+"/tools/adjust_stock", `{"sku":"W-101","delta":12,"reason":"delivery"}`); code != 200 || !strings.Contains(fmt.Sprint(out["result"]), "W-101") {
		t.Errorf("HTTP adjust_stock: %d %v", code, out)
	}
	if code, out, _ := post(t, base+"/tools/refund", `{}`); code != 502 || out["error"] != "gateway timeout" {
		t.Errorf("HTTP refund: %d %v", code, out)
	}
	var calls struct {
		Calls []struct {
			Tool     string `json:"tool"`
			Replayed *int   `json:"replayed"`
			Exact    bool   `json:"exact"`
			IsError  bool   `json:"is_error"`
		} `json:"calls"`
	}
	if err := json.Unmarshal([]byte(readText(t, state)), &calls); err != nil {
		t.Fatal(err)
	}
	if len(calls.Calls) != 5 || !calls.Calls[0].Exact || calls.Calls[1].Exact || !calls.Calls[1].IsError || calls.Calls[2].Replayed != nil || !calls.Calls[3].Exact {
		t.Errorf("calls.json = %+v", calls.Calls)
	}
}

// TestReplayReference runs the drafted reference script of each kind
// against the replay server: it makes the observed calls in order.
func TestReplayReference(t *testing.T) {
	tr, err := Parse(fixture(t, "chat-transcript.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not installed")
	}
	for _, kind := range []string{KindToolUse, KindMCP} {
		t.Run(kind, func(t *testing.T) {
			base, state := startReplay(t, s)
			script := filepath.Join(t.TempDir(), "reference.sh")
			if err := os.WriteFile(script, referenceScript(s, kind, ReplayServiceID, true), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", script)
			cmd.Env = append(os.Environ(), "TOOL_URL="+base, `BENCH_MCP_SERVERS={"tools":{"url":"`+base+`/mcp"}}`)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("reference: %v\n%s", err, out.String())
			}
			got := readText(t, state)
			if strings.Count(got, `"exact": true`) != 2 {
				t.Errorf("calls.json = %s", got)
			}
		})
	}
}

// TestReplayMutant runs the observed-calls mutant against the replay
// server: it makes every recorded call, the failed one included, and exits 0.
func TestReplayMutant(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not installed")
	}
	s := replaySession(t)
	for _, kind := range []string{KindToolUse, KindMCP} {
		t.Run(kind, func(t *testing.T) {
			base, state := startReplay(t, s)
			script := filepath.Join(t.TempDir(), "mutant.sh")
			if err := os.WriteFile(script, mutantScript(s, kind, ReplayServiceID), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", script)
			cmd.Env = append(os.Environ(), "TOOL_URL="+base, `BENCH_MCP_SERVERS={"tools":{"url":"`+base+`/mcp"}}`)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("mutant: %v\n%s", err, out)
			}
			if got := readText(t, state); strings.Count(got, `"exact": true`) != 3 || !strings.Contains(got, `"is_error": true`) {
				t.Errorf("calls.json = %s", got)
			}
		})
	}
}
