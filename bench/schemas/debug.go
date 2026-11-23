package schemas

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Run debugging (lane X20b): model-call replay, fork from a step and
// breakpoints. A debug experiment is a one-slot child of a sealed run. Its
// runs are never counted: the planner marks every slot uncounted, so no
// verdict, leaderboard or report card reads them.

// Debug modes (DebugSpec.Mode).
const (
	// DebugReplay answers every model call from the source run's recording;
	// nothing reaches a provider unless OnDivergence is live.
	DebugReplay = "replay"
	// DebugFork answers calls before Call from the recording, applies the
	// edit at Call, then runs live.
	DebugFork = "fork"
	// DebugStep runs live with breakpoints.
	DebugStep = "step"
)

// Divergence policies (DebugSpec.OnDivergence): what a replayed call does
// when the agent's request differs from the recording.
const (
	DivergenceStop  = "stop"  // answer an error; the run ends at the first difference
	DivergenceLive  = "live"  // go live from the first difference
	DivergenceServe = "serve" // keep answering from the recording by call number
)

// Ledger replay labels (LedgerEntry.Replay).
const (
	ReplayHit        = "hit"        // answered from the recording; the request matched
	ReplayDivergence = "divergence" // the request differed from the recording
	ReplayEdited     = "edited"     // the fork call: answered or sent with the operator's edit
	ReplayExhausted  = "exhausted"  // the agent made more calls than the recording holds
)

// LedgerUpstreamReplay labels a ledger line answered from a cassette.
const LedgerUpstreamReplay = "replay"

// DebugSpec makes an experiment a debug run of one recorded run. It is
// frozen in the manifest; the cassette travels with it so a remote worker
// needs nothing else.
type DebugSpec struct {
	Mode         string       `json:"mode" yaml:"mode"`
	FromRun      string       `json:"from_run,omitempty" yaml:"from_run,omitempty"`
	FromExp      string       `json:"from_exp,omitempty" yaml:"from_exp,omitempty"`
	Call         int          `json:"call,omitempty" yaml:"call,omitempty"` // fork: the first live call, 1-based
	OnDivergence string       `json:"on_divergence,omitempty" yaml:"on_divergence,omitempty"`
	Edit         *DebugEdit   `json:"edit,omitempty" yaml:"edit,omitempty"`
	Breakpoints  *Breakpoints `json:"breakpoints,omitempty" yaml:"breakpoints,omitempty"`
	Cassette     *Cassette    `json:"cassette,omitempty" yaml:"cassette,omitempty"`
	// FromCondition is the source run's fault condition when it was not
	// clean (a replay of a model-fault run). The debug run itself always
	// plans the clean condition: the cassette carries the faulted answers.
	FromCondition string `json:"from_condition,omitempty" yaml:"from_condition,omitempty"`
}

// DebugEdit is the operator's change at the fork call. Response replaces the
// model's answer to that call with this text; Message appends a user
// message to the request before it goes live. At most one is set.
type DebugEdit struct {
	Response string `json:"response,omitempty" yaml:"response,omitempty"`
	Message  string `json:"message,omitempty" yaml:"message,omitempty"`
}

// Kind names what the edit changes: "response", "message", or "" for none.
func (e *DebugEdit) Kind() string {
	switch {
	case e == nil:
		return ""
	case e.Response != "":
		return "response"
	case e.Message != "":
		return "message"
	}
	return ""
}

// Breakpoints say which model requests the fault proxy holds until the
// operator releases them. AfterTools holds the request that carries a
// result of one of these tools, so the run pauses right after the tool ran.
type Breakpoints struct {
	Calls      []int    `json:"calls,omitempty" yaml:"calls,omitempty"`
	Every      bool     `json:"every,omitempty" yaml:"every,omitempty"`
	AfterTools []string `json:"after_tools,omitempty" yaml:"after_tools,omitempty"`
}

// Empty reports whether no breakpoint is set.
func (b *Breakpoints) Empty() bool {
	return b == nil || (len(b.Calls) == 0 && !b.Every && len(b.AfterTools) == 0)
}

