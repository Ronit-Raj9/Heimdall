package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestKeyBudgetRefusal checks G10: a provider whose keys are all over budget
// is refused with the key's name, one usable key is enough, a pinned key id
// is checked on its own, and over-budget keys are never delivered.
func TestKeyBudgetRefusal(t *testing.T) {
	ctx := context.Background()
	ks := keystore.NewMemory()
	mk := func(provider, label string, budget float64) *keystore.Key {
		k, err := ks.Create(ctx, "org_a", keystore.KindProvider, keystore.Put{Provider: provider, Label: label, Value: "sk-" + label + "-123456789", BudgetUSD: budget}, "t")
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	a1 := mk("anthropic", "main", 10)
	if err := ks.AddSpend(ctx, "org_a", a1.ID, 12); err != nil {
		t.Fatal(err)
	}
	err := KeyBudgetRefusal(ctx, ks, "org_a", []string{"anthropic"}, nil)
	if !errors.Is(err, ErrKeyOverBudget) || !strings.Contains(err.Error(), `key "main" is over its budget; raise it on Models`) {
		t.Fatalf("over budget: %v", err)
	}
	if err := KeyBudgetRefusal(ctx, ks, "org_a", []string{"openai"}, nil); err != nil {
		t.Fatalf("provider without org keys refused: %v", err)
	}
	creds, err := UsableProviderCredentials(ctx, ks, "org_a")
	if err != nil || len(creds) != 0 {
		t.Fatalf("over-budget key delivered: %+v %v", creds, err)
	}

	a2 := mk("anthropic", "spare", 0) // no budget: always usable
	if err := KeyBudgetRefusal(ctx, ks, "org_a", []string{"anthropic"}, nil); err != nil {
		t.Fatalf("a usable second key still refused: %v", err)
	}
	if err := KeyBudgetRefusal(ctx, ks, "org_a", []string{"anthropic"}, []string{a1.ID}); !errors.Is(err, ErrKeyOverBudget) {
		t.Fatalf("pinned over-budget key: %v", err)
	}
	creds, err = UsableProviderCredentials(ctx, ks, "org_a")
	if err != nil || len(creds) != 1 || creds[0].KeyID != a2.ID || creds[0].Value == "" {
		t.Fatalf("delivery: %+v %v", creds, err)
	}

	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {}, "B": {}}}
	b := m.Arms["B"]
	b.Config.Gateway.Provider = "anthropic"
	m.Arms["B"] = b
	if got := ManifestProviders(m); len(got) != 1 || got[0] != "anthropic" {
		t.Fatalf("manifest providers %v", got)
	}
}

// TestSarvamKeyTest checks the Sarvam key test: POST /text-lid at the API
// root (a pasted /v1 base is trimmed) with api-subscription-key, and a bad
// key named by Sarvam's error code is reported invalid.
func TestSarvamKeyTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/text-lid" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("api-subscription-key") != "good-sarvam-key" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid API key","code":"invalid_api_key_error","request_id":"x"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"request_id":"x","language_code":"en-IN","script_code":"Latn"}`))
	}))
	defer srv.Close()
	k := &keystore.Key{Provider: "sarvam", BaseURL: srv.URL + "/v1"}
	if res := testProviderKey(context.Background(), false, k, "good-sarvam-key"); !res.OK {
		t.Fatalf("good key: %+v", res)
	}
	res := testProviderKey(context.Background(), false, k, "bad-key")
	if res.OK || res.Status != keystore.StatusInvalid {
		t.Fatalf("bad key: %+v", res)
	}
}

// TestQuickRunRefusesOverBudgetKey checks QuickRun answers 409 with the
// key's name, and plans nothing, when every key of the model's provider is
// over its budget.
func TestQuickRunRefusesOverBudgetKey(t *testing.T) {
	ctx := context.Background()
	ks := keystore.NewMemory()
	k, err := ks.Create(ctx, "org_a", keystore.KindProvider, keystore.Put{Provider: "anthropic", Label: "team", Value: "sk-team-123456789", BudgetUSD: 1}, "t")
	if err != nil {
		t.Fatal(err)
	}
	_ = ks.AddSpend(ctx, "org_a", k.ID, 1)
	l := &fakeLauncher{}
	r, _ := quickRunServer(t, Services{OrgID: "org_a", Launcher: l, Keys: ks})
	resp := postQuick(r, `{"model":{"provider":"anthropic","model":"claude-x"},"start":false}`)
	if resp.Response.StatusCode() != 409 || !strings.Contains(string(resp.Response.Body()), "is over its budget; raise it on Models") {
		t.Fatalf("status %d: %s", resp.Response.StatusCode(), resp.Response.Body())
	}
	if l.planned != nil || l.created != nil {
		t.Fatal("a refused QuickRun still planned or created")
	}
}
