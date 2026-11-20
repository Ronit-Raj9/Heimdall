package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Team runs (docs/multi-agent.md). Each member of a team gets a virtual key
// of its own on a key slot of its own, so the gateway log says which member
// made each model call (GatewayRequest.AgentID) and a fault plan can target
// one member. The run-wide cap is a Bifrost team budget over every member
// key; each member key carries its own sub-budget.

// memberKeySep joins the run id and the member id in a member key name, so
// RevokeStale can tell whose run a leftover key belonged to.
const memberKeySep = "--"

// budgetReset is the reset window of the budgets the bench sets. A run never
// lasts that long, so a budget never resets inside a run.
const budgetReset = "1w"

// memberKeyPattern matches member key names and run team names: the run id
// before the separator.
var memberKeyPattern = regexp.MustCompile(`^(r_[a-z0-9]+)--[a-z0-9][a-z0-9_-]*$`)

var _ schemas.TeamGateway = (*Gateway)(nil)

// MintMemberKeys implements schemas.TeamGateway. It creates the run's Bifrost
// team with the run-wide budget (rc.Agent.Budgets.USD, when set), then one
// virtual key per member on a free slot of the run's provider, in that team,
// with the member's model and sub-budget. A failure revokes every member key
// minted so far and the team.
func (g *Gateway) MintMemberKeys(ctx context.Context, rc *schemas.RunContext, run *schemas.RunKey, members []schemas.TeamMember) ([]schemas.MemberKey, error) {
	if rc == nil || rc.Run == nil || run == nil {
		return nil, errors.New("mint member keys: no run or run key")
	}
	runID := rc.Run.RunID
	p, model, err := g.runProvider(rc)
	if err != nil {
		return nil, fmt.Errorf("mint member keys for run %s: %w", runID, err)
	}
	teamID := ""
	if rc.Agent != nil && rc.Agent.Budgets.USD > 0 {
		if teamID, err = g.createTeam(ctx, runID+memberKeySep+"team", rc.Agent.Budgets.USD); err != nil {
			return nil, fmt.Errorf("mint member keys for run %s: %w", runID, err)
		}
	}
	var keys []schemas.MemberKey
	lead := leadIndex(members)
	for i, m := range members {
		var k schemas.MemberKey
		var err error
		if i == lead {
			k, err = g.leadKey(ctx, run, teamID, m)
		} else {
			k, err = g.mintMember(ctx, runID, p, model, teamID, m)
		}
		if err != nil {
			partial := &schemas.RunKey{Members: keys}
			// 3-A-03: a failed rollback is reported, and the team id stays
			// recorded on the run key so RevokeKey retries it.
			if rbErr := g.revokeMembers(context.WithoutCancel(ctx), partial, teamID); rbErr != nil {
				if teamID != "" {
					g.mu.Lock()
					g.teams[run.ID] = teamID
					g.mu.Unlock()
				}
				return nil, fmt.Errorf("mint member keys for run %s: member %s: %w", runID, m.ID, errors.Join(err, fmt.Errorf("rollback: %w", rbErr)))
			}
			return nil, fmt.Errorf("mint member keys for run %s: member %s: %w", runID, m.ID, err)
		}
		keys = append(keys, k)
	}
	g.mu.Lock()
	g.teams[run.ID] = teamID
	g.mu.Unlock()
	g.logger.Info("team member keys minted", "run_id", runID, "members", len(keys), "team_id", teamID)
	return keys, nil
}

// mintMember takes a free slot of the provider and creates one member key.
func (g *Gateway) mintMember(ctx context.Context, runID string, p ProviderSetup, model, teamID string, m schemas.TeamMember) (schemas.MemberKey, error) {
	name := runID + memberKeySep + m.ID
	if !memberKeyPattern.MatchString(name) {
		return schemas.MemberKey{}, fmt.Errorf("member key name %q does not match %s", name, memberKeyPattern)
	}
	if m.Model != "" {
		model = m.Model
	} else if m.Config != nil && m.Config.Model != "" {
		model = m.Config.Model
	}
	slot, keyID, err := g.takeReserved(ctx, p, runID, name)
	if err != nil {
		return schemas.MemberKey{}, err
	}
	extra := map[string]any{}
	if teamID != "" {
		extra["team_id"] = teamID
	}
	if m.BudgetUSD > 0 {
		extra["budgets"] = []map[string]any{{"max_limit": m.BudgetUSD, "reset_duration": budgetReset}}
	}
	key, err := g.createVirtualKey(ctx, name, p, slot, keyID, model, nil, nil, extra) // a member key has no routing policy or extra models
	if err != nil {
		g.releaseSlot(slot.Name, name)
		return schemas.MemberKey{}, err
	}
	g.mu.Lock()
	g.inUse[slot.Name] = key.ID
	g.minted[key.ID] = mintRecord{slot: slot.Name, at: time.Now()}
	g.memberOf[key.ID] = runID
	g.mu.Unlock()
	return schemas.MemberKey{AgentID: m.ID, ID: key.ID, Name: key.Name, Value: key.Value, KeySlot: key.KeySlot,
		SlotAuth: key.SlotAuth, BudgetUSD: m.BudgetUSD}, nil
}