// Cassette is the recording of one run's model calls, built from its sealed
// proxy ledger and gateway.raw.jsonl. Requests are kept only as normalized
// hashes; responses are kept whole.
type Cassette struct {
	SourceRun string          `json:"source_run"`
	Entries   []CassetteEntry `json:"entries"`
}

// CassetteEntry is one recorded conversation call.
type CassetteEntry struct {
	Call    int    `json:"call"`               // 1-based ledger call number
	ReqHash string `json:"req_hash,omitempty"` // normalized request hash (replay.RequestHash)
	Model   string `json:"model,omitempty"`
	Stream  bool   `json:"stream,omitempty"`
	Status  int    `json:"status"`
	// Body is the response body: JSON, or for a stream the stored stream
	// payloads one per SSE event. Empty with Missing set when the gateway
	// kept no body for the call.
	Body    string `json:"body,omitempty"`
	Missing string `json:"missing,omitempty"`
}

// MaxCassetteBytes caps the response bytes one cassette carries.
const MaxCassetteBytes = 16 << 20

// Validate checks a debug spec before it is planned.
func (d *DebugSpec) Validate() error {
	if d == nil {
		return nil
	}
	switch d.Mode {
	case DebugReplay, DebugFork:
		if d.Cassette == nil || len(d.Cassette.Entries) == 0 {
			return fmt.Errorf("debug %s needs a recording of the source run's model calls", d.Mode)
		}
	case DebugStep:
		if d.Breakpoints.Empty() {
			return errors.New("debug step needs at least one breakpoint")
		}
	default:
		return fmt.Errorf("debug mode must be replay, fork or step, got %q", d.Mode)
	}
	switch d.OnDivergence {
	case "", DivergenceStop, DivergenceLive, DivergenceServe:
	default:
		return fmt.Errorf("on_divergence must be stop, live or serve, got %q", d.OnDivergence)
	}
	if d.Mode == DebugFork && d.Call < 1 {
		return errors.New("fork needs call >= 1")
	}
	if d.Mode != DebugFork && (d.Call != 0 || d.Edit != nil) {
		return errors.New("call and edit apply to a fork only")
	}
	if d.Edit != nil && d.Edit.Response != "" && d.Edit.Message != "" {
		return errors.New("edit either the response or add a message, not both")
	}
	if b := d.Breakpoints; b != nil {
		for _, c := range b.Calls {
			if c < 1 {
				return fmt.Errorf("breakpoint call must be >= 1, got %d", c)
			}
		}
	}
	if c := d.Cassette; c != nil {
		n := 0
		for _, e := range c.Entries {
			n += len(e.Body)
		}
		if n > MaxCassetteBytes {
			return fmt.Errorf("recording holds %d response bytes, more than the %d a debug run carries", n, MaxCassetteBytes)
		}
	}
	return nil
}

// Divergence is the policy in effect: the spec's, else stop for a replay
// (a reproduction at no cost) and live for a fork (it goes live anyway).
func (d *DebugSpec) Divergence() string {
	if d == nil {
		return ""
	}
	if d.OnDivergence != "" {
		return d.OnDivergence
	}
	if d.Mode == DebugFork {
		return DivergenceLive
	}
	return DivergenceStop
}

// RunDebug links a debug run to the run it came from (run.json).
type RunDebug struct {
	Mode    string `json:"mode"`
	FromRun string `json:"from_run,omitempty"`
	FromExp string `json:"from_exp,omitempty"`
	Call    int    `json:"call,omitempty"`
	// FromCondition is DebugSpec.FromCondition: the source run's fault
	// condition, empty when it was clean.
	FromCondition string `json:"from_condition,omitempty"`
	// Edit is what a fork changed at its fork call: "message" (a message
	// for the agent was added) or "response" (the model's answer was
	// replaced); empty when nothing was changed (fix wave 3, N247).
	Edit string `json:"edit,omitempty"`
	// HeldMS is the time the run spent held at breakpoints; the agent's
	// wall clock did not run during it.
	HeldMS int64 `json:"held_ms,omitempty"`
}

