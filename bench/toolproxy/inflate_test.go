package toolproxy

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInflateText(t *testing.T) {
	a := inflateText(1000, "n1")
	if len(a) != 4000 {
		t.Fatalf("len %d, want 4000", len(a))
	}
	if a != inflateText(1000, "n1") {
		t.Error("same nonce gave different filler")
	}
	if a == inflateText(1000, "n2") {
		t.Error("another nonce gave the same filler")
	}
	if !strings.HasPrefix(a, "[000001] ") {
		t.Errorf("filler starts %q", a[:20])
	}
	if got := len(inflateText(maxInflateTokens+5, "n")); got != maxInflateTokens*4 {
		t.Errorf("capped len %d", got)
	}
}

func TestProxyInflateMCP(t *testing.T) {
	up := &fakeMCP{}
	upstream := httptest.NewServer(up)
	defer upstream.Close()
	tp := newTestProxy(t, upstream.URL, ProtocolMCP)
	tp.srv.hold = func(context.Context, time.Duration) error { return nil }
	tp.setFault(t, `{"mode":"inflate","tool":"echo","calls":1,"tokens":2000,"nonce":"n_i"}`)
	_, _, body := post(t, tp.front.URL, "/s/fs/mcp", callBody(1, "echo", "hi"), nil)
	if !strings.Contains(body, "echo: hi") || !strings.Contains(body, "[000001] ") {
		t.Fatalf("answer lacks the real result or the filler: %.200q", body)
	}
	if len(body) < 8000 {
		t.Errorf("answer is %d bytes, want the filler of about 8000", len(body))
	}
	rows := tp.callRows(t)
	if len(rows) != 1 || rows[0].Fault != FaultInflate || rows[0].Nonce != "n_i" || rows[0].IsError {
		t.Fatalf("rows = %+v", rows)
	}
}
