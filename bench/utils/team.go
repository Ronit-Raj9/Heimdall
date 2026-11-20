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

// TeamIDs lists the member ids of a team in team order.
func TeamIDs(team []schemas.TeamMember) []string {
	out := make([]string, len(team))
	for i, m := range team {
		out[i] = m.ID
	}
	return out
}

// MemberRouteURL rewrites a tool proxy service URL (http://toolproxy:9200/s/
// <id>/...) or external API URL (http://toolproxy:9200/x/<id>/...) onto a
// member's route (/m/<token>/s/<id>/... or /m/<token>/x/<id>/...), so the
// proxy stamps the member on its calls. Any other URL is returned unchanged.
func MemberRouteURL(u, token string) string {
	if token == "" {
		return u
	}
	for _, marker := range []string{":9200/s/", ":9200" + ExternalPathPrefix} {
		if i := strings.Index(u, marker); i >= 0 {
			return u[:i] + ":9200/m/" + token + u[i+len(":9200"):]
		}
	}
	return u
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
