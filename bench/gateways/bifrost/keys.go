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
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// vkPageSize is the page size for listing virtual keys.
const vkPageSize = 100

// runIDPattern matches the virtual key names MintKey creates: run ids from
// utils.NewID("r", n). Keys named otherwise are never touched.
var runIDPattern = regexp.MustCompile(`^r_[a-z0-9]+$`)

// MintKey implements schemas.Gateway. It takes a free key slot and creates
// the run's virtual key, named after the run id: one provider, that one slot,
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
	model, err := g.runModel(rc)
	if err != nil {
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	slot, keyID, err := g.acquireSlot(runID)
	if err != nil {
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	key, err := g.createVirtualKey(ctx, runID, slot, keyID, model)
	if err != nil {
		g.releaseSlot(slot.Name, runID)
		return nil, fmt.Errorf("mint key for run %s: %w", runID, err)
	}
	g.mu.Lock()
	g.inUse[slot.Name] = key.ID
	g.minted[key.ID] = mintRecord{slot: slot.Name, at: time.Now()}
	g.mu.Unlock()
	g.logger.Info("virtual key minted", "run_id", runID, "virtual_key_id", key.ID, "key_slot", slot.Name, "model", model)
	return key, nil
}

// RevokeKey implements schemas.Gateway. A key that is already gone counts as
// revoked. The slot is released only when this process minted the key, so a
// stale RunKey can never free a slot another run holds.
func (g *Gateway) RevokeKey(ctx context.Context, key *schemas.RunKey) error {
	if key == nil || key.ID == "" {
		return nil
	}
	if err := g.deleteVirtualKey(ctx, key.ID); err != nil {
		return fmt.Errorf("revoke virtual key %s (%s): %w", key.ID, key.Name, err)
	}
	g.forget(key.ID)
	g.logger.Info("virtual key revoked", "virtual_key_id", key.ID, "name", key.Name, "key_slot", key.KeySlot)
	return nil
}

// RevokeStale implements schemas.Gateway. It deletes every virtual key whose
// name looks like a run id and whose run is not in active, and returns how
// many it deleted.
func (g *Gateway) RevokeStale(ctx context.Context, active map[string]bool) (int, error) {
	keys, err := g.listVirtualKeys(ctx)
	if err != nil {
		return 0, fmt.Errorf("revoke stale keys: %w", err)
	}
	n := 0
	var errs []error
	for _, k := range keys {
		if !runIDPattern.MatchString(k.Name) || active[k.Name] {
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

// runModel is the model the run's key allows: the agent config's model, or
// the gateway model when the config names none. A provider other than the
// gateway's is refused: the slots belong to one provider.
func (g *Gateway) runModel(rc *schemas.RunContext) (string, error) {
	model := g.cfg.Model
	if rc.Agent != nil {
		if rc.Agent.Model != "" {
			model = rc.Agent.Model
		}
		if p := rc.Agent.Gateway.Provider; p != "" && p != g.cfg.Provider {
			return "", fmt.Errorf("agent provider %s is not the gateway provider %s", p, g.cfg.Provider)
		}
	}
	return model, nil
}

// acquireSlot reserves the first free slot in config order for runID.
func (g *Gateway) acquireSlot(runID string) (Slot, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.keyIDs) == 0 {
		return Slot{}, "", errors.New("gateway not set up: Setup has not run")
	}
	for _, s := range g.cfg.Slots {
		if _, busy := g.inUse[s.Name]; busy {
			continue
		}
		id, ok := g.keyIDs[s.Name]
		if !ok {
			continue
		}
		g.inUse[s.Name] = "pending:" + runID
		return s, id, nil
	}
	return Slot{}, "", fmt.Errorf("no free key slot: %d of %d in use", len(g.inUse), len(g.cfg.Slots))
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
func (g *Gateway) createVirtualKey(ctx context.Context, runID string, slot Slot, keyID, model string) (*schemas.RunKey, error) {
	if err := g.ensureSlotModel(ctx, slot, keyID, model); err != nil {
		return nil, err
	}
	body := map[string]any{
		"name": runID,
		"provider_configs": []map[string]any{{
			"provider":       g.cfg.Provider,
			"weight":         1,
			"allowed_models": []string{model},
			"key_ids":        []string{keyID},
		}},
		"is_active": true,
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
	return &schemas.RunKey{
		ID: vk.ID, Name: vk.Name, Value: vk.Value, KeySlot: slot.Name, SlotAuth: slot.Value,
		Provider: g.cfg.Provider, Model: model,
	}, nil
}

// ensureSlotModel adds model to the slot's provider key when an arm runs a
// model other than the gateway's. A key with an empty model list serves
// nothing on v2.5.1 ("no keys found that support model"), so the list is
// always explicit.
func (g *Gateway) ensureSlotModel(ctx context.Context, slot Slot, keyID, model string) error {
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
	if _, err := g.upsertKey(ctx, slot, keyID, models); err != nil {
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
