package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
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
	slot, keyID, err := g.acquireRunSlots(ctx, p, runID, teamSlots(rc)) // Multi-agent area: a team takes all its slots at once (teamslots.go)
	if err != nil {
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	var routing *schemas.RoutingPolicy // W9 routing policy under test (routing.go)
	var models []string                // X8 voice agents: the agent's declared extra models (gateway.models)
	if rc.Agent != nil {
		routing = rc.Agent.Gateway.Routing
		models = rc.Agent.Gateway.Models
	}
	extra := map[string]any{}
	if usd := runBudget(rc); usd > 0 { // Multi-agent area: the equal_budget fairness rule enforces the run's cap here (team.go)
		if routing != nil && routing.Limits != nil && routing.Limits.BudgetUSD > 0 { // 3-A-09: never enforce a different budget than the routing treatment recorded
			g.releaseSlot(slot.Name, runID)
			g.releaseReserved(runID)
			return nil, fmt.Errorf("mint key for run %s: the routing policy's budget_usd %.2f and the fairness cap %.2f both limit the run key; the planner refuses this", runID, routing.Limits.BudgetUSD, usd)
		}
		extra["budgets"] = []map[string]any{{"max_limit": usd, "reset_duration": budgetReset}}
	}
	key, err := g.createVirtualKey(ctx, runID, p, slot, keyID, model, routing, models, extra)
	if err != nil {
		g.releaseSlot(slot.Name, runID)
		g.releaseReserved(runID)
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	if err := g.applyRouteRule(ctx, key, p.Name, routing); err != nil { // W9: the run's VK-scoped routing rule
		if derr := g.deleteVirtualKey(ctx, key.ID); derr != nil {
			err = errors.Join(err, fmt.Errorf("delete virtual key %s: %w", key.ID, derr))
		}
		g.releaseSlot(slot.Name, runID)
		g.releaseReserved(runID) // F1: a team's reserved slots (and their claims) go too
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	if !g.publishMint(slot.Name, "pending:"+runID, key, mintRecord{slot: slot.Name, at: time.Now(), run: runID}) {
		err := fmt.Errorf("the claim on key slot %s was lost while the key was created", slot.Name)
		if key.RoutingRuleID != "" {
			if derr := g.deleteRouteRule(ctx, key.RoutingRuleID); derr != nil {
				err = errors.Join(err, fmt.Errorf("delete routing rule %s: %w", key.RoutingRuleID, derr))
			}
		}
		if derr := g.deleteVirtualKey(ctx, key.ID); derr != nil {
			err = errors.Join(err, fmt.Errorf("delete virtual key %s: %w", key.ID, derr))
		}
		g.releaseSlot(slot.Name, runID)
		g.releaseReserved(runID)
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	g.logger.Info("virtual key minted", "run_id", runID, "virtual_key_id", key.ID, "provider", p.Name,
		"mode", p.Mode, "key_slot", slot.Name, "model", model)
	return key, nil
}

// RevokeKey implements schemas.Gateway. A key that is already gone counts as
// revoked. The slot is released only when this process minted the key, so a
// stale RunKey can never free a slot another run holds.
func (g *Gateway) RevokeKey(ctx context.Context, key *schemas.RunKey) error {
	return g.revokeKey(ctx, key, "")
}

// RevokeKeyHoldSlots implements schemas.SlotHoldingRevoker: it revokes like
// RevokeKey but keeps the key's slots, and their claims at the fault proxy,
// held as "draining" until ReleaseKeySlots. The engine clears the slots'
// fault plans in between, so no other run can take a slot and arm a plan
// that the clear would then refuse (409, another owner) or wipe (this
// process).
func (g *Gateway) RevokeKeyHoldSlots(ctx context.Context, key *schemas.RunKey) error {
	if key == nil || key.ID == "" {
		return nil
	}
	return g.revokeKey(ctx, key, key.Name)
}

// ReleaseKeySlots implements schemas.SlotHoldingRevoker: it frees every slot
// held for key's run (draining after RevokeKeyHoldSlots, or reserved and
// never used) and releases their claims. Slots still held by a key whose
// revocation failed stay held, as after a failed RevokeKey.
func (g *Gateway) ReleaseKeySlots(key *schemas.RunKey) {
	if key == nil || key.Name == "" {
		return
	}
	drop := map[string]string{}
	g.mu.Lock()
	for name, holder := range g.inUse {
		if holder == drainingPrefix+key.Name || holder == reservedPrefix+key.Name {
			delete(g.inUse, name)
			g.unclaimLocked(name, drop)
		}
	}
	g.mu.Unlock()
	g.releaseClaims(drop)
}

// revokeKey is RevokeKey; with hold set (the run's key name) the freed slots
// stay held as drainingPrefix+hold and reserved slots stay reserved, until
// ReleaseKeySlots.
func (g *Gateway) revokeKey(ctx context.Context, key *schemas.RunKey, hold string) error {
	if key == nil || key.ID == "" {
		return nil
	}
	if hold == "" {
		g.releaseReserved(key.Name)
	}
	// 3-A-02: every independent cleanup step runs and the errors are joined,
	// so one failed member or team DELETE never leaves the lead key, its
	// routing rule or its slot behind. The team id stays recorded until its
	// DELETE is confirmed, so a retry still knows what to delete.
	var errs []error
	teamID := g.teamFor(key.ID)
	membersErr := g.revokeMemberKeys(ctx, key, hold)
	if membersErr != nil {
		errs = append(errs, fmt.Errorf("revoke members of virtual key %s (%s): %w", key.ID, key.Name, membersErr))
	}
	if key.RoutingRuleID != "" { // W9: the run's routing rule goes first, so no rule outlives its key
		if err := g.deleteRouteRule(ctx, key.RoutingRuleID); err != nil { // on failure the key still goes; the janitor deletes the orphan rule
			errs = append(errs, fmt.Errorf("revoke virtual key %s (%s): %w", key.ID, key.Name, err))
		}
	}
	keyErr := g.deleteVirtualKey(ctx, key.ID)
	if keyErr != nil {
		errs = append(errs, fmt.Errorf("revoke virtual key %s (%s): %w", key.ID, key.Name, keyErr))
	} else {
		g.forgetHold(key.ID, hold)
		g.logger.Info("virtual key revoked", "virtual_key_id", key.ID, "name", key.Name, "key_slot", key.KeySlot)
	}
	if teamID != "" && membersErr == nil && keyErr == nil { // a team goes once the keys in it are gone
		if err := g.deleteTeam(ctx, teamID); err != nil {
			errs = append(errs, fmt.Errorf("revoke team of virtual key %s (%s): %w", key.ID, key.Name, err))
		} else {
			g.dropTeam(key.ID)
		}
	}
	return errors.Join(errs...)
}

// RevokeStale implements schemas.Gateway. It deletes every virtual key whose
// name looks like a run id and whose run is not in active, and returns how
// many it deleted. F1: a run that holds a live key slot claim at the fault
// proxy, by this process or another bench process, is kept as if active;
// when the claims cannot be read, nothing is revoked.
func (g *Gateway) RevokeStale(ctx context.Context, active map[string]bool) (int, error) {
	// 3-B-03: candidates are listed before the claims are read. A key minted
	// by another process is created after that process claimed its slot, so
	// every listed key whose run is live is covered by the later claim read.
	var errs []error
	rules, rulesErr := g.listRouteRules(ctx)
	if rulesErr != nil {
		errs = append(errs, fmt.Errorf("revoke stale routing rules: %w", rulesErr))
	}
	keys, keysErr := g.listVirtualKeys(ctx)
	teams, teamsErr := g.listTeams(ctx)      // 3-A-03
	agents, agentsErr := g.listA2ANames(ctx) // 3-A-04
	active, err := g.withClaimHolders(ctx, active)
	if err != nil {
		return 0, fmt.Errorf("revoke stale keys: nothing revoked, the key slot claims of other bench processes could not be read: %w", err)
	}
	if rulesErr == nil {
		if _, err := g.deleteStaleRules(ctx, rules, active); err != nil { // W9: stale run routing rules first (routing.go)
			errs = append(errs, fmt.Errorf("revoke stale routing rules: %w", err))
		}
	}
	if keysErr != nil {
		return 0, errors.Join(append(errs, fmt.Errorf("revoke stale keys: %w", keysErr))...)
	}
	n := 0
	keyLeft := map[string]bool{} // runs with a key that could not be deleted keep their team
	for _, k := range keys {
		run, ok := staleRun(k.Name)                         // Multi-agent area: member keys carry their run id (team.go)
		if !ok || active[run] || active[ownerRun(k.Name)] { // W3: a service or judge key of a live run stays
			continue
		}
		if err := g.deleteVirtualKey(ctx, k.ID); err != nil {
			errs = append(errs, fmt.Errorf("revoke stale key %s (%s): %w", k.ID, k.Name, err))
			keyLeft[run] = true
			continue
		}
		g.forget(k.ID)
		n++
		g.logger.Info("stale virtual key revoked", "virtual_key_id", k.ID, "name", k.Name)
	}
	errs = append(errs, g.removeStaleTeams(ctx, teams, teamsErr, active, keyLeft)...)
	errs = append(errs, g.removeStaleA2A(ctx, agents, agentsErr, active)...)
	return n, errors.Join(errs...)
}

// withClaimHolders returns active plus every run that holds a live key slot
// claim at the fault proxy (F1). Read it after listing the cleanup
// candidates (3-B-03). An error means liveness is unknown: nothing may go.
func (g *Gateway) withClaimHolders(ctx context.Context, active map[string]bool) (map[string]bool, error) {
	held, err := g.liveClaimRuns(ctx)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]bool, len(active)+len(held))
	for run, live := range active {
		merged[run] = live
	}
	for run := range held {
		merged[run] = true
	}
	return merged, nil
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
	p, ok := g.orgProvider(rc, name) // F4: the experiment's organization keys (orgcreds.go)
	if !ok {
		p, ok = g.provider(name)
	}
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

// releaseSlot frees slot if runID's mint still holds it as pending, and its
// claim at the fault proxy (F1).
func (g *Gateway) releaseSlot(slot, runID string) {
	drop := map[string]string{}
	g.mu.Lock()
	if g.inUse[slot] == "pending:"+runID {
		delete(g.inUse, slot)
		g.unclaimLocked(slot, drop)
	}
	g.mu.Unlock()
	g.releaseClaims(drop)
}

// forget drops a minted key and frees its slot and the slot's claim at the
// fault proxy (F1).
func (g *Gateway) forget(vkID string) {
	g.forgetHold(vkID, "")
}

// drainingPrefix marks a slot whose key was revoked by RevokeKeyHoldSlots:
// still held, with its claim, until ReleaseKeySlots frees it.
const drainingPrefix = "draining:"

// forgetHold is forget; with hold set the slot stays held as
// drainingPrefix+hold and keeps its claim.
func (g *Gateway) forgetHold(vkID, hold string) {
	drop := map[string]string{}
	g.mu.Lock()
	rec, ok := g.minted[vkID]
	if !ok {
		g.mu.Unlock()
		return
	}
	delete(g.minted, vkID)
	if g.inUse[rec.slot] == vkID {
		if hold != "" {
			g.inUse[rec.slot] = drainingPrefix + hold
		} else {
			delete(g.inUse, rec.slot)
			g.unclaimLocked(rec.slot, drop)
		}
	}
	g.mu.Unlock()
	g.releaseClaims(drop)
}

// createVirtualKey makes sure the slot's provider key serves model, then
// creates the run's virtual key on that slot.
func (g *Gateway) createVirtualKey(ctx context.Context, runID string, p ProviderSetup, slot Slot, keyID, model string, routing *schemas.RoutingPolicy, models []string, extra map[string]any) (*schemas.RunKey, error) {
	if err := g.ensureSlotModel(ctx, p, slot, keyID, model); err != nil {
		return nil, err
	}
	for _, m := range models { // X8 voice agents: declared extra models on the slot
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
		addAllowedModels(body, routing.EffectiveModels(model)) // a rule's unmatched requests keep the agent's model
		for _, m := range routing.EffectiveModels(model) {
			if err := g.ensureSlotModel(ctx, p, slot, keyID, m); err != nil {
				return nil, err
			}
		}
	}
	addAllowedModels(body, models) // X8 voice agents: the declared extra models on the key
	for k, v := range extra {
		body[k] = v
	}
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
	key := &schemas.RunKey{ID: vk.ID, Name: vk.Name, Value: vk.Value, KeySlot: slot.Name, Provider: p.Name, Model: model,
		OrgKeyID: g.orgSlotKeyID(slot.Name)} // G10: the organization key behind an org slot, for its budget
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
	// 3-A-07: when key slots are shared with other bench processes through
	// claims, this process's copy of the slot's models is not authoritative:
	// another owner may have rewritten the list since. Under the slot's
	// claim, the provider key is read again and updated only when the model
	// is really missing, keeping every model other owners added.
	if shared, err := g.claimsFor(ctx, p); err != nil {
		return err
	} else if shared {
		remote, err := g.slotModels(ctx, p, slot)
		if err != nil {
			return err
		}
		current, have = remote, slices.Contains(remote, model)
		g.mu.Lock()
		g.models[slot.Name] = toSet(remote)
		g.mu.Unlock()
	}
	if have {
		return nil
	}
	sort.Strings(current)
	models := mergeModels(current, model)
	if p.Mode == ModeDirect && p.KeyEnv != "" { // an org direct slot carries its own credential (A09)
		if slot.Value = strings.TrimSpace(g.cfg.Getenv(p.KeyEnv)); slot.Value == "" {
			return fmt.Errorf("direct provider %s: %s is not set", p.Name, p.KeyEnv)
		}
	}
	_, models, err := g.upsertKey(ctx, p.Name, slot, keyID, models)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.models[slot.Name] = toSet(models)
	g.mu.Unlock()
	return nil
}

// slotModels reads the models the slot's provider key serves now.
func (g *Gateway) slotModels(ctx context.Context, p ProviderSetup, slot Slot) ([]string, error) {
	var existing struct {
		Keys []providerKey `json:"keys"`
	}
	if err := g.call(ctx, http.MethodGet, providerPath(p.Name)+"/keys", nil, &existing); err != nil {
		return nil, fmt.Errorf("list %s keys: %w", p.Name, err)
	}
	for _, k := range existing.Keys {
		if k.Name == slot.Name {
			return append([]string(nil), k.Models...), nil
		}
	}
	return nil, fmt.Errorf("key slot %s: not found at provider %s", slot.Name, p.Name)
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

// runBudget is the run key's USD cap: Budgets.USD of the arm when the
// experiment declares the equal_budget fairness rule, which needs the cap
// enforced at the gateway (docs/multi-agent.md); 0 otherwise. A team run
// enforces its cap on the team instead (MintMemberKeys).
func runBudget(rc *schemas.RunContext) float64 {
	if rc == nil || rc.Agent == nil || rc.Experiment == nil || rc.Experiment.Fairness != schemas.FairnessEqualBudget {
		return 0
	}
	if len(utils.TeamOf(rc)) > 0 { // X5b: a solo baseline arm on a team task keeps its run cap (utils.TeamOf)
		return 0
	}
	return rc.Agent.Budgets.USD
}
