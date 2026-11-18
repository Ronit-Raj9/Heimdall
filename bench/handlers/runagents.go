package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/attribution"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Caps on what one RunAgents response carries.
const (
	maxHandoffText = 4 << 10 // bytes of one payload, sent or delivered
	maxSpanLabel   = 120     // runes of a span label
)

// plantedToken matches the bench's nonce shape: only planted text ever puts
// one in a run, so a handoff that carried one carried a planted fact.
var plantedToken = regexp.MustCompile(`nonce_[0-9a-z]{24}`)

// BlameReader is optionally implemented by a ReadIndex: it returns the
// blame.json child artifact of a run (attribution.Read), or nil when none
// was written. Without it, or without an artifact, the agents view computes
// tier 1 from the sealed files.
type BlameReader interface {
	RunBlame(expID, runID, sealHash string) (*attribution.Blame, error)
}

// RunAgents is the response of GET /api/bench/runs/{id}/agents: the agents
// of a multi-agent run, what each one did and cost, who handed what to
// whom, and which agent the run's failure is blamed on
// (docs/multi-agent-safety.md). Available is false for a single-agent run
// (no agents.json); its lists are then empty, never absent. Every string
// that came from an agent (labels, payloads) is inert display text.
type RunAgents struct {
	RunID     string             `json:"run_id"`
	Available bool               `json:"available"`
	Tree      *schemas.AgentTree `json:"tree,omitempty"`
	// Members has one row per agent of the tree, in tree order (depth first).
	Members []AgentStats `json:"members"`
	// Spans are the swimlane blocks: one per timeline row with an agent id,
	// placed in milliseconds from the run's first row. Critical marks the
	// blocks on the critical path (CriticalPath).
	Spans []AgentSpan `json:"spans"`
	// WallMS is the span from the first to the last timeline row; BusyMS
	// the sum of every agent's busy time, so BusyMS/WallMS is the
	// parallelism factor. CriticalMS is the length of the critical path.
	WallMS     int64 `json:"wall_ms"`
	BusyMS     int64 `json:"busy_ms"`
	CriticalMS int64 `json:"critical_ms"`
	// Handoffs are the tree's handoff, message, spawn and return edges in
	// time order, with the payload as sent and as delivered when the run
	// recorded them (clipped).
	Handoffs []AgentHandoff `json:"handoffs"`
	// Blame is the run's blame.json: the child artifact when one was
	// written (it may carry a replay), otherwise tier 1 computed from the
	// sealed gates and timeline. Absent for a single-agent run.
	Blame *attribution.Blame `json:"blame,omitempty"`
	// Truncated is true when the timeline was longer than one response
	// carries; spans then cover the first rows only.
	Truncated bool `json:"truncated,omitempty"`
}

// AgentStats is one agent's row in the per-agent table. A count is null
// when the run did not record the evidence behind it (for example tokens
// and cost need gateway rows with the agent's id), never zero.
type AgentStats struct {
	AgentID          string   `json:"agent_id"`
	Role             string   `json:"role,omitempty"`
	Model            string   `json:"model,omitempty"`
	ParentID         string   `json:"parent_id,omitempty"`
	Depth            int      `json:"depth"`
	Source           string   `json:"source"`   // strongest attribution source of the agent
	Strength         string   `json:"strength"` // proven | declared | reported
	Steps            int      `json:"steps"`    // timeline rows with this agent id
	ModelTurns       int      `json:"model_turns"`
	ToolCalls        int      `json:"tool_calls"`
	InputTokens      *int64   `json:"input_tokens"`
	OutputTokens     *int64   `json:"output_tokens"`
	CostUSD          *float64 `json:"cost_usd"`
	MessagesSent     int      `json:"messages_sent"`
	MessagesReceived int      `json:"messages_received"`
	BusyMS           int64    `json:"busy_ms"`
	Exit             string   `json:"exit,omitempty"`
}

// AgentSpan is one swimlane block.
type AgentSpan struct {
	AgentID  string `json:"agent_id"`
	Seq      int    `json:"seq"`  // the timeline row
	Kind     string `json:"kind"` // the row kind: model_turn, tool_request, tool_execution, command, file_change, ...
	StartMS  int64  `json:"start_ms"`
	EndMS    int64  `json:"end_ms"`
	Label    string `json:"label"` // clipped row summary
	IsError  bool   `json:"is_error,omitempty"`
	Critical bool   `json:"critical,omitempty"`
}

