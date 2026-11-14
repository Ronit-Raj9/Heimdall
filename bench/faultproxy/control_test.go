package faultproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestControlClientRoundTrip(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	ctx := context.Background()
	var ctrl schemas.FaultController = env.client
	plan := schemas.FaultPlan{Mode: ModeOnce, Call: 1, Status: 503, RunID: "run-1", Nonce: "nonce_abc"}
	if err := ctrl.SetPlan(ctx, "slot-a", plan); err != nil {
		t.Fatal(err)
	}
	if got, ok := env.srv.plans.get("slot-a"); !ok || got != plan {
		t.Fatalf("plan on server = %+v, %v", got, ok)
	}
	env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody)
	env.call(t, http.MethodGet, "/v1/models", "auth-a", "")
	env.call(t, http.MethodPost, "/v1/messages", "auth-b", msgBody) // another run's slot
	entries, err := ctrl.Ledger(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Action != ActionInject || entries[0].Nonce != "nonce_abc" || entries[1].Kind != KindModels {
		t.Fatalf("ledger = %+v", entries)
	}
	if err := VerifyChain(entries); err != nil {
		t.Fatalf("run ledger chain: %v", err)
	}
	if err := ctrl.ClearPlan(ctx, "slot-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.srv.plans.get("slot-a"); ok {
		t.Fatal("plan still bound after ClearPlan")
	}
	empty, err := ctrl.Ledger(ctx, "no-such-run")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty ledger = %v, %v", empty, err)
	}
	if err := env.client.Health(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestControlRejects(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	ctx := context.Background()
	good := schemas.FaultPlan{Mode: ModePersistent, FromCall: 1, Status: 503}
	cases := []struct {
		name    string
		do      func() error
		wantErr string
	}{
		{"no token", func() error { return NewClient(env.ctrl.URL).SetPlan(ctx, "slot-a", good) }, "401"},
		{"wrong token", func() error { return NewClient(env.ctrl.URL, WithToken("nope")).ClearPlan(ctx, "slot-a") }, "401"},
		{"unknown slot", func() error { return env.client.SetPlan(ctx, "slot-z", good) }, "404"},
		{"invalid plan", func() error { return env.client.SetPlan(ctx, "slot-a", schemas.FaultPlan{Mode: ModeOnce}) }, "400"},
		{"ledger without run id", func() error {
			_, err := env.client.Ledger(ctx, "")
			return err
		}, "400"},
		{"unreachable", func() error {
			c := NewClient("http://127.0.0.1:1", WithHTTPClient(&http.Client{Timeout: time.Second}))
			return c.Health(ctx)
		}, "health"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.do()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
	if _, ok := env.srv.plans.get("slot-a"); ok {
		t.Fatal("a rejected write changed the plan table")
	}
}

func TestControlRejectsUnknownPlanFields(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	req, err := http.NewRequest(http.MethodPut, env.ctrl.URL+"/plans/slot-a", strings.NewReader(`{"mode":"none","sneaky":1}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.srv.ControlToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func TestControlUpstreamsAuth(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	ctx := context.Background()
	tests := []struct {
		name    string
		client  *Client
		wantErr string // empty: success
	}{
		{"control token", env.client, ""},
		{"no token", NewClient(env.ctrl.URL), "status 401: control token required"},
		{"wrong token", NewClient(env.ctrl.URL, WithToken("nope")), "status 401: control token required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, err := tt.client.CheckUpstreams(ctx)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if rep.Upstreams == nil || len(rep.Upstreams) != 0 {
					t.Fatalf("scripted-only proxy reported %+v, want an empty list", rep.Upstreams)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || errors.Is(err, ErrUpstreamCheckUnsupported) {
				t.Fatalf("err = %v, want %q and not the unsupported sentinel", err, tt.wantErr)
			}
		})
	}
}

func TestClientCheckUpstreams(t *testing.T) {
	// oldProxy has the control routes of a proxy that predates /upstreams.
	oldProxy := http.NewServeMux()
	oldProxy.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	oldProxy.HandleFunc("PUT /upstreams", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	tests := []struct {
		name        string
		handler     http.Handler
		path        string // appended to the fake's URL as the control base
		unsupported bool
		wantErr     string // empty: success
		want        []UpstreamCheck
	}{
		{name: "old proxy answers 404", handler: oldProxy, path: "/old", unsupported: true, wantErr: "status 404"},
		{name: "old proxy answers 405", handler: oldProxy, unsupported: true, wantErr: "status 405"},
		{name: "proxy error is not unsupported", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
		}), wantErr: "status 500: boom"},
		{name: "report decoded", handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/upstreams" || r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
				return
			}
			_, err := w.Write([]byte(`{"checked_at":"2026-11-14T10:00:00Z","upstreams":[` +
				`{"provider":"openai","host":"api.openai.com:443","ok":false,"latency_ms":12.5,"error":"dial tcp: lookup api.openai.com: no such host"}]}`))
			if err != nil {
				t.Errorf("write: %v", err)
			}
		}), want: []UpstreamCheck{{Provider: "openai", Host: "api.openai.com:443", LatencyMS: 12.5,
			Error: "dial tcp: lookup api.openai.com: no such host"}}},
		{name: "empty report is an empty list", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, err := w.Write([]byte(`{"checked_at":"2026-11-14T10:00:00Z"}`))
			if err != nil {
				t.Errorf("write: %v", err)
			}
		}), want: []UpstreamCheck{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			rep, err := NewClient(srv.URL+tt.path, WithToken("tok")).CheckUpstreams(context.Background())
			if errors.Is(err, ErrUpstreamCheckUnsupported) != tt.unsupported {
				t.Fatalf("err = %v, unsupported sentinel want %v", err, tt.unsupported)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Upstreams) != len(tt.want) || rep.Upstreams == nil || rep.CheckedAt.IsZero() {
				t.Fatalf("report = %+v, want %+v", rep, tt.want)
			}
			for i := range tt.want {
				if rep.Upstreams[i] != tt.want[i] {
					t.Fatalf("upstream %d = %+v, want %+v", i, rep.Upstreams[i], tt.want[i])
				}
			}
		})
	}
}
