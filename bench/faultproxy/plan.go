package faultproxy

import (
	"crypto/sha256"
	"encoding/binary"
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
	// ModeSlow holds the calls of its window (DelayMS before the upstream
	// request, FirstByteDelayMS before the answer's first byte) and then
	// answers them normally (lane X22).
	ModeSlow = "slow"
	// ModeFlaky fails each call of its window with probability FailProb,
	// seeded by the plan's nonce so a run replays exactly (lane X22).
	ModeFlaky = "flaky"
)

// PlanModes lists every plan mode this proxy honours, in the order /health
// reports them. A caller that needs a mode checks for it there first: an
// older proxy rejects a mode it does not know with 400.
var PlanModes = []string{ModeNone, ModePersistent, ModeOnce, ModeScripted, ModeStreamCut, ModeReplay, ModeSlow, ModeFlaky}

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
	ActionReject = "reject" // unknown credential (never tied to a run), or a request refused for unpersisted plan state
)

// ValidatePlan checks a fault plan before it is bound to a slot. An empty mode
// means none, so a plan that only tags a clean run with its run id is valid.
func ValidatePlan(p schemas.FaultPlan) error {
	if p.RetryAfterS < 0 {
		return fmt.Errorf("retry_after_s must be >= 0, got %d", p.RetryAfterS)
	}
	if err := validateDebugPlan(p); err != nil { // X20b debug runs (debug.go)
		return err
	}
	if p.Model != "" && p.Mode != ModePersistent && p.Mode != ModeOnce && p.Mode != ModeSlow && p.Mode != ModeFlaky { // W9: a model filter only narrows a fault
		return fmt.Errorf("model %q needs mode persistent, once, slow or flaky, got %q", p.Model, p.Mode)
	}
	if err := validateWindow(p); err != nil {
		return err
	}
	switch p.Mode {
	case "", ModeNone, ModeReplay:
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
	case ModeSlow:
		if p.DelayMS == 0 && p.FirstByteDelayMS == 0 {
			return errors.New("slow plan needs delay_ms or first_byte_delay_ms")
		}
		if p.Status != 0 || p.RetryAfterS != 0 || p.Call != 0 {
			return errors.New("slow plan injects no status; status, retry_after_s and call must be unset")
		}
		return nil
	case ModeFlaky:
		if !(p.FailProb > 0 && p.FailProb <= 1) {
			return fmt.Errorf("flaky plan needs fail_prob in (0, 1], got %v", p.FailProb)
		}
	default:
		return fmt.Errorf("mode must be none, persistent, once, scripted, stream_cut, replay, slow or flaky, got %q", p.Mode)
	}
	if p.Status < 400 || p.Status > 599 {
		return fmt.Errorf("status must be 4xx or 5xx, got %d", p.Status)
	}
	return nil
}

// maxDelayMS caps the holds of a slow plan: ten minutes.
const maxDelayMS = 600_000

