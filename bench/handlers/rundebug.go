package handlers

// Run debugging (lane X20b): replay a sealed run's model calls at no cost,
// fork it from step N with an edit, rerun it with breakpoints, release a
// held call, and read the raw request and response of each model call.
//
//	GET  /runs/{id}/calls                the run's model calls: ledger call, gateway row, replay label, hold
//	GET  /runs/{id}/calls/{request}      one call's raw request and response (redacted, capped)
//	POST /runs/{id}/replay               a replay run of a sealed run; never counted
//	POST /runs/{id}/fork                 replay to call N, apply the edit, continue live; never counted
//	POST /runs/{id}/debug                rerun live with breakpoints; never counted
//	GET  /runs/{id}/holds                the calls a breakpoint holds now, and the time held
//	POST /runs/{id}/holds/release        continue, step, edit or abort one held call

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/replay"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// HoldStatusDoc and HoldReleaseDoc name the schema types in the API description.
type (
	HoldStatusDoc  = schemas.HoldStatus
	HoldReleaseDoc = schemas.HoldRelease
)

// maxCallBody caps the request or response text one call answer carries.
const maxCallBody = 256 << 10

func init() {
	RegisterOrgArea("rundebug", func(env OrgEnv, r *router.Router) {
		h := &runDebugHandler{env: env}
		p := APIPrefix + "/runs/{id}"
		r.GET(p+"/calls", env.R(h.calls))
		r.GET(p+"/calls/{request}", env.R(h.call))
		r.POST(p+"/replay", env.W(h.replay))
		r.POST(p+"/fork", env.W(h.fork))
		r.POST(p+"/debug", env.W(h.debug))
		r.GET(p+"/holds", env.R(h.holds))
		r.POST(p+"/holds/release", env.W(h.release))
	})
}

type runDebugHandler struct {
	env OrgEnv
}

// RunCall is one model call of a run as the debugger shows it.
type RunCall struct {
	Call           int     `json:"call"`
	RequestID      string  `json:"request_id,omitempty"`
	Status         int     `json:"status"`
	Action         string  `json:"action"`
	Stream         bool    `json:"stream,omitempty"`
	Replay         string  `json:"replay,omitempty"`          // hit | divergence | edited | exhausted
	ReplayExpected string  `json:"replay_expected,omitempty"` // the recorded request hash a divergent call missed
	HeldMS         float64 `json:"held_ms,omitempty"`
	Operator       string  `json:"operator,omitempty"`
}

// RunCalls is the answer of GET /runs/{id}/calls.
type RunCalls struct {
	RunID string              `json:"run_id"`
	Calls []RunCall           `json:"calls"`
	Debug *schemas.RunDebug   `json:"debug,omitempty"`
	First *RunCall            `json:"first_divergence,omitempty"`
	Holds *schemas.HoldStatus `json:"holds,omitempty"`
}

// RunCallDetail is the answer of GET /runs/{id}/calls/{request}.
type RunCallDetail struct {
	RunID     string `json:"run_id"`
	RequestID string `json:"request_id"`
	Call      int    `json:"call,omitempty"`
	Model     string `json:"model,omitempty"`
	Stream    bool   `json:"stream,omitempty"`
	Status    int    `json:"status"`
	Request   string `json:"request"`
	Response  string `json:"response"`
	Clipped   bool   `json:"clipped,omitempty"`
	Available bool   `json:"available"` // false when the gateway stored no body for the call
}

// DebugRunRequest is the body of POST /runs/{id}/replay, /fork and /debug.
type DebugRunRequest struct {
	Call         int                  `json:"call,omitempty"`          // fork: the first live call
	Response     string               `json:"response,omitempty"`      // fork: the model's answer at call
	Message      string               `json:"message,omitempty"`       // fork: a user message added at call
	OnDivergence string               `json:"on_divergence,omitempty"` // stop | live | serve
	Breakpoints  *schemas.Breakpoints `json:"breakpoints,omitempty"`
}

// DebugRunResponse is the answer of the three debug run routes.
type DebugRunResponse struct {
	ExpID   string `json:"exp_id"`
	Mode    string `json:"mode"`
	FromRun string `json:"from_run"`
	Started bool   `json:"started"`
}

