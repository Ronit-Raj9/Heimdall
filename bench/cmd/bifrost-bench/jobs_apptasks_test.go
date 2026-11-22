package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/apptasks"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

const appTasksJobSpec = `openapi: 3.0.3
info: {title: Acme Shop API, version: 1.0.0}
paths:
  /orders/{order_id}:
    get:
      operationId: getOrder
      parameters: [{name: order_id, in: path, required: true, schema: {type: string}}]
    patch:
      operationId: updateOrder
      parameters: [{name: order_id, in: path, required: true, schema: {type: string}}]
      requestBody:
        content:
          application/json:
            schema: {type: object, properties: {status: {type: string}}}
`

// appTasksGatewayChat is the gateway's chat route: it answers each op and
// records the virtual key and model it was called with.
func appTasksGatewayChat(t *testing.T) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(b, &req)
		mu.Lock()
		seen = append(seen, r.Header.Get("x-bf-vk")+" "+req.Model)
		mu.Unlock()
		user := req.Messages[len(req.Messages)-1].Content
		reply := `{"scenarios": [{"rule_id": "R1", "title": "Refund O-1", "opening_message": "Refund O-1", "persona_goal": "Refund",
		 "expected_actions": [{"tool": "getOrder", "args": {"order_id": "O-1"}}, {"tool": "updateOrder", "args": {"order_id": "O-1", "body": {"status": "refunded"}}}]}]}`
		switch {
		case strings.HasPrefix(user, "Read the company's API"):
			reply = `{"rules": [{"id": "R1", "text": "Refund delivered orders.", "kind": "must", "operations": ["updateOrder"]}]}`
		case strings.HasPrefix(user, "Write the starting data"):
			reply = `{"collections": {"orders": [{"id": "O-1", "status": "delivered"}]}}`
		}
		out, _ := json.Marshal(map[string]any{"model": "judge-model", "choices": []any{map[string]any{"message": map[string]any{"content": reply}}},
			"usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 200}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func appTasksHostedEnv(t *testing.T) (jobEnv, keystore.Store, *keystore.Key, *orgCalGateway, *[]string) {
	t.Helper()
	env, ks, key, gw, _ := hostedCalibrateFixture(t)
	srv, seen := appTasksGatewayChat(t)
	old := calibrateGatewayFor
	calibrateGatewayFor = func(context.Context, jobEnv) (schemas.Gateway, string, func(), error) {
		return gw, srv.URL, func() {}, nil
	}
	t.Cleanup(func() { calibrateGatewayFor = old })
	env.ProjectRoot = t.TempDir()
	return env, ks, key, gw, seen
}

func runAppTasks(env jobEnv, params string) (any, string, error) {
	log := &testJobLog{}
	res, err := runAppTasksGenerate(context.Background(), env, &jobs.Job{Params: json.RawMessage(params)}, log, nil)
	return res, log.String(), err
}

func TestAppTasksJobHosted(t *testing.T) {
	env, ks, key, gw, seen := appTasksHostedEnv(t)
	st := appTasksStore(env.ProjectRoot)
	d, _, err := st.Create(apptasks.Upload{Suite: "shop-support", App: "shop", Spec: []byte(appTasksJobSpec), Policy: []byte("Refund delivered orders.")})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"draft":"` + d.ID + `","stage":"rules","provider":"openai","model":"judge-model"}`, // no yes
		`{"draft":"x","stage":"rules","yes":true,"provider":"openai","model":"judge-model"}`,
		`{"draft":"` + d.ID + `","stage":"all","yes":true,"provider":"openai","model":"judge-model"}`,
		`{"draft":"` + d.ID + `","stage":"rules","yes":true}`} {
		if _, _, err := validateAppTasksGenerate(context.Background(), env, json.RawMessage(bad)); !errors.Is(err, jobs.ErrInvalidParams) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	ok := `{"draft":"` + d.ID + `","stage":"%s","yes":true,"provider":"openai","model":"judge-model"}`
	if _, _, err := runAppTasks(env, strings.Replace(ok, "%s", "tasks", 1)); err == nil || !strings.Contains(err.Error(), "approve") {
		t.Fatalf("tasks before rules: %v", err)
	}
	res, log, err := runAppTasks(env, strings.Replace(ok, "%s", "rules", 1))
	if err != nil {
		t.Fatalf("rules: %v\n%s", err, log)
	}
	r := res.(*appTasksResult)
	if r.Status != apptasks.StatusRulesDrafted || r.Rules != 1 || r.KeyID != key.ID || r.SpentUSD <= 0 || r.CapUSD != hostedAppTasksMaxUSD {
		t.Fatalf("rules result %+v\n%s", r, log)
	}
	if len(gw.creds) != 1 || gw.creds[0].Value != "sk-org-own" || gw.minted != 1 || gw.revoked != 1 {
		t.Fatalf("gateway creds %+v minted %d revoked %d", gw.creds, gw.minted, gw.revoked)
	}
	if (*seen)[0] != "sk-judge openai/judge-model" {
		t.Fatalf("chat called with %q", (*seen)[0])
	}
	if strings.Contains(log, "sk-org-own") || strings.Contains(log, "sk-judge") {
		t.Fatal("a key in the job log")
	}
	k, _ := ks.Get(context.Background(), "org1", key.ID)
	if k.MonthSpentUSD <= 0 {
		t.Fatalf("spend not added: %+v", k)
	}

	d, _ = st.Load(d.ID)
	m, _, _ := st.Mock(d)
	if err := apptasks.ApproveRules(d, m, d.Rules, "ana"); err != nil {
		t.Fatal(err)
	}
	_ = st.Save(d)
	res, log, err = runAppTasks(env, strings.Replace(ok, "%s", "tasks", 1))
	if err != nil {
		t.Fatalf("tasks: %v\n%s", err, log)
	}
	if r := res.(*appTasksResult); r.Tasks != 1 || r.Status != apptasks.StatusGenerated {
		t.Fatalf("tasks result %+v\n%s", r, log)
	}

	// The hosted USD cap is hard: a cap below one call refuses before it is sent.
	old := hostedAppTasksMaxUSD
	hostedAppTasksMaxUSD = 0.0001
	t.Cleanup(func() { hostedAppTasksMaxUSD = old })
	n := len(*seen)
	if _, _, err := runAppTasks(env, strings.Replace(ok, "%s", "tasks", 1)); !errors.Is(err, apptasks.ErrBudget) && (err == nil || !strings.Contains(err.Error(), "budget")) {
		t.Fatalf("over the cap: %v", err)
	}
	if len(*seen) != n {
		t.Fatal("a call was sent past the cap")
	}

	// No key for the provider: refused with the add-key link.
	env.Services = handlers.Services{OrgID: "org1", Hosted: true, Keys: keystore.NewMemory()}
	if _, _, err := runAppTasks(env, strings.Replace(ok, "%s", "rules", 1)); err == nil || !strings.Contains(err.Error(), "/models?add=openai") {
		t.Fatalf("no key: %v", err)
	}
}
