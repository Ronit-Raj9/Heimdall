package faultproxy

// Debug runs (lane X20b): model-call replay from a cassette, the fork edit,
// and breakpoints that hold a request until the operator releases it.
//
//	PUT  /cassettes/{slot}       body schemas.Cassette; loads the recording a replay plan answers from
//	GET  /holds?run_id=          schemas.HoldStatus: the run's held requests and its total held time
//	POST /holds/{slot}/release   body schemas.HoldRelease: continue, step, edit or abort one held call
//
// A replay plan (mode replay, FaultPlan.Replay) answers call k from the
// slot's cassette while the agent's request matches the recording
// (replay.RequestHash). A request that differs is a divergence, recorded in
// the ledger with the hash it missed, and handled by the plan's policy:
// stop (an error answer, then every later call refused), live (forwarded
// from then on) or serve (answered from the recording anyway). Calls from
// UntilCall on go live; UntilCall itself carries the operator's edit.
//
// A hold plan (FaultPlan.Hold) blocks a matching request until it is
// released or MaxHold passes (Bifrost gives up on an upstream after five
// minutes, so a hold auto-continues before that). Time spent held is
// reported per run, so the bench can stop the agent's wall clock meanwhile.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/replay"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	// ModeReplay answers calls from the slot's cassette (debug.go).
	ModeReplay = "replay"

	// DefaultMaxHold is how long a breakpoint holds a request before it
	// continues by itself: under Bifrost's 300 s upstream timeout.
	DefaultMaxHold = 240 * time.Second

	maxCassetteBody = schemas.MaxCassetteBytes + 4<<20
	maxReleaseBody  = 1 << 20
	maxHoldRequest  = 64 << 10 // request bytes a hold shows
)

// debugState is the debug side of the proxy: cassettes, the replay state of
// each slot, and the held requests.
type debugState struct {
	mu        sync.Mutex
	dir       string // cassette files; "" keeps them in memory
	cassettes map[string]*schemas.Cassette
	slots     map[string]*slotDebug
	holds     map[holdKey]*pendingHold
	maxHold   time.Duration
	now       func() time.Time
}

// slotDebug is one slot's progress through its debug plan. It is reset
// whenever the slot's plan is set or cleared.
type slotDebug struct {
	runID    string
	live     bool          // a divergence under policy live: every later call is forwarded
	stopped  string        // why every later call is refused (divergence or abort); "" while running
	stepNext bool          // the operator stepped: hold the next call
	held     time.Duration // finished holds
}

type holdKey struct {
	slot string
	call int
}

// pendingHold is one held request waiting for its release.
type pendingHold struct {
	hold    schemas.Hold
	release chan schemas.HoldRelease
}

func newDebugState(dir string) (*debugState, error) {
	d := &debugState{dir: dir, cassettes: map[string]*schemas.Cassette{}, slots: map[string]*slotDebug{},
		holds: map[holdKey]*pendingHold{}, maxHold: DefaultMaxHold, now: time.Now}
	if dir == "" {
		return d, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cassettes %s: %w", dir, err)
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read cassette %s: %w", e.Name(), err)
		}
		var c schemas.Cassette
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("cassette %s: %w", e.Name(), err)
		}
		d.cassettes[name] = &c
	}
	return d, nil
}

// CassettesPath is the folder beside the ledger that keeps loaded cassettes
// across a restart, as PlansPath keeps plans.
func CassettesPath(ledgerPath string) string {
	if ledgerPath == "" {
		return ""
	}
	return ledgerPath + ".cassettes"
}

// putCassette stores c for slot, on disk first.
func (d *debugState) putCassette(slot string, c *schemas.Cassette) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dir != "" {
		if err := writePlans(filepath.Join(d.dir, slot+".json"), c); err != nil {
			return fmt.Errorf("persist cassette: %w", err)
		}
	}
	d.cassettes[slot] = c
	return nil
}

// hasCassette reports whether slot has a cassette loaded.
func (d *debugState) hasCassette(slot string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cassettes[slot] != nil
}

