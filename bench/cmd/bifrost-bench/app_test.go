package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/faultproxy"
)

func TestProxyUpstreams(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		unsupported bool
		wantErr     bool
		want        []engine.UpstreamStatus
	}{
		{name: "report mapped", status: http.StatusOK, body: `{"checked_at":"2026-11-14T10:00:00Z","upstreams":[` +
			`{"provider":"anthropic","host":"api.anthropic.com:443","ok":true,"latency_ms":40},` +
			`{"provider":"openai","host":"api.openai.com:443","ok":false,"error":"dial tcp: lookup api.openai.com: no such host"},` +
			`{"provider":"groq","host":"api.groq.com:443","skipped":"through an HTTP proxy"}]}`,
			want: []engine.UpstreamStatus{
				{Provider: "anthropic", Host: "api.anthropic.com:443", OK: true},
				{Provider: "openai", Host: "api.openai.com:443", Error: "dial tcp: lookup api.openai.com: no such host"},
				{Provider: "groq", Host: "api.groq.com:443", Skipped: "through an HTTP proxy"},
			}},
		{name: "older proxy is the engine sentinel", status: http.StatusNotFound, body: "404 page not found",
			unsupported: true, wantErr: true},
		{name: "other errors pass through", status: http.StatusUnauthorized, body: `{"error":"control token required"}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				if _, err := w.Write([]byte(tt.body)); err != nil {
					t.Errorf("write: %v", err)
				}
			}))
			defer srv.Close()
			var c engine.UpstreamChecker = proxyUpstreams{faultproxy.NewClient(srv.URL, faultproxy.WithToken("tok"))}
			got, err := c.CheckUpstreams(context.Background())
			if errors.Is(err, engine.ErrUpstreamCheckUnsupported) != tt.unsupported || (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v, unsupported %v", err, tt.wantErr, tt.unsupported)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("statuses = %+v, want %+v", got, tt.want)
			}
		})
	}
}