func (h *runDebugHandler) lookup(ctx *fasthttp.RequestCtx) (*RunView, bool) {
	idx := h.env.Services.Index
	if idx == nil {
		sendUnavailable(ctx, h.env.Mode, "run index")
		return nil, false
	}
	id, ok := idParam(ctx, h.env.Mode, "id", "run")
	if !ok {
		return nil, false
	}
	rv, ok := idx.Run(id)
	if !ok || rv == nil {
		sendNotFound(ctx, h.env.Mode, "run", id)
		return nil, false
	}
	if refuseHeldOutRun(ctx, h.env.Mode, idx, false, rv) {
		return nil, false
	}
	return rv, true
}

func (h *runDebugHandler) fail(ctx *fasthttp.RequestCtx, status int, msg string) {
	t := lib.ErrTypeBadRequest
	switch status {
	case fasthttp.StatusServiceUnavailable:
		t = lib.ErrTypeUnavailable
	case fasthttp.StatusConflict:
		t = lib.ErrTypeConflict
	case fasthttp.StatusNotFound:
		t = lib.ErrTypeNotFound
	}
	lib.SendError(ctx, h.env.Mode, status, t, msg)
}

// runFile reads one file of the run folder; a missing file gives nil.
func (h *runDebugHandler) runFile(runID, name string) ([]byte, error) {
	b, _, err := readInternal(h.env.Services.Index, runID, name)
	return b, err
}

func (h *runDebugHandler) calls(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	id := rv.Run.RunID
	ledger, err := h.runFile(id, replay.LedgerFile)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "read proxy ledger of run "+id, err)
		return
	}
	raw, err := h.runFile(id, replay.RawFile)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "read gateway rows of run "+id, err)
		return
	}
	out := RunCalls{RunID: id, Calls: joinCalls(id, replay.ParseLedger(ledger), replay.ParseRaw(raw)), Debug: rv.Run.Debug}
	for i := range out.Calls {
		if out.Calls[i].Replay == schemas.ReplayDivergence || out.Calls[i].Replay == schemas.ReplayExhausted {
			c := out.Calls[i]
			out.First = &c
			break
		}
	}
	if !rv.Sealed && h.env.Services.Holds != nil {
		work, cancel := workContext(ctx, readTimeout)
		defer cancel()
		if st, err := h.env.Services.Holds.Holds(work, id); err == nil {
			out.Holds = st
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// joinCalls lists a run's conversation calls in call order, each joined to
// its gateway row by request hash. Without a ledger every row is a call.
func joinCalls(runID string, ledger []schemas.LedgerEntry, raw []replay.RawCall) []RunCall {
	bySHA := map[string]string{}
	for _, r := range raw {
		if r.Request != "" {
			bySHA[utils.SHA256Bytes([]byte(r.Request))] = r.RequestID
		}
	}
	out := []RunCall{}
	for _, e := range ledger {
		if e.Kind != "message" || e.Call < 1 || (e.RunID != "" && e.RunID != runID) {
			continue
		}
		out = append(out, RunCall{Call: e.Call, RequestID: bySHA[e.ReqSHA256], Status: e.Status, Action: e.Action, Stream: e.Stream,
			Replay: e.Replay, ReplayExpected: e.ReplayExpected, HeldMS: e.HeldMS, Operator: e.Operator})
	}
	if len(out) == 0 {
		for i, r := range raw {
			out = append(out, RunCall{Call: i + 1, RequestID: r.RequestID, Status: r.Status, Action: "pass", Stream: r.Stream})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Call < out[j].Call })
	return out
}

func (h *runDebugHandler) call(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	reqID, ok := idParam(ctx, h.env.Mode, "request", "model call")
	if !ok {
		return
	}
	id := rv.Run.RunID
	raw, err := h.runFile(id, replay.RawFile)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "read gateway rows of run "+id, err)
		return
	}
	for _, r := range replay.ParseRaw(raw) {
		if r.RequestID != reqID {
			continue
		}
		out := RunCallDetail{RunID: id, RequestID: reqID, Model: r.Model, Stream: r.Stream, Status: r.Status,
			Available: r.Available && (r.Request != "" || r.Response != "")}
		var c1, c2 bool
		out.Request, c1 = clipBody(prettyJSON(r.Request))
		resp := r.Response
		if r.Stream {
			resp = strings.Join(replay.StreamPayloads(r.Response), "\n")
		}
		out.Response, c2 = clipBody(prettyJSON(resp))
		out.Clipped = c1 || c2
		if ledger, err := h.runFile(id, replay.LedgerFile); err == nil {
			sha := utils.SHA256Bytes([]byte(r.Request))
			for _, e := range replay.ParseLedger(ledger) {
				if e.Kind == "message" && e.ReqSHA256 == sha && e.Call > 0 {
					out.Call = e.Call
					break
				}
			}
		}
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
		return
	}
	sendNotFound(ctx, h.env.Mode, "model call", reqID)
}

