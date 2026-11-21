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
const orgSlotsPerProvider = 8 // a team agent takes one per member; 4 starved it behind single runs

var orgIDSafe = regexp.MustCompile(`[^a-z0-9]+`)

// orgCreds is the gateway's organization key state.
type orgCreds struct {
	mu      sync.Mutex
	byOrg   map[string]map[string]ProviderSetup // org -> provider -> its org slots
	expOrg  map[string]string                   // experiment id -> org
	slotKey map[string]string                   // org slot name -> keystore key id behind it (G10 budgets)
	next    map[string]int                      // org+provider -> round robin start of the next run's slot search
}

var orgInitMu sync.Mutex

// orgs returns the gateway's organization key state, created on first use.
// It takes its own lock, never g.mu, so callers holding g.mu may use it.
func (g *Gateway) orgs() *orgCreds {
	orgInitMu.Lock()
	defer orgInitMu.Unlock()
	if g.orgSt == nil {
		g.orgSt = &orgCreds{byOrg: map[string]map[string]ProviderSetup{}, expOrg: map[string]string{},
			slotKey: map[string]string{}, next: map[string]int{}}
	}
	return g.orgSt
}

// bifrostProvider maps a key store provider id to the Bifrost provider name
// (schemas.ResolveBifrostProvider: openai-compatible is openai, google is
// gemini, a custom slug stays itself).
func bifrostProvider(p string) string {
	name, _, _ := schemas.ResolveBifrostProvider(p)
	return name
}

// OrgSlotName is the Bifrost key name of an organization slot.
func OrgSlotName(orgID, provider string, n int) string {
	return fmt.Sprintf("org_%s_%s_%d", orgIDSafe.ReplaceAllString(strings.ToLower(orgID), ""), orgIDSafe.ReplaceAllString(provider, ""), n)
}

