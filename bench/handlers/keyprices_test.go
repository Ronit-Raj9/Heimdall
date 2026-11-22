package handlers

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/keystore"
)

// priceKeys builds a memory key store for org o1 with the given keys.
func priceKeys(t *testing.T, puts ...keystore.Put) keystore.Store {
	t.Helper()
	ks := keystore.NewMemory()
	for _, p := range puts {
		if p.Value == "" {
			p.Value = "sk-test-abcdefgh"
		}
		if _, err := ks.Create(context.Background(), "o1", keystore.KindProvider, p, "u"); err != nil {
			t.Fatal(err)
		}
	}
	return ks
}

// withPriceList installs list as the server's price list for one test.
func withPriceList(t *testing.T, list map[string]evidence.ModelPrice) {
	t.Helper()
	SetPriceList(func(model string) (evidence.ModelPrice, bool) {
		p, ok := list[model]
		return p, ok
	})
	t.Cleanup(func() { SetPriceList(nil) })
}

// TestKeyPriceBareModelSingleKey (F1.price): the live key has a price and
// no model list, and cost rows name the bare model ("gpt-6.2-sol"). With
// exactly one such priced key the bare name takes its price; with two the
// key price does not decide.
func TestKeyPriceBareModelSingleKey(t *testing.T) {
	ks := priceKeys(t, keystore.Put{Provider: "openai", Label: "ronit-biforst",
		Price: &keystore.Price{InputPerMTok: 2, OutputPerMTok: 10, CachePerMTok: 0.1}})
	lookup := KeyPriceLookup(context.Background(), ks, "o1")
	if lookup == nil {
		t.Fatal("no lookup")
	}
	p, ok := lookup("gpt-6.2-sol")
	if !ok || p.InputPerMTok != 2 || p.OutputPerMTok != 10 || p.CacheReadPerMTok != 0.1 {
		t.Fatalf("bare model = %+v %v, want the key's price", p, ok)
	}
	if p, ok := lookup("openai/gpt-6.2-sol"); !ok || p.InputPerMTok != 2 {
		t.Fatalf("provider/model = %+v %v", p, ok)
	}
	if _, ok := lookup("anthropic/claude-x"); ok {
		t.Fatal("another provider's model took the key's price")
	}

	two := priceKeys(t,
		keystore.Put{Provider: "openai", Price: &keystore.Price{InputPerMTok: 2, OutputPerMTok: 10}},
		keystore.Put{Provider: "groq", Price: &keystore.Price{InputPerMTok: 1, OutputPerMTok: 1}})
	if _, ok := KeyPriceLookup(context.Background(), two, "o1")("gpt-6.2-sol"); ok {
		t.Fatal("two priced keys with no model list: a bare model must not take either price")
	}
}

// TestKeyPriceFallsBackToPriceList: a model no key prices takes the
// server's price list; a key price still wins over the list.
func TestKeyPriceFallsBackToPriceList(t *testing.T) {
	withPriceList(t, map[string]evidence.ModelPrice{
		"gpt-6.2-sol": {InputPerMTok: 2, OutputPerMTok: 10, CacheReadPerMTok: 0.1},
		"gpt-5-mini":  {InputPerMTok: 0.25, OutputPerMTok: 2},
	})
	lookup := KeyPriceLookup(context.Background(), nil, "o1")
	if lookup == nil {
		t.Fatal("no lookup without keys although the price list is set")
	}
	if p, ok := lookup("gpt-6.2-sol"); !ok || p.OutputPerMTok != 10 {
		t.Fatalf("price list = %+v %v", p, ok)
	}
	if _, ok := lookup("nope"); ok {
		t.Fatal("unknown model priced")
	}
	ks := priceKeys(t, keystore.Put{Provider: "openai", Models: []string{"gpt-5-mini"},
		Price: &keystore.Price{InputPerMTok: 9, OutputPerMTok: 9}})
	lookup = KeyPriceLookup(context.Background(), ks, "o1")
	if p, ok := lookup("gpt-5-mini"); !ok || p.InputPerMTok != 9 {
		t.Fatalf("key price must win: %+v %v", p, ok)
	}
	if p, ok := lookup("gpt-6.2-sol"); !ok || p.InputPerMTok != 2 {
		t.Fatalf("listed key, other model: price list = %+v %v", p, ok)
	}
}
