package faultproxy

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// wsEchoUpstream answers a WebSocket handshake and then echoes raw bytes,
// recording the handshake request.
func wsEchoUpstream(t *testing.T, seen chan<- *http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(r.Context())
		if !isWebSocketUpgrade(r) {
			http.Error(w, "want an upgrade", http.StatusUpgradeRequired)
			return
		}
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+
			base64.StdEncoding.EncodeToString(sum[:])+"\r\nOpenai-Organization: org-secret\r\n\r\n")
		_, _ = io.Copy(conn, buf)
	}))
}

func dialRealtime(t *testing.T, env *testEnv, auth string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	addr := strings.TrimPrefix(env.data.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = io.WriteString(conn, "GET /v1/realtime?model=gpt-realtime HTTP/1.1\r\nHost: "+addr+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nAuthorization: Bearer "+auth+"\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn, br, resp
}

func realtimeEnv(t *testing.T, upURL string) *testEnv {
	specs := DefaultProviders()
	for i := range specs {
		specs[i].BaseURL = upURL
	}
	keys := map[string]string{"OPENAI_API_KEY": "sk-openai-real"}
	return newTestEnvConfig(t, Config{Providers: specs, Getenv: func(k string) string { return keys[k] }})
}

// X8 voice agents: a Realtime session is relayed with the real key, bytes
// both ways, one ledger line of kind audio with status 101.
func TestRealtimeRelay(t *testing.T) {
	seen := make(chan *http.Request, 4)
	up := wsEchoUpstream(t, seen)
	defer up.Close()
	env := realtimeEnv(t, up.URL)
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1"})
	conn, br, resp := dialRealtime(t, env, "oa-auth-1")
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" || resp.Header.Get("Openai-Organization") != "" {
		t.Fatalf("handshake %d %v", resp.StatusCode, resp.Header)
	}
	got := <-seen
	if got.Header.Get("Authorization") != "Bearer sk-openai-real" || got.URL.Query().Get("model") != "gpt-realtime" || got.URL.Path != "/v1/realtime" {
		t.Fatalf("upstream handshake: auth %q url %s", got.Header.Get("Authorization"), got.URL)
	}
	if _, err := conn.Write([]byte("frame-bytes")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, len("frame-bytes"))
	if _, err := io.ReadFull(br, echo); err != nil || string(echo) != "frame-bytes" {
		t.Fatalf("echo %q %v", echo, err)
	}
	conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var lines []schemas.LedgerEntry
		for _, e := range env.entries(t) {
			if e.Path == "/v1/realtime" {
				lines = append(lines, e)
			}
		}
		if len(lines) == 1 {
			if e := lines[0]; e.Kind != KindAudio || e.Action != ActionPass || e.Status != http.StatusSwitchingProtocols || e.RunID != "r1" {
				t.Fatalf("ledger line %+v", e)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no ledger line for the session")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A plan with the Realtime model refuses the session before any upstream
// dial; the scripted model has no Realtime session.
func TestRealtimeOutageAndScripted(t *testing.T) {
	seen := make(chan *http.Request, 4)
	up := wsEchoUpstream(t, seen)
	defer up.Close()
	env := realtimeEnv(t, up.URL)
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1", Mode: ModePersistent, FromCall: 1, Status: 503, Model: "gpt-realtime"})
	conn, _, resp := dialRealtime(t, env, "oa-auth-1")
	conn.Close()
	if resp.StatusCode != 503 || len(seen) != 0 {
		t.Fatalf("outage: status %d, upstream saw %d", resp.StatusCode, len(seen))
	}
	scripted := newTestEnvConfig(t, Config{})
	scripted.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r2"})
	conn, _, resp = dialRealtime(t, scripted, "oa-auth-1")
	conn.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("scripted realtime: %d", resp.StatusCode)
	}
}
