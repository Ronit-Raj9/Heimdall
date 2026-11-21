// Package webhooks stores an organization's outgoing webhooks and delivers
// signed event payloads to them (web parity lane L19, Integrations).
//
// Hooks live in <project root>/.heimdall/webhooks/hooks.json (0600), outside
// every area the project files API serves, so a signing secret is never
// readable through the files UI or the project export. With a Cipher (the
// keystore's master key, OpenWith) the secret is stored encrypted and a
// plaintext one from an older file is encrypted on first read. The secret
// is shown once when a hook is created; afterwards only its last four
// characters are.
//
// Deliveries go through a durable outbox (outbox.go): each event becomes one
// file per subscribed hook under outbox/, retried with exponential backoff
// for up to 24 hours, so a restart loses nothing.
//
// Each delivery is a POST of a JSON Event with these headers:
//
//	X-Heimdall-Event:     the event type, e.g. experiment.finished
//	X-Heimdall-Delivery:  the delivery id
//	X-Heimdall-Timestamp: unix seconds
//	X-Heimdall-Signature: sha256=<hex HMAC-SHA256(secret, timestamp + "." + body)>
package webhooks

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Event types a hook can subscribe to.
const (
	EventExperimentFinished = "experiment.finished" // an experiment ended (succeeded, failed or cancelled): its run job, a schedule.fire job, or an in-process run
	EventJobFailed          = "job.failed"          // any other background job failed
	EventPing               = "ping"                // the Test button; always delivered to the hook tested
)

// Events lists the subscribable event types.
var Events = []string{EventExperimentFinished, EventJobFailed}

// Limits.
const (
	MaxHooks         = 20
	MaxURLBytes      = 2048
	MaxDescBytes     = 200
	maxDeliveries    = 200
	maxResponseBytes = 4 << 10
)

// Errors.
var (
	ErrNotFound = errors.New("webhook not found")
	ErrInvalid  = errors.New("invalid webhook")
)

// Hook is one stored webhook. Secret never leaves the server after Create.
type Hook struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Events      []string  `json:"events"`
	Description string    `json:"description,omitempty"`
	Enabled     bool      `json:"enabled"`
	Secret      string    `json:"secret,omitempty"`
	SecretEnc   string    `json:"secret_enc,omitempty"` // Secret sealed by the Cipher; Secret is then empty on disk
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Cipher seals hook secrets at rest. keystore.Sealer implements it with the
// server's master key and the org as associated data.
type Cipher interface {
	Seal(id, plaintext string) (string, error)
	Open(id, sealed string) (string, error)
}

