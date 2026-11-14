package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeProxy is a fault proxy control API that answers /health and, when
// upstreams is set, GET /upstreams with that body and status. It counts the
// upstream checks it receives.
type fakeProxy struct {
	upstreams       string // response body; empty: the route does not exist (older proxy)
	upstreamsStatus int
	token           string
	checks          atomic.Int64
}

func (f *fakeProxy) handler(t *testing.T) http.Handler {
	writeBody := func(w http.ResponseWriter, status int, body string) {
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("fake proxy write: %v", err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeBody(w, http.StatusOK, `{"status":"ok"}`)
	})
	if f.upstreams != "" {
		mux.HandleFunc("GET /upstreams", func(w http.ResponseWriter, r *http.Request) {
			f.checks.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+f.token {
				writeBody(w, http.StatusUnauthorized, `{"error":"control token required"}`)
				return
			}
			writeBody(w, f.upstreamsStatus, f.upstreams)
		})
	}
	return mux
}

func TestFaultProxyChecks(t *testing.T) {
	const okReport = `{"checked_at":"2026-11-14T10:00:00Z","upstreams":[` +
		`{"provider":"anthropic","host":"api.anthropic.com:443","ok":true,"latency_ms":41.6},` +
		`{"provider":"openai","host":"api.openai.com:443","ok":false,"latency_ms":5000,` +
		`"error":"dial tcp: lookup api.openai.com on 127.0.0.11:53: server misbehaving"},` +
		`{"provider":"groq","host":"api.groq.com:443","ok":false,"latency_ms":0,"skipped":"upstream traffic goes through the HTTP proxy at corp:3128; not dialled"}]}`
	pass := func(id, detail string) schemas.DoctorCheck {
		return schemas.DoctorCheck{ID: id, Status: schemas.DoctorPass, Detail: detail}
	}
	row := func(id string, st schemas.DoctorStatus, detail string) schemas.DoctorCheck {
		return schemas.DoctorCheck{ID: id, Status: st, Detail: detail}
	}
	tests := []struct {
		name       string
		proxy      *fakeProxy // nil: nothing listens at the control URL
		token      string
		want       []schemas.DoctorCheck // the fault proxy row is checked by status only when its detail is "*"
		wantChecks int64
	}{
		{name: "upstream rows per provider", proxy: &fakeProxy{upstreams: okReport, upstreamsStatus: 200, token: "tok"}, token: "tok",
			wantChecks: 1, want: []schemas.DoctorCheck{
				pass("fault proxy", "*"),
				pass("upstream anthropic", "api.anthropic.com:443, 42 ms"),
				row("upstream openai", schemas.DoctorFail, "dial tcp: lookup api.openai.com on 127.0.0.11:53: server misbehaving"),
				row("upstream groq", schemas.DoctorSkip, "api.groq.com:443: upstream traffic goes through the HTTP proxy at corp:3128; not dialled"),
			}},
		{name: "older proxy warns once", proxy: &fakeProxy{}, token: "tok", want: []schemas.DoctorCheck{
			pass("fault proxy", "*"),
			row("upstreams", schemas.DoctorWarn, "proxy too old for upstream check"),
		}},
		{name: "missing token warns without calling", proxy: &fakeProxy{upstreams: okReport, upstreamsStatus: 200, token: "tok"},
			want: []schemas.DoctorCheck{
				pass("fault proxy", "*"),
				row("fault proxy token", schemas.DoctorWarn, "BENCH_FAULTPROXY_TOKEN is not set; plans cannot be armed"),
				row("upstreams", schemas.DoctorWarn, "BENCH_FAULTPROXY_TOKEN is not set; upstream check skipped"),
			}},
		{name: "wrong token fails", proxy: &fakeProxy{upstreams: okReport, upstreamsStatus: 200, token: "tok"}, token: "nope",
			wantChecks: 1, want: []schemas.DoctorCheck{
				pass("fault proxy", "*"),
				row("upstreams", schemas.DoctorFail, "fault proxy upstream check: status 401: control token required"),
			}},
		{name: "scripted only", proxy: &fakeProxy{upstreams: `{"checked_at":"2026-11-14T10:00:00Z","upstreams":[]}`, upstreamsStatus: 200, token: "tok"},
			token: "tok", wantChecks: 1, want: []schemas.DoctorCheck{
				pass("fault proxy", "*"),
				row("upstreams", schemas.DoctorSkip, "no provider has a real key in the fault proxy; the scripted model answers"),
			}},
		{name: "proxy down", token: "tok", want: []schemas.DoctorCheck{
			row("fault proxy", schemas.DoctorFail, "*"),
			row("upstreams", schemas.DoctorSkip, "fault proxy is not reachable"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controlURL := "http://" + closedLoopback(t)
			if tt.proxy != nil {
				srv := httptest.NewServer(tt.proxy.handler(t))
				defer srv.Close()
				controlURL = srv.URL
			}
			got := faultProxyChecks(context.Background(), controlURL, tt.token)
			if len(got) == len(tt.want) && got[0].Status == tt.want[0].Status && tt.want[0].Detail == "*" {
				got[0].Detail = "*"
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("rows:\n got  %+v\n want %+v", got, tt.want)
			}
			if tt.proxy != nil && tt.proxy.checks.Load() != tt.wantChecks {
				t.Fatalf("upstream checks = %d, want %d", tt.proxy.checks.Load(), tt.wantChecks)
			}
		})
	}
}

// closedLoopback returns a loopback address nothing listens on.
func closedLoopback(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