// ApplyOrgCredentials registers the organization's keys: placeholder slots
// in Bifrost, the real keys at the fault proxy. Every key of a provider is
// used: the provider gets max(orgSlotsPerProvider, keys) slots and slot n is
// backed by key (n-1) mod keys, so concurrent runs spread over the keys
// round robin (G10). It is idempotent and cheap to call per lease.
func (g *Gateway) ApplyOrgCredentials(ctx context.Context, orgID string, creds []ProviderCredential) error {
	if strings.TrimSpace(orgID) == "" || orgIDSafe.ReplaceAllString(strings.ToLower(orgID), "") == "" {
		return errors.New("org credentials: empty organization id")
	}
	if len(creds) == 0 {
		return nil
	}
	reg, _ := g.cfg.Claims.(OrgSlotRegistrar)
	byProvider := map[string][]ProviderCredential{}
	order := []string{}
	seen := map[string]bool{}
	for _, c := range creds {
		p, spec, _ := schemas.ResolveBifrostProvider(c.Provider)
		v := strings.TrimSpace(c.Value)
		// A keyless self-hosted server (ollama, vllm, sgl) may have no key;
		// it is told apart by its base URL.
		id := p + "\x00" + v + "\x00" + c.BaseURL
		if p == "" || (v == "" && !spec.Keyless) || seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := byProvider[p]; !ok {
			order = append(order, p)
		}
		byProvider[p] = append(byProvider[p], c)
	}
	needProxy := false
	for _, p := range order {
		if _, spec, _ := schemas.ResolveBifrostProvider(byProvider[p][0].Provider); spec.Route != schemas.RouteDirect {
			needProxy = true
		}
	}
	if needProxy && (reg == nil || g.cfg.Claims == nil || g.cfg.FaultProxyURL == "") {
		return errors.New("org credentials need the fault proxy control API on this runner (BENCH_FAULTPROXY_CONTROL_URL and its token)")
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

func (g *Gateway) applyOrgProvider(ctx context.Context, reg OrgSlotRegistrar, st *orgCreds, orgID, provider string, keys []ProviderCredential) error {
	model := ""
	for _, c := range keys {
		if len(c.Models) > 0 {
			model = c.Models[0]
			break
		}
	}
	if model == "" {
		if shared, ok := g.provider(provider); ok {
			model = shared.Model
		}
	}
	n := max(g.orgSlotsPer(), len(keys))
	_, spec, custom := schemas.ResolveBifrostProvider(keys[0].Provider)
	direct := spec.Route == schemas.RouteDirect
	setup := ProviderSetup{Name: provider, Mode: ModeProxied, Model: model, Custom: custom}
	if direct {
		setup.Mode = ModeDirect
	}
	fp := make([]OrgSlot, 0, n)
	keyOf := make(map[string]string, n)
	for i := 1; i <= n; i++ {
		name := OrgSlotName(orgID, provider, i)
		c := keys[(i-1)%len(keys)]
		slot, fpSlot := orgSlotFor(spec, name, provider, c, g.cfg.FaultProxyURL, direct)
		setup.Slots = append(setup.Slots, slot)
		if !direct {
			fp = append(fp, fpSlot)
		}
		keyOf[name] = c.KeyID
	}
	// The fault proxy first: a slot Bifrost could route to must never reach
	// the proxy unknown. Registering again is harmless, survives a proxy
	// restart, and swaps in a changed key set. A direct provider (AWS,
	// Google OAuth, token exchange) never passes the proxy.
	if len(fp) > 0 {
		if err := reg.RegisterOrgSlots(ctx, fp); err != nil {
			return fmt.Errorf("register keys at the fault proxy: %w", err)
		}
	}
	st.mu.Lock()
	prev, known := st.byOrg[orgID][provider]
	st.mu.Unlock()
	if !known || len(prev.Slots) != len(setup.Slots) || prev.Model != setup.Model {
		if _, shared := g.provider(provider); !shared && !known {
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
	for name, id := range keyOf {
		st.slotKey[name] = id
	}
	st.mu.Unlock()
	g.logger.Info("organization provider keys registered", "org", orgID, "provider", provider, "keys", len(keys), "slots", len(setup.Slots))
	return nil
}

// orgSlotFor builds one organization slot for Bifrost and, for a proxied
// provider, its fault proxy registration (lane A09).
//
// Proxied: Bifrost's key value is the slot name (a placeholder) and its key
// config points at the fault proxy where the provider reads its endpoint
// from the key (azure); the proxy gets the real key and the real base URL
// (the key's own base URL: custom, azure endpoint, self-hosted server).
// Non-secret key config (cloudflare account_id) goes to Bifrost as is.
//
// Direct: Bifrost gets the real credential and key config (AWS keys and
// region, Vertex service account, Databricks workspace and token, Copilot
// token), because these providers sign requests a proxy cannot rewrite.
func orgSlotFor(spec schemas.BifrostProviderSpec, name, provider string, c ProviderCredential, faultProxyURL string, direct bool) (Slot, OrgSlot) {
	fields := schemas.CredentialFields(spec, c.Value)
	if direct {
		block, cfg, value := schemas.BifrostKeyConfig(spec, fields, c.BaseURL, "")
		s := Slot{Name: name, Value: value}
		if block != "" {
			s.Extra = map[string]any{block: cfg}
		}
		return s, OrgSlot{}
	}
	block, cfg, key := schemas.BifrostKeyConfig(spec, fields, c.BaseURL, faultProxyURL)
	s := Slot{Name: name, Value: name}
	if block != "" {
		s.Extra = map[string]any{block: cfg}
	}
	return s, OrgSlot{Name: name, Provider: proxyProvider(provider, spec), Auth: name, Key: key, BaseURL: c.BaseURL}
}

// proxyProvider is the fault proxy provider entry a slot uses: a custom
// OpenAI-compatible provider rides the openai entry (same paths and auth)
// with its own base URL.
func proxyProvider(provider string, spec schemas.BifrostProviderSpec) string {
	if spec.Custom {
		return "openai"
	}
	return provider
}

// orgSlotKeyID is the keystore id of the organization key behind slot, ""
// for a shared slot.
func (g *Gateway) orgSlotKeyID(slot string) string {
	if !strings.HasPrefix(slot, "org_") {
		return ""
	}
	st := g.orgs()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.slotKey[slot]
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
	prov := bifrostProvider(name)
	p, ok := st.byOrg[org][prov]
	if !ok || len(p.Slots) < 2 {
		return p, ok
	}
	// G10 round robin: each run starts its free-slot search one slot later,
	// so sequential runs also spread over the organization's keys.
	rr := org + "\x00" + prov
	start := st.next[rr] % len(p.Slots)
	st.next[rr] = start + 1
	rot := make([]Slot, 0, len(p.Slots))
	rot = append(rot, p.Slots[start:]...)
	rot = append(rot, p.Slots[:start]...)
	p.Slots = rot
	return p, true
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

// orgSlotsPer is the configured slots per organization provider
// (Config.OrgSlotsPerProvider), orgSlotsPerProvider when unset.
func (g *Gateway) orgSlotsPer() int {
	if g.cfg.OrgSlotsPerProvider > 0 {
		return g.cfg.OrgSlotsPerProvider
	}
	return orgSlotsPerProvider
}
