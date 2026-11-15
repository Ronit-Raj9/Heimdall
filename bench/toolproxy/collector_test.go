package toolproxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The tool proxy serves the agent event collector beside its routes; the
// collector's ledger lands next to the tool ledger.
func TestProxyServesCollector(t *testing.T) {
	dir := t.TempDir()
	rs := &Routes{RunID: "r1", Routes: []Route{{Service: "docs", Upstream: "http://127.0.0.1:1", Protocol: ProtocolHTTP}}}
	srv, err := newServer(Config{LedgerPath: filepath.Join(dir, "tools.jsonl"), Logger: schemas.NoopLogger{}}, rs)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, agentevents.PathEvents, strings.NewReader(`{"type":"plan","output":"step 1"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	raw, err := os.ReadFile(filepath.Join(dir, agentevents.LedgerName))
	if err != nil {
		t.Fatal(err)
	}
	if recs, _ := agentevents.ReadRecords(raw); len(recs) != 1 || recs[0].Type != "plan" {
		t.Fatalf("ledger %s", raw)
	}
	tools, _ := os.ReadFile(filepath.Join(dir, "tools.jsonl"))
	if len(strings.TrimSpace(string(tools))) != 0 {
		t.Fatalf("collector traffic reached the tool ledger: %s", tools)
	}
}

func TestServeCollectorOnly(t *testing.T) {
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeCollector(ctx, CollectorConfig{Listen: addr, LedgerPath: filepath.Join(dir, agentevents.LedgerName)})
	}()
	var resp *http.Response
	for i := 0; i < 100; i++ {
		if resp, err = http.Get("http://" + addr + "/healthz"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v", err)
	}
	resp.Body.Close()
	resp, err = http.Post("http://"+addr+agentevents.PathOTLP, "application/json", strings.NewReader(`{"resourceSpans":[]}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("otlp: %v %v", err, resp)
	}
	resp.Body.Close()
	resp, _ = http.Get("http://" + addr + "/s/docs/x")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("collector-only server routed /s/: %d", resp.StatusCode)
	}
	resp.Body.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
