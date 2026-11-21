package tenancy

import (
	"math"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// API rate limits (lane X16, audit N18): a token bucket per credential (an
// API token, or a signed-in user's account) and one per org, checked by the
// dispatcher after the request is authorized. A request over either limit
// gets 429 with Retry-After in whole seconds. Runner tokens are not limited
// here: their heartbeats and uploads are paced by the worker protocol, and
// starving a runner would fail a run. Anonymous callers have no credential
// bucket; the org bucket of a public org still applies. The buckets live in
// memory (auth.Limiter), so several replicas multiply the limits.

// RateLimitConfig shapes the two buckets. A zero Burst turns that bucket
// off.
type RateLimitConfig struct {
	PerCredential auth.RateLimit
	PerOrg        auth.RateLimit
}

// DefaultRateLimits are generous for people and scripts alike: a credential
// may burst 120 requests and sustain 20 a second; an org, all its members
// and tokens together, 600 and 100 a second.
var DefaultRateLimits = RateLimitConfig{
	PerCredential: auth.RateLimit{Burst: 120, Every: 50 * time.Millisecond},
	PerOrg:        auth.RateLimit{Burst: 600, Every: 10 * time.Millisecond},
}

// RateLimits holds the live buckets.
type RateLimits struct {
	cred *auth.Limiter
	org  *auth.Limiter
}

// NewRateLimits builds the buckets of c. now may be nil (time.Now).
func NewRateLimits(c RateLimitConfig, now func() time.Time) *RateLimits {
	rl := &RateLimits{}
	if c.PerCredential.Burst > 0 {
		rl.cred = auth.NewLimiter(c.PerCredential, now)
	}
	if c.PerOrg.Burst > 0 {
		rl.org = auth.NewLimiter(c.PerOrg, now)
	}
	return rl
}

// allow takes one request from the caller's buckets, answering 429 itself
// when either is empty. A nil RateLimits allows everything.
func (rl *RateLimits) allow(ctx *fasthttp.RequestCtx, mode schemas.Mode, p *auth.Principal, o *Org) bool {
	if rl == nil || p == nil || p.Kind == auth.KindRunner || p.Kind == auth.KindLocal {
		return true
	}
	if ok, wait := rl.cred.Allow(credentialKey(p)); !ok {
		tooMany(ctx, mode, wait, "too many requests from this credential")
		return false
	}
	if o != nil {
		if ok, wait := rl.org.Allow(o.ID); !ok {
			tooMany(ctx, mode, wait, "too many requests for this organization")
			return false
		}
	}
	return true
}

// credentialKey names the caller's bucket: the API token, else the user.
// Anonymous callers have none ("" is never limited).
func credentialKey(p *auth.Principal) string {
	switch {
	case p.Kind == auth.KindToken && p.TokenID != "":
		return "token:" + p.TokenID
	case p.Kind == auth.KindUser && p.UserID != "":
		return "user:" + p.UserID
	}
	return ""
}

func tooMany(ctx *fasthttp.RequestCtx, mode schemas.Mode, wait time.Duration, msg string) {
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	ctx.Response.Header.Set(fasthttp.HeaderRetryAfter, strconv.Itoa(secs))
	lib.SendError(ctx, mode, fasthttp.StatusTooManyRequests, errTypeRateLimited, msg+"; retry in "+strconv.Itoa(secs)+"s")
}

// errTypeRateLimited is the error type of a 429, the one the auth routes
// already answer (auth.Error rate_limited).
const errTypeRateLimited = "rate_limited"
