package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// The durable outbox (lane X16, audit N17). Every delivery is one JSON file
// under <project>/.heimdall/webhooks/outbox/<delivery id>.json holding the
// exact body to send. A failed attempt stays in the file with its next
// attempt time: 30 s after the first failure, doubling up to one hour
// between attempts, for up to MaxRetryWindow after the event. A delivery
// that succeeds moves to the delivery log; one that is refused for good (a
// 4xx other than 408 and 429, a deleted hook) or runs out of time stays in
// the outbox as failed, so POST .../retry can queue it again. Files survive
// a restart; the server's outbox worker calls ProcessDue on every org.

// Retry policy.
const (
	MaxRetryWindow = 24 * time.Hour
	firstBackoff   = 30 * time.Second
	maxBackoff     = time.Hour
	maxFailedKept  = 200
)

// ErrAlreadyDelivered refuses a retry of a delivered delivery.
var ErrAlreadyDelivered = errors.New("webhook delivery already succeeded")

// outboxItem is one queued delivery.
type outboxItem struct {
	Delivery Delivery        `json:"delivery"`
	Body     json.RawMessage `json:"body"`
}

// processing serializes the attempts of one store across goroutines, so the
// first attempt of Dispatch and the worker never send the same file twice.
var processing sync.Map // dir -> *sync.Mutex

// outbox locking: obMu guards the files; it is never held while sending.
var obMu sync.Mutex

func (s *Store) outboxDir() string { return filepath.Join(s.dir, "outbox") }

func (s *Store) itemPath(id string) string { return filepath.Join(s.outboxDir(), id+".json") }

// Enqueue writes one outbox file per enabled hook subscribed to ev's type
// and returns their delivery ids. Nothing is sent.
func (s *Store) Enqueue(ev Event) ([]string, error) {
	hs, err := s.List()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var ids []string
	for _, h := range hs {
		if !h.Enabled || !slices.Contains(h.Events, ev.Type) {
			continue
		}
		// The body is rendered in the hook's format now, so a retry sends
		// the same bytes (and a PagerDuty dedup key stays the same).
		body, err := Render(h, ev)
		if err != nil {
			return ids, err
		}
		next := now
		it := outboxItem{Body: body, Delivery: Delivery{ID: "whd_" + randHex(8), HookID: h.ID, Event: ev.Type, EventID: ev.ID, At: now,
			State: StateRetrying, NextAttemptAt: &next}}
		if err := s.writeItem(it); err != nil {
			return ids, err
		}
		ids = append(ids, it.Delivery.ID)
	}
	return ids, nil
}

// ProcessDue makes one attempt for every queued delivery whose next
// attempt time has come, and returns how many it attempted.
func (s *Store) ProcessDue(ctx context.Context, snd Sender, now time.Time) (int, error) {
	items, err := s.items()
	if err != nil {
		return 0, err
	}
	var ids []string
	for _, it := range items {
		if due(it, now) {
			ids = append(ids, it.Delivery.ID)
		}
	}
	return len(s.processIDs(ctx, snd, now, ids)), nil
}

// Retry queues a failed delivery again, attempted at the next pass.
func (s *Store) Retry(id string) (Delivery, error) {
	if !strings.HasPrefix(id, "whd_") || strings.ContainsAny(id, `/\.`) {
		return Delivery{}, ErrNotFound
	}
	obMu.Lock()
	defer obMu.Unlock()
	it, err := s.readItem(id)
	if errors.Is(err, os.ErrNotExist) {
		if ds, lerr := s.loadDeliveries(); lerr == nil {
			for _, d := range ds {
				if d.ID == id && d.OK {
					return Delivery{}, ErrAlreadyDelivered
				}
			}
		}
		return Delivery{}, fmt.Errorf("%w: delivery %s is not in the queue (only queued and failed deliveries can be retried)", ErrNotFound, id)
	}
	if err != nil {
		return Delivery{}, err
	}
	now := time.Now().UTC()
	it.Delivery.State, it.Delivery.NextAttemptAt = StateRetrying, &now
	// A manual retry gets a fresh 24 h window.
	it.Delivery.At = now
	if err := s.writeItemLocked(it); err != nil {
		return Delivery{}, err
	}
	return it.Delivery, nil
}

