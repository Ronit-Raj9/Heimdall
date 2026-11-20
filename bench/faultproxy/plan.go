package faultproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Plan modes understood by the proxy. ModeScripted answers every call of
// the slot's run from the scripted model, whatever the provider and whether
// or not the proxy holds its real key, and never forwards a request or a key
// upstream; it injects nothing. `agent check` binds it so a conformance run
// is free.
const (
	ModeNone       = "none"
	ModePersistent = "persistent"
	ModeOnce       = "once"
	ModeScripted   = "scripted"
	// ModeStreamCut cuts a streamed answer after its first tool-call delta
	// (streamcut.go; Safety probes area, S1).
	ModeStreamCut = "stream_cut"
)

// PlanModes lists every plan mode this proxy honours, in the order /health
// reports them. A caller that needs a mode checks for it there first: an
// older proxy rejects a mode it does not know with 400.
var PlanModes = []string{ModeNone, ModePersistent, ModeOnce, ModeScripted, ModeStreamCut}

// Request kinds recorded in the ledger. Only KindMessage is counted.
const (
	KindMessage     = "message"
	KindCountTokens = "count_tokens"
	KindModels      = "models"
	KindOther       = "other"
	// KindAudio is a speech or transcription request (X8 voice agents,
	// ProviderSpec.Audio). It is never counted for a plan without a model;
	// a plan with a model (an STT-outage probe on whisper-1, say) counts and
	// fails audio requests for that model among themselves.
	KindAudio = "audio"
)

// Ledger actions.
const (
	ActionPass   = "pass"
	ActionInject = "inject"
	ActionReject = "reject" // unknown credential; never tied to a run
)

// ValidatePlan checks a fault plan before it is bound to a slot. An empty mode
// means none, so a plan that only tags a clean run with its run id is valid.
func ValidatePlan(p schemas.FaultPlan) error {
	if p.RetryAfterS < 0 {
		return fmt.Errorf("retry_after_s must be >= 0, got %d", p.RetryAfterS)
	}
	if p.Model != "" && p.Mode != ModePersistent && p.Mode != ModeOnce { // W9: a model filter only narrows a fault
		return fmt.Errorf("model %q needs mode persistent or once, got %q", p.Model, p.Mode)
	}
	switch p.Mode {
	case "", ModeNone:
		return nil
	case ModeScripted:
		if p.FromCall != 0 || p.Call != 0 || p.Status != 0 || p.RetryAfterS != 0 {
			return errors.New("scripted plan injects nothing; from_call, call, status and retry_after_s must be unset")
		}
		return nil
	case ModePersistent:
		if p.FromCall < 1 {
			return errors.New("persistent plan needs from_call >= 1")
		}
	case ModeOnce:
		if p.Call < 1 {
			return errors.New("once plan needs call >= 1")
		}
	case ModeStreamCut:
		if p.Call < 1 && p.FromCall < 1 {
			return errors.New("stream_cut plan needs call >= 1 or from_call >= 1")
		}
		if p.Status != 0 || p.RetryAfterS != 0 {
			return errors.New("stream_cut plan injects no status; status and retry_after_s must be unset")
		}
		return nil
	default:
		return fmt.Errorf("mode must be none, persistent, once, scripted or stream_cut, got %q", p.Mode)
	}
	if p.Status < 400 || p.Status > 599 {
		return fmt.Errorf("status must be 4xx or 5xx, got %d", p.Status)
	}
	return nil
}

// planTable holds the fault plan and call counter of every slot. Counters
// start at zero whenever a plan is set or cleared, so call numbers in the
// ledger are relative to the run that owns the slot.
type planTable struct {
	mu     sync.Mutex
	slots  map[string]*slotState
	claims map[string]*slotClaim // F1: slot ownership across bench processes (claims.go)
	now    func() time.Time
	// path is the file that keeps every bound plan across a proxy restart
	// (3-B-01): a scripted plan forgotten on restart would let its run's
	// next call reach the real upstream. Empty keeps plans in memory only.
	path string
}