// createTeam creates a Bifrost team with one budget and returns its id.
func (g *Gateway) createTeam(ctx context.Context, name string, usd float64) (string, error) {
	body := map[string]any{"name": name, "budgets": []map[string]any{{"max_limit": usd, "reset_duration": budgetReset}}}
	var out struct {
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	if err := g.call(ctx, http.MethodPost, "/api/governance/teams", body, &out); err != nil {
		return "", fmt.Errorf("create team %s: %w", name, err)
	}
	if out.Team.ID == "" {
		return "", fmt.Errorf("create team %s: bifrost returned no id", name)
	}
	return out.Team.ID, nil
}

// revokeMembers deletes the member keys of a run key and its team. Keys that
// are already gone count as revoked. The team is deleted only once every
// member key is gone.
func (g *Gateway) revokeMembers(ctx context.Context, key *schemas.RunKey, teamID string) error {
	if err := g.revokeMemberKeys(ctx, key); err != nil {
		return err
	}
	if teamID != "" {
		return g.deleteTeam(ctx, teamID)
	}
	return nil
}

// revokeMemberKeys deletes every member key of a run key except the lead's,
// attempting each one and joining the errors.
func (g *Gateway) revokeMemberKeys(ctx context.Context, key *schemas.RunKey) error {
	var errs []error
	for _, m := range key.Members {
		if m.ID == "" || m.ID == key.ID {
			continue // the lead runs on the run key, which RevokeKey deletes
		}
		if err := g.deleteVirtualKey(ctx, m.ID); err != nil {
			errs = append(errs, fmt.Errorf("revoke member key %s (%s): %w", m.ID, m.Name, err))
			continue
		}
		g.forget(m.ID)
		g.mu.Lock()
		delete(g.memberOf, m.ID)
		g.mu.Unlock()
	}
	return errors.Join(errs...)
}

// teamFor returns the Bifrost team of a run key. It stays recorded until
// dropTeam, so a failed cleanup can be retried (3-A-02).
func (g *Gateway) teamFor(keyID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.teams[keyID]
}

// dropTeam forgets the Bifrost team of a run key once its DELETE succeeded.
func (g *Gateway) dropTeam(keyID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.teams, keyID)
}

func (g *Gateway) deleteTeam(ctx context.Context, id string) error {
	err := g.call(ctx, http.MethodDelete, "/api/governance/teams/"+url.PathEscape(id), nil, nil)
	if err == nil || isStatus(err, http.StatusNotFound) {
		return nil
	}
	return fmt.Errorf("delete team %s: %w", id, err)
}

// keyIDs are the virtual key ids whose rows make up the run: the run key and
// every member key.
func keyIDs(key *schemas.RunKey) string {
	ids := []string{key.ID}
	for _, m := range key.Members {
		if m.ID != "" && m.ID != key.ID {
			ids = append(ids, m.ID)
		}
	}
	return strings.Join(ids, ",")
}

// memberAgents maps member key ids to member ids.
func memberAgents(key *schemas.RunKey) map[string]string {
	if len(key.Members) == 0 {
		return nil
	}
	out := make(map[string]string, len(key.Members))
	for _, m := range key.Members {
		out[m.ID] = m.AgentID
	}
	return out
}

// staleRun is the run id a key name belongs to: the name itself for a run
// key, the part before the separator for a member key or a run team.
func staleRun(name string) (string, bool) {
	if runIDPattern.MatchString(name) {
		return name, true
	}
	if m := memberKeyPattern.FindStringSubmatch(name); m != nil {
		return m[1], true
	}
	return "", false
}

