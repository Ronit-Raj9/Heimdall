package evidence

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Agent events area (docs/agent-events.md): agent-reported events join the
// step timeline as rows of their own source (agent_reported, proof partial).
// A row never counts toward agent_steps: when a trusted witness saw the same
// action, that witness's row already counts it and the reported row only
// links to it (witness_of) with corroboration "corroborated"; when none did,
// the row is the agent's word alone ("agent_reported_only"). Reported rows
// keep the agent's nesting (parent_seq), so subagent runs and handoffs show
// as a tree. A run with no reported events gets its timeline unchanged.

// reportedWindow is the slack around a reported step's start and end within
// which a trusted witness counts as the same action.
const reportedWindow = time.Second

// joinReported adds agent-reported rows to a finished timeline and returns
// the timeline renumbered. Existing rows keep their order among themselves.
func joinReported(steps []schemas.StepRecord, ev *schemas.Evidence) []schemas.StepRecord {
	var reported []schemas.Event
	for _, e := range ev.Events {
		if e.Source == schemas.SourceAgentReported {
			reported = append(reported, e)
		}
	}
	if len(reported) == 0 {
		return steps
	}
	gatewayComplete := ev.Requests != nil && ev.Requests.Complete
	type turn struct {
		seq, step  int
		start, end time.Time
	}
	var turns []turn
	requests := map[string]int{} // tool call id -> index of the request row
	for i, s := range steps {
		switch {
		case s.Kind == schemas.StepModelTurn && !s.IsError:
			end := s.TS
			if s.DurationMS != nil {
				end = s.TS.Add(time.Duration(*s.DurationMS) * time.Millisecond)
			}
			turns = append(turns, turn{seq: i, step: s.Turn, start: s.TS, end: end})
		case s.Kind == schemas.StepToolRequest && s.ToolCallID != "":
			requests[s.ToolCallID] = i
		}
	}
	turnOf := func(t time.Time) int {
		n := 0
		for _, tr := range turns {
			if !tr.end.After(t) {
				n = tr.step
			}
		}
		return n
	}
	used := map[int]bool{} // trusted rows already matched by a report
	// rows: existing rows first (index i), then reported rows (index
	// len(steps)+k). Links hold indices until renumbering.
	type rrow struct {
		schemas.StepRecord
		witnessOf int // index + 1
		parent    int // index + 1
	}
	all := make([]rrow, 0, len(steps)+len(reported))
	for _, s := range steps {
		all = append(all, rrow{StepRecord: s})
	}
	byEvent := map[string]int{}
	for _, e := range reported {
		r := rrow{}
		r.TS, r.Kind, r.Source, r.Proof = e.TS, schemas.StepAgentEvent, schemas.StepSourceReported, schemas.EvidencePartial
		r.Turn, r.EventType, r.Channel, r.ReportID = turnOf(e.TS), e.ReportedType, e.Channel, e.ReportedID
		r.ToolCallID, r.ToolName, r.DurationMS, r.IsError = e.ToolCallID, e.ToolName, e.DurationMS, e.IsError
		r.Summary = clip(utils.RedactSecrets(e.Summary), stepSummaryMax)
		r.Corroboration = schemas.ReportedOnly
		end := e.TS
		if e.DurationMS != nil {
			end = e.TS.Add(time.Duration(*e.DurationMS) * time.Millisecond)
		}
		lo, hi := e.TS.Add(-reportedWindow), end.Add(reportedWindow)
		in := func(t time.Time) bool { return !t.Before(lo) && !t.After(hi) }
		match := -1
		switch e.ReportedType {
		case agentevents.TypeToolCall, agentevents.TypeToolResult:
			match = matchReportedTool(steps, used, e, in)
			if match >= 0 && steps[match].Source == schemas.StepSourceToolProxy && steps[match].IsError != e.IsError {
				r.Corroboration = schemas.ReportedConflicts
				r.Conflict = fmt.Sprintf("the agent reports error=%t, the tool proxy ledger says error=%t", e.IsError, steps[match].IsError)
			}
			if ri, ok := requests[e.ToolCallID]; ok && match < 0 && steps[ri].Execution == schemas.ExecutedWithProof {
				// The model requested this call and a trusted witness saw it
				// run; the report names the same call id.
				match = ri
			}
			if _, ok := requests[e.ToolCallID]; !ok && e.ToolCallID != "" && gatewayComplete && e.ReportedType == agentevents.TypeToolCall && match < 0 {
				r.Conflict = "the agent reports a tool call id the complete gateway log does not have"
			}
		case agentevents.TypeLLM:
			for _, tr := range turns {
				if !used[tr.seq] && in(tr.start) {
					match = tr.seq
					break
				}
			}
		}
		if match >= 0 {
			used[match] = true
			r.witnessOf = match + 1
			if r.Corroboration != schemas.ReportedConflicts {
				r.Corroboration = schemas.Corroborated
			}
		}
		byEvent[e.EventID] = len(all)
		all = append(all, r)
	}
	for k, e := range reported {
		if e.ParentEventID == "" {
			continue
		}
		if pi, ok := byEvent[e.ParentEventID]; ok && pi != len(steps)+k {
			all[len(steps)+k].parent = pi + 1
		}
	}
	order := make([]int, len(all))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ra, rb := all[order[a]], all[order[b]]
		if !ra.TS.Equal(rb.TS) {
			return ra.TS.Before(rb.TS)
		}
		return order[a] < order[b]
	})
	newSeq := make([]int, len(all))
	for n, i := range order {
		newSeq[i] = n + 1
	}
	oldToIdx := map[int]int{}
	for i, s := range steps {
		oldToIdx[s.Seq] = i
	}
	remap := func(old int) int {
		if i, ok := oldToIdx[old]; ok {
			return newSeq[i]
		}
		return 0
	}
	out := make([]schemas.StepRecord, len(all))
	for n, i := range order {
		r := all[i].StepRecord
		r.Seq = n + 1
		if i < len(steps) {
			if r.WitnessOf > 0 {
				r.WitnessOf = remap(r.WitnessOf)
			}
			if len(r.WitnessedBy) > 0 {
				wb := make([]int, len(r.WitnessedBy))
				for j, w := range r.WitnessedBy {
					wb[j] = remap(w)
				}
				sort.Ints(wb)
				r.WitnessedBy = wb
			}
		} else {
			if all[i].witnessOf > 0 {
				r.WitnessOf = newSeq[all[i].witnessOf-1]
			}
			if all[i].parent > 0 {
				r.ParentSeq = newSeq[all[i].parent-1]
			}
			r.TS = r.TS.UTC()
			r.Summary, r.ToolName = utils.RedactSecrets(r.Summary), utils.RedactSecrets(r.ToolName)
		}
		out[n] = r
	}
	return out
}

// matchReportedTool returns the index of a trusted row that witnessed the
// reported tool step, or -1: a tool proxy ledger row whose tool name fits,
// else a command root whose command line holds the reported input, inside
// the window and not yet matched by another report.
func matchReportedTool(steps []schemas.StepRecord, used map[int]bool, e schemas.Event, in func(time.Time) bool) int {
	for i, s := range steps {
		if used[i] || s.Source != schemas.StepSourceToolProxy || !in(s.TS) {
			continue
		}
		server, tool, _ := strings.Cut(s.Summary, " ")
		if s.ToolName != "" {
			tool = s.ToolName
		}
		if toolNameFits(e.ToolName, server, tool) {
			return i
		}
	}
	want := strings.Join(strings.Fields(e.Target), " ")
	if len(want) < 3 {
		return -1
	}
	for i, s := range steps {
		if used[i] || s.Source != schemas.StepSourceProcess || s.Op == "runtime" || !in(s.TS) {
			continue
		}
		if strings.Contains(strings.Join(strings.Fields(s.Summary), " "), want) {
			return i
		}
	}
	return -1
}
