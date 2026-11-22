package main

// Index refresh off the hot path (INFRA_I4 item 1). After every sealed run
// the launcher used to rebuild the organization's whole index, which reads
// every run of every experiment: about N squared run reads over an
// evaluation of N runs. Now a seal marks its slot dirty, and a debounced
// refresh re-reads only the dirty slots of that experiment
// (store.Index.RefreshSlots). The full Rebuild still runs at experiment end
// (execute), on create and import paths that call l.rebuild, and here at
// most every indexFullEvery, so anything a refresh could miss is corrected.

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// Variables so tests can shorten them.
var (
	// indexRefreshDelay collects the seals of a burst into one refresh.
	indexRefreshDelay = 2 * time.Second
	// indexFullEvery is the longest time between two full rebuilds while
	// runs keep sealing.
	indexFullEvery = 10 * time.Minute
)

// slotIndexRefresher batches slot refreshes for one index.
type slotIndexRefresher struct {
	ix     *store.Index
	logger schemas.Logger

	mu       sync.Mutex
	dirty    map[string]map[string]bool // exp -> slots
	timer    *time.Timer
	lastFull time.Time
}

var slotIndexRefreshers sync.Map // *store.Index -> *slotIndexRefresher

func refresherFor(ix *store.Index, logger schemas.Logger) *slotIndexRefresher {
	if r, ok := slotIndexRefreshers.Load(ix); ok {
		return r.(*slotIndexRefresher)
	}
	r, _ := slotIndexRefreshers.LoadOrStore(ix, &slotIndexRefresher{ix: ix, logger: logger, dirty: map[string]map[string]bool{}, lastFull: time.Now()})
	return r.(*slotIndexRefresher)
}

// noteSeal marks a slot of an experiment dirty and schedules a refresh.
func (r *slotIndexRefresher) noteSeal(expID, slotID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	slots := r.dirty[expID]
	if slots == nil {
		slots = map[string]bool{}
		r.dirty[expID] = slots
	}
	if slotID != "" {
		slots[slotID] = true
	}
	if r.timer == nil {
		r.timer = time.AfterFunc(indexRefreshDelay, func() { r.flush(context.Background()) })
	}
}

// flush applies every pending refresh now: a full rebuild when the last one
// is older than indexFullEvery, else one RefreshSlots per experiment. A
// refresh that fails falls back to a full rebuild.
func (r *slotIndexRefresher) flush(ctx context.Context) {
	r.mu.Lock()
	dirty := r.dirty
	r.dirty = map[string]map[string]bool{}
	r.timer = nil
	full := time.Since(r.lastFull) >= indexFullEvery
	if full {
		r.lastFull = time.Now()
	}
	r.mu.Unlock()
	if len(dirty) == 0 {
		return
	}
	if full {
		r.rebuild(ctx)
		return
	}
	exps := make([]string, 0, len(dirty))
	for id := range dirty {
		exps = append(exps, id)
	}
	sort.Strings(exps)
	for _, id := range exps {
		slots := make([]string, 0, len(dirty[id]))
		for s := range dirty[id] {
			slots = append(slots, s)
		}
		sort.Strings(slots)
		if err := r.ix.RefreshSlots(ctx, id, slots); err != nil {
			r.logger.Warn("index refresh failed; rebuilding the whole index", "exp_id", id, "error", err.Error())
			r.rebuild(ctx)
			return
		}
	}
}

func (r *slotIndexRefresher) rebuild(ctx context.Context) {
	if err := r.ix.Rebuild(ctx); err != nil {
		r.logger.Warn("index rebuild failed", "error", err.Error())
	}
}

// refreshOnSeal is the launcher's index update after a sealed run.
func (l *launcher) refreshOnSeal(expID, slotID string) {
	if l.ix == nil {
		return
	}
	refresherFor(l.ix, l.a.logger).noteSeal(expID, slotID)
}

// refreshExperiment re-indexes one experiment (a create): the other
// experiments of the organization are not read again.
func (l *launcher) refreshExperiment(ctx context.Context, expID string) error {
	if l.ix == nil {
		return nil
	}
	if err := l.ix.RefreshExperiment(ctx, expID); err != nil {
		l.a.logger.Warn("index refresh failed; rebuilding the whole index", "exp_id", expID, "error", err.Error())
		return l.rebuild(ctx)
	}
	return nil
}
