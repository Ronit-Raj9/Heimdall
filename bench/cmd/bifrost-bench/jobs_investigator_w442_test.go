package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// investigatorFixture is the hosted calibrate fixture with a chat server
// that answers every call with reply, priced as judge-model.
func investigatorFixture(t *testing.T, reply string) (jobEnv, keystore.Store, *keystore.Key, *orgCalGateway, *[]string) {
	t.Helper()
	env, ks, key, gw, _ := hostedCalibrateFixture(t)
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		bodies = append(bodies, string(raw))
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "judge-model",
			"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}, "usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 100}})
	}))
	t.Cleanup(srv.Close)
	old := calibrateGatewayFor
	calibrateGatewayFor = func(context.Context, jobEnv) (schemas.Gateway, string, func(), error) {
		return gw, srv.URL, func() {}, nil
	}
	t.Cleanup(func() { calibrateGatewayFor = old })
	return env, ks, key, gw, &bodies
}

func TestInvestigatorCallerCapsAndRevokes(t *testing.T) {
	env, ks, key, gw, _ := investigatorFixture(t, "fine")
	if _, err := ks.Update(context.Background(), "org1", key.ID, keystore.Put{Provider: "openai", Label: "mine", BudgetUSD: 3}, "u"); err != nil {
		t.Fatal(err)
	}
	if err := ks.AddSpend(context.Background(), "org1", key.ID, 1); err != nil {
		t.Fatal(err)
	}
	if openCaller == nil {
		t.Fatal("openCaller is not set")
	}
	j := &jobs.Job{ID: "job7"}
	c, done, err := openCaller(context.Background(), env, j, modelParams{Provider: "openai", Model: "judge-model", BudgetUSD: 50, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.Budget() != 2 {
		t.Fatalf("budget %v, want 2 (the key's month budget left, under the 5.00 cap)", c.Budget())
	}
	if gw.bound["job7"] != "org1" {
		t.Fatalf("job not bound to the org: %v", gw.bound)
	}
	if _, err := c.Call(context.Background(), investigator.Request{Purpose: "findings", Messages: []investigator.Message{{Role: "user", Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	spent := c.Spent()
	if spent <= 0 {
		t.Fatal("no spend recorded")
	}
	done()
	done()
	if gw.calGateway.revoked != 1 {
		t.Fatalf("key revoked %d times, want 1", gw.calGateway.revoked)
	}
	k, err := ks.Get(context.Background(), "org1", key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if k.MonthSpentUSD < 1+spent-1e-9 || k.MonthSpentUSD > 1+spent+1e-9 {
		t.Fatalf("month spend %v, want %v", k.MonthSpentUSD, 1+spent)
	}
}

func TestInvestigatorCallerHostedCap(t *testing.T) {
	env, _, _, _, _ := investigatorFixture(t, "fine")
	c, done, err := openCaller(context.Background(), env, &jobs.Job{ID: "job8"}, modelParams{Provider: "openai", Model: "judge-model", BudgetUSD: 50, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if c.Budget() != hostedInvestigatorMaxUSD {
		t.Fatalf("budget %v, want %v", c.Budget(), hostedInvestigatorMaxUSD)
	}
}

func TestInvestigatorCallerRefusals(t *testing.T) {
	env, ks, key, _, _ := investigatorFixture(t, "fine")
	cases := []struct {
		p    modelParams
		want string
	}{
		{modelParams{Provider: "openai", BudgetUSD: 1, Yes: true}, "Pick a model first."},
		{modelParams{Provider: "openai", Model: "judge-model", BudgetUSD: 1}, "Confirm the cost first."},
		{modelParams{Provider: "openai", Model: "judge-model", Yes: true}, "Set a spending cap above $0 first."},
		{modelParams{Provider: "openai", Model: "unpriced-model", BudgetUSD: 1, Yes: true}, "This model has no price on file, so the spending cap cannot hold. Pick another model."},
	}
	for _, c := range cases {
		_, _, err := openCaller(context.Background(), env, &jobs.Job{ID: "j"}, c.p)
		if err == nil || err.Error() != c.want {
			t.Errorf("%+v: got %v, want %q", c.p, err, c.want)
		}
	}
	if _, err := ks.Update(context.Background(), "org1", key.ID, keystore.Put{Provider: "openai", Label: "mine", BudgetUSD: 1}, "u"); err != nil {
		t.Fatal(err)
	}
	if err := ks.AddSpend(context.Background(), "org1", key.ID, 1); err != nil {
		t.Fatal(err)
	}
	_, _, err := openCaller(context.Background(), env, &jobs.Job{ID: "j"}, modelParams{Provider: "openai", Model: "judge-model", BudgetUSD: 1, Yes: true})
	if err == nil || !strings.Contains(err.Error(), "Your model key for openai has used its month budget.") {
		t.Fatalf("got %v", err)
	}
}
