package evidence

import (
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// placement is the agent a timeline row belongs to and how that is known.
type placement struct {
	agent, source string
}

// attribute sets AgentID and AgentSource on each timeline row, strongest
// source first, and counts the coverage. Rows that only witness a counted
// row (WitnessOf) take that row's agent and are left out of the counts.
//
//   - Gateway rows (model turns, attempts, requested calls): the member key
//     or declared label of the gateway row with the same request id, or of
//     the logical request it belongs to.
//   - Tool proxy rows: the member route the call came in on
//     (ToolCall.AgentID).
//   - Agent-stream rows and requested calls the gateway could not place: the
//     transcript's agent for the same tool call id.
//   - Agent-reported rows: the reported event's agent, else the nearest
//     reported parent row's.
//   - In an in-process run only, any row still unplaced: the deepest
//     subagent whose reported or transcript lifetime holds the row's time,
//     when exactly one does; the main agent when none does. Team members run
//     at the same time, so a team's unplaced rows stay unknown.
func (b *agentBuilder) attribute(ev *schemas.Evidence, steps []schemas.StepRecord) schemas.AgentCoverage {
	byRequest := requestAgents(ev.Requests)
	byLedger := ledgerAgents(ev.ToolCalls)
	byCall, byReport := eventAgents(ev.Events)
	bySeq := map[int]int{}
	for i := range steps {
		bySeq[steps[i].Seq] = i
	}
	set := func(i int, p placement) {
		if p.agent != "" && steps[i].AgentID == "" {
			steps[i].AgentID, steps[i].AgentSource = p.agent, p.source
			b.node(p.agent, p.source)
		}
	}
	for i := range steps {
		s := &steps[i]
		switch s.Source {
		case schemas.StepSourceGateway:
			set(i, byRequest[s.RequestID])
			if s.ToolCallID != "" {
				set(i, byCall[s.ToolCallID])
			}
		case schemas.StepSourceToolProxy:
			set(i, byLedger[ledgerKey(s.TS, s.ToolName)])
		case schemas.StepSourceAgent:
			set(i, byCall[s.ToolCallID])
		case schemas.StepSourceReported:
			set(i, byReport[s.Channel+"\x00"+s.ReportID])
		}
	}
	// Reported rows nest: a row with no agent of its own takes its parent's.
	for pass := 0; pass < 8; pass++ {
		changed := false
		for i := range steps {
			if steps[i].AgentID != "" || steps[i].ParentSeq == 0 {
				continue
			}
			if j, ok := bySeq[steps[i].ParentSeq]; ok && steps[j].AgentID != "" {
				set(i, placement{steps[j].AgentID, steps[j].AgentSource})
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for i := range steps {
		if steps[i].AgentID != "" || steps[i].WitnessOf != 0 || b.shape != schemas.AgentShapeInProcess {
			continue
		}
		set(i, b.byTime(steps[i].TS))
	}
	for i := range steps {
		if steps[i].AgentID == "" && steps[i].WitnessOf != 0 {
			if j, ok := bySeq[steps[i].WitnessOf]; ok {
				set(i, placement{steps[j].AgentID, steps[j].AgentSource})
			}
		}
	}
	return b.coverage(steps)
}

// coverage counts the rows that are not witnesses of another row, by the
// source and the strength of their agent.
func (b *agentBuilder) coverage(steps []schemas.StepRecord) schemas.AgentCoverage {
	cov := schemas.AgentCoverage{BySource: map[string]int{}, ByStrength: map[string]int{}}
	for _, s := range steps {
		if s.WitnessOf != 0 {
			continue
		}
		cov.Steps++
		if s.AgentID == "" {
			cov.Unknown++
			continue
		}
		cov.Attributed++
		cov.BySource[s.AgentSource]++
		cov.ByStrength[schemas.AgentSourceStrength(s.AgentSource)]++
	}
	for _, src := range schemas.AgentSources {
		if b.avail[src] {
			cov.Available = append(cov.Available, src)
		}
	}
	if len(cov.BySource) == 0 {
		cov.BySource, cov.ByStrength = nil, nil
	}
	return cov
}

// byTime places a row of an in-process run by time: the deepest non-root
// agent whose known lifetime holds t, when exactly one does at that depth;
// the main agent when none does; nobody when two siblings overlap.
func (b *agentBuilder) byTime(t time.Time) placement {
	best, depth, tie := "", -1, false
	for _, id := range b.order {
		n := b.nodes[id]
		if n.ParentID == "" || n.Started.IsZero() || n.Ended.IsZero() || n.Source == schemas.AgentSourceGatewayKey {
			continue
		}
		if t.Before(n.Started) || t.After(n.Ended) {
			continue
		}
		switch {
		case n.Depth > depth:
			best, depth, tie = id, n.Depth, false
		case n.Depth == depth:
			tie = true
		}
	}
	if tie {
		return placement{}
	}
	if best == "" {
		root, ok := b.nodes[schemas.AgentRoot]
		if !ok {
			return placement{}
		}
		return placement{root.ID, root.Source}
	}
	return placement{best, b.nodes[best].Source}
}

// requestAgents maps every gateway row id, and the id of the logical request
// it belongs to, to the agent the row names.
func requestAgents(log *schemas.RequestLog) map[string]placement {
	out := map[string]placement{}
	if log == nil {
		return out
	}
	parents := map[string]string{}
	for _, r := range log.Requests {
		if r.ID != "" {
			parents[r.ID] = r.ParentID
		}
	}
	for _, r := range log.Requests {
		id, src := gatewayAgent(r)
		if id == "" {
			continue
		}
		p := placement{id, src}
		for _, k := range []string{r.ID, rootOf(r, parents)} {
			if cur, ok := out[k]; !ok || schemas.AgentSourceRank(src) < schemas.AgentSourceRank(cur.source) {
				out[k] = p
			}
		}
	}
	return out
}

// ledgerKey identifies a tool proxy row in the timeline: its start time and
// tool name, as BuildTimeline copies them.
func ledgerKey(t time.Time, tool string) string {
	return t.UTC().Format(time.RFC3339Nano) + "\x00" + tool
}

// ledgerAgents maps tool proxy rows to the member whose route they came in
// on.
func ledgerAgents(calls []schemas.ToolCall) map[string]placement {
	out := map[string]placement{}
	for _, c := range calls {
		if c.AgentID != "" {
			out[ledgerKey(c.Started, c.Tool)] = placement{c.AgentID, schemas.AgentSourceGatewayKey}
		}
	}
	return out
}

// eventAgents maps transcript tool call ids and reported event ids to the
// agent their events name.
func eventAgents(events []schemas.Event) (map[string]placement, map[string]placement) {
	byCall, byReport := map[string]placement{}, map[string]placement{}
	for _, e := range events {
		switch {
		case e.Source == schemas.SourceAgentStream && e.AgentID != "" && e.ToolCallID != "":
			byCall[e.ToolCallID] = placement{e.AgentID, schemas.AgentSourceTranscript}
		case e.Source == schemas.SourceAgentReported && e.AgentID != "" && e.ReportedID != "":
			id := e.AgentID
			if e.ReportedType == reportedHandoff {
				continue
			}
			byReport[e.Channel+"\x00"+e.ReportedID] = placement{id, reportedSource(e)}
		}
	}
	return byCall, byReport
}
