package handlers

import (
	"bufio"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Live event tuning. The recorder keeps the newest events per experiment for
// the polling fallback; SSE streams send a comment as a keep-alive.
const (
	eventLogLimit  = 2000
	sseKeepAlive   = 15 * time.Second
	sseRetryMillis = 2000 // the designed polling interval, also EventSource's reconnect delay
	sseEndEvent    = "end"
)

// eventRecorder subscribes to the launcher once per experiment and keeps the
// newest events, so the polling fallback can answer "everything since seq".
type eventRecorder struct {
	launcher Launcher
	stop     chan struct{}
	stopOnce sync.Once

	mu   sync.Mutex
	logs map[string]*expEventLog
}

// expEventLog is one experiment's recorded events. The recorder numbers
// them itself, so the poll cursor is gap-free whatever the launcher sets.
type expEventLog struct {
	events    []schemas.LiveEvent
	next      int  // seq of the next event; seqs start at 1
	following bool // a subscription goroutine is running
}

// newEventRecorder creates a recorder. launcher may be nil.
func newEventRecorder(launcher Launcher) *eventRecorder {
	return &eventRecorder{launcher: launcher, stop: make(chan struct{}), logs: map[string]*expEventLog{}}
}

// follow subscribes to expID unless a subscription is already running. A
// finished experiment's channel closes at once, so following it is cheap.
func (r *eventRecorder) follow(expID string) {
	if r.launcher == nil {
		return
	}
	r.mu.Lock()
	log := r.logOf(expID)
	if log.following {
		r.mu.Unlock()
		return
	}
	ch, cancel := r.launcher.Events(expID)
	if ch == nil {
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return
	}
	log.following = true
	r.mu.Unlock()
	go r.consume(expID, ch, cancel)
}

// since returns the recorded events with seq > after, the cursor for the next
// poll, and whether older events were dropped from the window.
func (r *eventRecorder) since(expID string, after int) (events []schemas.LiveEvent, next int, truncated bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	log := r.logOf(expID)
	next = log.next - 1
	if next < 0 {
		next = 0
	}
	events = []schemas.LiveEvent{}
	for _, e := range log.events {
		if e.Seq > after {
			events = append(events, e)
		}
	}
	truncated = len(log.events) > 0 && log.events[0].Seq > after+1
	return events, next, truncated
}

// close ends every subscription and every open SSE stream.
func (r *eventRecorder) close() {
	r.stopOnce.Do(func() { close(r.stop) })
}

// consume records events until the channel closes or the recorder stops.
func (r *eventRecorder) consume(expID string, ch <-chan schemas.LiveEvent, cancel func()) {
	defer func() {
		if cancel != nil {
			cancel()
		}
		r.mu.Lock()
		r.logOf(expID).following = false
		r.mu.Unlock()
	}()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			r.mu.Lock()
			log := r.logOf(expID)
			ev.Seq = log.next
			log.events = append(log.events, ev)
			log.next++
			if len(log.events) > eventLogLimit {
				log.events = append([]schemas.LiveEvent(nil), log.events[len(log.events)-eventLogLimit:]...)
			}
			r.mu.Unlock()
		case <-r.stop:
			return
		}
	}
}

// logOf returns the log of expID, creating it. Callers hold mu.
func (r *eventRecorder) logOf(expID string) *expEventLog {
	log, ok := r.logs[expID]
	if !ok {
		log = &expEventLog{next: 1}
		r.logs[expID] = log
	}
	return log
}

// streamSSE writes live events as server-sent events until the channel
// closes, the client goes away (a failed flush), the server stops or allowed
// (the request's stream guard) no longer lets the caller read. It runs after
// the handler returned, so it must not touch the request context.
func streamSSE(w *bufio.Writer, mode schemas.Mode, ch <-chan schemas.LiveEvent, cancel func(), stop <-chan struct{}, allowed func() bool) {
	if cancel != nil {
		defer cancel()
	}
	if !writeSSE(w, "retry: "+strconv.Itoa(sseRetryMillis)+"\n\n") {
		return
	}
	keepAlive := time.NewTicker(sseKeepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				// A named "end" event: onmessage listeners never see it, and a
				// client listening for it closes instead of reconnecting.
				if data, err := sseData(schemas.LiveEvent{Kind: sseEndEvent, At: time.Now().UTC()}, mode); err == nil {
					writeSSE(w, "event: "+sseEndEvent+"\n"+data)
				}
				return
			}
			if !allowed() {
				return
			}
			data, err := sseData(ev, mode)
			if err != nil {
				continue
			}
			if !writeSSE(w, data) {
				return
			}
		case <-keepAlive.C:
			if !allowed() || !writeSSE(w, ": keep-alive\n\n") {
				return
			}
		case <-stop:
			return
		}
	}
}

// sseData formats one event as an SSE message: its seq as the id when the
// launcher numbered it, and the event JSON with mode merged in as the data.
func sseData(ev schemas.LiveEvent, mode schemas.Mode) (string, error) {
	body, err := sonic.ConfigStd.Marshal(ev)
	if err != nil {
		return "", fmt.Errorf("encode live event: %w", err)
	}
	body, err = lib.WithMode(body, mode)
	if err != nil {
		return "", fmt.Errorf("encode live event: %w", err)
	}
	msg := "data: " + string(body) + "\n\n"
	if ev.Seq > 0 {
		msg = "id: " + strconv.Itoa(ev.Seq) + "\n" + msg
	}
	return msg, nil
}

// writeSSE writes and flushes one chunk. False means the client is gone.
func writeSSE(w *bufio.Writer, chunk string) bool {
	if _, err := w.WriteString(chunk); err != nil {
		return false
	}
	return w.Flush() == nil
}
