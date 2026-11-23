package handlers

import (
	"context"
	"slices"
	"sync"
	"time"
)

// pausedTTL is how long a read of an experiment's run controls is reused
// by its status, like the run problem (fix wave 3, N199).
const pausedTTL = time.Second

// pausedCache holds the newest run controls read per experiment.
type pausedCache struct {
	mu   sync.Mutex
	byID map[string]pausedRead
}

type pausedRead struct {
	at     time.Time
	paused bool
	models []string
}

// pausedFacts reads whether the experiment's run controls pause it and the
// models they hold, from the launcher's RunOperator; nothing when the
// launcher has no run controls or they cannot be read.
func (h *ExperimentsHandler) pausedFacts(expID string) (bool, []string) {
	op, ok := h.launcher.(RunOperator)
	if !ok {
		return false, nil
	}
	c := &h.paused
	c.mu.Lock()
	if r, hit := c.byID[expID]; hit && time.Since(r.at) < pausedTTL {
		c.mu.Unlock()
		return r.paused, slices.Clone(r.models)
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	st, _, _ := op.Controls(ctx, expID)
	r := pausedRead{at: time.Now()}
	if st != nil { // a log that does not verify still returns its state (err wraps runctl.ErrBrokenLog)
		r.paused, r.models = st.Paused(), slices.Clone(st.PausedModels)
	}
	c.mu.Lock()
	if c.byID == nil {
		c.byID = map[string]pausedRead{}
	}
	c.byID[expID] = r
	c.mu.Unlock()
	return r.paused, slices.Clone(r.models)
}
