package engine

import (
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/secrets"
)

// Live run events from remote workers: while a leased slot runs, the
// worker forwards its pipeline's progress events (run_started, stage) to
// the coordinator on the lease heartbeat, so a hosted server's live views
// show a remote run as it goes instead of only once its bundle is uploaded.
// The events are display only; the uploaded, sealed run is the record.

// DefaultLiveEventEvery is how often a worker flushes buffered live events
// between its regular lease heartbeats.
const DefaultLiveEventEvery = 2 * time.Second

// maxLiveBuffer bounds the events a worker holds for one lease while the
// coordinator cannot be reached; the oldest are dropped first.
const maxLiveBuffer = 4 * schemas.MaxHeartbeatEvents

// liveBuffer collects one lease's live events between heartbeats.
type liveBuffer struct {
	mu      sync.Mutex
	evs     []schemas.WorkerEvent
	secrets []string // the lease's secret values, masked in messages
	dropped int
}

// setSecrets records the lease's secret values so no message carries one.
func (b *liveBuffer) setSecrets(m map[string]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.secrets = b.secrets[:0]
	for _, v := range m {
		b.secrets = append(b.secrets, v)
	}
}

// add is the pipeline's Emit sink. Only the pipeline's progress kinds are
// kept; messages are redacted and cut.
func (b *liveBuffer) add(ev Event) {
	if !liveKinds[ev.Kind] {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	msg := secrets.Redact(ev.Message, b.secrets...)
	if len(msg) > schemas.MaxWorkerEventMessage {
		msg = strings.ToValidUTF8(msg[:schemas.MaxWorkerEventMessage], "")
	}
	at := ev.At
	if at.IsZero() {
		at = time.Now()
	}
	b.evs = append(b.evs, schemas.WorkerEvent{Kind: ev.Kind, RunID: ev.RunID, Stage: ev.Stage, Message: msg, At: at.UTC()})
	if len(b.evs) > maxLiveBuffer {
		b.dropped += len(b.evs) - maxLiveBuffer
		b.evs = append([]schemas.WorkerEvent(nil), b.evs[len(b.evs)-maxLiveBuffer:]...)
	}
}

// take returns at most MaxHeartbeatEvents of the oldest buffered events and
// removes them. b may be nil.
func (b *liveBuffer) take() []schemas.WorkerEvent {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(len(b.evs), schemas.MaxHeartbeatEvents)
	if n == 0 {
		return nil
	}
	out := append([]schemas.WorkerEvent(nil), b.evs[:n]...)
	b.evs = b.evs[n:]
	return out
}

// putBack returns events a failed heartbeat did not deliver to the front.
func (b *liveBuffer) putBack(evs []schemas.WorkerEvent) {
	if b == nil || len(evs) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evs = append(evs, b.evs...)
	if len(b.evs) > maxLiveBuffer {
		b.dropped += len(b.evs) - maxLiveBuffer
		b.evs = append([]schemas.WorkerEvent(nil), b.evs[len(b.evs)-maxLiveBuffer:]...)
	}
}

// pending reports whether events wait to be sent. b may be nil.
func (b *liveBuffer) pending() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.evs) > 0
}
