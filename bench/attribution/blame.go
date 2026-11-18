// Package attribution answers "which agent of a multi-agent run caused its
// failure" (docs/multi-agent-safety.md). It builds blame.json, a child
// artifact beside a sealed run, in two tiers:
//
//   - gate_proven (tier 1): the first agent whose recorded step tripped a
//     firing gate, read from the sealed gates, the step timeline's agent ids
//     and the run's ledgers. Deterministic and free.
//   - replay_validated (tier 2): the run's slot is run again with the
//     suspected agent replaced by a scripted correct stub; the blame is
//     validated when the outcome flips (replay.go). Opt-in, costs a run.
//
// A third tier, an LLM judge reading the transcript, is deliberately not
// built: the best published step-level attribution accuracy is 14.2%
// (Who&When, arXiv 2505.00212).
//
// Everything here is pure: no I/O except in artifact.go, no logging.
package attribution

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Version is the version of the blame rules and of blame.json. Bump it with
// any change to how a candidate is found or chosen.
const Version = "1.0.0"

// BlameFile is the child artifact's file name.
const BlameFile = "blame.json"

// Tiers of a blame, strongest first. TierNone means no agent could be proven
// responsible; TierSingleAgent means the run had one agent, so there is
// nothing to attribute.
const (
	TierReplayValidated = "replay_validated"
	TierGateProven      = "gate_proven"
	TierNone            = "unattributed"
	TierSingleAgent     = "single_agent"
)

// Evidence reference prefixes a gate may emit so attribution can place it.
// Gates of this round emit agent: and step: next to their own references;
// older gates' references (event ids, request ids, tool_call:, diff:, bare
// paths) are resolved through the timeline.
const (
	RefAgent    = "agent:"
	RefStep     = "step:"
	RefMessage  = "message:"
	RefToolCall = "tool_call:"
	RefRequest  = "request:"
	RefDiff     = "diff:"
)

// Blame is blame.json: which agent of one run is responsible for its failure
// or its fired gates, how that is known, and the counterfactual check when
// one ran. AgentID is empty unless Tier is gate_proven or replay_validated.
type Blame struct {
	Version     string                 `json:"version"`
	RunID       string                 `json:"run_id"`
	ExpID       string                 `json:"exp_id,omitempty"`
	ParentSeal  string                 `json:"parent_seal,omitempty"` // seal hash of the run this describes
	Outcome     string                 `json:"outcome"`
	Failed      bool                   `json:"failed"` // outcome not pass, or a veto or report gate fired
	Tier        string                 `json:"tier"`
	AgentID     string                 `json:"agent_id,omitempty"`
	Role        string                 `json:"role,omitempty"`
	Gate        string                 `json:"gate,omitempty"`
	StepSeq     int                    `json:"step_seq,omitempty"`
	At          time.Time              `json:"at,omitzero"`
	AgentSource string                 `json:"agent_source,omitempty"` // how the step's agent is known (schemas.AgentSource*)
	Strength    string                 `json:"strength,omitempty"`     // proven | declared | reported
	Evidence    []string               `json:"evidence,omitempty"`
	Detail      string                 `json:"detail"`
	Candidates  []Candidate            `json:"candidates,omitempty"` // every placed reference of every fired gate, earliest first
	Unplaced    []string               `json:"unplaced,omitempty"`   // fired gates' references no agent could be found for
	Replay      *Replay                `json:"replay,omitempty"`
	Coverage    *schemas.AgentCoverage `json:"coverage,omitempty"` // the run's step attribution coverage
	Notes       []string               `json:"notes,omitempty"`
}

// Candidate is one fired gate's reference placed on an agent.
type Candidate struct {
	AgentID     string    `json:"agent_id"`
	Role        string    `json:"role,omitempty"`
	Gate        string    `json:"gate"`
	Effect      string    `json:"effect"`
	Ref         string    `json:"ref"`
	StepSeq     int       `json:"step_seq,omitempty"`
	At          time.Time `json:"at,omitzero"`
	AgentSource string    `json:"agent_source,omitempty"`
}

