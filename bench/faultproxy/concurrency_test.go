package faultproxy

import (
	"path/filepath"
	"testing"
	"time"
)

// TestConcurrencyPerProvider is E8: slots per provider scale with
// Config.SlotsPerProvider and live claims are counted per provider.
func TestConcurrencyPerProvider(t *testing.T) {
	srv, err := NewServer(Config{LedgerPath: filepath.Join(t.TempDir(), "l.jsonl"), Upstream: UpstreamScripted, SlotsPerProvider: 32})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	got, _, _ := srv.plans.claim("owner-token-0123456789", []string{"fp-slot-1", "fp-slot-2", "fp-slot-3"}, 2, time.Minute, "r1", "")
	if len(got) != 2 {
		t.Fatalf("claimed %v", got)
	}
	by := map[string]ProviderConcurrency{}
	for _, c := range srv.Concurrency() {
		by[c.Provider] = c
	}
	if a := by[ProviderAnthropic]; a.Slots != 32 || a.Claimed != 2 {
		t.Fatalf("anthropic = %+v", a)
	}
	if o := by[ProviderOpenAI]; o.Slots != 32 || o.Claimed != 0 {
		t.Fatalf("openai = %+v", o)
	}
	if !srv.knownSlot("oa-slot-32") || srv.knownSlot("oa-slot-33") {
		t.Fatal("openai slots are not oa-slot-1..32")
	}
}

func TestConcurrencyDefault24(t *testing.T) {
	srv, err := NewServer(Config{LedgerPath: filepath.Join(t.TempDir(), "l.jsonl"), Upstream: UpstreamScripted})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	for _, c := range srv.Concurrency() {
		if c.Slots != 24 {
			t.Fatalf("%s has %d slots, want the default 24", c.Provider, c.Slots)
		}
	}
}
