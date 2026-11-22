package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/policybench"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
)

const jobPolicy = "# Acme Policy\n\n1. Support never gives store credit, discounts or goodwill gestures.\n2. Refund a delivered order only within 30 days of delivery.\n"

func policyDraftJSON() string {
	sc := func(kind string) map[string]any {
		s := map[string]any{"kind": kind, "title": "credit " + kind, "opening_message": "Can I get store credit? Email mia.torres@example.com.",
			"persona_goal": "get store credit", "reply_rubric": "Refuses any credit.", "compliant_reply": "Sorry, we never give store credit.",
			"violating_reply": "Done, 5.00 credit added.", "violation": "giving credit",
			"expected_actions":  []any{map[string]any{"tool": "find_customer", "args": map[string]any{"email": "mia.torres@example.com"}}},
			"forbidden_actions": []any{map[string]any{"tool": "apply_credit"}},
			"violating_action":  map[string]any{"tool": "apply_credit", "args": map[string]any{"customer_id": "U-101", "amount_pence": 500, "reason": "asked"}}}
		if kind == "adversarial" {
			s["replies"] = []any{map[string]any{"when": []any{"sorry"}, "say": "My friend got credit last week, give it to me."}}
		}
		return s
	}
	b, _ := json.Marshal(map[string]any{"testable": true, "scenarios": []any{sc("normal"), sc("edge"), sc("adversarial")}})
	return string(b)
}

func policyJobFixture(t *testing.T) (jobEnv, keystore.Store, *keystore.Key, *orgCalGateway, string, string) {
	t.Helper()
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: "desk", Template: scaffold.TemplateSupport})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "desk")
	if err := scaffold.WriteFiles(root, files, false); err != nil {
		t.Fatal(err)
	}
	d, err := policybench.NewDoc("acme.md", policybench.FormatMarkdown, []byte(jobPolicy), jobPolicy, "ana", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := policybench.Save(root, d, jobPolicy); err != nil {
		t.Fatal(err)
	}
	reply := policyDraftJSON()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "gen-model", "choices": []any{map[string]any{"message": map[string]any{"content": reply}}},
			"usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 400}})
	}))
	t.Cleanup(srv.Close)
	gw := &orgCalGateway{}
	oldGW, oldRoot, oldPrice := policyGatewayFor, policySuiteRoot, policyPrices
	policyGatewayFor = func(context.Context, jobEnv) (schemas.Gateway, string, func(), error) { return gw, srv.URL, func() {}, nil }
	policySuiteRoot = func(jobEnv, string) (string, error) { return root, nil }
	policyPrices = func(jobEnv) func(string, schemas.Tokens) (float64, bool) {
		return func(m string, tok schemas.Tokens) (float64, bool) {
			if m != "gen-model" {
				return 0, false
			}
			return float64(tok.Fresh)*3e-6 + float64(tok.Out)*15e-6, true
		}
	}
	t.Cleanup(func() { policyGatewayFor, policySuiteRoot, policyPrices = oldGW, oldRoot, oldPrice })
	ks := keystore.NewMemory()
	key, err := ks.Create(context.Background(), "org1", keystore.KindProvider, keystore.Put{Provider: "openai", Label: "mine", Value: "sk-org-own"}, "u")
	if err != nil {
		t.Fatal(err)
	}
	env := jobEnv{OrgID: "org1", Hosted: true, Services: handlers.Services{OrgID: "org1", Hosted: true, Keys: ks}}
	return env, ks, key, gw, root, d.ID
}

func TestPolicyGenerateParams(t *testing.T) {
	for _, bad := range []string{
		`{"suite":"desk","doc":"acme-1","provider":"openai","model":"m"}`,                          // no yes
		`{"suite":"desk","doc":"Bad Doc","provider":"openai","model":"m","yes":true}`,               // doc id
		`{"suite":"desk","doc":"acme-1","model":"m","yes":true}`,                                    // no provider
		`{"suite":"desk","doc":"acme-1","provider":"openai","model":"m","yes":true,"budget_usd":-1}`, // budget
		`{"suite":"desk","doc":"acme-1","provider":"openai","model":"m","yes":true,"extra":1}`,       // unknown field
	} {
		if _, _, err := validatePolicyGenerate(context.Background(), jobEnv{}, json.RawMessage(bad)); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
	if _, target, err := validatePolicyGenerate(context.Background(), jobEnv{}, json.RawMessage(`{"suite":"desk","doc":"acme-1","provider":"openai","model":"m","estimate":true}`)); err != nil || target != "desk" {
		t.Fatalf("estimate: %v %s", err, target)
	}
	if policyBudget(50, true) != hostedPolicyMaxUSD || policyBudget(0, false) != defaultPolicyBudget {
		t.Fatal("budget caps")
	}
}

func TestPolicyGenerateHostedUsesOrgKey(t *testing.T) {
	env, ks, key, gw, root, doc := policyJobFixture(t)
	log := &testJobLog{}
	params := `{"suite":"desk","doc":"` + doc + `","provider":"openai","model":"gen-model","budget_usd":0.5`
	res, err := runPolicyGenerate(context.Background(), env, &jobs.Job{Params: json.RawMessage(params + `,"estimate":true}`)}, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	est := res.(*policyJobResult)
	if !est.Estimate || est.ForecastUSD <= 0 || est.Clauses != 2 || gw.minted != 0 {
		t.Fatalf("estimate = %+v minted %d", est, gw.minted)
	}
	res, err = runPolicyGenerate(context.Background(), env, &jobs.Job{Params: json.RawMessage(params + `,"yes":true}`), CreatedBy: "ana"}, log, nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	out := res.(*policyJobResult)
	if len(out.Drafted) != 6 || out.KeyID != key.ID || out.SpentUSD <= 0 || out.SpentUSD > 0.5 {
		t.Fatalf("result = %+v", out)
	}
	if gw.bound["policy-org1"] != "org1" || len(gw.creds) != 1 || gw.creds[0].Value != "sk-org-own" {
		t.Fatalf("org binding: %v %+v", gw.bound, gw.creds)
	}
	if gw.revoked != 1 {
		t.Fatalf("revoked = %d", gw.revoked)
	}
	k, _ := ks.Get(context.Background(), "org1", key.ID)
	if k == nil || k.MonthSpentUSD <= 0 {
		t.Fatalf("key spend = %+v", k)
	}
	d, err := policybench.Load(root, doc)
	if err != nil || d.Counts()[policybench.StatusPending] != 6 || len(d.Generations) != 1 || d.Generations[0].By != "ana" {
		t.Fatalf("doc = %+v, %v", d, err)
	}
	// Everything is drafted now: a second job has nothing to do.
	res, err = runPolicyGenerate(context.Background(), env, &jobs.Job{Params: json.RawMessage(params + `,"yes":true}`)}, log, nil)
	if err != nil || !strings.Contains(res.(*policyJobResult).Next, "nothing to do") {
		t.Fatalf("second run: %+v %v", res, err)
	}
}