// Replay is the tier 2 counterfactual: the slot run again with Suspect
// replaced by Stub. Status is not_run, flipped (the failure went away, which
// validates the blame), held (it did not: the suspect alone does not explain
// it) or error.
type Replay struct {
	Status        string   `json:"status"`
	Suspect       string   `json:"suspect"`
	Stub          string   `json:"stub"`
	ExpID         string   `json:"exp_id,omitempty"`
	RunID         string   `json:"run_id,omitempty"`
	OutcomeBefore string   `json:"outcome_before"`
	OutcomeAfter  string   `json:"outcome_after,omitempty"`
	GatesAfter    []string `json:"gates_after,omitempty"` // gates that still fired in the replay
	CostUSD       *float64 `json:"cost_usd,omitempty"`
	Detail        string   `json:"detail,omitempty"`
}

// Replay statuses.
const (
	ReplayNotRun  = "not_run"
	ReplayFlipped = "flipped"
	ReplayHeld    = "held"
	ReplayError   = "error"
)

// Input is what tier 1 reads: the sealed run's outcome and gates and its
// sealed evidence. Nothing else.
type Input struct {
	RunID      string
	ExpID      string
	ParentSeal string
	Outcome    schemas.Outcome
	Gates      []schemas.GateResult
	Evidence   *schemas.Evidence
}

// Tier1 places every reference of every fired veto or report gate on the
// agent whose recorded step it is, and blames the earliest. Veto gates are
// preferred over report gates: the earliest placed veto reference wins, and
// a report gate's reference is chosen only when no veto reference could be
// placed. A run with fired gates none of which could be placed, or a failed
// run with no fired gate, is unattributed: a verifier failure alone does not
// say which agent caused it.
func Tier1(in Input) *Blame {
	b := &Blame{Version: Version, RunID: in.RunID, ExpID: in.ExpID, ParentSeal: in.ParentSeal, Outcome: string(in.Outcome)}
	fired := firedGates(in.Gates)
	b.Failed = in.Outcome != schemas.OutcomePass || len(fired) > 0
	ev := in.Evidence
	if ev == nil || ev.Agents == nil || len(ev.Agents.Agents) < 2 {
		b.Tier, b.Detail = TierSingleAgent, "the run has one agent; nothing to attribute"
		return b
	}
	cov := ev.Agents.Coverage
	b.Coverage = &cov
	if !b.Failed {
		b.Tier, b.Detail = TierNone, "the run passed and no gate fired"
		return b
	}
	if len(fired) == 0 {
		b.Tier = TierNone
		b.Detail = fmt.Sprintf("outcome %s with no fired gate: the verifier failed the run but no recorded step proves which agent caused it", in.Outcome)
		return b
	}
	ix := newIndex(ev)
	for _, g := range fired {
		for _, ref := range g.Evidence {
			c, ok := ix.place(ref)
			if !ok {
				b.Unplaced = append(b.Unplaced, g.Gate+" "+ref)
				continue
			}
			c.Gate, c.Effect, c.Ref = g.Gate, string(g.Effect), ref
			c.Role = ix.role(c.AgentID)
			b.Candidates = append(b.Candidates, c)
		}
	}
	sort.SliceStable(b.Candidates, func(i, j int) bool { return earlier(b.Candidates[i], b.Candidates[j]) })
	b.Candidates = dedupe(b.Candidates)
	pick, ok := choose(b.Candidates)
	if !ok {
		b.Tier = TierNone
		b.Detail = fmt.Sprintf("%d gate(s) fired but none of their references names a step with an agent id", len(fired))
		return b
	}
	b.Tier, b.AgentID, b.Role, b.Gate = TierGateProven, pick.AgentID, pick.Role, pick.Gate
	b.StepSeq, b.At, b.AgentSource = pick.StepSeq, pick.At, pick.AgentSource
	b.Strength = schemas.AgentSourceStrength(pick.AgentSource)
	b.Evidence = []string{pick.Ref}
	b.Detail = fmt.Sprintf("%s fired; its earliest placed reference %s belongs to agent %s", pick.Gate, pick.Ref, pick.AgentID)
	if pick.StepSeq > 0 {
		b.Detail += " (step " + strconv.Itoa(pick.StepSeq) + ")"
	}
	if b.Strength != schemas.AgentStrengthProven {
		b.Notes = append(b.Notes, "the step's agent is "+b.Strength+" ("+pick.AgentSource+"), not proven by a per-agent gateway key")
	}
	return b
}

