package faultproxy

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestValidateBudgetSqueeze(t *testing.T) {
	cases := []struct {
		name string
		plan schemas.FaultPlan
		ok   bool
	}{
		{"defaults", schemas.FaultPlan{Mode: ModeBudgetSqueeze, Call: 2}, true},
		{"grace and 429", schemas.FaultPlan{Mode: ModeBudgetSqueeze, Call: 2, GraceCalls: 3, Status: 429}, true},
		{"no call", schemas.FaultPlan{Mode: ModeBudgetSqueeze}, false},
		{"from_call", schemas.FaultPlan{Mode: ModeBudgetSqueeze, Call: 1, FromCall: 2}, false},
		{"bad status", schemas.FaultPlan{Mode: ModeBudgetSqueeze, Call: 1, Status: 503}, false},
		{"negative grace", schemas.FaultPlan{Mode: ModeBudgetSqueeze, Call: 1, GraceCalls: -1}, false},
		{"grace on another mode", schemas.FaultPlan{Mode: ModeOnce, Call: 1, Status: 503, GraceCalls: 1}, false},
	}
	for _, tc := range cases {
		err := ValidatePlan(tc.plan)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

func TestPlanTableBudgetSqueeze(t *testing.T) {
	tab := newPlanTable()
	if err := tab.set("s", schemas.FaultPlan{Mode: ModeBudgetSqueeze, Call: 2, GraceCalls: 2, RunID: "r"}); err != nil {
		t.Fatal(err)
	}
	type want struct {
		inject    bool
		status    int
		remaining int
	}
	wants := []want{{false, 0, -1}, {false, 0, -1}, {false, 0, 1}, {false, 0, 0}, {true, 402, -1}, {true, 402, -1}}
	for i, w := range wants {
		d := tab.begin("s", KindMessage)
		if d.Inject != w.inject || d.Status != w.status || d.Remaining != w.remaining || d.Budget != w.inject {
			t.Errorf("call %d: got inject %v status %d remaining %d budget %v, want %+v", i+1, d.Inject, d.Status, d.Remaining, d.Budget, w)
		}
	}
	// Uncounted kinds never move the squeeze.
	if d := tab.begin("s", KindCountTokens); d.Inject || d.Remaining != -1 {
		t.Errorf("count_tokens decided %+v", d)
	}
}

func TestProxyBudgetSqueeze(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeBudgetSqueeze, Call: 1, RunID: "run-1", Nonce: "nonce-1"})
	resp, _ := env.call(t, "POST", "/v1/messages", "auth-a", msgBody)
	if resp.StatusCode != 200 || resp.Header.Get(BudgetRemainingHeader) != "" {
		t.Fatalf("call 1: status %d header %q", resp.StatusCode, resp.Header.Get(BudgetRemainingHeader))
	}
	resp, _ = env.call(t, "POST", "/v1/messages", "auth-a", msgBody)
	if resp.StatusCode != 200 || resp.Header.Get(BudgetRemainingHeader) != "0" {
		t.Fatalf("call 2 (grace): status %d header %q", resp.StatusCode, resp.Header.Get(BudgetRemainingHeader))
	}
	resp, body := env.call(t, "POST", "/v1/messages", "auth-a", msgBody)
	if resp.StatusCode != 402 || !strings.Contains(string(body), "Budget exhausted") || !strings.Contains(string(body), "nonce-1") {
		t.Fatalf("call 3: status %d body %s", resp.StatusCode, body)
	}
	var actions []string
	for _, e := range env.entries(t) {
		if e.Kind == KindMessage {
			actions = append(actions, e.Action)
		}
	}
	if strings.Join(actions, ",") != "pass,pass,inject" {
		t.Errorf("ledger actions %v", actions)
	}
}