// Agent labels a caller can set on a model call, as Bifrost logs them in a
// row's metadata: x-bf-lh-heimdall-agent is logged as heimdall-agent with no
// gateway config; x-heimdall-agent and baggage are logged when the gateway's
// logging_headers list them.
var agentLabelKeys = []string{"heimdall-agent", "x-heimdall-agent", "heimdall_agent"}

// agentLabel reads the declared agent from a row's metadata, or from an OTel
// baggage header entry heimdall.agent. Empty when none.
func agentLabel(meta map[string]any) string {
	for _, k := range agentLabelKeys {
		if s, ok := meta[k].(string); ok && strings.TrimSpace(s) != "" {
			return clipLabel(s)
		}
	}
	if s, ok := meta["baggage"].(string); ok {
		for _, part := range strings.Split(s, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			if ok && strings.TrimSpace(k) == "heimdall.agent" {
				if dec, err := url.QueryUnescape(strings.TrimSpace(v)); err == nil {
					v = dec
				}
				v, _, _ = strings.Cut(v, ";")
				return clipLabel(v)
			}
		}
	}
	return ""
}

// clipLabel keeps an agent label inert and short.
func clipLabel(s string) string {
	s = strings.TrimSpace(s)
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		out = append(out, r)
		if len(out) == 128 {
			break
		}
	}
	return string(out)
}

// leadIndex is the member that runs on the run's own key: the first with
// role lead, else the first member.
func leadIndex(members []schemas.TeamMember) int {
	for i, m := range members {
		if m.Role == schemas.AgentRoleLead {
			return i
		}
	}
	return 0
}

// leadKey gives the lead the run's own key and slot. The run key joins the
// run's Bifrost team and takes the lead's sub-budget, so the run-wide cap
// covers the lead's spend too.
func (g *Gateway) leadKey(ctx context.Context, run *schemas.RunKey, teamID string, m schemas.TeamMember) (schemas.MemberKey, error) {
	body := map[string]any{}
	if teamID != "" {
		body["team_id"] = teamID
	}
	if m.BudgetUSD > 0 {
		body["budgets"] = []map[string]any{{"max_limit": m.BudgetUSD, "reset_duration": budgetReset}}
	}
	if len(body) > 0 {
		if err := g.call(ctx, http.MethodPut, "/api/governance/virtual-keys/"+url.PathEscape(run.ID), body, nil); err != nil {
			return schemas.MemberKey{}, fmt.Errorf("update run key %s for lead %s: %w", run.ID, m.ID, err)
		}
	}
	return schemas.MemberKey{AgentID: m.ID, ID: run.ID, Name: run.Name, Value: run.Value, KeySlot: run.KeySlot,
		SlotAuth: run.SlotAuth, BudgetUSD: m.BudgetUSD}, nil
}

// benchTeam is a Bifrost governance team as GET /api/governance/teams
// returns it.
type benchTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// listTeams lists every governance team; none on a gateway without the
// endpoint.
func (g *Gateway) listTeams(ctx context.Context) ([]benchTeam, error) {
	var page struct {
		Teams []benchTeam `json:"teams"`
	}
	err := g.call(ctx, http.MethodGet, "/api/governance/teams", nil, &page)
	if isStatus(err, http.StatusNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	return page.Teams, nil
}

// removeStaleTeams deletes bench run teams (named <run id>--team) of runs
// that are not active, for the janitor (3-A-03). A run whose keys could not
// all be deleted keeps its team, so no key is left pointing at a deleted
// team; the next sweep retries.
func (g *Gateway) removeStaleTeams(ctx context.Context, teams []benchTeam, listErr error, active, keyLeft map[string]bool) []error {
	if listErr != nil {
		return []error{fmt.Errorf("revoke stale teams: %w", listErr)}
	}
	var errs []error
	for _, t := range teams {
		run, ok := staleRun(t.Name)
		if !ok || t.Name != run+memberKeySep+"team" || active[run] || keyLeft[run] || t.ID == "" {
			continue
		}
		if err := g.deleteTeam(ctx, t.ID); err != nil {
			errs = append(errs, fmt.Errorf("revoke stale team %s (%s): %w", t.ID, t.Name, err))
			continue
		}
		g.logger.Info("stale team deleted", "team_id", t.ID, "name", t.Name)
	}
	return errs
}
