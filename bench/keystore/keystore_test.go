package keystore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/keystore"
)

func master(b byte) []byte {
	m := make([]byte, 32)
	for i := range m {
		m[i] = b
	}
	return m
}

func stores(t *testing.T) map[string]keystore.Store {
	t.Helper()
	d := dbtest.Open(t)
	dbs, err := keystore.Open(d, master(7))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]keystore.Store{"db": dbs, "memory": keystore.NewMemory()}
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			k, err := s.Create(ctx, "org_a", keystore.KindProvider, keystore.Put{Provider: "openai", Label: "main", Value: "sk-test-1234567890abcd", BudgetUSD: 25, Models: []string{"gpt-4o-mini", " ", "gpt-4o-mini"}}, "u1")
			if err != nil {
				t.Fatal(err)
			}
			if k.Last4 != "abcd" || k.Status != keystore.StatusUntested || k.BudgetUSD != 25 || len(k.Models) != 1 {
				t.Fatalf("bad key %+v", k)
			}
			if v, err := s.Reveal(ctx, "org_a", k.ID); err != nil || v != "sk-test-1234567890abcd" {
				t.Fatalf("reveal = %q, %v", v, err)
			}
			// Another org sees nothing.
			if _, err := s.Get(ctx, "org_b", k.ID); !errors.Is(err, keystore.ErrNotFound) {
				t.Fatalf("cross-org get: %v", err)
			}
			if _, err := s.Reveal(ctx, "org_b", k.ID); !errors.Is(err, keystore.ErrNotFound) {
				t.Fatalf("cross-org reveal: %v", err)
			}
			if err := s.SetStatus(ctx, "org_a", k.ID, keystore.StatusOK, time.Now()); err != nil {
				t.Fatal(err)
			}
			// Listed models are stored sorted, deduplicated and capped.
			as, ok := s.(keystore.AvailableSetter)
			if !ok {
				t.Fatal("store does not remember listed models")
			}
			if len(k.ModelsAvailable) != 0 {
				t.Fatalf("new key models_available = %v", k.ModelsAvailable)
			}
			many := []string{"zeta", "alpha", "alpha", ""}
			for i := 0; i < 300; i++ {
				many = append(many, "m"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+time.Duration(i).String())
			}
			if err := as.SetModelsAvailable(ctx, "org_a", k.ID, many); err != nil {
				t.Fatal(err)
			}
			if g, _ := s.Get(ctx, "org_a", k.ID); len(g.ModelsAvailable) != keystore.MaxModelsAvailable || g.ModelsAvailable[0] != "alpha" {
				t.Fatalf("models_available = %d %v", len(g.ModelsAvailable), g.ModelsAvailable[:3])
			}
			if err := as.SetModelsAvailable(ctx, "org_b", k.ID, []string{"x"}); !errors.Is(err, keystore.ErrNotFound) {
				t.Fatalf("cross-org set models: %v", err)
			}
			// Update without a value keeps it and the status.
			u, err := s.Update(ctx, "org_a", k.ID, keystore.Put{Provider: "openai", Label: "renamed", BudgetUSD: 5}, "u1")
			if err != nil || u.Label != "renamed" || u.Status != keystore.StatusOK || u.Last4 != "abcd" {
				t.Fatalf("update = %+v, %v", u, err)
			}
			// A new value resets the status.
			u, err = s.Update(ctx, "org_a", k.ID, keystore.Put{Provider: "openai", Value: "sk-other-0000000000wxyz"}, "u1")
			if err != nil || u.Last4 != "wxyz" || u.Status != keystore.StatusUntested {
				t.Fatalf("update value = %+v, %v", u, err)
			}
			if err := s.AddSpend(ctx, "org_a", k.ID, 1.5); err != nil {
				t.Fatal(err)
			}
			if err := s.AddSpend(ctx, "org_a", k.ID, 2); err != nil {
				t.Fatal(err)
			}
			g, _ := s.Get(ctx, "org_a", k.ID)
			if g.MonthSpentUSD != 3.5 {
				t.Fatalf("spend = %v", g.MonthSpentUSD)
			}
			list, err := s.List(ctx, "org_a", keystore.KindProvider)
			if err != nil || len(list) != 1 {
				t.Fatalf("list = %v, %v", list, err)
			}
			if l, _ := s.List(ctx, "org_a", keystore.KindSecret); len(l) != 0 {
				t.Fatalf("secret list = %v", l)
			}
			if err := s.Delete(ctx, "org_a", k.ID, "u1"); err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(ctx, "org_a", k.ID, "u1"); !errors.Is(err, keystore.ErrNotFound) {
				t.Fatalf("second delete: %v", err)
			}
		})
	}
}

func TestStoreValidation(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			bad := []keystore.Put{
				{Provider: "openai"},                                       // no value
				{Provider: "nope", Value: "x"},                             // unknown provider
				{Provider: "openai-compatible", Value: "x"},                // no base URL
				{Provider: "openai", Value: "x", BaseURL: "ftp://x"},       // bad URL
				{Provider: "openai", Value: "x", BaseURL: "https://u:p@x"}, // credentials in URL
				{Provider: "openai", Value: "x", BudgetUSD: -1},            // negative budget
				{Provider: "openai", Value: strings.Repeat("x", 9000)},     // too long
			}
			for i, p := range bad {
				if _, err := s.Create(ctx, "o", keystore.KindProvider, p, ""); !errors.Is(err, keystore.ErrInvalid) {
					t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
				}
			}
		})
	}
}