// processIDs attempts the listed deliveries once each (those still queued
// and due), and returns their state afterwards.
func (s *Store) processIDs(ctx context.Context, snd Sender, now time.Time, ids []string) []Delivery {
	if len(ids) == 0 {
		return nil
	}
	m, _ := processing.LoadOrStore(s.dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	hs, err := s.List()
	if err != nil {
		return nil
	}
	var out []Delivery
	for _, id := range ids {
		obMu.Lock()
		it, err := s.readItem(id)
		obMu.Unlock()
		if err != nil || !due(it, now) {
			continue
		}
		d := s.attempt(ctx, snd, hs, it, now)
		out = append(out, d)
	}
	return out
}

// attempt sends one queued delivery and stores the outcome.
func (s *Store) attempt(ctx context.Context, snd Sender, hs []Hook, it outboxItem, now time.Time) Delivery {
	d := it.Delivery
	i := slices.IndexFunc(hs, func(h Hook) bool { return h.ID == d.HookID })
	start := time.Now()
	permanent := false
	if i < 0 || !hs[i].Enabled {
		d.Error, permanent = "the webhook was deleted or turned off", true
	} else {
		h := hs[i]
		var code int
		var err error
		if !snd.AllowPrivate {
			err = ValidateURL(h.URL, false)
			permanent = err != nil
		}
		if err == nil {
			code, err = snd.post(ctx, snd.client(), h, Event{Type: d.Event}, d.ID, it.Body)
		}
		d.Attempts++
		d.StatusCode, d.Error, d.OK = code, "", false
		switch {
		case err != nil:
			d.Error = err.Error()
		case code >= 200 && code < 300:
			d.OK = true
		default:
			d.Error = fmt.Sprintf("receiver answered HTTP %d", code)
			permanent = code < 500 && code != http.StatusTooManyRequests && code != http.StatusRequestTimeout
		}
	}
	d.DurationMS = time.Since(start).Milliseconds()
	if d.OK {
		d.State, d.NextAttemptAt = StateDelivered, nil
		obMu.Lock()
		_ = os.Remove(s.itemPath(d.ID))
		obMu.Unlock()
		// The log has its own lock (mu); never take it while holding obMu.
		_ = s.Record(d)
		return d
	}
	obMu.Lock()
	defer obMu.Unlock()
	switch {
	case permanent || now.Sub(d.At) >= MaxRetryWindow:
		d.State, d.NextAttemptAt = StateFailed, nil
		it.Delivery = d
		_ = s.writeItemLocked(it)
		s.trimFailedLocked()
	default:
		next := now.Add(Backoff(d.Attempts))
		d.State, d.NextAttemptAt = StateRetrying, &next
		it.Delivery = d
		_ = s.writeItemLocked(it)
	}
	return d
}

// Backoff is the wait after the n-th failed attempt: 30 s, doubling, at
// most one hour.
func Backoff(n int) time.Duration {
	b := firstBackoff
	for i := 1; i < n && b < maxBackoff; i++ {
		b *= 2
	}
	return min(b, maxBackoff)
}

func due(it outboxItem, now time.Time) bool {
	return it.Delivery.State == StateRetrying && (it.Delivery.NextAttemptAt == nil || !it.Delivery.NextAttemptAt.After(now))
}

// outboxDeliveries lists the queued and failed deliveries.
func (s *Store) outboxDeliveries() ([]Delivery, error) {
	items, err := s.items()
	if err != nil {
		return nil, err
	}
	out := make([]Delivery, 0, len(items))
	for _, it := range items {
		out = append(out, it.Delivery)
	}
	return out, nil
}

func (s *Store) items() ([]outboxItem, error) {
	obMu.Lock()
	defer obMu.Unlock()
	ents, err := os.ReadDir(s.outboxDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []outboxItem
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || !strings.HasPrefix(id, "whd_") {
			continue
		}
		if it, err := s.readItem(id); err == nil {
			out = append(out, it)
		}
	}
	slices.SortFunc(out, func(a, b outboxItem) int { return a.Delivery.At.Compare(b.Delivery.At) })
	return out, nil
}

func (s *Store) readItem(id string) (outboxItem, error) {
	var it outboxItem
	b, err := os.ReadFile(s.itemPath(id))
	if err != nil {
		return it, err
	}
	if err := json.Unmarshal(b, &it); err != nil {
		return it, fmt.Errorf("outbox item %s: %w", id, err)
	}
	return it, nil
}

func (s *Store) writeItem(it outboxItem) error {
	obMu.Lock()
	defer obMu.Unlock()
	return s.writeItemLocked(it)
}

func (s *Store) writeItemLocked(it outboxItem) error {
	if err := os.MkdirAll(s.outboxDir(), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(it)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.outboxDir(), ".item-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.itemPath(it.Delivery.ID))
}

// trimFailedLocked keeps the newest maxFailedKept failed deliveries.
func (s *Store) trimFailedLocked() {
	ents, err := os.ReadDir(s.outboxDir())
	if err != nil {
		return
	}
	var failed []outboxItem
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if it, err := s.readItem(id); err == nil && it.Delivery.State == StateFailed {
			failed = append(failed, it)
		}
	}
	if len(failed) <= maxFailedKept {
		return
	}
	slices.SortFunc(failed, func(a, b outboxItem) int { return a.Delivery.At.Compare(b.Delivery.At) })
	for _, it := range failed[:len(failed)-maxFailedKept] {
		_ = os.Remove(s.itemPath(it.Delivery.ID))
	}
}