// reset starts slot's debug progress over for run runID. keepCassette
// keeps the loaded cassette (a replay plan was just bound); otherwise it is
// dropped with its file. Holds still pending on the slot continue.
func (d *debugState) reset(slot, runID string, keepCassette bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.slots[slot] = &slotDebug{runID: runID}
	if !keepCassette && d.cassettes[slot] != nil {
		delete(d.cassettes, slot)
		if d.dir != "" {
			_ = os.Remove(filepath.Join(d.dir, slot+".json"))
		}
	}
	for k, h := range d.holds {
		if k.slot == slot {
			h.release <- schemas.HoldRelease{Call: k.call, Action: schemas.HoldContinue}
			delete(d.holds, k)
		}
	}
}

func (d *debugState) slot(slot string) *slotDebug {
	st := d.slots[slot]
	if st == nil {
		st = &slotDebug{}
		d.slots[slot] = st
	}
	return st
}

// status returns runID's holds and held time.
func (d *debugState) status(runID string) schemas.HoldStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := schemas.HoldStatus{RunID: runID, Holds: []schemas.Hold{}}
	now := d.now()
	var total time.Duration
	for _, st := range d.slots {
		if st.runID == runID {
			total += st.held
			out.Stopped = out.Stopped || st.stopped == schemas.HoldAbort
		}
	}
	for _, h := range d.holds {
		if h.hold.RunID == runID {
			out.Holds = append(out.Holds, h.hold)
			total += now.Sub(h.hold.Since)
		}
	}
	sort.Slice(out.Holds, func(i, j int) bool { return out.Holds[i].Call < out.Holds[j].Call })
	out.HeldMS = total.Milliseconds()
	return out
}

// release hands r to the hold of slot and call. It is an error when no
// such hold is pending.
func (d *debugState) release(slot string, r schemas.HoldRelease) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := holdKey{slot, r.Call}
	h, ok := d.holds[k]
	if !ok {
		return errNoHold
	}
	delete(d.holds, k)
	h.release <- r
	return nil
}

var errNoHold = errors.New("no request is held at that call; it may have continued already")

// holdReason says why bp holds call, or "" when it does not.
func holdReason(bp *schemas.Breakpoints, step bool, call int, tools []string) string {
	switch {
	case step:
		return "step"
	case bp == nil:
		return ""
	case bp.Every:
		return "every call"
	case slices.Contains(bp.Calls, call):
		return fmt.Sprintf("call %d", call)
	}
	for _, t := range tools {
		if slices.Contains(bp.AfterTools, t) {
			return "after tool " + t
		}
	}
	return ""
}

// requestShape is the replay request shape of a conversation request.
func requestShape(p ProviderSpec, path string) string {
	switch ScriptedShape(p, path) {
	case FormatAnthropic:
		return replay.ShapeAnthropic
	case FormatOpenAI:
		if strings.HasSuffix(strings.TrimSuffix(path, "/"), "/responses") {
			return replay.ShapeResponses
		}
		return replay.ShapeChat
	}
	if p.Format == FormatGemini {
		return replay.ShapeGemini
	}
	return ""
}