func TestCiphertextBoundToMasterKey(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Open(t)
	s1, _ := keystore.Open(d, master(1))
	k, err := s1.Create(ctx, "o", keystore.KindProvider, keystore.Put{Provider: "groq", Value: "gsk_secretvalue_123"}, "")
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := keystore.Open(d, master(2))
	if _, err := s2.Reveal(ctx, "o", k.ID); err == nil {
		t.Fatal("a different master key decrypted the value")
	}
	var enc string
	if err := d.QueryRow(ctx, d.Rebind(`SELECT value_enc FROM model_keys WHERE id = ?`), k.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "secretvalue") {
		t.Fatal("plaintext stored")
	}
	if _, err := keystore.Open(d, []byte("short")); !errors.Is(err, keystore.ErrNoMasterKey) {
		t.Fatalf("short master: %v", err)
	}
}

// TestStoreProviderShapes checks keys for every Bifrost provider shape
// (lane A09): structured AWS, Vertex and Cloudflare values, base URLs for
// Azure, Databricks and self-hosted servers, keyless vLLM, and custom
// OpenAI-compatible names.
func TestStoreProviderShapes(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) { checkProviderShapes(ctx, t, s) })
	}
}

func checkProviderShapes(ctx context.Context, t *testing.T, s keystore.Store) {
	good := []keystore.Put{
		{Provider: "deepseek", Value: "sk-ds-1"},
		{Provider: "bedrock", Value: `{"access_key":"AKIA1","secret_key":"s","region":"us-east-1"}`},
		{Provider: "vertex", Value: `{"project_id":"p","region":"global","auth_credentials":"{}"}`},
		{Provider: "cloudflare", Value: `{"api_key":"t","account_id":"a"}`},
		{Provider: "azure", Value: "az", BaseURL: "https://r.openai.azure.com"},
		{Provider: "databricks", Value: "dapi", BaseURL: "https://w.cloud.databricks.com"},
		{Provider: "vllm", BaseURL: "https://vllm.example.com/v1"},
		{Provider: "zhipu", Value: "zk", BaseURL: "https://open.bigmodel.cn/api/paas/v4"},
	}
	for i, p := range good {
		if _, err := s.Create(ctx, "o", keystore.KindProvider, p, ""); err != nil {
			t.Errorf("good %d (%s): %v", i, p.Provider, err)
		}
	}
	bad := []keystore.Put{
		{Provider: "bedrock", Value: `{"region":"us-east-1"}`},
		{Provider: "vertex", Value: `{"project_id":"p"}`},
		{Provider: "cloudflare", Value: "token-only"},
		{Provider: "azure", Value: "az"},
		{Provider: "vllm"},
		{Provider: "zhipu", Value: "zk"},
		{Provider: "Bad Name", Value: "x", BaseURL: "https://x.example.com/v1"},
	}
	for i, p := range bad {
		if _, err := s.Create(ctx, "o", keystore.KindProvider, p, ""); !errors.Is(err, keystore.ErrInvalid) {
			t.Errorf("bad %d (%s): err = %v, want ErrInvalid", i, p.Provider, err)
		} else if strings.Contains(err.Error(), "us-east-1") || strings.Contains(err.Error(), "token-only") {
			t.Errorf("bad %d: message echoes the value: %v", i, err)
		}
	}
}

// TestStorePrice: a provider key keeps its per-key price through create,
// update (replaced, never kept) and both stores; an all-zero price is no
// price, and bad values are refused.
func TestStorePrice(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			p := &keystore.Price{InputPerMTok: 0.5, OutputPerMTok: 2, CachePerMTok: 0.05}
			k, err := s.Create(ctx, "org_a", keystore.KindProvider, keystore.Put{Provider: "xcodebest", BaseURL: "https://api.xcodebest.example/v1", Value: "sk-test-1234567890abcd", Price: p}, "u1")
			if err != nil {
				t.Fatal(err)
			}
			if k.Price == nil || *k.Price != *p {
				t.Fatalf("created price = %+v", k.Price)
			}
			if g, _ := s.Get(ctx, "org_a", k.ID); g.Price == nil || *g.Price != *p {
				t.Fatalf("read price = %+v", g.Price)
			}
			u, err := s.Update(ctx, "org_a", k.ID, keystore.Put{Provider: "xcodebest", BaseURL: "https://api.xcodebest.example/v1", Price: &keystore.Price{InputPerMTok: 1, OutputPerMTok: 3}}, "u1")
			if err != nil || u.Price == nil || u.Price.InputPerMTok != 1 || u.Price.CacheRate() != 1 {
				t.Fatalf("updated price = %+v, %v", u.Price, err)
			}
			u, err = s.Update(ctx, "org_a", k.ID, keystore.Put{Provider: "xcodebest", BaseURL: "https://api.xcodebest.example/v1", Price: &keystore.Price{}}, "u1")
			if err != nil || u.Price != nil {
				t.Fatalf("zero price = %+v, %v (want none)", u.Price, err)
			}
			for _, bad := range []keystore.Price{{InputPerMTok: -1}, {OutputPerMTok: keystore.MaxPricePerMTok + 1}} {
				if _, err := s.Update(ctx, "org_a", k.ID, keystore.Put{Provider: "xcodebest", BaseURL: "https://api.xcodebest.example/v1", Price: &bad}, "u1"); !errors.Is(err, keystore.ErrInvalid) {
					t.Errorf("price %+v: err = %v, want invalid", bad, err)
				}
			}
		})
	}
}
