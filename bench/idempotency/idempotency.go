// Package idempotency honours the Idempotency-Key request header on every
// POST under /api/ (fix wave 3, N186): a caller that sends the same key for
// the same request within 24 hours gets the first answer again instead of
// starting the work a second time, so a double click or a retry after a slow
// answer makes one evaluation, not two.
//
// Keys live in memory (one hosted process; a restart forgets them). A key is
// scoped to the caller (principal kind, user id and token id), the org the
// request acts in (the X-Heimdall-Org header, else the principal's org), the
// method and the path, so one caller's key never returns another caller's or
// another org's answer. The body (and query string) are tied to the key by a
// sha256: the same key with another request is refused with 422. Only 2xx
// answers of at most 1 MiB are kept; any other answer forgets the key, so a
// corrected retry runs. Uploads (multipart or over 1 MiB) and streamed
// answers pass through and are never buffered or kept.
package idempotency

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// Header is the request header that carries the key.
const Header = "Idempotency-Key"

// ReplayedHeader is set to "true" on an answer replayed from the store.
const ReplayedHeader = "Idempotent-Replayed"

const (
	defaultTTL        = 24 * time.Hour
	defaultMaxEntries = 10000
	// defaultMaxBytes bounds the kept answer bodies together, so 10,000
	// large answers cannot hold gigabytes; the oldest go first.
	defaultMaxBytes = 64 << 20
	// defaultWait is how long a duplicate waits for the first request
	// (the evaluation launch timeout).
	defaultWait = 3 * time.Minute
	// maxRequestBytes: larger bodies are uploads and pass through.
	maxRequestBytes = lib.MaxBodyBytes
	// maxResponseBytes: larger answers are not kept.
	maxResponseBytes = 1 << 20
)

// Messages, exact (fix wave 3 brief, H13 decision 1).
const (
	msgBadKey    = `Idempotency-Key must be 16 to 64 letters, digits, "-" or "_".`
	msgPending   = "This request is still being processed. Open Evaluations to see it."
	msgOtherBody = "This Idempotency-Key was already used for a different request."
)

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

type entry struct {
	scope   string
	sum     [sha256.Size]byte
	created time.Time
	done    chan struct{} // closed when the first request finished
	// Set before done is closed; read only after it is closed.
	ok          bool // a kept 2xx answer
	status      int
	contentType string
	body        []byte
	elem        *list.Element
}

// Store holds the keys of one server. The zero value is not usable; use
// NewStore.
type Store struct {
	mu         sync.Mutex
	entries    map[string]*entry
	order      *list.List // oldest first
	bytes      int
	ttl        time.Duration
	maxEntries int
	maxBytes   int
	wait       time.Duration
	now        func() time.Time
}

// NewStore returns an empty store with a 24 hour life and at most 10,000
// keys.
func NewStore() *Store {
	return &Store{
		entries: map[string]*entry{}, order: list.New(),
		ttl: defaultTTL, maxEntries: defaultMaxEntries, maxBytes: defaultMaxBytes,
		wait: defaultWait, now: time.Now,
	}
}

// Len returns the number of keys held (pending and finished).
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// removeLocked drops e if it is still the entry of its scope.
func (s *Store) removeLocked(e *entry) {
	if cur, ok := s.entries[e.scope]; ok && cur == e {
		delete(s.entries, e.scope)
	}
	if e.elem != nil {
		s.order.Remove(e.elem)
		e.elem = nil
		s.bytes -= len(e.body)
	}
}

// trimLocked drops expired keys, then the oldest until the bounds hold.
// A dropped pending key only stops later duplicates from waiting on it; the
// first request still answers its own caller.
func (s *Store) trimLocked() {
	cutoff := s.now().Add(-s.ttl)
	for f := s.order.Front(); f != nil; f = s.order.Front() {
		e := f.Value.(*entry)
		if e.created.After(cutoff) && len(s.entries) <= s.maxEntries && s.bytes <= s.maxBytes {
			return
		}
		s.removeLocked(e)
	}
}

// claim returns the live entry of scope, or makes a new pending one owned by
// the caller (owner true). Check and insert happen under one lock, so two
// requests with the same key at the same instant make one owner.
func (s *Store) claim(scope string, sum [sha256.Size]byte) (e *entry, owner bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trimLocked()
	if cur, ok := s.entries[scope]; ok {
		return cur, false
	}
	e = &entry{scope: scope, sum: sum, created: s.now(), done: make(chan struct{})}
	s.entries[scope] = e
	e.elem = s.order.PushBack(e)
	s.trimLocked()
	return e, true
}

