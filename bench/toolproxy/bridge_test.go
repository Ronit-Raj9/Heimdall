package toolproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestMain lets the test binary act as a stdio MCP server for the bridge
// tests when TOOLPROXY_FAKE_STDIO is set.
func TestMain(m *testing.M) {
	if os.Getenv("TOOLPROXY_FAKE_STDIO") == "1" {
		fakeStdioServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeStdioServer answers initialize (after asking the client for its roots
// and sending a notification, as real servers do), tools/call and exit.
func fakeStdioServer() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	w := bufio.NewWriter(os.Stdout)
	send := func(v any) {
		b, _ := json.Marshal(v)
		w.Write(append(b, '\n'))
		w.Flush()
	}
	for sc.Scan() {
		var m rpcMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		switch m.Method {
		case "initialize":
			send(map[string]any{"jsonrpc": "2.0", "id": "srv-1", "method": "roots/list"})
			send(map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"level": "info"}})
			fmt.Fprintln(w, "not json at all")
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"serverInfo": map[string]any{"name": "fake-stdio"}}})
		case methodToolsCall:
			name, args := readToolParams(m.Params)
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": name + " " + string(args)}}}})
		case "exit":
			os.Exit(3)
		case "":
			// The bridge's answer to roots/list: it must be a refusal.
			if strings.Contains(sc.Text(), `"srv-1"`) && strings.Contains(sc.Text(), `"error"`) {
				fmt.Fprintln(os.Stderr, "roots refused")
			}
		}
	}
}

func startTestBridge(t *testing.T) (*Bridge, *httptest.Server) {
	t.Helper()
	t.Setenv("TOOLPROXY_FAKE_STDIO", "1")
	ctx, cancel := context.WithCancel(context.Background())
	b, err := StartBridge(ctx, []string{os.Args[0], "-test.run=^$"}, "/mcp", schemas.NoopLogger{})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	srv := httptest.NewServer(b)
	t.Cleanup(func() {
		srv.Close()
		cancel()
		<-b.Exited()
	})
	return b, srv
}

func TestBridge(t *testing.T) {
	b, srv := startTestBridge(t)
	tests := []struct {
		name, method, path, body string
		wantStatus               int
		wantBody                 []string
	}{
		{name: "healthz", method: "GET", path: "/healthz", wantStatus: 200},
		{name: "initialize", method: "POST", path: "/mcp", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, wantStatus: 200, wantBody: []string{"fake-stdio", `"id":1`}},
		{name: "notification", method: "POST", path: "/mcp", body: `{"jsonrpc":"2.0","method":"notifications/initialized"}`, wantStatus: 202},
		{name: "call", method: "POST", path: "/mcp", body: callBody(2, "echo", "hi"), wantStatus: 200, wantBody: []string{`echo {\"text\":\"hi\"}`}},
		{name: "batch", method: "POST", path: "/mcp", body: "[" + callBody(3, "a", "") + "," + callBody(4, "b", "") + "]", wantStatus: 200, wantBody: []string{`"id":3`, `"id":4`, `[{`}},
		{name: "parse error", method: "POST", path: "/mcp", body: `nope`, wantStatus: 400, wantBody: []string{"-32700"}},
		{name: "no stream", method: "GET", path: "/mcp", wantStatus: 405},
		{name: "delete session", method: "DELETE", path: "/mcp", wantStatus: 200},
		{name: "other path", method: "POST", path: "/x", wantStatus: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, srv.URL+tt.path, strings.NewReader(tt.body))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var buf strings.Builder
			_, _ = bufio.NewReader(resp.Body).WriteTo(&buf)
			resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tt.wantStatus, buf.String())
			}
			for _, w := range tt.wantBody {
				if !strings.Contains(buf.String(), w) {
					t.Errorf("body %q lacks %q", buf.String(), w)
				}
			}
		})
	}
	t.Run("server exit fails pending requests and health", func(t *testing.T) {
		_, _, body := post(t, srv.URL, "/mcp", `{"jsonrpc":"2.0","id":9,"method":"exit"}`, nil)
		if !strings.Contains(body, "stdio server exited") {
			t.Fatalf("body %q", body)
		}
		select {
		case <-b.Exited():
		case <-time.After(5 * time.Second):
			t.Fatal("bridge did not see the exit")
		}
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("healthz %d after exit", resp.StatusCode)
		}
	})
}

func TestStartBridgeErrors(t *testing.T) {
	tests := []struct {
		name string
		argv []string
	}{
		{name: "no command"},
		{name: "missing binary", argv: []string{"/nonexistent/mcp-server"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := StartBridge(context.Background(), tt.argv, "", nil); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

// TestProxyInFrontOfBridge runs the whole chain in process: client, tool
// proxy, bridge, stdio server.
func TestProxyInFrontOfBridge(t *testing.T) {
	_, br := startTestBridge(t)
	tp := newTestProxy(t, br.URL, ProtocolMCP)
	post(t, tp.front.URL, "/s/fs/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, nil)
	_, _, body := post(t, tp.front.URL, "/s/fs/mcp", callBody(2, "read_file", "x"), nil)
	if !strings.Contains(body, "read_file") {
		t.Fatalf("body %q", body)
	}
	rows := tp.rows(t)
	if len(rows) != 1 || rows[0].Tool != "read_file" || rows[0].IsError || rows[0].Status != 200 {
		t.Fatalf("rows = %+v", rows)
	}
}
