package toolproxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestProxyLostAckMCP checks that a lost_ack call reaches the server (the
// effect happens), that the agent gets a timeout error after the hold, and
// that the one ledger row keeps the server's real answer.
func TestProxyLostAckMCP(t *testing.T) {
	for _, sse := range []bool{false, true} {
		for _, batch := range []bool{false, true} {
			t.Run(fmt.Sprintf("sse=%v/batch=%v", sse, batch), func(t *testing.T) {
				up := &fakeMCP{sse: sse}
				upstream := httptest.NewServer(up)
				defer upstream.Close()
				tp := newTestProxy(t, upstream.URL, ProtocolMCP)
				tp.setFault(t, `{"mode":"lost_ack","server":"fs","tool":"echo","calls":1,"timeout_s":7,"nonce":"nonce_abc"}`)
				body := callBody(1, "echo", "refund")
				if batch {
					body = "[" + body + "," + callBody(2, "other", "x") + "]"
				}
				status, hdr, got := post(t, tp.front.URL, "/s/fs/mcp", body, nil)
				if status != 200 || !strings.Contains(hdr.Get("Content-Type"), "json") {
					t.Fatalf("status %d type %q", status, hdr.Get("Content-Type"))
				}
				if !strings.Contains(got, "timed out after 7s") || !strings.Contains(got, `"isError":true`) || strings.Contains(got, "echo: refund") {
					t.Fatalf("agent answer %s", got)
				}
				if batch && !strings.Contains(got, "echo: x") {
					t.Fatalf("the other call's answer is missing: %s", got)
				}
				if calls := up.toolCalls(); len(calls) < 1 || calls[0] != "echo" {
					t.Fatalf("server calls %v: the lost call must reach the server", calls)
				}
				if len(tp.holds) != 1 || tp.holds[0] != 7*time.Second {
					t.Fatalf("holds %v", tp.holds)
				}
				rows := tp.waitCallRows(t, 1)
				r := rows[0]
				if r.Fault != FaultLostAck || r.Nonce != "nonce_abc" || !r.IsError || !r.Undelivered || !r.FaultDelivered || r.Status != 200 || !strings.Contains(r.Result, "echo: refund") {
					t.Fatalf("row %+v", r)
				}
				// The next call is not lost (calls: 1).
				_, _, again := post(t, tp.front.URL, "/s/fs/mcp", callBody(3, "echo", "refund"), nil)
				if !strings.Contains(again, "echo: refund") {
					t.Fatalf("second call %s", again)
				}
			})
		}
	}
}

// TestProxyLostAckHTTP checks lost_ack on a plain HTTP tool route.
func TestProxyLostAckHTTP(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"order":"O-1","status":"created"}`)
	}))
	defer upstream.Close()
	tp := newTestProxy(t, upstream.URL, ProtocolHTTP)
	tp.setFault(t, `{"mode":"lost_ack","server":"fs","timeout_s":3,"nonce":"nonce_abc"}`)
	resp, err := http.Post(tp.front.URL+"/s/fs/orders", "application/json", strings.NewReader(`{"sku":"KT-12"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout || !strings.Contains(string(b), "timed out") {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("server hits %d", hits)
	}
	rows := tp.waitCallRows(t, 1)
	if r := rows[0]; r.Fault != FaultLostAck || !r.Undelivered || !r.FaultDelivered || r.Status != 200 || !strings.Contains(r.Result, "created") {
		t.Fatalf("row %+v", r)
	}
}

// TestProxyLostAckHangUp checks that an agent that hangs up during the hold
// leaves a lost_ack row whose timeout answer is not marked delivered, on both
// the MCP and the plain HTTP route (3-D-13).
func TestProxyLostAckHangUp(t *testing.T) {
	for _, proto := range []string{ProtocolMCP, ProtocolHTTP} {
		t.Run(proto, func(t *testing.T) {
			var upstream *httptest.Server
			if proto == ProtocolMCP {
				upstream = httptest.NewServer(&fakeMCP{})
			} else {
				upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.WriteString(w, `{"status":"created"}`)
				}))
			}
			defer upstream.Close()
			tp := newTestProxy(t, upstream.URL, proto)
			tp.setFault(t, `{"mode":"lost_ack","server":"fs","timeout_s":3,"nonce":"nonce_h"}`)
			tp.srv.hold = func(context.Context, time.Duration) error { return context.Canceled }
			body, path := callBody(1, "echo", "refund"), "/s/fs/mcp"
			if proto == ProtocolHTTP {
				body, path = `{"sku":"KT-12"}`, "/s/fs/orders"
			}
			if resp, err := http.Post(tp.front.URL+path, "application/json", strings.NewReader(body)); err == nil {
				resp.Body.Close()
			}
			rows := tp.waitCallRows(t, 1)
			if r := rows[0]; r.Fault != FaultLostAck || r.Status != 200 || r.FaultDelivered {
				t.Fatalf("row %+v", r)
			}
		})
	}
}

// TestProxyLostAckOpenStream checks the hold starts once the lost call's
// response arrives, while the server keeps its event stream open
// (QC2-14-05).
func TestProxyLostAckOpenStream(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"echo: refund\"}]}}\n\n")
		w.(http.Flusher).Flush()
		select { // idle stream, open until the test ends
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(release)
	tp := newTestProxy(t, upstream.URL, ProtocolMCP)
	tp.setFault(t, `{"mode":"lost_ack","server":"fs","tool":"echo","calls":1,"timeout_s":7,"nonce":"nonce_abc"}`)
	done := make(chan string, 1)
	go func() {
		_, _, got := post(t, tp.front.URL, "/s/fs/mcp", callBody(1, "echo", "refund"), nil)
		done <- got
	}()
	select {
	case got := <-done:
		if !strings.Contains(got, "timed out after 7s") {
			t.Fatalf("agent answer %s", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lost_ack waited for the open stream to end")
	}
}
