package toolproxy

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func newTestExchange(t *testing.T) (*exchange, *Server) {
	t.Helper()
	rs, err := ParseRoutes([]byte(`{"routes":[{"service":"fs","upstream":"http://fs:1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := newServer(Config{LedgerPath: filepath.Join(t.TempDir(), "tools.jsonl")}, rs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	fixed := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	return s.newExchange(rs.Routes[0]), s
}

func TestExchangeUnanswered(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		status      int
		wantIsError bool
		wantResult  string
	}{
		{name: "call without answer failed", key: "1", status: 200, wantIsError: true, wantResult: "stream ended"},
		{name: "notification accepted", key: "", status: 202, wantIsError: false, wantResult: ""},
		{name: "notification refused", key: "", status: 500, wantIsError: true, wantResult: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, _ := newTestExchange(t)
			p := x.track(tt.key, "echo", []byte(`{}`))
			x.unanswered(tt.status, []byte("stream ended"))
			if p.row.IsError != tt.wantIsError || p.row.Result != tt.wantResult || p.row.Status != tt.status {
				t.Fatalf("row = %+v", p.row)
			}
		})
	}
}

func TestExchangeAnswerMessage(t *testing.T) {
	x, _ := newTestExchange(t)
	p := x.track("1", "echo", nil)
	tests := []struct {
		name    string
		msg     string
		wantHit bool
	}{
		{name: "server request is not an answer", msg: `{"jsonrpc":"2.0","id":1,"method":"roots/list"}`},
		{name: "unknown id", msg: `{"jsonrpc":"2.0","id":2,"result":{}}`},
		{name: "string id differs from number", msg: `{"jsonrpc":"2.0","id":"1","result":{}}`},
		{name: "matching answer", msg: `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`, wantHit: true},
		{name: "second answer ignored", msg: `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, _, ok := parseRPC([]byte(tt.msg))
			if !ok {
				t.Fatal("bad fixture")
			}
			_, got, _ := x.answerMessage(items[0], 200)
			if (got != nil) != tt.wantHit || (got != nil && got != p) {
				t.Fatalf("hit %v, want %v", got != nil, tt.wantHit)
			}
		})
	}
}

func TestExchangeCommitOnce(t *testing.T) {
	x, s := newTestExchange(t)
	a := x.track("1", "a", nil)
	x.track("2", "b", nil)
	a.answer(json.RawMessage(`{}`), false, 200, s.now())
	x.commit(a)
	x.commit(a)
	x.unanswered(200, nil)
	x.commitAll(true)
	x.commitAll(false)
	if s.ledger.seq != 2 {
		t.Fatalf("wrote %d rows, want 2", s.ledger.seq)
	}
}
