package evidence

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// GatewaySource is the name of the gateway request log source.
const GatewaySource = "gateway"

// requestsFile is the run folder file holding the gateway's rows.
const requestsFile = "gateway.requests.jsonl"

// Gateway request kinds and statuses this package reads.
const (
	kindMessage      = "message"
	statusSuccess    = "success"
	statusError      = "error"
	statusProcessing = "processing"
)

var _ schemas.EvidenceSource = (*gatewaySource)(nil)

// gatewaySource fetches the run's rows from the gateway, which is the trace of
// record for model calls (DESIGN.md 11), and normalizes them into events.
type gatewaySource struct {
	gateway schemas.Gateway
	logger  schemas.Logger
}

// GetName implements schemas.Part.
func (s *gatewaySource) GetName() string { return GatewaySource }

// GetVersion implements schemas.Part.
func (s *gatewaySource) GetVersion() string { return version }

// Collect implements schemas.EvidenceSource. Without a gateway nothing is
// recorded and ev.Requests stays nil, which metrics read as "no gateway log",
// never as zero requests. The ledger collected first is the completeness
// expectation (DESIGN.md 13.4). An incomplete set is kept and marked by the
// gateway; it also becomes an error event so the trace shows the gap.
func (s *gatewaySource) Collect(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if s.gateway == nil {
		return nil
	}
	id := runID(rc)
	if rc.Key == nil {
		return fmt.Errorf("fetch gateway requests for run %s: %w", id, errNoKey)
	}
	log, err := s.gateway.FetchRequests(ctx, rc.Key, schemas.FetchExpectation{LedgerEntries: ev.Ledger})
	if err != nil {
		return fmt.Errorf("fetch gateway requests for run %s: %w", id, err)
	}
	if log == nil {
		return fmt.Errorf("fetch gateway requests for run %s: %w", id, errNoLog)
	}
	ev.Requests = log
	utils.MarkExternalTraffic(rc, log) // Remote area: a remote_http agent's external model calls leave tokens, cost and steps unknown
	if err := writeJSONL(rc.Dir, requestsFile, log.Requests); err != nil {
		return fmt.Errorf("write gateway requests for run %s: %w", id, err)
	}
	events := NormalizeRequests(rc, log)
	if !log.Complete {
		pickLogger(s.logger, rc).Warn("gateway request log incomplete", "run_id", id, "reason", log.Reason)
		events = append(events, incompleteEvent(rc, log, len(events)+1))
	}
	ev.Events = append(ev.Events, events...)
	return writeEvents(rc, ev)
}

var (
	errNoKey = errors.New("run has no virtual key")
	errNoLog = errors.New("gateway returned no request log")
)

// NormalizeRequests turns the gateway's message rows into events, in the
// order the requests were made. It is pure.
//
// A turn is one logical model request: a fallback row whose parent is
// another row shares its parent's turn, so turn indexes match the steps
// metric. Turns count from 1; events with turn 0 belong to no turn. Each row
// yields, in order: a tool_result per tool result id seen for the first time
// in its input (the conversation is resent every turn, so a result counts
// where it first appears), a model_request, a retry per attempt after the
// first, a fallback when the row was served by a fallback, a model_response
// unless the row is still processing, and a tool_call per tool call in the
// response not seen before. Rows of other kinds (token counting, model
// listing) are not model turns and yield nothing.
func NormalizeRequests(rc *schemas.RunContext, log *schemas.RequestLog) []schemas.Event {
	if log == nil {
		return nil
	}
	n := &normalizer{
		seq:        newEventSeq(rc, schemas.SourceGateway, schemas.EvidenceGateway),
		turnOf:     map[string]int{},
		requestOf:  map[string]string{},
		callEvent:  map[string]string{},
		seenResult: map[string]bool{},
	}
	rows := messageRowsInOrder(log.Requests)
	parent := map[string]string{}
	for _, r := range rows {
		if r.ID != "" {
			parent[r.ID] = r.ParentID
		}
	}
	for _, r := range rows {
		n.row(r, rootOf(r, parent))
	}
	return n.events
}

// normalizer carries the state NormalizeRequests needs across rows.
type normalizer struct {
	seq        *eventSeq
	events     []schemas.Event
	turns      int
	turnOf     map[string]int    // root row id -> turn index
	requestOf  map[string]string // root row id -> its model_request event id
	callEvent  map[string]string // tool call id -> tool_call event id
	seenResult map[string]bool   // tool result ids already emitted
}

// row appends the events of one message row.
func (n *normalizer) row(r schemas.GatewayRequest, root string) {
	turn, ok := n.turnOf[root]
	if !ok || root == "" {
		n.turns++
		turn = n.turns
		if root != "" {
			n.turnOf[root] = turn
		}
	}
	n.toolResults(r, turn)
	req := n.add(n.base(r, schemas.EventModelRequest, r.Timestamp, turn))
	if _, ok := n.requestOf[root]; !ok && root != "" {
		n.requestOf[root] = req.EventID
	}
	n.retries(r, turn, req.EventID)
	if r.FallbackIndex > 0 {
		fb := n.base(r, schemas.EventFallback, r.Timestamp, turn)
		fb.ParentEventID = req.EventID
		if rootReq, ok := n.requestOf[root]; ok && rootReq != req.EventID {
			fb.ParentEventID = rootReq
		}
		fb.Summary = clip(fmt.Sprintf("fallback %d: requested %s, served %s", r.FallbackIndex, r.ModelRequested, r.ModelServed), 200)
		n.add(fb)
	}
	if r.Status == statusProcessing {
		return
	}
	resp := n.response(r, turn, req.EventID)
	n.toolCalls(r, turn, resp.EventID, resp.TS)
}