// ReplayPlan is the replay part of a fault plan (FaultPlan.Replay). Calls
// below UntilCall (every call when 0) are answered from the slot's
// cassette; call UntilCall gets the edit and later calls go live.
type ReplayPlan struct {
	UntilCall    int    `json:"until_call,omitempty"`
	OnDivergence string `json:"on_divergence,omitempty"`
	EditResponse string `json:"edit_response,omitempty"`
	EditMessage  string `json:"edit_message,omitempty"`
}

// Hold release actions.
const (
	HoldContinue = "continue" // send the request on; stop at the next breakpoint
	HoldStep     = "step"     // send it on and hold the very next call too
	HoldEdit     = "edit"     // answer with the operator's response, or send with an added message
	HoldAbort    = "abort"    // answer an error and refuse every later call of the run
)

// Hold is one model request a breakpoint is holding.
type Hold struct {
	Slot    string    `json:"slot"`
	RunID   string    `json:"run_id"`
	Call    int       `json:"call"`
	Since   time.Time `json:"since"`
	Reason  string    `json:"reason"` // call N | every call | after tool X | step
	Model   string    `json:"model,omitempty"`
	Stream  bool      `json:"stream,omitempty"`
	Request string    `json:"request,omitempty"` // the pending request body, secrets redacted, capped
	Clipped bool      `json:"clipped,omitempty"`
	// Deadline is when the hold continues by itself: the gateway gives up
	// on an upstream that answers nothing for five minutes.
	Deadline time.Time `json:"deadline"`
}

// HoldStatus is the fault proxy's answer to GET /holds?run_id=.
type HoldStatus struct {
	RunID  string `json:"run_id"`
	Holds  []Hold `json:"holds"`
	HeldMS int64  `json:"held_ms"` // total time the run's calls were held, current holds included
	// Stopped is set once the operator aborted the run.
	Stopped bool `json:"stopped,omitempty"`
}

// HoldRelease is the body of a release.
type HoldRelease struct {
	Call     int    `json:"call"`
	Action   string `json:"action"`
	Response string `json:"response,omitempty"` // edit: the model's answer
	Message  string `json:"message,omitempty"`  // edit: a user message appended to the request
}

// Validate checks a release.
func (r HoldRelease) Validate() error {
	switch r.Action {
	case HoldContinue, HoldStep, HoldAbort:
		if r.Response != "" || r.Message != "" {
			return fmt.Errorf("%s takes no response or message", r.Action)
		}
	case HoldEdit:
		if (r.Response == "") == (r.Message == "") {
			return errors.New("edit needs a response or a message, not both")
		}
	default:
		return fmt.Errorf("action must be continue, step, edit or abort, got %q", r.Action)
	}
	if r.Call < 1 {
		return errors.New("call must be >= 1")
	}
	return nil
}

// ReplayController is optionally implemented by a FaultController: it
// loads a cassette onto a key slot before a replay plan is bound.
type ReplayController interface {
	PutCassette(ctx context.Context, slot string, c *Cassette) error
}

// HoldController is optionally implemented by a FaultController: the
// breakpoints of a run and their release.
type HoldController interface {
	Holds(ctx context.Context, runID string) (*HoldStatus, error)
	ReleaseHold(ctx context.Context, slot string, r HoldRelease) error
}

// heldClockKey carries a HeldClock in a context.
type heldClockKey struct{}

// HeldClock reports how long a run has been held at breakpoints so far.
type HeldClock func() time.Duration

// WithHeldClock returns ctx carrying clock: an environment's wall budget
// is extended by the time it reports, so a run held at a breakpoint does
// not time out.
func WithHeldClock(ctx context.Context, clock HeldClock) context.Context {
	return context.WithValue(ctx, heldClockKey{}, clock)
}

// HeldClockFrom returns the HeldClock ctx carries, or nil.
func HeldClockFrom(ctx context.Context) HeldClock {
	c, _ := ctx.Value(heldClockKey{}).(HeldClock)
	return c
}