// serveDebug applies a debug plan to one counted conversation request
// before it is answered. It returns handled true when it answered the
// request itself, else the body to forward (an added message changes it).
func (s *Server) serveDebug(w *statusWriter, r *http.Request, p ProviderSpec, slot string, plan schemas.FaultPlan, d decision, body []byte, stream bool, e *schemas.LedgerEntry) (bool, []byte) {
	shape := requestShape(p, r.URL.Path)
	s.debug.mu.Lock()
	st := s.debug.slot(slot)
	if st.runID == "" {
		st.runID = d.RunID
	}
	stopped, step := st.stopped, st.stepNext
	st.stepNext = false
	s.debug.mu.Unlock()
	if stopped != "" {
		e.Action, e.Operator = ActionReject, stopped
		s.writeError(w, p.Format, http.StatusBadRequest, errInvalid, stopMessage(stopped))
		return true, nil
	}
	if reason := holdReason(plan.Hold, step, d.Call, replay.NewToolResults(body, shape)); reason != "" {
		rel, ok := s.hold(r.Context(), slot, d, reason, requestModel(p, r, KindMessage, body), stream, body, e)
		if !ok { // the caller went away while held
			e.Action = ActionReject
			return true, nil
		}
		switch rel.Action {
		case schemas.HoldAbort:
			s.debug.mu.Lock()
			s.debug.slot(slot).stopped = schemas.HoldAbort
			s.debug.mu.Unlock()
			e.Action = ActionReject
			s.writeError(w, p.Format, http.StatusBadRequest, errInvalid, stopMessage(schemas.HoldAbort))
			return true, nil
		case schemas.HoldStep:
			s.debug.mu.Lock()
			s.debug.slot(slot).stepNext = true
			s.debug.mu.Unlock()
		case schemas.HoldEdit:
			if rel.Response != "" {
				e.Action, e.Upstream = ActionPass, schemas.LedgerUpstreamReplay
				s.writeEdited(w, r, p, body, stream, rel.Response, e)
				return true, nil
			}
			nb, err := replay.AppendUserMessage(body, shape, rel.Message)
			if err != nil {
				e.Action = ActionReject
				s.writeError(w, p.Format, http.StatusBadRequest, errInvalid, "fault proxy could not add the message to this request: "+err.Error())
				return true, nil
			}
			body = nb
		}
	}
	if plan.Replay == nil {
		return false, body
	}
	s.debug.mu.Lock()
	live := s.debug.slot(slot).live
	c := s.debug.cassettes[slot]
	s.debug.mu.Unlock()
	rp := plan.Replay
	switch {
	case live || (rp.UntilCall > 0 && d.Call > rp.UntilCall):
		return false, body
	case rp.UntilCall > 0 && d.Call == rp.UntilCall:
		switch {
		case rp.EditResponse != "":
			e.Replay, e.Action, e.Upstream = schemas.ReplayEdited, ActionPass, schemas.LedgerUpstreamReplay
			s.writeEdited(w, r, p, body, stream, rp.EditResponse, e)
			return true, nil
		case rp.EditMessage != "":
			nb, err := replay.AppendUserMessage(body, shape, rp.EditMessage)
			if err != nil {
				e.Action = ActionReject
				s.writeError(w, p.Format, http.StatusBadRequest, errInvalid, "fault proxy could not add the fork message to this request: "+err.Error())
				return true, nil
			}
			e.Replay = schemas.ReplayEdited
			return false, nb
		}
		return false, body
	}
	entry, found := cassetteEntry(c, d.Call)
	got := replay.RequestHash(body)
	switch {
	case !found:
		e.Replay = schemas.ReplayExhausted
	case !replay.Usable(entry):
		e.Replay, e.ReplayExpected = schemas.ReplayDivergence, entry.ReqHash
	case entry.ReqHash != "" && got != entry.ReqHash:
		e.Replay, e.ReplayExpected = schemas.ReplayDivergence, entry.ReqHash
	default:
		e.Replay, e.Action, e.Upstream = schemas.ReplayHit, ActionPass, schemas.LedgerUpstreamReplay
		s.writeRecorded(w, p, r.URL.Path, entry, e)
		return true, nil
	}
	policy := rp.OnDivergence
	if policy == "" {
		policy = schemas.DivergenceStop
	}
	if policy == schemas.DivergenceServe && found && replay.Usable(entry) {
		e.Action, e.Upstream = ActionPass, schemas.LedgerUpstreamReplay
		s.writeRecorded(w, p, r.URL.Path, entry, e)
		return true, nil
	}
	if policy == schemas.DivergenceLive {
		s.debug.mu.Lock()
		s.debug.slot(slot).live = true
		s.debug.mu.Unlock()
		return false, body
	}
	s.debug.mu.Lock()
	s.debug.slot(slot).stopped = e.Replay
	s.debug.mu.Unlock()
	e.Action = ActionReject
	s.writeError(w, p.Format, http.StatusBadRequest, errInvalid, stopMessage(e.Replay)+fmt.Sprintf(" (call %d)", d.Call))
	return true, nil
}

