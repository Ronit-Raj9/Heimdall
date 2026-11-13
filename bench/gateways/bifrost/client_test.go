package bifrost

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestCall(t *testing.T) {
	f := newFakeBifrost(t)
	tests := []struct {
		name       string
		token      string
		path       string
		wantStatus int
	}{
		{name: "token accepted", token: testToken, path: "/api/version"},
		{name: "missing token refused", token: "", path: "/api/version", wantStatus: 401},
		{name: "not found", token: testToken, path: "/api/providers/anthropic", wantStatus: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(f.srv.URL)
			cfg.SetupToken = tt.token
			if tt.token == "" {
				cfg.SetupToken = "wrong-token-value"
			}
			g, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			var out string
			err = g.call(context.Background(), http.MethodGet, tt.path, nil, &out)
			if tt.wantStatus == 0 {
				if err != nil || out != "v2.5.1" {
					t.Fatalf("call = %q, %v", out, err)
				}
				return
			}
			if !isStatus(err, tt.wantStatus) {
				t.Fatalf("err = %v, want status %d", err, tt.wantStatus)
			}
			if strings.Contains(err.Error(), cfg.SetupToken) {
				t.Errorf("error leaks the setup token: %v", err)
			}
		})
	}
}

func TestSnippet(t *testing.T) {
	long := strings.Repeat("x", errorBodyBytes+10)
	tests := []struct {
		in, want string
	}{
		{"  short \n", "short"},
		{long, long[:errorBodyBytes] + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.want[:min(10, len(tt.want))], func(t *testing.T) {
			if got := snippet([]byte(tt.in)); got != tt.want {
				t.Errorf("snippet = %q", got)
			}
		})
	}
}
