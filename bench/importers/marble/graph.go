package marble

import (
	"fmt"
	"regexp"
	"slices"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// memberIDRe is the bench's rule for team member ids (engine/team.go): they
// name containers, gateway keys and run folder files.
var memberIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// edge is one relationship of the upstream graph as declared.
type edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // e.g. "collaborate with", "parent"
}

// routes is who may message whom in a team run: the upstream relationship
// graph read the way MARBLE's agents read it. An agent may open a
// communication session with every agent a relationship connects it to, in
// either direction (marble/agent/base_agent.py builds its list of available
// agents from both ends of each relationship), and a session is a two-way
// conversation, so the allowed pairs are undirected. Self-loops give no
// route.
type routes struct {
	Members  []string            `json:"members"`  // team order
	Lead     string              `json:"lead"`     // the member that writes the deliverable
	Allowed  map[string][]string `json:"allowed"`  // member -> members it may message, sorted
	Edges    []edge              `json:"edges"`    // the upstream relationships, as declared
	Complete bool                `json:"complete"` // every pair of members may talk
	Mode     string              `json:"mode"`     // upstream coordinate_mode (graph when blank)
}

// buildRoutes maps a record's agents and relationships to the team's
// routes. It refuses (with the reason) what the upstream engine would also
// refuse: a relationship that is not a triple or that names an agent the
// task does not declare, and agent ids the bench cannot use as member ids.
// lead names the lead; empty means the first agent.
func buildRoutes(r record, lead string) (*routes, string) {
	ids := make([]string, 0, len(r.Agents))
	seen := map[string]bool{}
	for _, a := range r.Agents {
		switch {
		case !memberIDRe.MatchString(a.AgentID):
			return nil, fmt.Sprintf("agent id %q is not a valid team member id (%s)", a.AgentID, memberIDRe)
		case seen[a.AgentID]:
			return nil, fmt.Sprintf("agent id %q is used twice", a.AgentID)
		}
		seen[a.AgentID] = true
		ids = append(ids, a.AgentID)
	}
	if len(ids) == 0 {
		return nil, "the task declares no agents"
	}
	if lead == "" {
		lead = ids[0]
	}
	if !seen[lead] {
		return nil, fmt.Sprintf("lead %q is not an agent of this task", lead)
	}
	rt := &routes{Members: ids, Lead: lead, Allowed: map[string][]string{}, Mode: r.CoordinateMode}
	if rt.Mode == "" {
		rt.Mode = "graph"
	}
	pairs := map[string]map[string]bool{}
	add := func(a, b string) {
		if pairs[a] == nil {
			pairs[a] = map[string]bool{}
		}
		pairs[a][b] = true
	}
	for i, rel := range r.Relationships {
		if len(rel) != 3 {
			return nil, fmt.Sprintf("relationship %d has %d elements, want [agent, agent, relationship]", i+1, len(rel))
		}
		a, b := rel[0], rel[1]
		for _, id := range []string{a, b} {
			if !seen[id] {
				return nil, fmt.Sprintf("relationship %d names agent %q, which the task does not declare", i+1, id)
			}
		}
		rt.Edges = append(rt.Edges, edge{From: a, To: b, Kind: rel[2]})
		if a == b {
			continue
		}
		add(a, b)
		add(b, a)
	}
	for _, id := range ids {
		var to []string
		for peer := range pairs[id] {
			to = append(to, peer)
		}
		sort.Strings(to)
		rt.Allowed[id] = to
	}
	rt.Complete = true
	for _, id := range ids {
		if len(rt.Allowed[id]) != len(ids)-1 {
			rt.Complete = false
		}
	}
	return rt, ""
}

// may reports whether from may message to.
func (rt *routes) may(from, to string) bool {
	return slices.Contains(rt.Allowed[from], to)
}

// pathToLead is the shortest route from member to the lead, member first and
// the lead last (member alone when it is the lead), or nil when the lead
// cannot be reached through the graph.
func (rt *routes) pathToLead(member string) []string {
	if member == rt.Lead {
		return []string{member}
	}
	prev := map[string]string{member: ""}
	queue := []string{member}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range rt.Allowed[cur] {
			if _, ok := prev[next]; ok {
				continue
			}
			prev[next] = cur
			if next == rt.Lead {
				var path []string
				for at := next; at != ""; at = prev[at] {
					path = append([]string{at}, path...)
				}
				return path
			}
			queue = append(queue, next)
		}
	}
	return nil
}

// unreachable lists the members that cannot reach the lead through the
// graph, in team order.
func (rt *routes) unreachable() []string {
	var out []string
	for _, id := range rt.Members {
		if rt.pathToLead(id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// team is the task's team: the lead first gets role lead, every other
// member role worker. No member sub-budget is set: the run-wide cap of the
// arm (budgets.usd) bounds the whole team.
func (rt *routes) team() []schemas.TeamMember {
	out := make([]schemas.TeamMember, 0, len(rt.Members))
	for _, id := range rt.Members {
		role := schemas.AgentRoleWorker
		if id == rt.Lead {
			role = schemas.AgentRoleLead
		}
		out = append(out, schemas.TeamMember{ID: id, Role: role})
	}
	return out
}