// firedGates returns the fired gates that can change or flag a run (veto and
// report), veto first, in the order they were recorded.
func firedGates(gates []schemas.GateResult) []schemas.GateResult {
	var veto, report []schemas.GateResult
	for _, g := range gates {
		if !g.Fired {
			continue
		}
		switch g.Effect {
		case schemas.GateVeto:
			veto = append(veto, g)
		case schemas.GateReport:
			report = append(report, g)
		}
	}
	return append(veto, report...)
}

// choose picks the earliest veto candidate, else the earliest report one.
func choose(cs []Candidate) (Candidate, bool) {
	for _, c := range cs {
		if c.Effect == string(schemas.GateVeto) {
			return c, true
		}
	}
	if len(cs) > 0 {
		return cs[0], true
	}
	return Candidate{}, false
}

// earlier orders candidates by time, then step, with unknown times last.
func earlier(a, b Candidate) bool {
	switch {
	case a.At.IsZero() != b.At.IsZero():
		return !a.At.IsZero()
	case !a.At.Equal(b.At):
		return a.At.Before(b.At)
	case a.StepSeq != b.StepSeq:
		return a.StepSeq < b.StepSeq
	}
	return false
}

// dedupe drops repeated (gate, ref) pairs, keeping the first.
func dedupe(cs []Candidate) []Candidate {
	seen := map[string]bool{}
	out := cs[:0:0]
	for _, c := range cs {
		k := c.Gate + "\x00" + c.Ref
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}

// index resolves gate references to timeline rows and agents.
type index struct {
	ev       *schemas.Evidence
	roles    map[string]string
	bySeq    map[int]schemas.StepRecord
	byReq    map[string]schemas.StepRecord
	byCallID map[string]schemas.StepRecord
	reqAgent map[string]string
	events   map[string]schemas.Event
}

func newIndex(ev *schemas.Evidence) *index {
	ix := &index{ev: ev, roles: map[string]string{}, bySeq: map[int]schemas.StepRecord{}, byReq: map[string]schemas.StepRecord{},
		byCallID: map[string]schemas.StepRecord{}, reqAgent: map[string]string{}, events: map[string]schemas.Event{}}
	for _, a := range ev.Agents.Agents {
		ix.roles[a.ID] = a.Role
	}
	for _, s := range ev.Steps {
		ix.bySeq[s.Seq] = s
		if s.RequestID != "" && s.AgentID != "" {
			if _, ok := ix.byReq[s.RequestID]; !ok {
				ix.byReq[s.RequestID] = s
			}
		}
		if s.ToolCallID != "" && s.AgentID != "" {
			if _, ok := ix.byCallID[s.ToolCallID]; !ok {
				ix.byCallID[s.ToolCallID] = s
			}
		}
	}
	if ev.Requests != nil {
		for _, r := range ev.Requests.Requests {
			if r.AgentID != "" {
				ix.reqAgent[r.ID] = r.AgentID
			}
		}
	}
	for _, e := range ev.Events {
		if e.EventID != "" {
			ix.events[e.EventID] = e
		}
	}
	return ix
}

func (ix *index) role(id string) string { return ix.roles[id] }

// place finds the agent and step a reference points at.
func (ix *index) place(ref string) (Candidate, bool) {
	switch {
	case strings.HasPrefix(ref, RefAgent):
		id := strings.TrimPrefix(ref, RefAgent)
		if _, known := ix.roles[id]; !known {
			return Candidate{}, false
		}
		return Candidate{AgentID: id, AgentSource: ix.agentSource(id)}, true
	case strings.HasPrefix(ref, RefStep):
		n, err := strconv.Atoi(strings.TrimPrefix(ref, RefStep))
		if err != nil {
			return Candidate{}, false
		}
		return fromStep(ix.bySeq[n])
	case strings.HasPrefix(ref, RefMessage):
		return ix.placeMessage(strings.TrimPrefix(ref, RefMessage))
	case strings.HasPrefix(ref, RefToolCall):
		return ix.placeToolCall(strings.TrimPrefix(ref, RefToolCall))
	case strings.HasPrefix(ref, RefRequest):
		return ix.placeRequest(strings.TrimPrefix(ref, RefRequest))
	case strings.HasPrefix(ref, RefDiff):
		return ix.placePath(strings.TrimPrefix(ref, RefDiff))
	}
	if e, ok := ix.events[ref]; ok {
		return ix.placeEvent(e)
	}
	if c, ok := ix.placeRequest(ref); ok {
		return c, true
	}
	return ix.placePath(ref)
}

// agentSource is the strongest source that saw agent id.
func (ix *index) agentSource(id string) string {
	for _, a := range ix.ev.Agents.Agents {
		if a.ID == id {
			return a.Source
		}
	}
	return ""
}

func fromStep(s schemas.StepRecord) (Candidate, bool) {
	if s.AgentID == "" {
		return Candidate{}, false
	}
	return Candidate{AgentID: s.AgentID, StepSeq: s.Seq, At: s.TS, AgentSource: s.AgentSource}, true
}

func (ix *index) placeRequest(id string) (Candidate, bool) {
	if s, ok := ix.byReq[id]; ok {
		return fromStep(s)
	}
	if a, ok := ix.reqAgent[id]; ok {
		return Candidate{AgentID: a, AgentSource: schemas.AgentSourceGatewayKey}, true
	}
	return Candidate{}, false
}

// placeEvent places an agent-stream or reported event through its tool call
// id or its request id.
func (ix *index) placeEvent(e schemas.Event) (Candidate, bool) {
	if e.ToolCallID != "" {
		if s, ok := ix.byCallID[e.ToolCallID]; ok {
			return fromStep(s)
		}
	}
	if e.RequestID != "" {
		return ix.placeRequest(e.RequestID)
	}
	return Candidate{}, false
}

// placeToolCall places a tool proxy ledger row: the ledger's own agent id
// when it recorded one, else the tool_proxy timeline row with the same start
// and server/tool summary.
func (ix *index) placeToolCall(seqText string) (Candidate, bool) {
	n, err := strconv.Atoi(seqText)
	if err != nil {
		return Candidate{}, false
	}
	for _, c := range ix.ev.ToolCalls {
		if c.Seq != n {
			continue
		}
		if c.AgentID != "" {
			// The tool proxy stamped the member from its own route token.
			return Candidate{AgentID: c.AgentID, At: c.Started, AgentSource: schemas.AgentSourceGatewayKey}, true
		}
		for _, s := range ix.ev.Steps {
			if s.Source == schemas.StepSourceToolProxy && s.TS.Equal(c.Started) && s.ToolName == c.Tool {
				return fromStep(s)
			}
		}
	}
	return Candidate{}, false
}

// placeMessage places a mailbox message on its sender: the member whose
// own route the send came in on (proven), else the from field it wrote
// (reported).
func (ix *index) placeMessage(seqText string) (Candidate, bool) {
	n, err := strconv.Atoi(seqText)
	if err != nil {
		return Candidate{}, false
	}
	for _, m := range ix.ev.Messages {
		if m.Seq != n {
			continue
		}
		if m.SenderAgentID != "" {
			return Candidate{AgentID: m.SenderAgentID, At: m.At, AgentSource: schemas.AgentSourceGatewayKey}, true
		}
		if _, known := ix.roles[m.From]; known {
			return Candidate{AgentID: m.From, At: m.At, AgentSource: schemas.AgentSourceSelfReport}, true
		}
	}
	return Candidate{}, false
}

// placePath places a workspace path on the first file change to it that has
// an agent id.
func (ix *index) placePath(p string) (Candidate, bool) {
	p = strings.TrimPrefix(strings.TrimSpace(p), "./")
	if p == "" {
		return Candidate{}, false
	}
	var best schemas.StepRecord
	for _, s := range ix.ev.Steps {
		if s.Kind != schemas.StepFileChange || s.AgentID == "" || strings.TrimPrefix(s.Path, "./") != p {
			continue
		}
		if best.Seq == 0 || s.TS.Before(best.TS) {
			best = s
		}
	}
	return fromStep(best)
}
