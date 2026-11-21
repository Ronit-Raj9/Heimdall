package bifrost

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

// F4 key delivery (lane L05). A hosted runner receives its organization's
// provider keys per lease and calls ApplyOrgCredentials. For each provider
// the organization has a key for, the gateway registers orgSlotsPerProvider
// key slots named org_<org>_<provider>_<n> in Bifrost. Their values are
// placeholders, exactly like the shared slots: the real key is handed only to
// the fault proxy (RegisterOrgSlot), which swaps it in upstream. So every
// call still goes agent, Bifrost, fault proxy, provider; the ledger and
// model faults work as for the shared slots, and Bifrost never stores an
// organization's key. BindExperimentOrg then makes MintKey (agents, judges,
// simulated users and voice services all mint through it) pick the
// organization's slots for that experiment's runs.

// ProviderCredential is one organization provider key.
type ProviderCredential = schemas.ProviderCredential

// OrgSlot is an organization key slot as the fault proxy registers it.
type OrgSlot = faultproxy.OrgSlot

// OrgSlotRegistrar is the fault proxy control API that holds organization
// keys (faultproxy.Client). The gateway's Claims client implements it.
type OrgSlotRegistrar interface {
	RegisterOrgSlots(ctx context.Context, slots []OrgSlot) error
}

// orgSlotsPerProvider is how many concurrent runs of one organization can
// use one provider on this runner.
const orgSlotsPerProvider = 4

var orgIDSafe = regexp.MustCompile(`[^a-z0-9]+`)

// orgCreds is the gateway's organization key state.
type orgCreds struct {
	mu     sync.Mutex
	byOrg  map[string]map[string]ProviderSetup // org -> provider -> its org slots
	expOrg map[string]string                   // experiment id -> org
}

var orgInitMu sync.Mutex

// orgs returns the gateway's organization key state, created on first use.
// It takes its own lock, never g.mu, so callers holding g.mu may use it.
func (g *Gateway) orgs() *orgCreds {
	orgInitMu.Lock()
	defer orgInitMu.Unlock()
	if g.orgSt == nil {
		g.orgSt = &orgCreds{byOrg: map[string]map[string]ProviderSetup{}, expOrg: map[string]string{}}
	}
	return g.orgSt
}

// bifrostProvider maps a key store provider id to the Bifrost provider name.
func bifrostProvider(p string) string {
	switch p = strings.ToLower(strings.TrimSpace(p)); p {
	case "openai-compatible":
		return "openai"
	case "google":
		return "gemini"
	}
	return p
}

// OrgSlotName is the Bifrost key name of an organization slot.
func OrgSlotName(orgID, provider string, n int) string {
	return fmt.Sprintf("org_%s_%s_%d", orgIDSafe.ReplaceAllString(strings.ToLower(orgID), ""), orgIDSafe.ReplaceAllString(provider, ""), n)
}

