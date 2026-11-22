// Package livefeed holds the newest screen frame of every running browser
// or desktop run, and the inputs a person queued for a debug run they took
// control of (lane Z03, schemas/recordings.go).
//
// It is memory only and display only: a frame is never written anywhere,
// and the sealed run folder stays the record. A hub serves one
// organization; For hands out one per org, and local mode's org ("" or
// "local") shares one, which in-process runs publish to directly. Remote
// runs publish through the worker route POST
// /workers/leases/{lease_id}/live-frame, which takes the queued inputs back
// to the worker in its answer.
package livefeed

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// FrameTTL is how long a frame is shown after it was published. A run that
// stopped publishing (it ended, or its worker went away) drops off the page.
const FrameTTL = 15 * time.Second

// forgetAfter is when a run's entry is removed altogether.
const forgetAfter = 2 * time.Minute

// MaxQueued bounds the inputs waiting for one run.
const MaxQueued = 32

// Errors of Queue.
var (
	ErrNotLive     = errors.New("the run is not showing its screen")
	ErrNotDebug    = errors.New("only a debug run can be taken over: start a debug run of this run first")
	ErrNoTakeover  = errors.New("this run's screen takes no input")
	ErrQueueFull   = errors.New("too many inputs are waiting; wait for the screen to catch up")
	ErrBadAction   = errors.New("action must be click, type, key or scroll")
	ErrServiceGone = errors.New("the service is not this run's live screen")
)

type entry struct {
	frame  schemas.LiveFrame
	at     time.Time // when the frame was published (server clock)
	inputs []schemas.HumanAction
	seq    int
	taken  int // inputs handed to the run so far
}

// Hub is one organization's live frames and queued inputs. Safe for
// concurrent use.
type Hub struct {
	mu   sync.Mutex
	runs map[string]*entry
	now  func() time.Time
}

// New returns an empty hub.
func New() *Hub { return &Hub{runs: map[string]*entry{}, now: time.Now} }

var (
	hubsMu sync.Mutex
	hubs   = map[string]*Hub{}
)

// For returns the hub of an organization. "" and "local" are the same org.
func For(orgID string) *Hub {
	if orgID == "" {
		orgID = "local"
	}
	hubsMu.Lock()
	defer hubsMu.Unlock()
	h, ok := hubs[orgID]
	if !ok {
		h = New()
		hubs[orgID] = h
	}
	return h
}

// Publish stores the run's newest frame. Inputs already queued stay.
func (h *Hub) Publish(f schemas.LiveFrame) {
	if f.RunID == "" || len(f.Data) == 0 || len(f.Data) > schemas.MaxLiveFrameBytes {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	h.pruneLocked(now)
	e := h.runs[f.RunID]
	if e == nil {
		e = &entry{}
		h.runs[f.RunID] = e
	}
	if f.At.IsZero() || f.At.After(now) {
		f.At = now
	}
	e.frame, e.at = f, now
	if !f.Debug || !f.TakeoverOK {
		e.inputs = nil
	}
}

// Frame returns the run's newest frame while it is fresh.
func (h *Hub) Frame(runID string) (schemas.LiveFrame, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.runs[runID]
	if e == nil || h.now().Sub(e.at) > FrameTTL {
		return schemas.LiveFrame{}, false
	}
	return e.frame, true
}

// Queue adds a person's input for a live debug run whose service takes
// input, and returns it with its sequence number.
func (h *Hub) Queue(runID string, a schemas.HumanAction) (schemas.HumanAction, error) {
	if err := CheckAction(a); err != nil {
		return a, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	e := h.runs[runID]
	switch {
	case e == nil || now.Sub(e.at) > FrameTTL:
		return a, ErrNotLive
	case !e.frame.Debug:
		return a, ErrNotDebug
	case !e.frame.TakeoverOK:
		return a, ErrNoTakeover
	case a.Service != "" && a.Service != e.frame.Service:
		return a, ErrServiceGone
	case len(e.inputs) >= MaxQueued:
		return a, ErrQueueFull
	}
	e.seq++
	a.Seq, a.At, a.Service = e.seq, now.UTC(), e.frame.Service
	e.inputs = append(e.inputs, a)
	return a, nil
}

// Take removes and returns the run's queued inputs, oldest first.
func (h *Hub) Take(runID string) []schemas.HumanAction {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.runs[runID]
	if e == nil || len(e.inputs) == 0 {
		return nil
	}
	out := e.inputs
	e.inputs = nil
	e.taken += len(out)
	return out
}

// Pending is the number of inputs waiting for the run.
func (h *Hub) Pending(runID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e := h.runs[runID]; e != nil {
		return len(e.inputs)
	}
	return 0
}

// End forgets a run: it stopped showing its screen.
func (h *Hub) End(runID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.runs, runID)
}

func (h *Hub) pruneLocked(now time.Time) {
	for id, e := range h.runs {
		if now.Sub(e.at) > forgetAfter {
			delete(h.runs, id)
		}
	}
}

// CheckAction validates one input. It is pure.
func CheckAction(a schemas.HumanAction) error {
	if a.FrameW < 0 || a.FrameH < 0 || a.FrameW > 16384 || a.FrameH > 16384 || (a.FrameW == 0) != (a.FrameH == 0) {
		return errors.New("frame_w and frame_h must both be set to the frame size, or both left out")
	}
	switch a.Action {
	case schemas.HumanClick:
		if a.X < 0 || a.Y < 0 || a.X > 16384 || a.Y > 16384 {
			return errors.New("click needs x and y on the screen")
		}
	case schemas.HumanType:
		if a.Text == "" || len(a.Text) > schemas.MaxHumanText {
			return errors.New("type needs text of at most 2000 bytes")
		}
	case schemas.HumanKey:
		if a.Combo == "" || len(a.Combo) > 64 || strings.ContainsAny(a.Combo, " \t\n") {
			return errors.New("key needs a combo such as ctrl+s or Return")
		}
	case schemas.HumanScroll:
		switch a.Direction {
		case "up", "down", "left", "right":
		default:
			return errors.New("scroll needs direction up, down, left or right")
		}
	default:
		return ErrBadAction
	}
	return nil
}
