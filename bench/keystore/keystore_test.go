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
