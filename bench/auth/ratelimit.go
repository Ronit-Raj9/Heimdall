package auth

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimit is one token bucket shape: Burst attempts at once, refilled at
// one attempt every Every.
type RateLimit struct {
	Burst int
	Every time.Duration
}

// Default limits for the unauthenticated auth routes. Per IP is loose enough
// for an office behind one NAT; per email is tight because it is what a
// password-guessing run against one account hits.
var (
	LoginPerIP     = RateLimit{Burst: 30, Every: 2 * time.Second}
	LoginPerEmail  = RateLimit{Burst: 10, Every: 30 * time.Second}
	SignupPerIP    = RateLimit{Burst: 5, Every: time.Minute}
	SignupPerEmail = RateLimit{Burst: 3, Every: 5 * time.Minute}
	// TokenPerIP limits other credential-probing routes (invitation lookup
	// and accept, password change, GitHub callback).
	TokenPerIP = RateLimit{Burst: 30, Every: 2 * time.Second}
)

// maxBuckets bounds a limiter's memory. When full, buckets that have
// refilled completely are dropped first; if none have, the limiter fails
// closed for new keys until some refill.
const maxBuckets = 100_000

// Limiter is an in-memory token bucket per key. It is per process: a
// deployment with several server replicas multiplies the effective limit by
// the replica count (docs/auth.md). Safe for concurrent use.
type Limiter struct {
	limit RateLimit
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter returns a limiter with limit. now may be nil (time.Now).
func NewLimiter(limit RateLimit, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	if limit.Burst < 1 {
		limit.Burst = 1
	}
	if limit.Every <= 0 {
		limit.Every = time.Second
	}
	return &Limiter{limit: limit, now: now, buckets: map[string]*bucket{}}
}

// Allow takes one attempt from key's bucket. It returns false, and how long
// until the next attempt is allowed, when the bucket is empty. An empty key
// is never limited by this limiter.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l == nil || key == "" {
		return true, 0
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			l.evictLocked(now)
			if len(l.buckets) >= maxBuckets {
				return false, l.limit.Every
			}
		}
		b = &bucket{tokens: float64(l.limit.Burst), last: now}
		l.buckets[key] = b
	}
	l.refill(b, now)
	if b.tokens < 1 {
		wait := time.Duration((1 - b.tokens) * float64(l.limit.Every))
		return false, wait
	}
	b.tokens--
	return true, 0
}

// Reset forgets key, for example after a successful login so a user who
// mistyped a few times starts fresh.
func (l *Limiter) Reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

func (l *Limiter) refill(b *bucket, now time.Time) {
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return
	}
	b.tokens += float64(elapsed) / float64(l.limit.Every)
	if max := float64(l.limit.Burst); b.tokens > max {
		b.tokens = max
	}
	b.last = now
}

// evictLocked drops every bucket that has refilled to full: forgetting it
// changes nothing.
func (l *Limiter) evictLocked(now time.Time) {
	for k, b := range l.buckets {
		l.refill(b, now)
		if b.tokens >= float64(l.limit.Burst) {
			delete(l.buckets, k)
		}
	}
}

// SignupGlobal caps web signups across every IP (account-farming from many
// addresses). Invitation signups are not counted. HEIMDALL_SIGNUP_PER_HOUR
// overrides the hourly rate (0 or unset keeps the default); see
// SignupGlobalFromEnv.
var SignupGlobal = RateLimit{Burst: 20, Every: 3 * time.Minute}

// SignupPerHourEnv tunes SignupGlobal: the number of new self-serve
// accounts allowed per hour, also the burst.
const SignupPerHourEnv = "HEIMDALL_SIGNUP_PER_HOUR"

// SignupGlobalFromEnv returns SignupGlobal, or the shape that admits n
// signups per hour when SignupPerHourEnv holds a positive integer n.
func SignupGlobalFromEnv(getenv func(string) string) RateLimit {
	n, err := strconv.Atoi(strings.TrimSpace(getenv(SignupPerHourEnv)))
	if err != nil || n <= 0 {
		return SignupGlobal
	}
	return RateLimit{Burst: n, Every: time.Hour / time.Duration(n)}
}