// slotState is one slot's plan and counters.
type slotState struct {
	plan       schemas.FaultPlan
	hasPlan    bool
	calls      int
	toolServed bool // the scripted model already answered this run with a tool_use
	modelCalls int  // W9: message calls for the plan's model, which number a model-filtered fault
	audioCalls int  // X8 voice agents: audio requests of the slot (ledger call numbers of KindAudio lines)
}

// decision is what the proxy does with one request.
type decision struct {
	RunID       string
	Nonce       string
	Call        int // 0 for uncounted kinds
	Inject      bool
	Status      int
	RetryAfterS int
	Scripted    bool // the run is bound to ModeScripted: answer from the scripted model, never upstream
	Cut         bool // ModeStreamCut takes this call: a streamed answer is cut after its first tool-call delta
}

func newPlanTable() *planTable {
	return &planTable{slots: map[string]*slotState{}, claims: map[string]*slotClaim{}, now: time.Now}
}

// set binds p to slot and resets the slot's counters.
func (t *planTable) set(slot string, p schemas.FaultPlan) error {
	if err := ValidatePlan(p); err != nil {
		return err
	}
	if p.Mode == "" {
		p.Mode = ModeNone
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.commitLocked(slot, &slotState{plan: p, hasPlan: true})
}

// clear removes slot's plan and resets its counters.
func (t *planTable) clear(slot string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.commitLocked(slot, &slotState{})
}

// commitLocked puts st on slot and writes the bound plans to t.path first,
// so a plan the proxy acknowledged survives a restart. When the write fails
// nothing changes. Called with t.mu held.
func (t *planTable) commitLocked(slot string, st *slotState) error {
	if t.path != "" {
		plans := map[string]schemas.FaultPlan{}
		for name, cur := range t.slots {
			if cur.hasPlan && name != slot {
				plans[name] = cur.plan
			}
		}
		if st.hasPlan {
			plans[slot] = st.plan
		}
		if err := writePlans(t.path, plans); err != nil {
			return fmt.Errorf("persist fault plans: %w", err)
		}
	}
	t.slots[slot] = st
	return nil
}

// writePlans replaces the plan file atomically.
func writePlans(path string, plans map[string]schemas.FaultPlan) error {
	b, err := json.Marshal(plans)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if serr := tmp.Sync(); werr == nil {
		werr = serr
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return werr
	}
	return os.Rename(tmp.Name(), path)
}

// loadPlanTable returns a plan table that persists to path, holding the
// plans a previous proxy process bound (3-B-01). Call counters start again
// at zero. A plan file that cannot be read or holds an invalid plan is an
// error: starting without it could forward a scripted run's calls.
func loadPlanTable(path string) (*planTable, error) {
	t := newPlanTable()
	t.path = path
	if path == "" {
		return t, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read fault plans %s: %w", path, err)
	}
	var plans map[string]schemas.FaultPlan
	if err := json.Unmarshal(b, &plans); err != nil {
		return nil, fmt.Errorf("fault plans %s: %w", path, err)
	}
	for slot, p := range plans {
		if err := ValidatePlan(p); err != nil {
			return nil, fmt.Errorf("fault plans %s: slot %s: %w", path, slot, err)
		}
		t.slots[slot] = &slotState{plan: p, hasPlan: true}
	}
	return t, nil
}

// get returns slot's plan, if one is bound.
func (t *planTable) get(slot string) (schemas.FaultPlan, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.slots[slot]
	if !ok || !st.hasPlan {
		return schemas.FaultPlan{}, false
	}
	return st.plan, true
}

// begin counts a request of kind on slot and decides whether to inject.
// Counting and deciding happen under one lock so concurrent requests on a
// slot get distinct call numbers. A plan with a model (W9) fires only on
// requests for that model, numbered among themselves: from_call 1 fails the
// first call for the model whatever other models were called before; the
// ledger call number still counts every message call of the slot.
func (t *planTable) begin(slot, kind string) decision { return t.beginModel(slot, kind, "") }

// beginModel is begin for a request whose model is known (W9).
func (t *planTable) beginModel(slot, kind, model string) decision {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.state(slot)
	d := decision{RunID: st.plan.RunID, Nonce: st.plan.Nonce, Scripted: st.plan.Mode == ModeScripted || st.plan.Scripted}
	if kind == KindAudio { // X8 voice agents: audio counts only for a plan aimed at its model
		st.audioCalls++
		d.Call = st.audioCalls
		if !filtered(st.plan) || !SameModel(model, st.plan.Model) {
			return d
		}
		st.modelCalls++
		if fires(st.plan, st.modelCalls) {
			d.Inject, d.Status, d.RetryAfterS = true, st.plan.Status, st.plan.RetryAfterS
		}
		return d
	}
	if kind != KindMessage {
		return d
	}
	st.calls++
	d.Call = st.calls
	n := d.Call
	if filtered(st.plan) {
		if !SameModel(model, st.plan.Model) {
			return d
		}
		st.modelCalls++
		n = st.modelCalls
	}
	if st.plan.Mode == ModeStreamCut { // Safety probes area (S1): a stream cut, not an error status
		d.Cut = cuts(st.plan, n)
		return d
	}
	if fires(st.plan, n) {
		d.Inject = true
		d.Status = st.plan.Status
		d.RetryAfterS = st.plan.RetryAfterS
	}
	return d
}

// takeToolTurn reports whether the scripted model should answer with a
// tool_use: true once per run, then false until the next plan change.
func (t *planTable) takeToolTurn(slot string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.state(slot)
	if st.toolServed {
		return false
	}
	st.toolServed = true
	return true
}

func (t *planTable) state(slot string) *slotState {
	st, ok := t.slots[slot]
	if !ok {
		st = &slotState{}
		t.slots[slot] = st
	}
	return st
}

func fires(p schemas.FaultPlan, call int) bool {
	switch p.Mode {
	case ModePersistent:
		return call >= p.FromCall
	case ModeOnce:
		return call == p.Call
	default:
		return false
	}
}

// filtered reports whether the plan fails only one model's requests.
func filtered(p schemas.FaultPlan) bool {
	return p.Model != "" && p.Model != ModelAll
}

// PlanFields lists the optional plan fields this proxy honours, as /health
// reports them (W9). The control API decodes plans strictly, so an older
// proxy refuses a plan with a model instead of failing every model.
//
// "scripted" answers every call a fault plan does not fault from the
// scripted model (a probe on a no-paid run); a proxy without it refuses the
// plan, so such a run fails closed.
var PlanFields = []string{"model", "scripted"}

// ModelAll is the plan model that fails every model, as an empty one does;
// it lets a probe say so explicitly for a routed run, whose gateway probe
// would otherwise narrow the fault to the run's primary target (W9).
const ModelAll = "*"

// SameModel compares a request's model with a plan's, ignoring one leading
// provider segment on either side ("anthropic/claude-x" equals "claude-x"),
// as the model fidelity gate does. Bifrost sends the bare model upstream.
func SameModel(a, b string) bool {
	if a == b {
		return a != ""
	}
	_, ar, aok := strings.Cut(a, "/")
	_, br, bok := strings.Cut(b, "/")
	return (aok && ar == b) || (bok && a == br) || (aok && bok && ar == br)
}

// requestModel is the model a conversation request asks the provider for:
// the body's "model" (Anthropic and OpenAI formats), or the Gemini path
// segment between "models/" and ":". Empty when it cannot be read; such a
// request never matches a model-filtered plan. It is pure.
func requestModel(p ProviderSpec, r *http.Request, kind string, body []byte) string {
	if kind == KindAudio { // X8 voice agents: a JSON speech body or a multipart transcription form
		return audioModel(r, body)
	}
	if kind != KindMessage {
		return ""
	}
	if p.Format == FormatGemini {
		_, rest, ok := strings.Cut(r.URL.Path, "/models/")
		if !ok {
			return ""
		}
		model, _, _ := strings.Cut(rest, ":")
		return model
	}
	var req struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	return req.Model
}

// cuts reports whether a stream_cut plan takes conversation call number
// call: exactly Call, or every call from FromCall.
func cuts(p schemas.FaultPlan, call int) bool {
	return (p.Call > 0 && call == p.Call) || (p.FromCall > 0 && call >= p.FromCall)
}