// View is a hook as the API returns it: the secret reduced to its last four.
type View struct {
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	Events       []string  `json:"events"`
	Description  string    `json:"description,omitempty"`
	Enabled      bool      `json:"enabled"`
	SecretLast4  string    `json:"secret_last4"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastDelivery *Delivery `json:"last_delivery,omitempty"`
}

// View returns the hook without its secret.
func (h Hook) View() View {
	last4 := h.Secret
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	return View{ID: h.ID, URL: redactURL(h.URL), Events: h.Events, Description: h.Description, Enabled: h.Enabled,
		SecretLast4: last4, CreatedBy: h.CreatedBy, CreatedAt: h.CreatedAt}
}

// Input is what Create and Update take. On Update a nil field keeps its value.
type Input struct {
	URL         *string   `json:"url,omitempty"`
	Events      *[]string `json:"events,omitempty"`
	Description *string   `json:"description,omitempty"`
	Enabled     *bool     `json:"enabled,omitempty"`
}

// Event is the JSON body of a delivery.
type Event struct {
	ID    string    `json:"id"`
	Type  string    `json:"type"`
	OrgID string    `json:"org_id,omitempty"`
	At    time.Time `json:"at"`
	Data  any       `json:"data"`
}

// Delivery is the outcome of one event sent to one hook.
type Delivery struct {
	ID         string    `json:"id"`
	HookID     string    `json:"hook_id"`
	Event      string    `json:"event"`
	EventID    string    `json:"event_id"`
	At         time.Time `json:"at"`
	OK         bool      `json:"ok"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	Attempts   int       `json:"attempts"`
	DurationMS int64     `json:"duration_ms"`
	// State is delivered, retrying (in the outbox, NextAttemptAt says when)
	// or failed (gave up; POST .../retry queues it again).
	State         string     `json:"state"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

// Delivery states.
const (
	StateDelivered = "delivered"
	StateRetrying  = "retrying"
	StateFailed    = "failed"
)

// Store keeps one project's hooks, outbox and recent deliveries.
type Store struct {
	dir    string
	cipher Cipher
}

// One lock for every store: writes are rare and small.
var mu sync.Mutex

// Open returns the store of a project root. Nothing is created until a write.
func Open(projectRoot string) *Store {
	return &Store{dir: filepath.Join(projectRoot, ".heimdall", "webhooks")}
}

// OpenWith is Open with secrets sealed by c at rest (nil: plaintext, 0600).
func OpenWith(projectRoot string, c Cipher) *Store {
	s := Open(projectRoot)
	s.cipher = c
	return s
}

func (s *Store) hooksPath() string      { return filepath.Join(s.dir, "hooks.json") }
func (s *Store) deliveriesPath() string { return filepath.Join(s.dir, "deliveries.jsonl") }

func (s *Store) load() ([]Hook, error) {
	b, err := os.ReadFile(s.hooksPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hs []Hook
	if err := json.Unmarshal(b, &hs); err != nil {
		return nil, fmt.Errorf("webhooks file is not valid JSON: %w", err)
	}
	legacy := false
	for i := range hs {
		h := &hs[i]
		if h.SecretEnc != "" {
			if s.cipher == nil {
				return nil, fmt.Errorf("webhook %s: its secret is encrypted and this server has no master key", h.ID)
			}
			plain, err := s.cipher.Open(h.ID, h.SecretEnc)
			if err != nil {
				return nil, fmt.Errorf("webhook %s: decrypt secret: %w", h.ID, err)
			}
			h.Secret, h.SecretEnc = plain, ""
		} else if h.Secret != "" && s.cipher != nil {
			legacy = true
		}
	}
	if legacy {
		// A file written before secrets were sealed: seal it now.
		if err := s.save(hs); err != nil {
			return nil, fmt.Errorf("encrypt webhook secrets: %w", err)
		}
	}
	return hs, nil
}

func (s *Store) save(hs []Hook) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	disk := slices.Clone(hs)
	if s.cipher != nil {
		for i := range disk {
			if disk[i].Secret == "" {
				continue
			}
			enc, err := s.cipher.Seal(disk[i].ID, disk[i].Secret)
			if err != nil {
				return fmt.Errorf("webhook %s: encrypt secret: %w", disk[i].ID, err)
			}
			disk[i].Secret, disk[i].SecretEnc = "", enc
		}
	}
	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".hooks-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.hooksPath())
}

// List returns every hook, secrets included (server-internal).
func (s *Store) List() ([]Hook, error) {
	mu.Lock()
	defer mu.Unlock()
	return s.load()
}

// Get returns one hook.
func (s *Store) Get(id string) (Hook, error) {
	hs, err := s.List()
	if err != nil {
		return Hook{}, err
	}
	for _, h := range hs {
		if h.ID == id {
			return h, nil
		}
	}
	return Hook{}, ErrNotFound
}

// Create adds a hook with a fresh secret; the returned Hook carries it.
func (s *Store) Create(in Input, actor string, allowPrivate bool) (Hook, error) {
	h := Hook{Enabled: true, CreatedBy: actor, CreatedAt: time.Now().UTC(), Events: []string{EventExperimentFinished}}
	if err := apply(&h, in, allowPrivate); err != nil {
		return Hook{}, err
	}
	if h.URL == "" {
		return Hook{}, fmt.Errorf("%w: url is required", ErrInvalid)
	}
	h.ID = "wh_" + randHex(8)
	h.Secret = "whsec_" + randHex(24)
	mu.Lock()
	defer mu.Unlock()
	hs, err := s.load()
	if err != nil {
		return Hook{}, err
	}
	if len(hs) >= MaxHooks {
		return Hook{}, fmt.Errorf("%w: at most %d webhooks per organization; delete one first", ErrInvalid, MaxHooks)
	}
	if err := s.save(append(hs, h)); err != nil {
		return Hook{}, err
	}
	return h, nil
}

// Update changes a hook's fields.
func (s *Store) Update(id string, in Input, allowPrivate bool) (Hook, error) {
	mu.Lock()
	defer mu.Unlock()
	hs, err := s.load()
	if err != nil {
		return Hook{}, err
	}
	for i := range hs {
		if hs[i].ID != id {
			continue
		}
		h := hs[i]
		if err := apply(&h, in, allowPrivate); err != nil {
			return Hook{}, err
		}
		hs[i] = h
		return h, s.save(hs)
	}
	return Hook{}, ErrNotFound
}

// Delete removes a hook.
func (s *Store) Delete(id string) error {
	mu.Lock()
	defer mu.Unlock()
	hs, err := s.load()
	if err != nil {
		return err
	}
	n := slices.DeleteFunc(slices.Clone(hs), func(h Hook) bool { return h.ID == id })
	if len(n) == len(hs) {
		return ErrNotFound
	}
	return s.save(n)
}

func apply(h *Hook, in Input, allowPrivate bool) error {
	if in.URL != nil {
		u := strings.TrimSpace(*in.URL)
		if err := ValidateURL(u, allowPrivate); err != nil {
			return err
		}
		h.URL = u
	}
	if in.Events != nil {
		var evs []string
		for _, e := range *in.Events {
			if !slices.Contains(Events, e) {
				return fmt.Errorf("%w: unknown event %q (one of %s)", ErrInvalid, e, strings.Join(Events, ", "))
			}
			if !slices.Contains(evs, e) {
				evs = append(evs, e)
			}
		}
		if len(evs) == 0 {
			return fmt.Errorf("%w: pick at least one event", ErrInvalid)
		}
		h.Events = evs
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if len(d) > MaxDescBytes {
			return fmt.Errorf("%w: description is longer than %d bytes", ErrInvalid, MaxDescBytes)
		}
		h.Description = d
	}
	if in.Enabled != nil {
		h.Enabled = *in.Enabled
	}
	return nil
}

// ValidateURL accepts an absolute http(s) URL. Without allowPrivate (hosted
// servers) only https is accepted and a literal private, loopback or
// link-local address is refused; names are checked again at dial time.
func ValidateURL(raw string, allowPrivate bool) error {
	if raw == "" || len(raw) > MaxURLBytes {
		return fmt.Errorf("%w: url must be 1 to %d bytes", ErrInvalid, MaxURLBytes)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: url must be an absolute http or https URL, e.g. https://example.com/hooks/heimdall", ErrInvalid)
	}
	if allowPrivate {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: this server only delivers to https URLs", ErrInvalid)
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return fmt.Errorf("%w: this server does not deliver to localhost", ErrInvalid)
	}
	if ip := net.ParseIP(host); ip != nil && blockedIP(ip) {
		return fmt.Errorf("%w: this server does not deliver to private or local addresses", ErrInvalid)
	}
	return nil
}

// GuardedDialer returns a dialer that refuses, at connect time, every
// loopback, private, link-local, multicast or unspecified address, so a
// public name that resolves to an internal address is still refused.
func GuardedDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip == nil || blockedIP(ip) {
			return fmt.Errorf("refused to connect to private or local address %s", host)
		}
		return nil
	}}
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

// redactURL drops userinfo and the query (which may carry a token) from a URL shown in the UI.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	if u.RawQuery != "" {
		u.RawQuery = "***"
	}
	return u.String()
}

// Deliveries returns the newest deliveries first, at most limit.
func (s *Store) Deliveries(limit int) ([]Delivery, error) {
	mu.Lock()
	defer mu.Unlock()
	ds, err := s.loadDeliveries()
	if err != nil {
		return nil, err
	}
	for i := range ds {
		if ds[i].State == "" { // recorded before states existed
			ds[i].State = StateFailed
			if ds[i].OK {
				ds[i].State = StateDelivered
			}
		}
	}
	queued, err := s.outboxDeliveries()
	if err != nil {
		return nil, err
	}
	ds = append(ds, queued...)
	slices.SortStableFunc(ds, func(a, b Delivery) int { return a.At.Compare(b.At) })
	slices.Reverse(ds)
	if limit > 0 && len(ds) > limit {
		ds = ds[:limit]
	}
	return ds, nil
}

func (s *Store) loadDeliveries() ([]Delivery, error) {
	f, err := os.Open(s.deliveriesPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ds []Delivery
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var d Delivery
		if json.Unmarshal(sc.Bytes(), &d) == nil {
			ds = append(ds, d)
		}
	}
	return ds, sc.Err()
}

// Record appends deliveries, keeping the newest maxDeliveries.
func (s *Store) Record(ds ...Delivery) error {
	if len(ds) == 0 {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	old, err := s.loadDeliveries()
	if err != nil {
		return err
	}
	all := append(old, ds...)
	if len(all) > maxDeliveries {
		all = all[len(all)-maxDeliveries:]
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, d := range all {
		_ = enc.Encode(d)
	}
	return os.WriteFile(s.deliveriesPath(), buf.Bytes(), 0o600)
}

// Sender delivers events over HTTP.
type Sender struct {
	Client       *http.Client  // nil: a client with a 10s timeout and, without AllowPrivate, a dialer that refuses private addresses
	AllowPrivate bool          // local servers deliver to localhost and private networks
	Attempts     int           // default 3
	Backoff      time.Duration // first retry wait, doubled each time; default 1s
}

func (snd Sender) client() *http.Client {
	if snd.Client != nil {
		return snd.Client
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	if !snd.AllowPrivate {
		d = GuardedDialer(5 * time.Second)
	}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second}
	return &http.Client{Timeout: 10 * time.Second, Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Sign returns the X-Heimdall-Signature value of a body sent at ts.
func Sign(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	m.Write([]byte("."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Send delivers ev to h, retrying on network errors and 5xx/429 answers.
func (snd Sender) Send(ctx context.Context, h Hook, ev Event) Delivery {
	d := Delivery{ID: "whd_" + randHex(8), HookID: h.ID, Event: ev.Type, EventID: ev.ID, At: time.Now().UTC()}
	start := time.Now()
	defer func() {
		d.DurationMS = time.Since(start).Milliseconds()
		d.State = StateFailed
		if d.OK {
			d.State = StateDelivered
		}
	}()
	body, err := json.Marshal(ev)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	attempts, backoff := snd.Attempts, snd.Backoff
	if attempts <= 0 {
		attempts = 3
	}
	if backoff <= 0 {
		backoff = time.Second
	}
	if !snd.AllowPrivate {
		if err := ValidateURL(h.URL, false); err != nil {
			d.Error = err.Error()
			return d
		}
	}
	c := snd.client()
	for i := 1; i <= attempts; i++ {
		d.Attempts = i
		code, err := snd.post(ctx, c, h, ev, d.ID, body)
		d.StatusCode, d.Error = code, ""
		if err != nil {
			d.Error = err.Error()
		} else if code >= 200 && code < 300 {
			d.OK = true
			return d
		} else {
			d.Error = fmt.Sprintf("receiver answered HTTP %d", code)
			if code < 500 && code != http.StatusTooManyRequests {
				return d
			}
		}
		if i == attempts {
			break
		}
		select {
		case <-ctx.Done():
			d.Error = ctx.Err().Error()
			return d
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return d
}

func (snd Sender) post(ctx context.Context, c *http.Client, h Hook, ev Event, deliveryID string, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	ts := time.Now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Heimdall-Webhooks/1")
	req.Header.Set("X-Heimdall-Event", ev.Type)
	req.Header.Set("X-Heimdall-Delivery", deliveryID)
	req.Header.Set("X-Heimdall-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Heimdall-Signature", Sign(h.Secret, ts, body))
	resp, err := c.Do(req)
	if err != nil {
		return 0, scrubErr(err, h.URL)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	return resp.StatusCode, nil
}

// scrubErr removes the raw URL (which may carry a token) from a client error.
func scrubErr(err error, raw string) error {
	msg := err.Error()
	if raw != "" {
		msg = strings.ReplaceAll(msg, raw, redactURL(raw))
		msg = strings.ReplaceAll(msg, strconv.Quote(raw), strconv.Quote(redactURL(raw)))
	}
	return errors.New(msg)
}

// Dispatch queues ev in the outbox for every enabled hook subscribed to its
// type and makes the first attempt at once; failures stay queued for the
// outbox worker (ProcessDue). It returns the deliveries after that attempt.
func Dispatch(ctx context.Context, st *Store, snd Sender, ev Event) []Delivery {
	ids, err := st.Enqueue(ev)
	if err != nil || len(ids) == 0 {
		return nil
	}
	return st.processIDs(ctx, snd, time.Now().UTC(), ids)
}

// NewEvent builds an event with a fresh id.
func NewEvent(typ, orgID string, data any) Event {
	return Event{ID: "evt_" + randHex(8), Type: typ, OrgID: orgID, At: time.Now().UTC(), Data: data}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