// validateWindow checks the recovery window and delay fields (lane X22):
// none negative, delays capped, fail_prob only on flaky, and a window end
// (calls, until_s) only on the modes that have a window.
func validateWindow(p schemas.FaultPlan) error {
	if p.Calls < 0 || p.UntilS < 0 || p.DelayMS < 0 || p.FirstByteDelayMS < 0 {
		return errors.New("calls, until_s, delay_ms and first_byte_delay_ms must be >= 0")
	}
	if p.DelayMS > maxDelayMS || p.FirstByteDelayMS > maxDelayMS {
		return fmt.Errorf("delay_ms and first_byte_delay_ms must be <= %d", maxDelayMS)
	}
	if p.FailProb != 0 && p.Mode != ModeFlaky {
		return fmt.Errorf("fail_prob needs mode flaky, got %q", p.Mode)
	}
	if (p.DelayMS != 0 || p.FirstByteDelayMS != 0) && p.Mode != ModeSlow {
		return fmt.Errorf("delay_ms and first_byte_delay_ms need mode slow, got %q", p.Mode)
	}
	if (p.Calls != 0 || p.UntilS != 0) && p.Mode != ModePersistent && p.Mode != ModeSlow && p.Mode != ModeFlaky {
		return fmt.Errorf("calls and until_s end a persistent, slow or flaky window, got mode %q", p.Mode)
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
	// windowStart is when the plan's window first fired (lane X22): until_s
	// counts from it. Zero until then.
	windowStart time.Time
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
	// DelayMS and FirstByteDelayMS hold a call a slow plan takes (lane X22).
	DelayMS          int
	FirstByteDelayMS int
	// Unpersisted is set when the slot's advanced counters could not be
	// written to the plan file (QC2-13-02): the request is refused, since a
	// restart would replay the call numbers and could fire a fault twice.
	Unpersisted error
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
	prev, had := t.slots[slot]
	t.slots[slot] = st
	if err := t.persistLocked(); err != nil {
		if had {
			t.slots[slot] = prev
		} else {
			delete(t.slots, slot)
		}
		return err
	}
	return nil
}

// persistedSlot is one slot's record in the plan file: the plan and, since
// QC2-13-02, its run's progress, so a restart neither fires a one-shot
// fault again nor serves the scripted tool turn twice.
type persistedSlot struct {
	Plan       schemas.FaultPlan `json:"plan"`
	Calls      int               `json:"calls,omitempty"`
	ModelCalls int               `json:"model_calls,omitempty"`
	AudioCalls int               `json:"audio_calls,omitempty"`
	ToolServed bool              `json:"tool_served,omitempty"`
	// WindowStart is when an until_s window opened (lane X22).
	WindowStart *time.Time `json:"window_start,omitempty"`
}

// planFile is the plan file's current format. Older files are a bare map
// of slot to plan (no progress), read by loadPlanTable as such.
type planFile struct {
	Version int                      `json:"version"`
	Slots   map[string]persistedSlot `json:"slots"`
}

const planFileVersion = 2

// persistLocked writes every bound plan with its counters to t.path.
// Called with t.mu held.
func (t *planTable) persistLocked() error {
	if t.path == "" {
		return nil
	}
	f := planFile{Version: planFileVersion, Slots: map[string]persistedSlot{}}
	for name, cur := range t.slots {
		if cur.hasPlan {
			ps := persistedSlot{Plan: cur.plan, Calls: cur.calls, ModelCalls: cur.modelCalls, AudioCalls: cur.audioCalls, ToolServed: cur.toolServed}
			if !cur.windowStart.IsZero() {
				ws := cur.windowStart
				ps.WindowStart = &ws
			}
			f.Slots[name] = ps
		}
	}
	if err := writePlans(t.path, f); err != nil {
		return fmt.Errorf("persist fault plans: %w", err)
	}
	return nil
}

// writePlans replaces the plan file atomically.
func writePlans(path string, plans any) error {
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
// plans a previous proxy process bound (3-B-01) with their call counters and
// scripted progress (QC2-13-02; a file of the older format restores plans
// with counters at zero). A plan file that cannot be read or holds an invalid plan is an
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
	var head struct {
		Version int `json:"version"`
	}
	slots := map[string]persistedSlot{}
	if json.Unmarshal(b, &head) == nil && head.Version != 0 {
		if head.Version != planFileVersion {
			return nil, fmt.Errorf("fault plans %s: unknown format version %d", path, head.Version)
		}
		var f planFile
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("fault plans %s: %w", path, err)
		}
		slots = f.Slots
	} else {
		var plans map[string]schemas.FaultPlan
		if err := json.Unmarshal(b, &plans); err != nil {
			return nil, fmt.Errorf("fault plans %s: %w", path, err)
		}
		for slot, p := range plans {
			slots[slot] = persistedSlot{Plan: p}
		}
	}
	for slot, ps := range slots {
		if err := ValidatePlan(ps.Plan); err != nil {
			return nil, fmt.Errorf("fault plans %s: slot %s: %w", path, slot, err)
		}
		if ps.Calls < 0 || ps.ModelCalls < 0 || ps.AudioCalls < 0 {
			return nil, fmt.Errorf("fault plans %s: slot %s: negative call counter", path, slot)
		}
		st := &slotState{plan: ps.Plan, hasPlan: true, calls: ps.Calls, modelCalls: ps.ModelCalls, audioCalls: ps.AudioCalls, toolServed: ps.ToolServed}
		if ps.WindowStart != nil {
			st.windowStart = *ps.WindowStart
		}
		t.slots[slot] = st
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

// beginModel is begin for a request whose model is known (W9). A counted
// call of a bound plan is persisted before it is decided (QC2-13-02); when
// that write fails the counters roll back and the decision carries
// Unpersisted, which the proxy refuses.
func (t *planTable) beginModel(slot, kind, model string) decision {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.state(slot)
	before := *st
	d := t.decideLocked(st, kind, model)
	if st.hasPlan && (st.calls != before.calls || st.modelCalls != before.modelCalls || st.audioCalls != before.audioCalls || !st.windowStart.Equal(before.windowStart)) {
		if err := t.persistLocked(); err != nil {
			*st = before
			return decision{RunID: d.RunID, Nonce: d.Nonce, Scripted: d.Scripted, Unpersisted: err}
		}
	}
	return d
}

// decideLocked counts the request on st and decides. Called with t.mu held.
func (t *planTable) decideLocked(st *slotState, kind, model string) decision {
	d := decision{RunID: st.plan.RunID, Nonce: st.plan.Nonce, Scripted: st.plan.Mode == ModeScripted || st.plan.Scripted}
	if kind == KindAudio { // X8 voice agents: audio counts only for a plan aimed at its model
		st.audioCalls++
		d.Call = st.audioCalls
		if !filtered(st.plan) || !SameModel(model, st.plan.Model) {
			return d
		}
		st.modelCalls++
		t.applyWindow(st, &d, st.modelCalls)
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
	t.applyWindow(st, &d, n)
	return d
}

// applyWindow decides call number n of the plan on st: inject, hold, or
// pass. A window that ends (calls, until_s) lets later calls through, so the
// model recovers (lane X22). Called with t.mu held.
func (t *planTable) applyWindow(st *slotState, d *decision, n int) {
	p := st.plan
	if !fires(p, n) {
		return
	}
	if p.UntilS > 0 {
		now := t.now()
		if st.windowStart.IsZero() {
			st.windowStart = now
		} else if now.Sub(st.windowStart) >= time.Duration(p.UntilS)*time.Second {
			return
		}
	}
	switch p.Mode {
	case ModeSlow:
		d.DelayMS, d.FirstByteDelayMS = p.DelayMS, p.FirstByteDelayMS
		return
	case ModeFlaky:
		if !flakyFails(p.Nonce, n, p.FailProb) {
			return
		}
	}
	d.Inject, d.Status, d.RetryAfterS = true, p.Status, p.RetryAfterS
}

// flakyFails is the seeded coin of a flaky plan: the same nonce and call
// number always give the same answer.
func flakyFails(nonce string, call int, p float64) bool {
	if p >= 1 {
		return true
	}
	if p <= 0 {
		return false
	}
	h := sha256.Sum256(fmt.Appendf(nil, "%s:%d", nonce, call))
	return float64(binary.BigEndian.Uint64(h[:8])>>11)/float64(1<<53) < p
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
	if st.hasPlan {
		if err := t.persistLocked(); err != nil { // QC2-13-02: an unrecorded tool turn could repeat after a restart
			st.toolServed = false
			return false
		}
	}
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
	case ModePersistent, ModeSlow, ModeFlaky:
		from := max(p.FromCall, 1)
		return call >= from && (p.Calls == 0 || call < from+p.Calls)
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
var PlanFields = []string{"model", "scripted", "replay", "hold", "calls", "until_s", "delay_ms", "first_byte_delay_ms", "fail_prob"}

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
