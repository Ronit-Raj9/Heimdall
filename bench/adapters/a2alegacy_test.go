package adapters

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// legacyA2A is an agent gateway in front of a pre-1.0 A2A agent: SendMessage
// is unknown, message/send answers a 0.x task that is still working, and
// tasks/get completes it.
type legacyA2A struct {
	mu      sync.Mutex
	methods []string
	parts   []map[string]any
}

func (f *legacyA2A) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params struct {
			Message struct {
				Kind  string           `json:"kind"`
				Role  string           `json:"role"`
				Parts []map[string]any `json:"parts"`
			} `json:"message"`
		} `json:"params"`
	}
	_ = json.Unmarshal(raw, &req)
	f.mu.Lock()
	f.methods = append(f.methods, req.Method)
	if req.Method == "message/send" {
		f.parts = req.Params.Message.Parts
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch req.Method {
	case "message/send":
		if req.Params.Message.Kind != "message" || req.Params.Message.Role != "user" {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"error":{"code":-32602,"message":"bad message"}}`, req.ID)
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"kind":"task","id":"t9","contextId":"c9","status":{"state":"working"}}}`, req.ID)
	case "tasks/get":
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"kind":"task","id":"t9","status":{"state":"completed"},"artifacts":[{"artifactId":"a","parts":[{"kind":"text","text":"30 days"}]}]}}`, req.ID)
	default:
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"error":{"code":-32601,"message":"Method not found"}}`, req.ID)
	}
}

// TestA2ALauncherLegacyFallback is Y03 (G1 P0-7): a pre-1.0 agent is sent
// message/send after SendMessage is refused as unknown, polled with
// tasks/get, and its 0.x answer is read like a 1.0 one.
func TestA2ALauncherLegacyFallback(t *testing.T) {
	f := &legacyA2A{}
	answer, out, err := runA2AAgainst(t, f, nil)
	if err != nil || answer != "30 days" {
		t.Fatalf("answer %q err %v\n%s", answer, err, out)
	}
	if got := strings.Join(f.methods, ","); got != "SendMessage,message/send,tasks/get" {
		t.Fatalf("methods %s", got)
	}
	if len(f.parts) == 0 || f.parts[0]["kind"] != "text" {
		t.Fatalf("legacy parts %v", f.parts)
	}
	if rep := lastReport(t, out); rep.FinalState != "TASK_STATE_COMPLETED" || rep.TaskID != "t9" {
		t.Fatalf("report %+v", rep)
	}

	// A card that says 0.x skips the 1.0 attempt.
	f2 := &legacyA2A{}
	if answer, out, err = runA2AAgainst(t, f2, map[string]string{"BENCH_A2A_PROTOCOL": "0.3.0"}); err != nil || answer != "30 days" {
		t.Fatalf("0.x card: answer %q err %v\n%s", answer, err, out)
	}
	if f2.methods[0] != "message/send" {
		t.Fatalf("methods %v", f2.methods)
	}
}

// runA2AAgainst runs the launcher against any handler.
func runA2AAgainst(t *testing.T, h http.Handler, env map[string]string) (string, string, error) {
	t.Helper()
	answer, out, _, err := runA2ALauncherHandler(t, h, env)
	return answer, out, err
}
