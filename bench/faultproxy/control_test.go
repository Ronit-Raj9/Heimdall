package faultproxy

import (
	"context"
	"net/http"
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