// AgentHandoff is one edge of the agent tree with what travelled on it.
// Sent is the payload as the sender sent it and Delivered as the receiver
// got it, both clipped; Altered is true when they differ (a lossy-handoff
// probe or a proxy truncation). Facts lists the probe's planted facts and
// whether each survived the edge.
type AgentHandoff struct {
	Index         int           `json:"index"`
	Kind          string        `json:"kind"` // spawn | handoff | message | claim | return
	From          string        `json:"from"`
	To            string        `json:"to,omitempty"`
	At            time.Time     `json:"at,omitzero"`
	AtMS          *int64        `json:"at_ms"` // ms from the run's first timeline row, as spans; null when either time is unknown
	Bytes         int64         `json:"bytes"`
	PayloadSHA256 string        `json:"payload_sha256,omitempty"`
	Sent          string        `json:"sent,omitempty"`
	Delivered     string        `json:"delivered,omitempty"`
	Altered       bool          `json:"altered,omitempty"`
	Ref           string        `json:"ref,omitempty"`
	Source        string        `json:"source"`
	Facts         []HandoffFact `json:"facts,omitempty"`
}

// HandoffFact is one planted fact checked on a handoff.
type HandoffFact struct {
	Fact      string `json:"fact"`
	Sent      bool   `json:"sent"`
	Delivered bool   `json:"delivered"`
}