// stopMessage is the error a stopped debug run's calls get.
func stopMessage(why string) string {
	switch why {
	case schemas.HoldAbort:
		return "fault proxy: the operator stopped this debug run"
	case schemas.ReplayExhausted:
		return "fault proxy: replay ended, the recorded run made no more model calls"
	default:
		return "fault proxy: replay stopped, the agent's request differs from the recorded run"
	}
}

// cassetteEntry is c's entry for call.
func cassetteEntry(c *schemas.Cassette, call int) (schemas.CassetteEntry, bool) {
	if c == nil {
		return schemas.CassetteEntry{}, false
	}
	for _, e := range c.Entries {
		if e.Call == call {
			return e, true
		}
	}
	return schemas.CassetteEntry{}, false
}

// hold blocks one request until its release, the caller leaving, or the
// maximum hold. ok is false when the caller left.
func (s *Server) hold(ctx context.Context, slot string, d decision, reason, model string, stream bool, body []byte, e *schemas.LedgerEntry) (schemas.HoldRelease, bool) {
	req := utils.RedactSecrets(string(body))
	clipped := len(req) > maxHoldRequest
	if clipped {
		req = req[:maxHoldRequest]
	}
	s.debug.mu.Lock()
	since := s.debug.now()
	ph := &pendingHold{
		hold: schemas.Hold{Slot: slot, RunID: d.RunID, Call: d.Call, Since: since.UTC(), Reason: reason,
			Model: model, Stream: stream, Request: req, Clipped: clipped, Deadline: since.Add(s.debug.maxHold).UTC()},
		release: make(chan schemas.HoldRelease, 1),
	}
	k := holdKey{slot, d.Call}
	s.debug.holds[k] = ph
	maxHold := s.debug.maxHold
	s.debug.mu.Unlock()
	s.logger.Info("breakpoint holding a request", "slot", slot, "run_id", d.RunID, "call", d.Call, "reason", reason)
	timer := time.NewTimer(maxHold)
	defer timer.Stop()
	var rel schemas.HoldRelease
	ok := true
	select {
	case rel = <-ph.release:
	case <-timer.C:
		rel = schemas.HoldRelease{Call: d.Call, Action: schemas.HoldContinue}
		e.Operator = "timeout"
	case <-ctx.Done():
		ok = false
		e.Operator = "caller_left"
	}
	s.debug.mu.Lock()
	if s.debug.holds[k] == ph {
		delete(s.debug.holds, k)
	}
	held := s.debug.now().Sub(since)
	s.debug.slot(slot).held += held
	s.debug.mu.Unlock()
	e.HeldMS = float64(held.Microseconds()) / 1000
	if e.Operator == "" {
		e.Operator = rel.Action
	}
	return rel, ok
}

// writeRecorded answers with a recorded response.
func (s *Server) writeRecorded(w http.ResponseWriter, p ProviderSpec, path string, entry schemas.CassetteEntry, e *schemas.LedgerEntry) {
	if entry.Body == "" {
		s.writeError(w, p.Format, statusOr(entry.Status, http.StatusBadGateway), errUpstream, "fault proxy: replayed error from the recorded run")
		return
	}
	sn := newSniffer(p.Format, entry.Stream)
	defer func() { e.ResponseID, e.Tokens = sn.result() }()
	if !entry.Stream || entry.Status >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusOr(entry.Status, http.StatusOK))
		_, _ = io.WriteString(io.MultiWriter(w, sn), entry.Body)
		return
	}
	shape := requestShape(p, path)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	out := io.MultiWriter(w, sn)
	for _, payload := range strings.Split(entry.Body, "\n\n") {
		frame := "data: " + payload + "\n\n"
		if t := replay.EventType(payload); t != "" && (shape == replay.ShapeAnthropic || shape == replay.ShapeResponses) {
			frame = "event: " + t + "\n" + frame
		}
		if _, err := io.WriteString(out, frame); err != nil {
			return
		}
		_ = rc.Flush()
	}
	if shape == replay.ShapeChat {
		_, _ = io.WriteString(out, "data: [DONE]\n\n")
		_ = rc.Flush()
	}
}

