package monitorcert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestHTTPMonitor(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{"answers", func(w http.ResponseWriter, r *http.Request) {
			var req schemas.MonitorRequest
			b, err := io.ReadAll(r.Body)
			if err != nil || json.Unmarshal(b, &req) != nil || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			decision := "allow"
			if req.Action != nil && strings.Contains(req.Action.Content, "rm") {
				decision = "block"
			}
			_, _ = w.Write([]byte(`{"decision":"` + decision + `"}`))
		}, ""},
		{"server error", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
		}, "status 503"},
		{"not json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }, "decode monitor answer"},
		{"too large", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, maxHTTPAnswer+10)) }, "over"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			m := newHTTPMonitor(&schemas.MonitorConfig{URL: srv.URL, TimeoutS: 5}, nil)
			reqs := []schemas.MonitorRequest{
				{Mode: "action", Action: &schemas.MonitorAction{Content: "ls"}},
				{Mode: "action", Action: &schemas.MonitorAction{Content: "rm -rf ."}},
			}
			resps, err := m.Score(context.Background(), reqs)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(resps) != 2 || resps[0].Decision != "allow" || resps[1].Decision != "block" {
				t.Errorf("answers %+v", resps)
			}
		})
	}
}
