package metrics

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Names of the multi-agent metrics (docs/multi-agent.md). They read the run's
// agent tree (Evidence.Agents) and the steps, gateway rows, cost rows and
// messages placed on its agents. A single-agent run has no tree, so each of
// them is unknown there, never zero; budget_exceeded is the exception and is
// measured on every run, because an equal-budget comparison needs it per arm
// whatever the arm's shape.
const (
	AgentsSpawned       = "agents_spawned"
	MaxAgentDepth       = "max_depth"
	MaxFanOut           = "max_fan_out"
	OrchestrationShare  = "orchestration_share"
	Handoffs            = "handoffs"
	AgentMessages       = "messages"
	AgentMessageBytes   = "message_bytes"
	DuplicateWorkRatio  = "duplicate_work_ratio"
	SerialCollapse      = "serial_collapse"
	CriticalPathS       = "critical_path_s"
	Parallelism         = "parallelism"
	DelegationLoops     = "delegation_loops"
	AttributionCoverage = "attribution_coverage"
	BudgetExceeded      = "budget_exceeded"
)

// Prefixes of the derived values (schemas.DerivedMetric). The per-agent ones
// end in the agent id with every character outside [a-z0-9_-] replaced by an
// underscore (agentKeys); the coverage ones end in an attribution strength.
const (
	AgentCostPrefix           = "agent_cost_usd."
	AgentTokensPrefix         = "agent_tokens."
	AgentToolCallsPrefix      = "agent_tool_calls."
	AgentModelTurnsPrefix     = "agent_model_turns."
	AttributionCoveragePrefix = "attribution_coverage_"
)

// Reasons a multi-agent metric has no number.
const (
	reasonSingleAgent    = "single-agent run: no agent tree, so multi-agent metrics do not apply"
	reasonNoAgents       = "the agent tree lists no agent"
	reasonNoOrchestrator = "no agent has an orchestration role (lead, router, judge)"
	reasonNoCostMapped   = "no cost row maps to an agent"
	reasonZeroCost       = "the run's priced cost is zero, so a share of it is undefined"
	reasonNoAttributed   = "no step of the timeline carries an agent id"
	reasonNoWork         = "the attributed steps wrote no file and requested no tool call"
	reasonNoTimelineRows = "the agent tree counts no timeline row to attribute"
	reasonNoCap          = "the arm has no total USD cap (budgets.usd)"
	reasonZeroSpan       = "the agents' start-to-end span is zero seconds"
)

const (
	maxAgentKey  = 64   // longest sanitized agent id in a derived name
	capTolerance = 1e-9 // float summing error, so a run that spent its cap exactly is not over it
)

var (
	_ schemas.DerivedMetric = (*multiAgentMetric)(nil)
)