// prettyJSON indents s when it is one JSON value; other text is kept.
func prettyJSON(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(b)
}

func clipBody(s string) (string, bool) {
	if len(s) <= maxCallBody {
		return s, false
	}
	return s[:maxCallBody], true
}

func (h *runDebugHandler) replay(ctx *fasthttp.RequestCtx) { h.start(ctx, schemas.DebugReplay) }
func (h *runDebugHandler) fork(ctx *fasthttp.RequestCtx)   { h.start(ctx, schemas.DebugFork) }
func (h *runDebugHandler) debug(ctx *fasthttp.RequestCtx)  { h.start(ctx, schemas.DebugStep) }

// start plans, freezes and starts a one-slot debug experiment of the run.
func (h *runDebugHandler) start(ctx *fasthttp.RequestCtx, mode string) {
	s := h.env.Services
	if s.Launcher == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "no experiment launcher on this server: debug runs need one")
		return
	}
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	var req DebugRunRequest
	if body := ctx.PostBody(); len(body) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(body)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			h.fail(ctx, fasthttp.StatusBadRequest, "parse debug run request: "+err.Error())
			return
		}
	}
	if !req.Breakpoints.Empty() && s.Holds == nil {
		h.fail(ctx, fasthttp.StatusConflict, "breakpoints need a worker this server controls; this server cannot hold model calls")
		return
	}
	m, ok := s.Index.Manifest(rv.Slot.ExpID)
	if !ok || m == nil {
		h.fail(ctx, fasthttp.StatusConflict, "the run's experiment has no frozen plan to debug from")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	exp, err := h.debugExperiment(work, rv, m, mode, &req)
	if err != nil {
		h.fail(ctx, debugStatus(err), err.Error())
		return
	}
	pm, err := s.Launcher.Plan(work, exp)
	if err == nil && pm == nil {
		err = errors.New("the planner returned no manifest")
	}
	if err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, "plan debug run: "+err.Error())
		return
	}
	if err := s.Launcher.Create(work, pm); err != nil {
		sendDepError(ctx, h.env.Mode, "create debug run "+pm.ExpID, err)
		return
	}
	tenancy.SetCreated(ctx, pm.ExpID)
	if err := s.Launcher.Start(withPrincipal(work, ctx), pm.ExpID); err != nil {
		sendDepError(ctx, h.env.Mode, "start debug run "+pm.ExpID+" (it was created; start it from its page)", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, DebugRunResponse{ExpID: pm.ExpID, Mode: mode, FromRun: rv.Run.RunID, Started: true})
}

var (
	errDebugBad      = errors.New("bad debug run request")
	errDebugConflict = errors.New("run cannot be debugged")
)

func debugStatus(err error) int {
	if errors.Is(err, errDebugConflict) {
		return fasthttp.StatusConflict
	}
	return fasthttp.StatusBadRequest
}