// ApplyOrgCredentials registers the organization's keys: placeholder slots
// in Bifrost, the real keys at the fault proxy. One key per provider is used
// (the first given). It is idempotent and cheap to call per lease.
func (g *Gateway) ApplyOrgCredentials(ctx context.Context, orgID string, creds []ProviderCredential) error {
	if strings.TrimSpace(orgID) == "" || orgIDSafe.ReplaceAllString(strings.ToLower(orgID), "") == "" {
		return errors.New("org credentials: empty organization id")
	}
	if len(creds) == 0 {
		return nil
	}
	reg, ok := g.cfg.Claims.(OrgSlotRegistrar)
	if !ok || g.cfg.Claims == nil || g.cfg.FaultProxyURL == "" {
		return errors.New("org credentials need the fault proxy control API on this runner (BENCH_FAULTPROXY_CONTROL_URL and its token)")
	}
	byProvider := map[string]ProviderCredential{}
	order := []string{}
	for _, c := range creds {
		p := bifrostProvider(c.Provider)
		if p == "" || strings.TrimSpace(c.Value) == "" {
			continue
		}
		if _, dup := byProvider[p]; !dup {
			byProvider[p] = c
			order = append(order, p)
		}
	}
	st := g.orgs()
	var errs []error
	for _, p := range order {
		if err := g.applyOrgProvider(ctx, reg, st, orgID, p, byProvider[p]); err != nil {
			errs = append(errs, fmt.Errorf("provider %s: %w", p, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("org credentials for %s: %w", orgID, errors.Join(errs...))
	}
	return nil
}

func (g *Gateway) applyOrgProvider(ctx context.Context, reg OrgSlotRegistrar, st *orgCreds, orgID, provider string, c ProviderCredential) error {
	model := ""
	if len(c.Models) > 0 {
		model = c.Models[0]
	} else if shared, ok := g.provider(provider); ok {
		model = shared.Model
	}
	setup := ProviderSetup{Name: provider, Mode: ModeProxied, Model: model}
	fp := make([]OrgSlot, 0, orgSlotsPerProvider)
	for i := 1; i <= orgSlotsPerProvider; i++ {
		name := OrgSlotName(orgID, provider, i)
		setup.Slots = append(setup.Slots, Slot{Name: name, Value: name})
		fp = append(fp, OrgSlot{Name: name, Provider: provider, Auth: name, Key: c.Value, BaseURL: c.BaseURL})
	}
	// The fault proxy first: a slot Bifrost could route to must never reach
	// the proxy unknown. Registering again is harmless and survives a proxy
	// restart.
	if err := reg.RegisterOrgSlots(ctx, fp); err != nil {
		return fmt.Errorf("register keys at the fault proxy: %w", err)
	}
	st.mu.Lock()
	_, known := st.byOrg[orgID][provider]
	st.mu.Unlock()
	if !known {
		if _, shared := g.provider(provider); !shared {
			if err := g.ensureProvider(ctx, setup); err != nil {
				return err
			}
		}
		if err := g.upsertOrgSlots(ctx, setup); err != nil {
			return err
		}
	}
	st.mu.Lock()
	if st.byOrg[orgID] == nil {
		st.byOrg[orgID] = map[string]ProviderSetup{}
	}
	st.byOrg[orgID][provider] = setup
	st.mu.Unlock()
	g.logger.Info("organization provider keys registered", "org", orgID, "provider", provider, "slots", len(setup.Slots))
	return nil
}

// upsertOrgSlots creates or updates the organization's placeholder slots in
// Bifrost and records their key ids, as Setup does for the shared slots.
func (g *Gateway) upsertOrgSlots(ctx context.Context, p ProviderSetup) error {
	var existing struct {
		Keys []providerKey `json:"keys"`
	}
	if err := g.call(ctx, "GET", providerPath(p.Name)+"/keys", nil, &existing); err != nil {
		return fmt.Errorf("list %s keys: %w", p.Name, err)
	}
	byName := make(map[string]providerKey, len(existing.Keys))
	for _, k := range existing.Keys {
		byName[k.Name] = k
	}
	for _, s := range p.Slots {
		id, models, err := g.upsertKey(ctx, p.Name, s, byName[s.Name].ID, mergeModels(byName[s.Name].Models, p.Model))
		if err != nil {
			return err
		}
		g.mu.Lock()
		g.keyIDs[s.Name] = id
		g.models[s.Name] = toSet(models)
		g.mu.Unlock()
	}
	return nil
}

// BindExperimentOrg routes the experiment's run keys to the organization's
// slots for every provider the organization registered.
func (g *Gateway) BindExperimentOrg(expID, orgID string) {
	if expID == "" || orgID == "" {
		return
	}
	st := g.orgs()
	st.mu.Lock()
	st.expOrg[expID] = orgID
	st.mu.Unlock()
}

// orgProvider returns the organization slots a run of rc uses for provider
// name, when its experiment is bound to an organization with a key for it.
func (g *Gateway) orgProvider(rc *schemas.RunContext, name string) (ProviderSetup, bool) {
	if rc == nil || rc.Run == nil || rc.Run.ExpID == "" {
		return ProviderSetup{}, false
	}
	st := g.orgs()
	st.mu.Lock()
	defer st.mu.Unlock()
	org, ok := st.expOrg[rc.Run.ExpID]
	if !ok {
		return ProviderSetup{}, false
	}
	p, ok := st.byOrg[org][bifrostProvider(name)]
	return p, ok
}

// orgSlotProvider returns the organization provider that owns slot.
func (g *Gateway) orgSlotProvider(slot string) (ProviderSetup, bool) {
	if !strings.HasPrefix(slot, "org_") {
		return ProviderSetup{}, false
	}
	st := g.orgs()
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, provs := range st.byOrg {
		for _, p := range provs {
			for _, s := range p.Slots {
				if s.Name == slot {
					return p, true
				}
			}
		}
	}
	return ProviderSetup{}, false
}
