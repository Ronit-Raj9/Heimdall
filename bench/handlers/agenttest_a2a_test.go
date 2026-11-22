package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// a2aTestAgent serves a card and a JSON-RPC endpoint. legacy makes it a 0.x
// agent: SendMessage is unknown, message/send answers.
func a2aTestAgent(t *testing.T, legacy, cardSays0x bool) (*httptest.Server, *[]string) {
	t.Helper()
	var methods []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if cardSays0x {
				fmt.Fprintf(w, `{"name":"old","url":%q,"protocolVersion":"0.3.0","preferredTransport":"JSONRPC"}`, srv.URL+"/rpc")
				return
			}
			fmt.Fprintf(w, `{"name":"support","protocolVersion":"1.0","supportedInterfaces":[{"url":%q,"protocolBinding":"JSONRPC","protocolVersion":"1.0"}]}`, srv.URL+"/rpc")
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		methods = append(methods, req.Method)
		switch {
		case req.Method == "SendMessage" && !legacy:
			if r.Header.Get("A2A-Version") != "1.0" {
				t.Errorf("no A2A-Version header")
			}
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"task":{"id":"t1","status":{"state":"TASK_STATE_WORKING"}}}}`, req.ID)
		case req.Method == "GetTask":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"id":"t1","status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"parts":[{"text":"READY"}]}]}}`, req.ID)
		case req.Method == "message/send" && legacy:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"kind":"message","parts":[{"kind":"text","text":"READY"}]}}`, req.ID)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"error":{"code":-32601,"message":"Method not found"}}`, req.ID)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &methods
}

func TestEndpointTestA2A(t *testing.T) {
	h := &agentTestHandler{client: endpointTestClient, now: time.Now}
	run := func(card string) *EndpointTestResult {
		cfg := &schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: card, TimeoutS: 20}}
		return h.run(context.Background(), cfg)
	}
	srv, methods := a2aTestAgent(t, false, false)
	if res := run(srv.URL + "/.well-known/agent-card.json"); !res.OK || !res.Matched || strings.Join(*methods, ",") != "SendMessage,GetTask" {
		t.Fatalf("1.0 agent: %+v %v", res, *methods)
	}
	srv, methods = a2aTestAgent(t, true, false)
	if res := run(srv.URL + "/card"); !res.OK || !res.Matched || strings.Join(*methods, ",") != "SendMessage,message/send" {
		t.Fatalf("refusing agent: %+v %v", res, *methods)
	}
	srv, methods = a2aTestAgent(t, true, true)
	if res := run(srv.URL + "/card"); !res.OK || strings.Join(*methods, ",") != "message/send" || len(res.Notes) == 0 {
		t.Fatalf("0.x card: %+v %v", res, *methods)
	}
	// A registration-only agent has nothing to call directly.
	if res := h.run(context.Background(), &schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{Registration: "x"}}); res.OK || !strings.Contains(res.Error, "Try one real task") {
		t.Fatalf("registration: %+v", res)
	}
	// Hosted servers call only public https addresses.
	hosted := &agentTestHandler{client: endpointTestClient, now: time.Now}
	hosted.env.Services.Hosted = true
	if res := hosted.run(context.Background(), &schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: srv.URL + "/card"}}); res.OK {
		t.Fatalf("hosted loopback card: %+v", res)
	}
}

func TestA2AEndpointFromCard(t *testing.T) {
	c := &a2aCard{URL: "https://x/rpc", ProtocolVersion: "0.2.5"}
	if u, v := a2aEndpoint(c); u != "https://x/rpc" || !a2aLegacy(v) {
		t.Fatalf("%s %s", u, v)
	}
	c = &a2aCard{URL: "https://x/grpc", PreferredTransport: "GRPC", Additional: []struct {
		URL       string `json:"url"`
		Transport string `json:"transport"`
	}{{URL: "https://x/jr", Transport: "JSONRPC"}}}
	if u, _ := a2aEndpoint(c); u != "https://x/jr" {
		t.Fatal(u)
	}
}
