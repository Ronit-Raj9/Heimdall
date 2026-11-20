package utils

import (
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Team member environment variables (docs/multi-agent.md): set in every team
// member's container by the run kernel.
const (
	EnvAgentID    = "HEIMDALL_AGENT_ID"    // the member's id
	EnvAgentRole  = "HEIMDALL_AGENT_ROLE"  // the member's role
	EnvTeam       = "HEIMDALL_TEAM"        // every member id, comma separated, in team order
	EnvMailboxURL = "HEIMDALL_MAILBOX_URL" // the member's own route to the team mailbox
	MailboxPort   = 8080                   // the mailbox service's port
	MailboxRecord = "/tmp/heimdall-mailbox/messages.jsonl"
)

// TeamOf is the run's team: the agent config's, else the task's; nil for a
// single-agent run. An arm whose config declares topology single or
// best_of_n and no team of its own runs alone on a task that declares a
// team: it is the baseline a team is compared with on the same tasks.
func TeamOf(rc *schemas.RunContext) []schemas.TeamMember {
	if rc == nil {
		return nil
	}
	if IsA2A(rc.Agent) { // an A2A agent delegates through the gateway (a2a.team), never through a native team; the planner refuses a task team on an A2A arm
		return nil
	}
	if rc.Agent != nil && len(rc.Agent.Team) > 0 {
		return rc.Agent.Team
	}
	if rc.Agent != nil && (rc.Agent.Topology == schemas.TopologySingle || rc.Agent.Topology == schemas.TopologyBestOfN) { // X5b: a single or best-of-N baseline arm runs alone on a task that declares a team
		return nil
	}
	if rc.Task != nil && len(rc.Task.Team) > 0 {
		return rc.Task.Team
	}
	return nil
}

// A2ATeamOf is the a2a.team workers of an A2A arm that registers its agents
// per run (no fixed registration); nil otherwise.
func A2ATeamOf(a *schemas.AgentConfig) []schemas.A2AMember {
	if !IsA2A(a) || a.A2A == nil || a.A2A.Registration != "" {
		return nil
	}
	return a.A2A.Team
}

// RunKeySlots is how many gateway key slots a run holds at once: one per
// native team member, the entry plus one per a2a.team worker, else one.
// Planning, reservation and minting all count with it.
func RunKeySlots(rc *schemas.RunContext) int {
	if rc == nil {
		return 1
	}
	if w := A2ATeamOf(rc.Agent); len(w) > 0 {
		return 1 + len(w)
	}
	if n := len(TeamOf(rc)); n > 1 {
		return n
	}
	return 1
}

// ArmTopology is an arm's topology for fairness: the one it declared, else
// team for an arm with a native team or an A2A team (a2a.team), else single.
// The engine and the verdict share it so both classify an arm alike.
func ArmTopology(cfg *schemas.AgentConfig) string {
	switch {
	case cfg == nil:
		return schemas.TopologySingle
	case cfg.Topology != "":
		return cfg.Topology
	case len(cfg.Team) > 0 || len(A2ATeamOf(cfg)) > 0:
		return schemas.TopologyTeam
	default:
		return schemas.TopologySingle
	}
}

// ArmMemberBudgets sums the member sub-budgets of an arm's native team and
// of its A2A workers.
func ArmMemberBudgets(cfg *schemas.AgentConfig) float64 {
	var sum float64
	if cfg == nil {
		return 0
	}
	for _, m := range cfg.Team {
		sum += m.BudgetUSD
	}
	for _, m := range A2ATeamOf(cfg) {
		sum += m.BudgetUSD
	}
	return sum
}

// TeamIDs lists the member ids of a team in team order.
func TeamIDs(team []schemas.TeamMember) []string {
	out := make([]string, len(team))
	for i, m := range team {
		out[i] = m.ID
	}
	return out
}

// MemberRouteURL rewrites a tool proxy service URL (http://toolproxy:9200/s/
// <id>/...) onto a member's route (/m/<token>/s/<id>/...), so the proxy
// stamps the member on its calls. Any other URL is returned unchanged.
func MemberRouteURL(u, token string) string {
	if token == "" {
		return u
	}
	const marker = ":9200/s/"
	i := strings.Index(u, marker)
	if i < 0 {
		return u
	}
	return u[:i] + ":9200/m/" + token + "/s/" + u[i+len(marker):]
}

// MemberRoutes is the tool proxy's token to member map of a run.
func MemberRoutes(routes map[string]string) map[string]string {
	if len(routes) == 0 {
		return nil
	}
	out := make(map[string]string, len(routes))
	ids := make([]string, 0, len(routes))
	for id := range routes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out[routes[id]] = id
	}
	return out
}

// RunSecrets lists every credential the bench issued to a run at runtime:
// the run key, every member key, every service gateway key (MCP through
// Bifrost) and the fault runtime token, without empty or repeated values.
// Stream sanitizers and serialized plan copies blank each one, so a new kind
// of runtime credential is added here once.
func RunSecrets(rc *schemas.RunContext) []string {
	if rc == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if rc.Key != nil {
		add(rc.Key.Value)
		for _, m := range rc.Key.Members {
			add(m.Value)
		}
	}
	for _, h := range rc.Services {
		add(h.GatewayKey)
	}
	if rc.FaultRuntime != nil {
		add(rc.FaultRuntime.Token)
	}
	return out
}