// debugExperiment builds the one-slot child experiment of run rv: its arm,
// task and condition, the parent's suite, budget and settings, and the
// debug spec with the cassette of the run's model calls.
func (h *runDebugHandler) debugExperiment(ctx context.Context, rv *RunView, m *schemas.Manifest, mode string, req *DebugRunRequest) (*schemas.Experiment, error) {
	if m.Experiment.Debug != nil && m.Experiment.Debug.Mode == schemas.DebugReplay && mode != schemas.DebugStep {
		// A replay of a replay reproduces the same recording: debug the source instead.
		return nil, fmt.Errorf("%w: this run is itself a replay; replay or fork its source run %s", errDebugConflict, m.Experiment.Debug.FromRun)
	}
	if rv.Slot.Condition != schemas.ConditionClean {
		return nil, fmt.Errorf("%w: only runs without a fault can be replayed, forked or stepped; this run ran under %s", errDebugConflict, rv.Slot.Condition)
	}
	arm, ok := m.Experiment.Arms[rv.Slot.Arm]
	if !ok {
		return nil, fmt.Errorf("%w: agent version %s is not in the evaluation's plan", errDebugConflict, rv.Slot.Arm)
	}
	d := &schemas.DebugSpec{Mode: mode, FromRun: rv.Run.RunID, FromExp: rv.Slot.ExpID, OnDivergence: req.OnDivergence, Breakpoints: req.Breakpoints}
	switch mode {
	case schemas.DebugFork:
		d.Call = req.Call
		if req.Response != "" || req.Message != "" {
			d.Edit = &schemas.DebugEdit{Response: req.Response, Message: req.Message}
		}
	default:
		if req.Call != 0 || req.Response != "" || req.Message != "" {
			return nil, fmt.Errorf("%w: call, response and message apply to a fork only", errDebugBad)
		}
	}
	if mode != schemas.DebugStep {
		if !rv.Sealed {
			return nil, fmt.Errorf("%w: the run is still running; replay or fork it once it has finished", errDebugConflict)
		}
		if sv, ok := h.env.Services.Index.(SealVerifier); ok {
			if err := sv.VerifySeal(ctx, rv.Run.RunID); err != nil {
				return nil, fmt.Errorf("%w: the run's files do not match its seal: %v", errDebugConflict, err)
			}
		}
		ledger, err := h.runFile(rv.Run.RunID, replay.LedgerFile)
		if err != nil {
			return nil, err
		}
		raw, err := h.runFile(rv.Run.RunID, replay.RawFile)
		if err != nil {
			return nil, err
		}
		cas, err := replay.Build(rv.Run.RunID, replay.ParseLedger(ledger), replay.ParseRaw(raw))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errDebugConflict, err)
		}
		if mode == schemas.DebugFork && req.Call > len(cas.Entries)+1 {
			return nil, fmt.Errorf("%w: the run made %d model calls; fork from call 1 to %d", errDebugBad, len(cas.Entries), len(cas.Entries)+1)
		}
		d.Cassette = cas
	}
	if err := d.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", errDebugBad, err)
	}
	e := m.Experiment
	name := mode + " " + rv.Run.RunID
	if mode == schemas.DebugFork {
		name += " at call " + strconv.Itoa(req.Call)
	}
	return &schemas.Experiment{
		Name: name, Suite: e.Suite, Base: e.Base,
		Arms:             map[string]schemas.ArmSpec{rv.Slot.Arm: arm},
		Primary:          schemas.Endpoint{Condition: schemas.ConditionClean, Metric: e.Primary.Metric},
		Conditions:       []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: schemas.TaskSelector{IDs: []string{rv.Slot.Task}}, Trials: 1}},
		Budget:           e.Budget,
		Verdict:          e.Verdict,
		ExtraGates:       e.ExtraGates,
		Seed:             e.Seed,
		Concurrency:      1,
		ManifestMode:     e.ManifestMode,
		Policy:           e.Policy,
		Requirements:     e.Requirements,
		Smoke:            e.Smoke,
		ScriptedUpstream: e.ScriptedUpstream,
		External:         e.External,
		Simulator:        e.Simulator,
		Runner:           e.Runner,
		Realism:          e.Realism,
		Environment:      e.Environment,
		Debug:            d,
	}, nil
}

func (h *runDebugHandler) holds(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	out := schemas.HoldStatus{RunID: rv.Run.RunID, Holds: []schemas.Hold{}}
	if hc := h.env.Services.Holds; hc != nil && !rv.Sealed {
		work, cancel := workContext(ctx, readTimeout)
		defer cancel()
		st, err := hc.Holds(work, rv.Run.RunID)
		if err != nil {
			sendDepError(ctx, h.env.Mode, "read breakpoints of run "+rv.Run.RunID, err)
			return
		}
		out = *st
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *runDebugHandler) release(ctx *fasthttp.RequestCtx) {
	hc := h.env.Services.Holds
	if hc == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "this server cannot hold model calls")
		return
	}
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	var rel schemas.HoldRelease
	dec := json.NewDecoder(strings.NewReader(string(ctx.PostBody())))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rel); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, "parse release: "+err.Error())
		return
	}
	if err := rel.Validate(); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	st, err := hc.Holds(work, rv.Run.RunID)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "read breakpoints of run "+rv.Run.RunID, err)
		return
	}
	for _, hold := range st.Holds {
		if hold.Call != rel.Call {
			continue
		}
		if err := hc.ReleaseHold(work, hold.Slot, rel); err != nil {
			h.fail(ctx, fasthttp.StatusConflict, err.Error())
			return
		}
		ctx.SetStatusCode(fasthttp.StatusNoContent)
		return
	}
	h.fail(ctx, fasthttp.StatusConflict, fmt.Sprintf("call %d is not held; it may have continued already", rel.Call))
}