// base returns an event of type typ carrying the row's request fields.
func (n *normalizer) base(r schemas.GatewayRequest, typ schemas.EventType, ts time.Time, turn int) schemas.Event {
	e := n.seq.event(typ, ts)
	e.TurnIndex = turn
	e.RequestID = r.ID
	e.Provider = r.Provider
	e.ModelReq = r.ModelRequested
	e.ModelServed = r.ModelServed
	return e
}

// add appends e and returns it.
func (n *normalizer) add(e schemas.Event) schemas.Event {
	n.events = append(n.events, e)
	return e
}

// toolResults emits the tool results first seen in r's input.
func (n *normalizer) toolResults(r schemas.GatewayRequest, turn int) {
	for _, id := range r.ToolResultIDs {
		if id == "" || n.seenResult[id] {
			continue
		}
		n.seenResult[id] = true
		e := n.base(r, schemas.EventToolResult, r.Timestamp, turn)
		e.ToolCallID = id
		e.ParentEventID = n.callEvent[id]
		n.add(e)
	}
}

// retries emits one retry event per physical attempt after the first, with
// the reason the attempt before it failed.
func (n *normalizer) retries(r schemas.GatewayRequest, turn int, parent string) {
	for i := 1; i < len(r.Attempts); i++ {
		a, prev := r.Attempts[i], r.Attempts[i-1]
		no := a.N
		if no == 0 {
			no = i + 1
		}
		e := n.base(r, schemas.EventRetry, r.Timestamp, turn)
		e.AttemptNo = &no
		e.ParentEventID = parent
		e.Summary = clip(fmt.Sprintf("attempt %d after status %d %s", no, prev.StatusCode, prev.FailReason), 200)
		n.add(e)
	}
}

// response emits the model_response of a finished row. Its time is the
// request time plus the recorded latency.
func (n *normalizer) response(r schemas.GatewayRequest, turn int, parent string) schemas.Event {
	ts := r.Timestamp.Add(time.Duration(r.LatencyMS * float64(time.Millisecond)))
	e := n.base(r, schemas.EventModelResponse, ts, turn)
	e.ParentEventID = parent
	if r.Tokens != nil {
		tok := *r.Tokens
		e.Tokens = &tok
	}
	if len(r.Attempts) > 0 {
		last := r.Attempts[len(r.Attempts)-1].N
		if last == 0 {
			last = len(r.Attempts)
		}
		e.AttemptNo = &last
	}
	if r.Status == statusError {
		e.IsError = true
		e.Summary = clip(fmt.Sprintf("error %d: %s", r.ErrorStatus, r.ErrorMessage), 200)
	} else {
		e.Summary = fmt.Sprintf("%d tool calls", len(r.ToolCalls))
	}
	return n.add(e)
}

// toolCalls emits one tool_call per call in the response, by distinct id, so
// a response logged on both a fallback row and its parent counts once.
func (n *normalizer) toolCalls(r schemas.GatewayRequest, turn int, parent string, ts time.Time) {
	for _, tc := range r.ToolCalls {
		if tc.ID != "" {
			if _, dup := n.callEvent[tc.ID]; dup {
				continue
			}
		}
		e := n.base(r, schemas.EventToolCall, ts, turn)
		e.ParentEventID = parent
		e.ToolCallID = tc.ID
		e.ToolName = tc.Name
		e.ArgsHash = tc.ArgsHash
		e.Target = tc.Target
		e.Summary = clip(tc.Name+" "+tc.Target, 200)
		n.add(e)
		if tc.ID != "" {
			n.callEvent[tc.ID] = e.EventID
		}
	}
}

// messageRowsInOrder returns a copy of the message rows sorted by time. The
// gateway may list rows newest first; ties keep the gateway's order.
func messageRowsInOrder(reqs []schemas.GatewayRequest) []schemas.GatewayRequest {
	var rows []schemas.GatewayRequest
	for _, r := range reqs {
		if r.Kind == kindMessage {
			rows = append(rows, r)
		}
	}
	slices.SortStableFunc(rows, func(a, b schemas.GatewayRequest) int {
		return a.Timestamp.Compare(b.Timestamp)
	})
	return rows
}

// rootOf follows parent links among the run's rows to the first row of the
// logical request. A parent outside the set ends the walk. A cycle, which only
// a corrupt log can hold, resolves to its smallest id so every row on it
// shares one root.
func rootOf(r schemas.GatewayRequest, parent map[string]string) string {
	cur := r.ID
	seen := map[string]bool{}
	for !seen[cur] {
		seen[cur] = true
		p, ok := parent[cur]
		if !ok || p == "" || p == cur {
			return cur
		}
		if _, known := parent[p]; !known {
			return cur
		}
		cur = p
	}
	least := cur
	for at := parent[cur]; at != cur; at = parent[at] {
		least = min(least, at)
	}
	return least
}

// incompleteEvent records that the gateway's log is not the whole run.
func incompleteEvent(rc *schemas.RunContext, log *schemas.RequestLog, seq int) schemas.Event {
	s := newEventSeq(rc, schemas.SourceGateway, schemas.EvidenceGateway)
	s.next = seq
	e := s.event(schemas.EventError, time.Time{})
	e.IsError = true
	e.Summary = clip("gateway request log incomplete: "+log.Reason, 300)
	return e
}

// newGatewaySource returns the gateway source.
func newGatewaySource(deps Deps) *gatewaySource {
	return &gatewaySource{gateway: deps.Gateway, logger: deps.Logger}
}
