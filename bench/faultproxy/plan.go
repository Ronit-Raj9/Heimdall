package faultproxy

import (
	"errors"
	"fmt"
	"sync"

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
	mu    sync.Mutex
	slots map[string]*slotState
}

// slotState is one slot's plan and counters.
type slotState struct {
	plan       schemas.FaultPlan
	hasPlan    bool
	calls      int
	toolServed bool // the scripted model already answered this run with a tool_use
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
	return &planTable{slots: map[string]*slotState{}}
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
	t.slots[slot] = &slotState{plan: p, hasPlan: true}
	return nil
}

// clear removes slot's plan and resets its counters.
func (t *planTable) clear(slot string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.slots[slot] = &slotState{}
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
// slot get distinct call numbers.
func (t *planTable) begin(slot, kind string) decision {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.state(slot)
	d := decision{RunID: st.plan.RunID, Nonce: st.plan.Nonce, Scripted: st.plan.Mode == ModeScripted}
	if kind != KindMessage {
		return d
	}
	st.calls++
	d.Call = st.calls
	if st.plan.Mode == ModeStreamCut {
		d.Cut = cuts(st.plan, d.Call)
		return d
	}
	if fires(st.plan, d.Call) {
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

// cuts reports whether a stream_cut plan takes conversation call number
// call: exactly Call, or every call from FromCall.
func cuts(p schemas.FaultPlan, call int) bool {
	return (p.Call > 0 && call == p.Call) || (p.FromCall > 0 && call >= p.FromCall)
}
