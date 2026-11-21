package faultproxy

import "sort"

// ProviderConcurrency is how many runs can use one provider through the
// proxy at once (its slots) and how many slots are claimed right now. Org
// counts the slots of organizations' own keys, which are separate from the
// shared slots.
type ProviderConcurrency struct {
	Provider string `json:"provider"`
	Slots    int    `json:"slots"`
	Claimed  int    `json:"claimed"`
	OrgSlots int    `json:"org_slots,omitempty"`
}

// Concurrency reports every provider's slots and live claims, sorted by
// provider name.
func (s *Server) Concurrency() []ProviderConcurrency {
	by := map[string]*ProviderConcurrency{}
	get := func(p string) *ProviderConcurrency {
		if p == "" {
			p = ProviderAnthropic
		}
		c, ok := by[p]
		if !ok {
			c = &ProviderConcurrency{Provider: p}
			by[p] = c
		}
		return c
	}
	provOf := map[string]string{}
	for _, ref := range s.slots {
		if _, seen := provOf[ref.name]; seen {
			continue
		}
		provOf[ref.name] = ref.provider
		get(ref.provider).Slots++
	}
	s.org.mu.RLock()
	for name, e := range s.org.byName {
		provOf[name] = e.ref.provider
		get(e.ref.provider).OrgSlots++
	}
	s.org.mu.RUnlock()
	for _, c := range s.plans.listClaims() {
		if p, ok := provOf[c.Slot]; ok {
			get(p).Claimed++
		}
	}
	out := make([]ProviderConcurrency, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}
