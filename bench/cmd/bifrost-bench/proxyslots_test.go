package main

import (
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/lib"
)

// TestProxySlots: --openai-slots adds openai slots (replacing the default
// oa-slot-1..4 in the proxy); empty keeps the default.
func TestProxySlots(t *testing.T) {
	got := proxySlots("fp-slot-1, fp-slot-2", "")
	want := []faultproxy.SlotConfig{{Name: "fp-slot-1", Auth: "fp-slot-1"}, {Name: "fp-slot-2", Auth: "fp-slot-2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	got = proxySlots("fp-slot-1", "oa-slot-1,oa-slot-5")
	want = []faultproxy.SlotConfig{{Name: "fp-slot-1", Auth: "fp-slot-1"},
		{Name: "oa-slot-1", Auth: "oa-slot-1", Provider: "openai"}, {Name: "oa-slot-5", Auth: "oa-slot-5", Provider: "openai"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

// TestProviderSetupsOpenAISlots: the bench's openai slots follow
// gateway.openai_key_slots, default oa-slot-1..4. Before, they were fixed at
// 4, so a 5-member team could never run on an openai model.
func TestProviderSetupsOpenAISlots(t *testing.T) {
	names := func(cfg *lib.Config) []string {
		for _, p := range providerSetups(cfg) {
			if p.Name == "openai" {
				var out []string
				for _, s := range p.Slots {
					out = append(out, s.Name)
				}
				return out
			}
		}
		return nil
	}
	if got := names(&lib.Config{}); !reflect.DeepEqual(got, []string{"oa-slot-1", "oa-slot-2", "oa-slot-3", "oa-slot-4"}) {
		t.Fatalf("default %v", got)
	}
	six := []string{"oa-slot-1", "oa-slot-2", "oa-slot-3", "oa-slot-4", "oa-slot-5", "oa-slot-6"}
	if got := names(&lib.Config{Gateway: lib.GatewayConfig{OpenAIKeySlots: six}}); !reflect.DeepEqual(got, six) {
		t.Fatalf("configured %v", got)
	}
}
