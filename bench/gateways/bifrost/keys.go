package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// vkPageSize is the page size for listing virtual keys.
const vkPageSize = 100

// runIDPattern matches the virtual key names MintKey creates: run ids from
// utils.NewID("r", n). Keys named otherwise are never touched.
var runIDPattern = regexp.MustCompile(`^r_[a-z0-9]+$`)

// MintKey implements schemas.Gateway. It takes a free key slot of the run's
// provider (rc.Agent.Gateway.Provider; anthropic when empty and configured,
// else the first configured provider) and creates the run's virtual key,
// named after the run id: one provider, that one slot,
// the run's model, weight 1 (without a weight a plain model id does not
// route). The engine never runs more slots than the pool holds, so an empty
// pool is an error, not a wait.
func (g *Gateway) MintKey(ctx context.Context, rc *schemas.RunContext) (*schemas.RunKey, error) {
	if rc == nil || rc.Run == nil || rc.Run.RunID == "" {
		return nil, errors.New("mint key: run context has no run id")
	}
	runID := rc.Run.RunID
	if !runIDPattern.MatchString(runID) {
		return nil, fmt.Errorf("mint key for run %s: run id does not match %s", runID, runIDPattern)
	}
	p, model, err := g.runProvider(rc)
	if err != nil {
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	slot, keyID, err := g.acquireSlot(p, runID)
	if err != nil {
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	var routing *schemas.RoutingPolicy // W9 routing policy under test (routing.go)
	var extra []string                 // X8 voice agents: the agent's declared extra models (gateway.models)
	if rc.Agent != nil {
		routing = rc.Agent.Gateway.Routing
		extra = rc.Agent.Gateway.Models
	}
	key, err := g.createVirtualKey(ctx, runID, p, slot, keyID, model, routing, extra)
	if err != nil {
		g.releaseSlot(slot.Name, runID)
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	if err := g.applyRouteRule(ctx, key, p.Name, routing); err != nil { // W9: the run's VK-scoped routing rule
		if derr := g.deleteVirtualKey(ctx, key.ID); derr != nil {
			err = errors.Join(err, fmt.Errorf("delete virtual key %s: %w", key.ID, derr))
		}
		g.releaseSlot(slot.Name, runID)
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	g.mu.Lock()
	g.inUse[slot.Name] = key.ID
	g.minted[key.ID] = mintRecord{slot: slot.Name, at: time.Now()}
	g.mu.Unlock()
	g.logger.Info("virtual key minted", "run_id", runID, "virtual_key_id", key.ID, "provider", p.Name,
		"mode", p.Mode, "key_slot", slot.Name, "model", model)
	return key, nil
}

// RevokeKey implements schemas.Gateway. A key that is already gone counts as
// revoked. The slot is released only when this process minted the key, so a
// stale RunKey can never free a slot another run holds.
func (g *Gateway) RevokeKey(ctx context.Context, key *schemas.RunKey) error {
	if key == nil || key.ID == "" {
		return nil
	}
	var ruleErr error
	if key.RoutingRuleID != "" { // W9: the run's routing rule goes first, so no rule outlives its key
		ruleErr = g.deleteRouteRule(ctx, key.RoutingRuleID) // on failure the key still goes; the janitor deletes the orphan rule
	}
	if err := g.deleteVirtualKey(ctx, key.ID); err != nil {
		return fmt.Errorf("revoke virtual key %s (%s): %w", key.ID, key.Name, errors.Join(err, ruleErr))
	}
	g.forget(key.ID)
	g.logger.Info("virtual key revoked", "virtual_key_id", key.ID, "name", key.Name, "key_slot", key.KeySlot)
	if ruleErr != nil {
		return fmt.Errorf("revoke virtual key %s (%s): %w", key.ID, key.Name, ruleErr)
	}
	return nil
}

// RevokeStale implements schemas.Gateway. It deletes every virtual key whose
// name looks like a run id and whose run is not in active, and returns how
// many it deleted.
func (g *Gateway) RevokeStale(ctx context.Context, active map[string]bool) (int, error) {
	var errs []error
	if _, err := g.revokeStaleRules(ctx, active); err != nil { // W9: stale run routing rules first (routing.go)
		errs = append(errs, fmt.Errorf("revoke stale routing rules: %w", err))
	}
	keys, err := g.listVirtualKeys(ctx)
	if err != nil {
		return 0, errors.Join(append(errs, fmt.Errorf("revoke stale keys: %w", err))...)
	}
	n := 0
	for _, k := range keys {
		if !runIDPattern.MatchString(k.Name) || active[k.Name] || active[ownerRun(k.Name)] { // W3: a service or judge key of a live run stays
			continue
		}
		if err := g.deleteVirtualKey(ctx, k.ID); err != nil {
			errs = append(errs, fmt.Errorf("revoke stale key %s (%s): %w", k.ID, k.Name, err))
			continue
		}
		g.forget(k.ID)
		n++
		g.logger.Info("stale virtual key revoked", "virtual_key_id", k.ID, "name", k.Name)
	}
	return n, errors.Join(errs...)
}

// virtualKey is a virtual key as Bifrost returns it.
type virtualKey struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// runProvider is the run's provider and the model its key allows: the
// agent config's model, or the provider's setup model when the config names
// none. A provider the gateway does not configure is refused.
func (g *Gateway) runProvider(rc *schemas.RunContext) (ProviderSetup, string, error) {
	name := ""
	if rc.Agent != nil {
		name = rc.Agent.Gateway.Provider
	}
	if name == "" {
		name = g.providers[0].Name
		if _, ok := g.provider(DefaultProvider); ok {
			name = DefaultProvider
		}
	}
	p, ok := g.provider(name)
	if !ok {
		return ProviderSetup{}, "", fmt.Errorf("agent provider %s is not the gateway provider (configured: %s)", name, g.providerNames())
	}
	model := p.Model
	if rc.Agent != nil && rc.Agent.Model != "" {
		model = rc.Agent.Model
	}
	return p, model, nil
}

func (g *Gateway) providerNames() string {
	names := make([]string, 0, len(g.providers))
	for _, p := range g.providers {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

// acquireSlot reserves the provider's first free slot in config order for
// runID.
func (g *Gateway) acquireSlot(p ProviderSetup, runID string) (Slot, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	busy := 0
	setUp := false
	for _, s := range p.Slots {
		if _, used := g.inUse[s.Name]; used {
			busy++
			continue
		}
		id, ok := g.keyIDs[s.Name]
		if !ok {
			continue
		}
		setUp = true
		g.inUse[s.Name] = "pending:" + runID
		return s, id, nil
	}
	if busy == 0 && !setUp {
		return Slot{}, "", errors.New("gateway not set up: Setup has not run")
	}
	return Slot{}, "", fmt.Errorf("no free key slot for %s: %d of %d in use", p.Name, busy, len(p.Slots))
}

// releaseSlot frees slot if runID's mint still holds it as pending.
func (g *Gateway) releaseSlot(slot, runID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inUse[slot] == "pending:"+runID {
		delete(g.inUse, slot)
	}
}

// forget drops a minted key and frees its slot.
func (g *Gateway) forget(vkID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.minted[vkID]
	if !ok {
		return
	}
	delete(g.minted, vkID)
	if g.inUse[rec.slot] == vkID {
		delete(g.inUse, rec.slot)
	}
}

// createVirtualKey makes sure the slot's provider key serves model, then
// creates the run's virtual key on that slot.
func (g *Gateway) createVirtualKey(ctx context.Context, runID string, p ProviderSetup, slot Slot, keyID, model string, routing *schemas.RoutingPolicy, extra []string) (*schemas.RunKey, error) {
	if err := g.ensureSlotModel(ctx, p, slot, keyID, model); err != nil {
		return nil, err
	}
	for _, m := range extra { // X8 voice agents: declared extra models on the slot
		if err := g.ensureSlotModel(ctx, p, slot, keyID, m); err != nil {
			return nil, err
		}
	}
	body := map[string]any{
		"name": runID,
		"provider_configs": []map[string]any{{
			"provider":       p.Name,
			"weight":         1,
			"allowed_models": []string{model},
			"key_ids":        []string{keyID},
		}},
		"is_active": true,
	}
	if routing != nil { // W9: every policy model on the slot and the key, plus its limits (routing.go)
		var err error
		if body, err = routedKeyBody(runID, p.Name, keyID, routing); err != nil {
			return nil, err
		}
		for _, m := range routing.Models() {
			if err := g.ensureSlotModel(ctx, p, slot, keyID, m); err != nil {
				return nil, err
			}
		}
	}
	addAllowedModels(body, extra) // X8 voice agents: the declared extra models on the key
	var out struct {
		VirtualKey virtualKey `json:"virtual_key"`
	}
	if err := g.call(ctx, http.MethodPost, "/api/governance/virtual-keys", body, &out); err != nil {
		return nil, fmt.Errorf("create virtual key: %w", err)
	}
	vk := out.VirtualKey
	if vk.ID == "" || vk.Value == "" {
		return nil, errors.New("create virtual key: bifrost returned no id or value")
	}
	key := &schemas.RunKey{ID: vk.ID, Name: vk.Name, Value: vk.Value, KeySlot: slot.Name, Provider: p.Name, Model: model}
	if p.Mode == ModeProxied {
		key.SlotAuth = slot.Value // a placeholder; a direct slot's value is the real key and stays in Bifrost
	}
	return key, nil
}

// ensureSlotModel adds model to the slot's provider key when an arm runs a
// model other than the gateway's. A key with an empty model list serves
// nothing on v2.5.1 ("no keys found that support model"), so the list is
// always explicit.
func (g *Gateway) ensureSlotModel(ctx context.Context, p ProviderSetup, slot Slot, keyID, model string) error {
	g.mu.Lock()
	have := g.models[slot.Name][model]
	current := make([]string, 0, len(g.models[slot.Name]))
	for m := range g.models[slot.Name] {
		current = append(current, m)
	}
	g.mu.Unlock()
	if have {
		return nil
	}
	sort.Strings(current)
	models := mergeModels(current, model)
	if p.Mode == ModeDirect {
		if slot.Value = strings.TrimSpace(g.cfg.Getenv(p.KeyEnv)); slot.Value == "" {
			return fmt.Errorf("direct provider %s: %s is not set", p.Name, p.KeyEnv)
		}
	}
	if _, err := g.upsertKey(ctx, p.Name, slot, keyID, models); err != nil {
		return err
	}
	g.mu.Lock()
	g.models[slot.Name] = toSet(models)
	g.mu.Unlock()
	return nil
}

func (g *Gateway) deleteVirtualKey(ctx context.Context, id string) error {
	err := g.call(ctx, http.MethodDelete, "/api/governance/virtual-keys/"+url.PathEscape(id), nil, nil)
	if err == nil || isStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// listVirtualKeys pages through every virtual key.
func (g *Gateway) listVirtualKeys(ctx context.Context) ([]virtualKey, error) {
	var all []virtualKey
	for offset := 0; ; offset += vkPageSize {
		q := url.Values{"limit": {strconv.Itoa(vkPageSize)}, "offset": {strconv.Itoa(offset)}}
		var page struct {
			VirtualKeys []virtualKey `json:"virtual_keys"`
		}
		if err := g.call(ctx, http.MethodGet, "/api/governance/virtual-keys?"+q.Encode(), nil, &page); err != nil {
			return nil, fmt.Errorf("list virtual keys: %w", err)
		}
		all = append(all, page.VirtualKeys...)
		if len(page.VirtualKeys) < vkPageSize {
			return all, nil
		}
	}
}

// ownerRun is the run a derived key name belongs to (W3 customer support):
// a judge's or a service's key is the run id plus one letter and a 6
// character suffix (graders/judge.go, environments/servicegateway.go), so a
// sweep keeps it while its run is live. "" for a name too short to be one.
func ownerRun(name string) string {
	const derived = 7
	if len(name) <= len("r_")+derived {
		return ""
	}
	return name[:len(name)-derived]
}

// addAllowedModels appends models the key body does not allow yet to its
// first provider config (X8 voice agents: gateway.models).
func addAllowedModels(body map[string]any, models []string) {
	if len(models) == 0 {
		return
	}
	pcs, ok := body["provider_configs"].([]map[string]any)
	if !ok || len(pcs) == 0 {
		return
	}
	have, _ := pcs[0]["allowed_models"].([]string)
	seen := map[string]bool{}
	for _, m := range have {
		seen[m] = true
	}
	out := append([]string(nil), have...)
	for _, m := range models {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	pcs[0]["allowed_models"] = out
}