// finish records the owner's answer: kept when ok, else the key is forgotten
// so a retry runs again. Waiting duplicates are released either way.
func (s *Store) finish(e *entry, ok bool, status int, contentType string, body []byte) {
	s.mu.Lock()
	switch {
	case ok && e.elem != nil:
		e.ok, e.status, e.contentType, e.body = true, status, contentType, body
		s.bytes += len(body)
		s.trimLocked()
	case ok:
		// Dropped from the store while it ran: the duplicates already
		// waiting on it still get its answer, nothing is kept.
		e.ok, e.status, e.contentType, e.body = true, status, contentType, body
	default:
		s.removeLocked(e)
	}
	s.mu.Unlock()
	close(e.done)
}

// actor names the caller, or "" when the request carries no signed-in
// principal (then the key is ignored).
func actor(p *auth.Principal) string {
	if p == nil || !p.IsAuthenticated() {
		return ""
	}
	if p.Kind != auth.KindLocal && p.UserID == "" && p.TokenID == "" {
		return ""
	}
	return string(p.Kind) + "\x00" + p.UserID + "\x00" + p.TokenID
}

// Middleware honours Idempotency-Key on POST requests under /api/ that carry
// a signed-in principal; every other request passes through untouched. Put
// it after the auth middleware (it reads the principal) and after any path
// rewrite. On the hosted server it must also run after the tenancy
// authorization (tenancy.DispatcherConfig.Serve), so a stored answer is never
// replayed to a caller who has since lost access; on the local server it is
// the last entry of the server chain.
func Middleware(mode schemas.Mode, s *Store) lib.Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			key := ctx.Request.Header.Peek(Header)
			if len(key) == 0 || !ctx.IsPost() || !strings.HasPrefix(string(ctx.Path()), "/api/") {
				next(ctx)
				return
			}
			who := actor(auth.PrincipalFrom(ctx))
			if who == "" {
				next(ctx)
				return
			}
			if !keyRe.Match(key) {
				lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msgBadKey)
				return
			}
			// Uploads and streamed bodies are not read here (nothing is
			// buffered beyond what the server already holds).
			if ctx.Request.IsBodyStream() || ctx.Request.Header.ContentLength() > maxRequestBytes ||
				bytes.HasPrefix(bytes.ToLower(ctx.Request.Header.ContentType()), []byte("multipart/")) {
				next(ctx)
				return
			}
			body := ctx.Request.Body()
			if len(body) > maxRequestBytes {
				next(ctx)
				return
			}
			org := string(ctx.Request.Header.Peek(tenancy.OrgHeader))
			if org == "" {
				org = auth.PrincipalFrom(ctx).OrgID
			}
			scope := strings.Join([]string{who, org, string(ctx.Method()), string(ctx.Path()), string(key)}, "\x00")
			h := sha256.New()
			h.Write(ctx.URI().QueryString())
			h.Write([]byte{0})
			h.Write(body)
			var sum [sha256.Size]byte
			copy(sum[:], h.Sum(nil))
			serve(ctx, mode, s, next, scope, sum)
		}
	}
}

func serve(ctx *fasthttp.RequestCtx, mode schemas.Mode, s *Store, next fasthttp.RequestHandler, scope string, sum [sha256.Size]byte) {
	deadline := time.NewTimer(s.wait)
	defer deadline.Stop()
	for {
		e, owner := s.claim(scope, sum)
		if owner {
			run(ctx, s, next, e)
			return
		}
		if e.sum != sum {
			lib.SendError(ctx, mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, msgOtherBody)
			return
		}
		select {
		case <-e.done:
		case <-deadline.C:
			lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, msgPending)
			return
		}
		if e.ok {
			ctx.Response.Reset()
			ctx.SetStatusCode(e.status)
			if e.contentType != "" {
				ctx.SetContentType(e.contentType)
			}
			ctx.Response.Header.Set(ReplayedHeader, "true")
			ctx.SetBody(e.body)
			return
		}
		// The first request failed and its key was forgotten: this one is
		// allowed to run (it claims the key again on the next turn).
	}
}

// run serves the owner and records its answer. A panic forgets the key.
func run(ctx *fasthttp.RequestCtx, s *Store, next fasthttp.RequestHandler, e *entry) {
	kept := false
	defer func() {
		if !kept {
			s.finish(e, false, 0, "", nil)
		}
	}()
	next(ctx)
	status := ctx.Response.StatusCode()
	if status < 200 || status > 299 || ctx.Response.IsBodyStream() {
		return
	}
	b := ctx.Response.Body()
	if len(b) > maxResponseBytes {
		return
	}
	kept = true
	s.finish(e, true, status, string(ctx.Response.Header.ContentType()), append([]byte(nil), b...))
}