// multiAgentMetric is one multi-agent metric: compute reads the run, derived
// (optional) adds run-time named values beside it.
type multiAgentMetric struct {
	desc    schemas.MetricDescriptor
	compute func(rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value
	derived func(ev *schemas.Evidence) map[string]schemas.Value
}

// GetName implements schemas.Part.
func (m *multiAgentMetric) GetName() string { return m.desc.Name }

// GetVersion implements schemas.Part.
func (m *multiAgentMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *multiAgentMetric) Descriptor() schemas.MetricDescriptor { return m.desc }

// Compute implements schemas.Metric.
func (m *multiAgentMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	return m.compute(rc, orEmpty(ev)), nil
}

// ComputeDerived implements schemas.DerivedMetric. A metric without derived
// values returns none; a reader treats a missing name as unknown, never 0.
func (m *multiAgentMetric) ComputeDerived(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (map[string]schemas.Value, error) {
	if m.derived == nil {
		return nil, nil
	}
	return m.derived(orEmpty(ev)), nil
}

// newMultiAgentMetric fills the descriptor fields every multi-agent metric
// shares.
func newMultiAgentMetric(name, label string, kind schemas.ValueKind, higher bool, level schemas.EvidenceLevel, desc string,
	compute func(*schemas.RunContext, *schemas.Evidence) schemas.Value) *multiAgentMetric {
	return &multiAgentMetric{
		desc: schemas.MetricDescriptor{Name: name, Version: version, Label: label, Kind: kind,
			HigherIsBetter: higher, MinEvidence: level, Description: desc},
		compute: compute,
	}
}

// withTree runs f only for a run with a non-empty agent tree; any other run is
// unknown.
func withTree(f func(t *schemas.AgentTree, ev *schemas.Evidence) schemas.Value) func(*schemas.RunContext, *schemas.Evidence) schemas.Value {
	return func(_ *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
		if v, bad := treeProblem(ev.Agents); bad {
			return v
		}
		return f(ev.Agents, ev)
	}
}

// newAgentsSpawnedMetric returns agents_spawned. The per-agent values
// (agent_cost_usd.<id>, agent_tokens.<id>, agent_tool_calls.<id>,
// agent_model_turns.<id>) are its derived values.
func newAgentsSpawnedMetric() *multiAgentMetric {
	m := newMultiAgentMetric(AgentsSpawned, "Agents spawned", schemas.KindCount, false, schemas.EvidenceNative,
		"Agents in the run's agent tree beyond the first (in-process subagents, or team members beyond one), with each agent's cost, tokens, tool calls and model turns as derived agent_cost_usd, agent_tokens, agent_tool_calls and agent_model_turns values, averaged over multi-agent runs.",
		withTree(func(t *schemas.AgentTree, _ *schemas.Evidence) schemas.Value {
			return treeValue(float64(len(t.Agents)-1), t)
		}))
	m.derived = perAgentValues
	return m
}

// newMaxDepthMetric returns max_depth.
func newMaxDepthMetric() *multiAgentMetric {
	return newMultiAgentMetric(MaxAgentDepth, "Max agent depth", schemas.KindCount, false, schemas.EvidenceNative,
		"Deepest agent of the run's agent tree, a root agent at depth 0 and an agent a root spawned at depth 1, averaged over multi-agent runs.",
		withTree(func(t *schemas.AgentTree, _ *schemas.Evidence) schemas.Value {
			deepest := 0
			for _, a := range t.Agents {
				deepest = max(deepest, a.Depth)
			}
			return treeValue(float64(deepest), t)
		}))
}

// newMaxFanOutMetric returns max_fan_out.
func newMaxFanOutMetric() *multiAgentMetric {
	return newMultiAgentMetric(MaxFanOut, "Max fan-out", schemas.KindCount, true, schemas.EvidenceNative,
		"Largest number of agents with the same parent in the run's agent tree, root agents counting as children of the run so a 3-member team has fan-out 3, averaged over multi-agent runs.",
		withTree(func(t *schemas.AgentTree, _ *schemas.Evidence) schemas.Value {
			children := map[string]int{}
			widest := 0
			for _, a := range t.Agents {
				children[a.ParentID]++
				widest = max(widest, children[a.ParentID])
			}
			return treeValue(float64(widest), t)
		}))
}

// newOrchestrationShareMetric returns orchestration_share.
func newOrchestrationShareMetric() *multiAgentMetric {
	return newMultiAgentMetric(OrchestrationShare, "Orchestration share of cost", schemas.KindRate, false, schemas.EvidenceGateway,
		"Share of the run's priced USD cost spent by agents whose role is lead, router or judge, each cost row placed on the agent of its gateway row (team member key, else declared agent label, else the agent of its timeline model turn), averaged over multi-agent runs whose every cost row was priced and placed on an agent.",
		withTree(orchestrationShare))
}

// newHandoffsMetric returns handoffs.
func newHandoffsMetric() *multiAgentMetric {
	return newMultiAgentMetric(Handoffs, "Handoffs", schemas.KindCount, false, schemas.EvidenceNative,
		"Handoff edges in the run's agent tree, one per agent handing control to another, averaged over multi-agent runs.",
		withTree(func(t *schemas.AgentTree, _ *schemas.Evidence) schemas.Value {
			return treeValue(float64(countEdges(t, schemas.AgentEdgeHandoff)), t)
		}))
}

// newMessagesMetric returns messages.
func newMessagesMetric() *multiAgentMetric {
	return newMultiAgentMetric(AgentMessages, "Agent messages", schemas.KindCount, false, schemas.EvidenceNative,
		"Messages between agents per run, read from the team mailbox records when the run has any and from the message edges of the agent tree otherwise, averaged over multi-agent runs.",
		withTree(func(t *schemas.AgentTree, ev *schemas.Evidence) schemas.Value {
			n, _, v := messageTotals(t, ev)
			return v(float64(n))
		}))
}

// newMessageBytesMetric returns message_bytes.
func newMessageBytesMetric() *multiAgentMetric {
	return newMultiAgentMetric(AgentMessageBytes, "Agent message bytes", schemas.KindCount, false, schemas.EvidenceNative,
		"Bytes of the messages counted in messages (each message body's size), averaged over multi-agent runs.",
		withTree(func(t *schemas.AgentTree, ev *schemas.Evidence) schemas.Value {
			_, size, v := messageTotals(t, ev)
			return v(float64(size))
		}))
}

// newDuplicateWorkRatioMetric returns duplicate_work_ratio.
func newDuplicateWorkRatioMetric() *multiAgentMetric {
	return newMultiAgentMetric(DuplicateWorkRatio, "Duplicate work ratio", schemas.KindRate, false, schemas.EvidenceNative,
		"Share of the distinct workspace paths written and distinct tool calls (same tool name and argument hash) that two or more agents did, over the timeline steps that carry an agent id, averaged over multi-agent runs with at least one attributed write or tool call.",
		withTree(duplicateWork))
}

// newSerialCollapseMetric returns serial_collapse.
func newSerialCollapseMetric() *multiAgentMetric {
	return newMultiAgentMetric(SerialCollapse, "Serial collapse", schemas.KindRate, false, schemas.EvidenceNative,
		"1 when two or more spawned agents (team members, for a team) ran with no two of their start-to-end intervals overlapping and 0 when some overlap, averaged over multi-agent runs where every such agent has a start and an end time.",
		withTree(serialCollapse))
}

// newCriticalPathMetric returns critical_path_s.
func newCriticalPathMetric() *multiAgentMetric {
	return newMultiAgentMetric(CriticalPathS, "Critical path (s)", schemas.KindDuration, false, schemas.EvidenceNative,
		"Wall seconds from the earliest agent start to the latest agent end in the run's agent tree, averaged over multi-agent runs where every agent has a start and an end time.",
		withTree(func(t *schemas.AgentTree, _ *schemas.Evidence) schemas.Value {
			spans, reason := agentSpans(t.Agents)
			if reason != "" {
				return schemas.Unknown(reason, schemas.EvidenceNative)
			}
			_, wall := busyAndWall(spans)
			return treeValue(wall.Seconds(), t)
		}))
}

// newParallelismMetric returns parallelism.
func newParallelismMetric() *multiAgentMetric {
	return newMultiAgentMetric(Parallelism, "Parallelism", schemas.KindCount, true, schemas.EvidenceNative,
		"Sum of every agent's start-to-end seconds divided by critical_path_s, 1 when the agents ran one at a time, averaged over multi-agent runs where every agent has a start and an end time and the span is above zero.",
		withTree(func(t *schemas.AgentTree, _ *schemas.Evidence) schemas.Value {
			spans, reason := agentSpans(t.Agents)
			if reason != "" {
				return schemas.Unknown(reason, schemas.EvidenceNative)
			}
			busy, wall := busyAndWall(spans)
			if wall <= 0 {
				return schemas.Unknown(reasonZeroSpan, schemas.EvidenceNative)
			}
			return treeValue(busy.Seconds()/wall.Seconds(), t)
		}))
}

// newDelegationLoopsMetric returns delegation_loops.
func newDelegationLoopsMetric() *multiAgentMetric {
	return newMultiAgentMetric(DelegationLoops, "Delegation loops", schemas.KindCount, false, schemas.EvidenceNative,
		"Spawn edges that start an agent with the same name and role as its spawner or one of the spawner's ancestors, or that repeat an earlier spawn from the same parent with the same payload hash, each edge counted once, averaged over multi-agent runs.",
		withTree(func(t *schemas.AgentTree, _ *schemas.Evidence) schemas.Value {
			return treeValue(float64(delegationLoops(t)), t)
		}))
}

// newAttributionCoverageMetric returns attribution_coverage; its derived
// values split it by attribution strength.
func newAttributionCoverageMetric() *multiAgentMetric {
	m := newMultiAgentMetric(AttributionCoverage, "Attribution coverage", schemas.KindRate, true, schemas.EvidenceNative,
		"Share of the run's step timeline rows that carry an agent id, split by attribution strength into derived attribution_coverage_proven, attribution_coverage_declared and attribution_coverage_reported values, averaged over multi-agent runs with at least one timeline row.",
		withTree(func(t *schemas.AgentTree, _ *schemas.Evidence) schemas.Value {
			c := t.Coverage
			if c.Steps <= 0 {
				return schemas.Unknown(reasonNoTimelineRows, schemas.EvidenceNative)
			}
			return treeValue(float64(c.Attributed)/float64(c.Steps), t)
		}))
	m.derived = coverageByStrength
	return m
}

// newBudgetExceededMetric returns budget_exceeded. It is measured on every
// run with a cap, single-agent runs included.
func newBudgetExceededMetric() *multiAgentMetric {
	return newMultiAgentMetric(BudgetExceeded, "Budget exceeded", schemas.KindRate, false, schemas.EvidenceGateway,
		"1 when the run's priced USD cost is above its arm's total USD cap (budgets.usd) and 0 when it is at or below it, measured on every run, averaged over runs whose arm has a cap and whose cost, or its bounds, settles the comparison.",
		budgetExceeded)
}

// treeProblem returns an unknown value and true when the run has no usable
// agent tree.
func treeProblem(t *schemas.AgentTree) (schemas.Value, bool) {
	switch {
	case t == nil:
		return schemas.Unknown(reasonSingleAgent, schemas.EvidenceNative), true
	case len(t.Agents) == 0:
		return schemas.Unknown(reasonNoAgents, schemas.EvidenceNative), true
	}
	return schemas.Value{}, false
}

// treeValue is an observed value at the level of the tree's weakest agent:
// gateway with strong evidence when every agent was seen on a bench-minted
// key, gateway with partial evidence when the weakest was a gateway-logged
// header the agent chose, native and partial when any agent is known only
// from the agent's own record.
func treeValue(v float64, t *schemas.AgentTree) schemas.Value {
	level, strength := schemas.EvidenceGateway, schemas.EvidenceStrong
	for _, a := range t.Agents {
		switch schemas.AgentSourceStrength(a.Source) {
		case schemas.AgentStrengthProven:
		case schemas.AgentStrengthDeclared:
			strength = schemas.EvidencePartial
		default:
			level, strength = schemas.EvidenceNative, schemas.EvidencePartial
		}
	}
	out := schemas.Observed(v, level)
	out.EvidenceLevel = strength
	return out
}

// countEdges counts the tree's edges of one kind.
func countEdges(t *schemas.AgentTree, kind string) int {
	n := 0
	for _, e := range t.Edges {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// messageTotals counts the run's messages and their bytes, with the
// constructor of the value at their evidence level. The mailbox records
// (Evidence.Messages) come from the tool proxy ledger, so they are preferred
// over the tree's message edges, which may come from the agent's own record.
func messageTotals(t *schemas.AgentTree, ev *schemas.Evidence) (int, int64, func(float64) schemas.Value) {
	if len(ev.Messages) > 0 {
		var size int64
		for _, m := range ev.Messages {
			if m.Size > 0 {
				size += m.Size
			} else {
				size += int64(len(m.Body))
			}
		}
		return len(ev.Messages), size, func(v float64) schemas.Value {
			out := schemas.Observed(v, schemas.EvidenceBoundary)
			out.EvidenceLevel = schemas.EvidenceStrong
			return out
		}
	}
	n, size := 0, int64(0)
	for _, e := range t.Edges {
		if e.Kind == schemas.AgentEdgeMessage {
			n++
			size += e.Size
		}
	}
	return n, size, func(v float64) schemas.Value { return treeValue(v, t) }
}

// agentSpend is the run's cost split by agent. Unpriced counts the rows that
// could not be priced, per agent; the empty agent holds the rows that map to
// no agent.
type agentSpend struct {
	byAgent  map[string]float64
	unpriced map[string]int
	rows     map[string]int
	total    float64
	all      int
}

// spendByAgent places every cost row on the agent of its gateway row. A
// not_applicable row adds nothing.
func spendByAgent(ev *schemas.Evidence, t *schemas.AgentTree) agentSpend {
	agentOf := requestAgents(ev, t)
	s := agentSpend{byAgent: map[string]float64{}, unpriced: map[string]int{}, rows: map[string]int{}}
	for _, row := range ev.Cost {
		if row.USD.State == schemas.ValueNotApplicable {
			continue
		}
		s.all++
		a := agentOf(row.RequestID)
		s.rows[a]++
		v, ok := row.USD.Float()
		if !ok {
			s.unpriced[a]++
			continue
		}
		s.byAgent[a] += v
		s.total += v
	}
	return s
}

// orchestrationShare is the cost of lead, router and judge agents over the
// run's cost. Rows that map to no agent bound the share instead of being
// dropped: they could belong to any agent.
func orchestrationShare(t *schemas.AgentTree, ev *schemas.Evidence) schemas.Value {
	orch := map[string]bool{}
	for _, a := range t.Agents {
		if slices.Contains(schemas.OrchestrationRoles, a.Role) {
			orch[a.ID] = true
		}
	}
	if len(orch) == 0 {
		return schemas.Unknown(reasonNoOrchestrator, schemas.EvidenceGateway)
	}
	if len(ev.Cost) == 0 {
		return schemas.Unknown(noCostReason(ev.Requests), schemas.EvidenceGateway)
	}
	s := spendByAgent(ev, t)
	unpriced := 0
	for _, n := range s.unpriced {
		unpriced += n
	}
	switch {
	case unpriced > 0:
		return schemas.Unknown(fmt.Sprintf("%d of %d cost rows unpriced", unpriced, s.all), schemas.EvidenceGateway)
	case s.all-s.rows[""] == 0:
		return schemas.Unknown(reasonNoCostMapped, schemas.EvidenceGateway)
	case s.total <= 0:
		return schemas.Unknown(reasonZeroCost, schemas.EvidenceGateway)
	}
	var share float64
	for id := range orch {
		share += s.byAgent[id]
	}
	lo := share / s.total
	if n := s.rows[""]; n > 0 {
		hi := (share + s.byAgent[""]) / s.total
		return schemas.UnknownWithin(fmt.Sprintf("%d of %d cost rows map to no agent", n, s.all), schemas.EvidenceGateway, lo, hi)
	}
	return schemas.Observed(lo, schemas.EvidenceGateway)
}

// requestAgents returns the agent of a gateway row id: the team member whose
// key the row came in on, else the agent its declared label names (by id, or
// by a name only one agent has), else the agent of the timeline model turn of
// its logical request (or of the attempt row with its id). An id that is not
// a gateway row is looked up in the timeline directly. Empty means unknown.
func requestAgents(ev *schemas.Evidence, t *schemas.AgentTree) func(id string) string {
	ids, names := map[string]bool{}, map[string]string{}
	for _, a := range t.Agents {
		ids[a.ID] = true
	}
	for _, a := range t.Agents {
		if a.Name == "" || ids[a.Name] {
			continue
		}
		if prev, ok := names[a.Name]; ok && prev != a.ID {
			names[a.Name] = "" // two agents share the name: it names neither
			continue
		}
		names[a.Name] = a.ID
	}
	stepAgent := map[string]string{}
	for _, s := range ev.Steps {
		if s.AgentID == "" || s.RequestID == "" || (s.Kind != schemas.StepModelTurn && s.Kind != schemas.StepModelAttempt) {
			continue
		}
		if _, ok := stepAgent[s.RequestID]; !ok || s.Kind == schemas.StepModelTurn {
			stepAgent[s.RequestID] = s.AgentID
		}
	}
	byID := map[string]schemas.GatewayRequest{}
	if ev.Requests != nil {
		for _, r := range ev.Requests.Requests {
			if r.ID != "" {
				byID[r.ID] = r
			}
		}
	}
	return func(id string) string {
		r, ok := byID[id]
		switch {
		case !ok:
			return stepAgent[id]
		case r.AgentID != "":
			return r.AgentID
		case ids[r.AgentLabel]:
			return r.AgentLabel
		case r.AgentLabel != "" && names[r.AgentLabel] != "":
			return names[r.AgentLabel]
		}
		if a := stepAgent[rootID(r, byID)]; a != "" {
			return a
		}
		return stepAgent[r.ID]
	}
}

// perAgentValues returns each agent's cost, tokens, tool calls and model
// turns. A family whose input is missing (no cost rows, an incomplete
// gateway log, no timeline) adds no value. Work that maps to no agent could
// be any agent's, so it turns each agent's number into a bound.
func perAgentValues(ev *schemas.Evidence) map[string]schemas.Value {
	t := ev.Agents
	if _, bad := treeProblem(t); bad {
		return nil
	}
	keys := agentKeys(t)
	out := map[string]schemas.Value{}
	if len(ev.Cost) > 0 {
		s := spendByAgent(ev, t)
		for id, key := range keys {
			out[AgentCostPrefix+key] = agentCostValue(s, id)
		}
	}
	if _, bad := requestLogProblem(ev.Requests); !bad {
		tok := tokensByAgent(ev, t)
		for id, key := range keys {
			out[AgentTokensPrefix+key] = tok(id)
		}
	}
	if ev.Steps != nil {
		tools := stepCounts(ev.Steps, schemas.StepToolRequest)
		turns := stepCounts(ev.Steps, schemas.StepModelTurn)
		for id, key := range keys {
			out[AgentToolCallsPrefix+key] = tools(id)
			out[AgentModelTurnsPrefix+key] = turns(id)
		}
	}
	return out
}

// agentCostValue is one agent's priced cost.
func agentCostValue(s agentSpend, id string) schemas.Value {
	switch {
	case s.unpriced[id] > 0:
		return schemas.Unknown(fmt.Sprintf("%d cost rows of agent %s unpriced", s.unpriced[id], id), schemas.EvidenceGateway)
	case s.unpriced[""] > 0:
		return schemas.Unknown(fmt.Sprintf("%d unpriced cost rows map to no agent", s.unpriced[""]), schemas.EvidenceGateway)
	case s.rows[""] > 0:
		lo := s.byAgent[id]
		return schemas.UnknownWithin(fmt.Sprintf("%d cost rows map to no agent", s.rows[""]), schemas.EvidenceGateway, lo, lo+s.byAgent[""])
	}
	return schemas.Observed(s.byAgent[id], schemas.EvidenceGateway)
}

// tokensByAgent returns each agent's tokens over the message rows placed on
// it.
func tokensByAgent(ev *schemas.Evidence, t *schemas.AgentTree) func(id string) schemas.Value {
	agentOf := requestAgents(ev, t)
	sum, missing, rows := map[string]int64{}, map[string]int{}, map[string]int{}
	for _, r := range messageRows(ev.Requests.Requests) {
		a := agentOf(r.ID)
		rows[a]++
		if r.Tokens == nil {
			missing[a]++
			continue
		}
		sum[a] += r.Tokens.Fresh + r.Tokens.CacheRead + r.Tokens.CacheWrite + r.Tokens.Out
	}
	return func(id string) schemas.Value {
		switch {
		case missing[id] > 0:
			return schemas.Unknown(fmt.Sprintf("%d message requests of agent %s have no usage", missing[id], id), schemas.EvidenceGateway)
		case missing[""] > 0:
			return schemas.Unknown(fmt.Sprintf("%d message requests without usage map to no agent", missing[""]), schemas.EvidenceGateway)
		case rows[""] > 0:
			lo := float64(sum[id])
			return schemas.UnknownWithin(fmt.Sprintf("%d message requests map to no agent", rows[""]), schemas.EvidenceGateway, lo, lo+float64(sum[""]))
		}
		return schemas.Observed(float64(sum[id]), schemas.EvidenceGateway)
	}
}

// stepCounts returns each agent's timeline rows of one kind. Rows of that
// kind with no agent id make every count a bound.
func stepCounts(steps []schemas.StepRecord, kind string) func(id string) schemas.Value {
	n := map[string]int{}
	for _, s := range steps {
		if s.Kind == kind {
			n[s.AgentID]++
		}
	}
	return func(id string) schemas.Value {
		if u := n[""]; u > 0 {
			return schemas.UnknownWithin(fmt.Sprintf("%d %s rows carry no agent id", u, kind), schemas.EvidenceNative, float64(n[id]), float64(n[id]+u))
		}
		return schemas.Observed(float64(n[id]), schemas.EvidenceNative)
	}
}

// agentKeys maps each agent id to the name part of its derived values:
// lower case, every character outside [a-z0-9_-] an underscore, at most
// maxAgentKey long. Two ids that sanitize alike get _2, _3, ... in id order.
func agentKeys(t *schemas.AgentTree) map[string]string {
	ids := make([]string, 0, len(t.Agents))
	for _, a := range t.Agents {
		ids = append(ids, a.ID)
	}
	sort.Strings(ids)
	out, used := map[string]string{}, map[string]bool{}
	for _, id := range ids {
		if _, ok := out[id]; ok {
			continue
		}
		key := sanitizeAgentID(id)
		for n := 2; used[key]; n++ {
			key = fmt.Sprintf("%s_%d", sanitizeAgentID(id), n)
		}
		used[key], out[id] = true, key
	}
	return out
}

// sanitizeAgentID returns id in the derived-name charset [a-z0-9_-].
func sanitizeAgentID(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
		if b.Len() >= maxAgentKey {
			break
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// duplicateWork is the share of distinct writes and tool calls that two or
// more agents did. A write is a file_change row, or a tool_request row of an
// edit tool with the paths its gateway reference names; a tool call is its
// name plus its argument hash from the gateway reference.
func duplicateWork(_ *schemas.AgentTree, ev *schemas.Evidence) schemas.Value {
	refs := toolRefs(ev.Requests)
	writers, calls := map[string]map[string]bool{}, map[string]map[string]bool{}
	attributed := 0
	for _, s := range ev.Steps {
		if s.AgentID == "" {
			continue
		}
		attributed++
		switch s.Kind {
		case schemas.StepFileChange:
			addAgent(writers, workspacePath(s.Path), s.AgentID)
		case schemas.StepToolRequest:
			ref, ok := refs[s.ToolCallID]
			if !ok {
				continue
			}
			name := s.ToolName
			if name == "" {
				name = ref.Name
			}
			if ref.ArgsHash != "" {
				addAgent(calls, name+"\x00"+ref.ArgsHash, s.AgentID)
			}
			if isEdit(schemas.Event{ToolName: name}) {
				for _, p := range refPaths(ref) {
					addAgent(writers, workspacePath(p), s.AgentID)
				}
			}
		}
	}
	if attributed == 0 {
		return schemas.Unknown(reasonNoAttributed, schemas.EvidenceNative)
	}
	den := len(writers) + len(calls)
	if den == 0 {
		return schemas.Unknown(reasonNoWork, schemas.EvidenceNative)
	}
	dup := 0
	for _, set := range []map[string]map[string]bool{writers, calls} {
		for _, agents := range set {
			if len(agents) >= 2 {
				dup++
			}
		}
	}
	out := schemas.Observed(float64(dup)/float64(den), schemas.EvidenceNative)
	out.EvidenceLevel = schemas.EvidencePartial
	return out
}

// toolRefs indexes the tool calls the gateway rows requested by id.
func toolRefs(log *schemas.RequestLog) map[string]schemas.ToolCallRef {
	out := map[string]schemas.ToolCallRef{}
	if log == nil {
		return out
	}
	for _, r := range log.Requests {
		for _, tc := range r.ToolCalls {
			if tc.ID != "" {
				out[tc.ID] = tc
			}
		}
	}
	return out
}

// refPaths returns the file paths a requested call names: its Paths hint,
// one per line, and its target.
func refPaths(tc schemas.ToolCallRef) []string {
	var out []string
	for _, p := range strings.Split(tc.Paths, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if tc.Target != "" {
		out = append(out, tc.Target)
	}
	return out
}

// workspacePath makes a path relative to the workspace root, so an absolute
// requested path and an observed workspace-relative change compare equal.
func workspacePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.TrimPrefix(path.Clean(p), "/workspace/")
	return strings.TrimPrefix(p, "./")
}

// addAgent records that agent did key; an empty key is ignored.
func addAgent(set map[string]map[string]bool, key, agent string) {
	if key == "" || key == "." {
		return
	}
	if set[key] == nil {
		set[key] = map[string]bool{}
	}
	set[key][agent] = true
}

// span is one agent's start and end.
type span struct{ start, end time.Time }

// agentSpans returns every agent's span, or the reason one is missing.
func agentSpans(agents []schemas.AgentNode) ([]span, string) {
	out := make([]span, 0, len(agents))
	for _, a := range agents {
		switch {
		case a.Started.IsZero() || a.Ended.IsZero():
			return nil, fmt.Sprintf("agent %s has no start or end time", a.ID)
		case a.Ended.Before(a.Started):
			return nil, fmt.Sprintf("agent %s ended before it started", a.ID)
		}
		out = append(out, span{a.Started, a.Ended})
	}
	return out, ""
}

// busyAndWall returns the summed span lengths and the span from the earliest
// start to the latest end.
func busyAndWall(spans []span) (time.Duration, time.Duration) {
	if len(spans) == 0 {
		return 0, 0
	}
	var busy time.Duration
	first, last := spans[0].start, spans[0].end
	for _, s := range spans {
		busy += s.end.Sub(s.start)
		if s.start.Before(first) {
			first = s.start
		}
		if s.end.After(last) {
			last = s.end
		}
	}
	return busy, last.Sub(first)
}

// serialCollapse is 1 when the spawned agents (every member of a team) ran
// one after another: two intervals that only touch do not overlap.
func serialCollapse(t *schemas.AgentTree, _ *schemas.Evidence) schemas.Value {
	var spawned []schemas.AgentNode
	for _, a := range t.Agents {
		if t.Shape == schemas.AgentShapeTeam || a.ParentID != "" || a.Depth > 0 {
			spawned = append(spawned, a)
		}
	}
	if len(spawned) < 2 {
		return schemas.Unknown(fmt.Sprintf("%d spawned agents: serial collapse needs 2 or more", len(spawned)), schemas.EvidenceNative)
	}
	spans, reason := agentSpans(spawned)
	if reason != "" {
		return schemas.Unknown(reason, schemas.EvidenceNative)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	end := spans[0].end
	for _, s := range spans[1:] {
		if s.start.Before(end) {
			return treeValue(0, t)
		}
		if s.end.After(end) {
			end = s.end
		}
	}
	return treeValue(1, t)
}

// spawn is one agent starting another, with the payload hash of the
// delegation when known.
type spawn struct{ from, to, payload string }

// delegationLoops counts spawn edges that loop: the started agent has the
// name and role of its spawner or an ancestor of it (or is one, by id), or
// the edge repeats an earlier spawn from the same parent with the same
// non-empty payload hash. Each edge counts once. A tree without spawn edges
// falls back to the parent links of its agents.
func delegationLoops(t *schemas.AgentTree) int {
	nodes := map[string]schemas.AgentNode{}
	for _, a := range t.Agents {
		nodes[a.ID] = a
	}
	var spawns []spawn
	for _, e := range t.Edges {
		if e.Kind == schemas.AgentEdgeSpawn {
			spawns = append(spawns, spawn{e.From, e.To, e.PayloadSHA256})
		}
	}
	if len(spawns) == 0 {
		for _, a := range t.Agents {
			if a.ParentID != "" {
				spawns = append(spawns, spawn{from: a.ParentID, to: a.ID})
			}
		}
	}
	seen := map[string]bool{}
	loops := 0
	for _, s := range spawns {
		key := s.from + "\x00" + s.payload
		repeat := s.payload != "" && seen[key]
		if s.payload != "" {
			seen[key] = true
		}
		if repeat || spawnsAncestor(nodes, s) {
			loops++
		}
	}
	return loops
}

// spawnsAncestor reports whether s starts an agent that is its spawner or an
// ancestor of it, by id or by the same name and role.
func spawnsAncestor(nodes map[string]schemas.AgentNode, s spawn) bool {
	key := ""
	if to, ok := nodes[s.to]; ok && (to.Name != "" || to.Role != "") {
		key = to.Name + "\x00" + to.Role
	}
	visited := map[string]bool{}
	for cur := s.from; cur != "" && !visited[cur]; {
		visited[cur] = true
		if cur == s.to {
			return true
		}
		n, ok := nodes[cur]
		if !ok {
			return false
		}
		if key != "" && n.Name+"\x00"+n.Role == key {
			return true
		}
		cur = n.ParentID
	}
	return false
}

// coverageByStrength returns attribution_coverage_<strength> for proven,
// declared and reported rows, when the tree counted rows and split them.
func coverageByStrength(ev *schemas.Evidence) map[string]schemas.Value {
	t := ev.Agents
	if _, bad := treeProblem(t); bad || t.Coverage.Steps <= 0 || (t.Coverage.ByStrength == nil && t.Coverage.Attributed > 0) {
		return nil
	}
	out := map[string]schemas.Value{}
	for _, s := range []string{schemas.AgentStrengthProven, schemas.AgentStrengthDeclared, schemas.AgentStrengthReported} {
		out[AttributionCoveragePrefix+s] = treeValue(float64(t.Coverage.ByStrength[s])/float64(t.Coverage.Steps), t)
	}
	return out
}

// budgetExceeded compares the run's priced cost with its arm's cap. An
// unknown cost whose bounds lie wholly above or at-or-below the cap still
// decides.
func budgetExceeded(rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
	if rc == nil || rc.Agent == nil || rc.Agent.Budgets.USD <= 0 {
		return schemas.Unknown(reasonNoCap, schemas.EvidenceGateway)
	}
	limit := rc.Agent.Budgets.USD
	cost, err := newCostUSDMetric().Compute(context.Background(), rc, ev)
	if err != nil {
		return schemas.Unknown(fmt.Sprintf("run cost: %v", err), schemas.EvidenceGateway)
	}
	flag := func(over bool) schemas.Value {
		if over {
			return schemas.Observed(1, schemas.EvidenceGateway)
		}
		return schemas.Observed(0, schemas.EvidenceGateway)
	}
	if c, ok := cost.Float(); ok {
		return flag(c > limit+capTolerance)
	}
	if b := cost.Bounds; b != nil {
		switch {
		case b[0] > limit+capTolerance:
			return flag(true)
		case b[1] <= limit+capTolerance:
			return flag(false)
		}
	}
	return schemas.Unknown(fmt.Sprintf("run cost unknown against the %.2f USD cap: %s", limit, cost.Reason), schemas.EvidenceGateway)
}