// writeEdited answers with text the operator wrote, in the request's shape.
func (s *Server) writeEdited(w http.ResponseWriter, r *http.Request, p ProviderSpec, body []byte, stream bool, text string, e *schemas.LedgerEntry) {
	switch requestShape(p, r.URL.Path) {
	case replay.ShapeAnthropic:
		var req messagesRequest
		_ = json.Unmarshal(body, &req)
		rp := newScriptedReply(body, req, false)
		rp.ID, rp.Text, rp.Input, rp.Output = utils.NewID("msg_edit", 20), text, 0, 0
		e.ResponseID, e.Tokens = rp.ID, rp.tokens()
		if stream {
			_ = writeScriptedStream(w, rp)
			return
		}
		s.writeJSON(w, http.StatusOK, rp.message())
	case replay.ShapeChat, replay.ShapeResponses:
		var req openAIRequest
		_ = json.Unmarshal(body, &req)
		responses := requestShape(p, r.URL.Path) == replay.ShapeResponses
		rp := newOpenAIReply(body, req, responses, false)
		rp.Text, rp.Prompt, rp.Cached, rp.Output = text, 0, 0, 0
		e.ResponseID, e.Tokens = rp.ID, rp.tokens()
		switch {
		case responses && stream:
			_ = writeSSE(w, rp.responseEvents(), true, false)
		case responses:
			s.writeJSON(w, http.StatusOK, rp.response(true))
		case stream:
			_ = writeSSE(w, rp.chatChunks(false), false, true)
		default:
			s.writeJSON(w, http.StatusOK, rp.chatCompletion())
		}
	default:
		msg := map[string]any{"candidates": []any{map[string]any{
			"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}, "finishReason": "STOP"}}}
		if stream {
			_ = writeSSE(w, []map[string]any{msg}, false, false)
			return
		}
		s.writeJSON(w, http.StatusOK, msg)
	}
}

func statusOr(status, def int) int {
	if status == 0 {
		return def
	}
	return status
}

func (s *Server) handlePutCassette(w http.ResponseWriter, r *http.Request) {
	slot, ok := s.controlSlot(w, r)
	if !ok {
		return
	}
	if held := s.plans.claimAgainst(slot, r.Header.Get(OwnerHeader)); held != nil {
		s.planRefused(w, "cassette", slot, held)
		return
	}
	var c schemas.Cassette
	dec := json.NewDecoder(io.LimitReader(r.Body, maxCassetteBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		s.controlError(w, http.StatusBadRequest, "decode cassette: "+err.Error())
		return
	}
	if len(c.Entries) == 0 {
		s.controlError(w, http.StatusBadRequest, "cassette has no entries")
		return
	}
	if err := s.debug.putCassette(slot, &c); err != nil {
		s.controlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logger.Info("cassette loaded", "slot", slot, "source_run", c.SourceRun, "calls", len(c.Entries))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHolds(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run_id")
	if runID == "" {
		s.controlError(w, http.StatusBadRequest, "run_id is required")
		return
	}
	s.writeJSON(w, http.StatusOK, s.debug.status(runID))
}

func (s *Server) handleHoldRelease(w http.ResponseWriter, r *http.Request) {
	slot, ok := s.controlSlot(w, r)
	if !ok {
		return
	}
	var rel schemas.HoldRelease
	dec := json.NewDecoder(io.LimitReader(r.Body, maxReleaseBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rel); err != nil {
		s.controlError(w, http.StatusBadRequest, "decode release: "+err.Error())
		return
	}
	if err := rel.Validate(); err != nil {
		s.controlError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.debug.release(slot, rel); err != nil {
		s.controlError(w, http.StatusConflict, err.Error())
		return
	}
	s.logger.Info("breakpoint released", "slot", slot, "call", rel.Call, "action", rel.Action)
	w.WriteHeader(http.StatusNoContent)
}

// PutCassette loads c onto slot. It implements schemas.ReplayController.
func (c *Client) PutCassette(ctx context.Context, slot string, cas *schemas.Cassette) error {
	body, err := json.Marshal(cas)
	if err != nil {
		return fmt.Errorf("encode cassette for slot %s: %w", slot, err)
	}
	if err := c.do(ctx, http.MethodPut, "/cassettes/"+escapeSlot(slot), body, nil); err != nil {
		return fmt.Errorf("load cassette on slot %s: %w", slot, err)
	}
	return nil
}

// Holds returns runID's held requests. It implements schemas.HoldController.
func (c *Client) Holds(ctx context.Context, runID string) (*schemas.HoldStatus, error) {
	var out schemas.HoldStatus
	if err := c.do(ctx, http.MethodGet, "/holds?run_id="+escapeQuery(runID), nil, &out); err != nil {
		return nil, fmt.Errorf("holds of run %s: %w", runID, err)
	}
	if out.Holds == nil {
		out.Holds = []schemas.Hold{}
	}
	return &out, nil
}

// ReleaseHold releases one held call on slot. It implements
// schemas.HoldController.
func (c *Client) ReleaseHold(ctx context.Context, slot string, rel schemas.HoldRelease) error {
	body, err := json.Marshal(rel)
	if err != nil {
		return err
	}
	if err := c.do(ctx, http.MethodPost, "/holds/"+escapeSlot(slot)+"/release", body, nil); err != nil {
		return fmt.Errorf("release call %d on slot %s: %w", rel.Call, slot, err)
	}
	return nil
}

var (
	_ schemas.ReplayController = (*Client)(nil)
	_ schemas.HoldController   = (*Client)(nil)
)

func escapeSlot(slot string) string { return url.PathEscape(slot) }

func escapeQuery(v string) string { return url.QueryEscape(v) }

// claimAgainst returns the claim another owner holds on slot, or nil.
func (t *planTable) claimAgainst(slot, owner string) *SlotClaim {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.heldAgainst(slot, owner)
}

// validateDebugPlan checks the replay and hold parts of a plan.
func validateDebugPlan(p schemas.FaultPlan) error {
	if p.Mode == ModeReplay {
		if p.Replay == nil {
			return errors.New("replay plan needs replay settings")
		}
		if p.FromCall != 0 || p.Call != 0 || p.Status != 0 || p.RetryAfterS != 0 {
			return errors.New("replay plan injects nothing; from_call, call, status and retry_after_s must be unset")
		}
	} else if p.Replay != nil {
		return fmt.Errorf("replay settings need mode replay, got %q", p.Mode)
	}
	if rp := p.Replay; rp != nil {
		if rp.UntilCall < 0 {
			return errors.New("replay until_call must be >= 0")
		}
		switch rp.OnDivergence {
		case "", schemas.DivergenceStop, schemas.DivergenceLive, schemas.DivergenceServe:
		default:
			return fmt.Errorf("replay on_divergence must be stop, live or serve, got %q", rp.OnDivergence)
		}
		if rp.EditResponse != "" && rp.EditMessage != "" {
			return errors.New("replay edit sets a response or a message, not both")
		}
		if (rp.EditResponse != "" || rp.EditMessage != "") && rp.UntilCall < 1 {
			return errors.New("replay edit needs until_call >= 1")
		}
	}
	if b := p.Hold; b != nil {
		for _, c := range b.Calls {
			if c < 1 {
				return fmt.Errorf("hold call must be >= 1, got %d", c)
			}
		}
	}
	return nil
}