// getRunAgents handles GET /api/bench/runs/{id}/agents. A single-agent run
// answers 200 with available false.
func (h *RunsHandler) getRunAgents(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	out, err := readRunAgents(h.index, rv)
	if err != nil {
		sendDepError(ctx, h.mode, "read agents of run "+rv.Run.RunID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// agentsEvidence is the part of evidence.json the agents view reads.
type agentsEvidence struct {
	Agents   *schemas.AgentTree     `json:"agents"`
	Messages []schemas.AgentMessage `json:"messages"`
	Requests *schemas.RequestLog    `json:"requests"`
	Cost     []schemas.CostRow      `json:"cost"`
}

// readRunAgents assembles the RunAgents of one run from its folder.
func readRunAgents(index ReadIndex, rv *RunView) (RunAgents, error) {
	runID := rv.Run.RunID
	out := RunAgents{RunID: runID, Members: []AgentStats{}, Spans: []AgentSpan{}, Handoffs: []AgentHandoff{}}
	var ev agentsEvidence
	if _, err := readInternalJSON(index, runID, FileEvidence, &ev); err != nil {
		return out, err
	}
	var tree schemas.AgentTree
	found, err := readInternalJSON(index, runID, schemas.AgentsFile, &tree)
	if err != nil {
		return out, err
	}
	if found {
		ev.Agents = &tree
	}
	if ev.Agents == nil || len(ev.Agents.Agents) == 0 {
		return out, nil
	}
	out.Available, out.Tree = true, ev.Agents
	msgs, err := readMessages(index, runID, ev.Messages)
	if err != nil {
		return out, err
	}
	steps, err := readRunSteps(index, runID)
	if err != nil {
		return out, err
	}
	out.Truncated = steps.Truncated
	out.Spans, out.WallMS = agentSpans(steps.Steps)
	out.CriticalMS = markCritical(out.Spans, msgs, steps.Steps)
	out.Members = agentStats(ev.Agents, steps.Steps, out.Spans, msgs, ev.Requests, ev.Cost)
	for _, m := range out.Members {
		out.BusyMS += m.BusyMS
	}
	out.Handoffs = agentHandoffs(ev.Agents, msgs, firstTS(steps.Steps))
	out.Blame, err = runBlame(index, rv, ev, msgs, steps.Steps)
	return out, err
}

// readInternalJSON decodes a run file the API reads but does not serve. A
// missing file leaves dst untouched and returns false.
func readInternalJSON(index ReadIndex, runID, name string, dst any) (bool, error) {
	data, found, err := readInternal(index, runID, name)
	if err != nil || !found {
		return false, err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return false, fmt.Errorf("parse %s of run %s: %w", name, runID, err)
	}
	return true, nil
}

// readMessages reads messages.jsonl, or falls back to evidence.json's
// messages. A line that does not parse is skipped.
func readMessages(index ReadIndex, runID string, fallback []schemas.AgentMessage) ([]schemas.AgentMessage, error) {
	data, found, err := readInternal(index, runID, schemas.MessagesFile)
	if err != nil || !found {
		return fallback, err
	}
	var out []schemas.AgentMessage
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m schemas.AgentMessage
		if json.Unmarshal(line, &m) == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// runBlame returns the run's blame child artifact when the index can read
// one, else tier 1 from the sealed gates, timeline, ledgers and events.
func runBlame(index ReadIndex, rv *RunView, ev agentsEvidence, msgs []schemas.AgentMessage, steps []schemas.StepRecord) (*attribution.Blame, error) {
	var note string
	if br, ok := index.(BlameReader); ok && rv.Sealed {
		b, err := br.RunBlame(rv.Slot.ExpID, rv.Run.RunID, rv.SealHash)
		switch {
		case err != nil:
			// A blame that cannot be read (or names another sealing) is
			// left out, not served: tier 1 is computed again below.
			note = "blame.json not used: " + err.Error()
		case b != nil:
			return b, nil
		}
	}
	var gates []schemas.GateResult
	if _, err := readInternalJSON(index, rv.Run.RunID, FileGates, &gates); err != nil {
		return nil, err
	}
	calls, err := readToolLedger(index, rv.Run.RunID)
	if err != nil {
		return nil, err
	}
	events, err := readEventLines(index, rv.Run.RunID)
	if err != nil {
		return nil, err
	}
	full := &schemas.Evidence{Agents: ev.Agents, Messages: msgs, Requests: ev.Requests, Steps: steps, ToolCalls: calls, Events: events}
	b := attribution.Tier1(attribution.Input{RunID: rv.Run.RunID, ExpID: rv.Slot.ExpID, ParentSeal: rv.SealHash,
		Outcome: rv.Run.Outcome, Gates: gates, Evidence: full})
	if note != "" {
		b.Notes = append(b.Notes, note)
	}
	return b, nil
}

// readEventLines reads events.jsonl; a line that does not parse is skipped.
func readEventLines(index ReadIndex, runID string) ([]schemas.Event, error) {
	data, found, err := readInternal(index, runID, FileEvents)
	if err != nil || !found {
		return nil, err
	}
	var out []schemas.Event
	for _, line := range bytes.Split(data, []byte("\n")) {
		var e schemas.Event
		if len(bytes.TrimSpace(line)) > 0 && json.Unmarshal(line, &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

// agentSpans places every timeline row with an agent id on its lane, in
// milliseconds from the first row of the run. A row with no duration is one
// millisecond wide.
func agentSpans(steps []schemas.StepRecord) ([]AgentSpan, int64) {
	var t0, t1 time.Time
	for _, s := range steps {
		if s.TS.IsZero() {
			continue
		}
		end := s.TS
		if s.DurationMS != nil {
			end = end.Add(time.Duration(*s.DurationMS) * time.Millisecond)
		}
		if t0.IsZero() || s.TS.Before(t0) {
			t0 = s.TS
		}
		if end.After(t1) {
			t1 = end
		}
	}
	spans := []AgentSpan{}
	for _, s := range steps {
		if s.AgentID == "" || s.TS.IsZero() || s.WitnessOf != 0 {
			continue
		}
		start := s.TS.Sub(t0).Milliseconds()
		end := start + 1
		if s.DurationMS != nil && *s.DurationMS > 0 {
			end = start + *s.DurationMS
		}
		spans = append(spans, AgentSpan{AgentID: s.AgentID, Seq: s.Seq, Kind: s.Kind, StartMS: start, EndMS: end, Label: clipRunes(s.Summary, maxSpanLabel), IsError: s.IsError})
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].StartMS < spans[j].StartMS })
	return spans, t1.Sub(t0).Milliseconds()
}

// markCritical marks the critical path: from the span that ends last, walk
// back through the latest-ending predecessor, where a span's predecessors
// are the earlier spans of its own lane and, across lanes, the sender's
// spans that ended before a message the span's agent received was sent. It
// returns the busy time on the path.
func markCritical(spans []AgentSpan, msgs []schemas.AgentMessage, steps []schemas.StepRecord) int64 {
	if len(spans) == 0 {
		return 0
	}
	var t0 time.Time
	for _, s := range steps {
		if !s.TS.IsZero() && (t0.IsZero() || s.TS.Before(t0)) {
			t0 = s.TS
		}
	}
	type link struct {
		from, to  string
		sent, got int64
	}
	var links []link
	for _, m := range msgs {
		got := m.DeliveredAt
		if got.IsZero() {
			got = m.At
		}
		for _, r := range msgReaders(m) {
			links = append(links, link{from: msgSender(m), to: r, sent: m.At.Sub(t0).Milliseconds(), got: got.Sub(t0).Milliseconds()})
		}
	}
	last := 0
	for i, s := range spans {
		if s.EndMS > spans[last].EndMS {
			last = i
		}
	}
	var busy int64
	for i := last; i >= 0; {
		spans[i].Critical = true
		busy += spans[i].EndMS - spans[i].StartMS
		next := -1
		for j := range spans {
			if j == i || spans[j].Critical || spans[j].EndMS > spans[i].StartMS {
				continue
			}
			ok := spans[j].AgentID == spans[i].AgentID
			for _, l := range links {
				if ok {
					break
				}
				ok = l.from == spans[j].AgentID && l.to == spans[i].AgentID && spans[j].EndMS <= l.sent && l.got <= spans[i].StartMS
			}
			if ok && (next < 0 || spans[j].EndMS > spans[next].EndMS) {
				next = j
			}
		}
		i = next
	}
	return busy
}

// agentStats builds the per-agent table in tree order.
func agentStats(tree *schemas.AgentTree, steps []schemas.StepRecord, spans []AgentSpan, msgs []schemas.AgentMessage, reqs *schemas.RequestLog, cost []schemas.CostRow) []AgentStats {
	order := treeOrder(tree.Agents)
	byID := map[string]*AgentStats{}
	out := make([]AgentStats, 0, len(order))
	for _, n := range order {
		out = append(out, AgentStats{AgentID: n.ID, Role: n.Role, Model: n.Model, ParentID: n.ParentID, Depth: n.Depth, Source: n.Source, Strength: n.Strength, Exit: n.Exit})
	}
	for i := range out {
		byID[out[i].AgentID] = &out[i]
	}
	for _, s := range steps {
		a := byID[s.AgentID]
		if a == nil || s.WitnessOf != 0 {
			continue
		}
		a.Steps++
		switch s.Kind {
		case schemas.StepModelTurn:
			a.ModelTurns++
		case schemas.StepToolRequest:
			a.ToolCalls++
		}
	}
	for agent, ms := range laneBusy(spans) {
		if a := byID[agent]; a != nil {
			a.BusyMS = ms
		}
	}
	for _, m := range msgs {
		if a := byID[msgSender(m)]; a != nil {
			a.MessagesSent++
		}
		for _, r := range msgReaders(m) {
			if a := byID[r]; a != nil {
				a.MessagesReceived++
			}
		}
	}
	addTokens(byID, reqs, cost)
	return out
}

// addTokens fills tokens and cost from the gateway rows with an agent id.
// They stay null when the gateway log is missing or incomplete, or when no
// row of the run carries an agent id.
func addTokens(byID map[string]*AgentStats, reqs *schemas.RequestLog, cost []schemas.CostRow) {
	if reqs == nil || !reqs.Complete {
		return
	}
	agentOf := map[string]string{}
	tagged := false
	for _, r := range reqs.Requests {
		if r.AgentID != "" {
			agentOf[r.ID], tagged = r.AgentID, true
		}
	}
	if !tagged {
		return
	}
	for _, a := range byID {
		var zi, zo int64
		var zc float64
		a.InputTokens, a.OutputTokens, a.CostUSD = &zi, &zo, &zc
	}
	for _, r := range reqs.Requests {
		a := byID[r.AgentID]
		if a == nil || r.Tokens == nil {
			continue
		}
		*a.InputTokens += r.Tokens.Fresh + r.Tokens.CacheRead + r.Tokens.CacheWrite
		*a.OutputTokens += r.Tokens.Out
	}
	for _, c := range cost {
		a := byID[agentOf[c.RequestID]]
		if a == nil {
			continue
		}
		if v, ok := c.USD.Float(); ok {
			*a.CostUSD += v
		} else {
			a.CostUSD = nil
		}
	}
}

// laneBusy is each agent's busy time: the union of its spans.
func laneBusy(spans []AgentSpan) map[string]int64 {
	byAgent := map[string][]AgentSpan{}
	for _, s := range spans {
		byAgent[s.AgentID] = append(byAgent[s.AgentID], s)
	}
	out := map[string]int64{}
	for agent, ss := range byAgent {
		sort.Slice(ss, func(i, j int) bool { return ss[i].StartMS < ss[j].StartMS })
		var total, curS, curE int64 = 0, -1, -1
		for _, s := range ss {
			if s.StartMS > curE {
				if curE > curS {
					total += curE - curS
				}
				curS, curE = s.StartMS, s.EndMS
			} else if s.EndMS > curE {
				curE = s.EndMS
			}
		}
		if curE > curS {
			total += curE - curS
		}
		out[agent] = total
	}
	return out
}

// treeOrder lists agents depth first: roots in recorded order, each followed
// by its children in recorded order. An agent whose parent is not in the
// tree is a root.
func treeOrder(nodes []schemas.AgentNode) []schemas.AgentNode {
	known := map[string]bool{}
	for _, n := range nodes {
		known[n.ID] = true
	}
	children := map[string][]schemas.AgentNode{}
	var roots []schemas.AgentNode
	for _, n := range nodes {
		if n.ParentID == "" || !known[n.ParentID] {
			roots = append(roots, n)
		} else {
			children[n.ParentID] = append(children[n.ParentID], n)
		}
	}
	out := make([]schemas.AgentNode, 0, len(nodes))
	seen := map[string]bool{}
	var walk func(n schemas.AgentNode)
	walk = func(n schemas.AgentNode) {
		if seen[n.ID] {
			return
		}
		seen[n.ID] = true
		out = append(out, n)
		for _, c := range children[n.ID] {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	for _, n := range nodes { // a cycle of parents: keep every agent
		walk(n)
	}
	return out
}

// agentHandoffs lists the messages with their payloads, then the tree's
// spawn, handoff, claim and return edges, in time order.
func agentHandoffs(tree *schemas.AgentTree, msgs []schemas.AgentMessage, t0 time.Time) []AgentHandoff {
	out := []AgentHandoff{}
	for _, m := range msgs {
		h := AgentHandoff{Kind: schemas.AgentEdgeMessage, From: msgSender(m), To: m.To, At: m.At, Bytes: m.Size, PayloadSHA256: m.BodySHA256,
			Sent: clipBytes(m.Body), Ref: "message:" + strconv.Itoa(m.Seq), Source: schemas.AgentSourceSelfReport}
		if m.SenderAgentID != "" {
			h.Source = schemas.AgentSourceGatewayKey // the sender's own bench-minted route token
		}
		if h.Bytes == 0 {
			h.Bytes = int64(len(m.Body))
		}
		if m.Delivered {
			h.Delivered = h.Sent
			if m.Altered {
				h.Delivered, h.Altered = clipBytes(m.Received), true
			}
		}
		h.Facts = handoffFacts(m.Body, h.Delivered, m.Delivered)
		out = append(out, h)
	}
	for _, e := range tree.Edges {
		if e.Kind == schemas.AgentEdgeMessage && len(msgs) > 0 {
			continue // the mailbox record above has the payloads
		}
		out = append(out, AgentHandoff{Kind: e.Kind, From: e.From, To: e.To, At: e.At, Bytes: e.Size, PayloadSHA256: e.PayloadSHA256, Ref: e.Ref, Source: e.Source})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	for i := range out {
		out[i].Index = i + 1
		if !t0.IsZero() && !out[i].At.IsZero() {
			ms := out[i].At.Sub(t0).Milliseconds()
			out[i].AtMS = &ms
		}
	}
	return out
}

// firstTS is the time of the run's first timeline row, the zero of every
// span and handoff offset.
func firstTS(steps []schemas.StepRecord) time.Time {
	var t0 time.Time
	for _, s := range steps {
		if !s.TS.IsZero() && (t0.IsZero() || s.TS.Before(t0)) {
			t0 = s.TS
		}
	}
	return t0
}

// handoffFacts lists the planted tokens a payload carried and whether each
// arrived.
func handoffFacts(sent, delivered string, wasDelivered bool) []HandoffFact {
	var out []HandoffFact
	seen := map[string]bool{}
	for _, tok := range plantedToken.FindAllString(sent, -1) {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, HandoffFact{Fact: "planted token " + tok[:12] + "...", Sent: true, Delivered: wasDelivered && strings.Contains(delivered, tok)})
	}
	return out
}

// msgSender is the proven sender of m, else its from field.
func msgSender(m schemas.AgentMessage) string {
	if m.SenderAgentID != "" {
		return m.SenderAgentID
	}
	return m.From
}

// msgReaders are the members m reached, or its addressee.
func msgReaders(m schemas.AgentMessage) []string {
	if len(m.DeliveredTo) > 0 {
		return m.DeliveredTo
	}
	if m.To != "" && m.To != "*" {
		return []string{m.To}
	}
	return nil
}

func clipBytes(s string) string {
	if len(s) <= maxHandoffText {
		return s
	}
	cut := maxHandoffText
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf(" [%d more bytes]", len(s)-cut)
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
