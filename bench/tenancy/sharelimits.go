package tenancy

import (
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Share link traffic (audit B4 and B9). Anyone holding a share link reads it
// without signing in, and old-form links show the org id. So
// share routes are limited by buckets of their own, one per link and one per
// client IP, and never by the org's bucket: a flood on a public badge can
// slow that badge, never the org's members, sessions or CI tokens. Each
// successful view also leaves an audit row in the org's chain, at most one
// per link, client IP and route an hour.

// Default share link limits: a link may burst 300 reads and sustain 50 a
// second (a README badge on a busy page); one client IP 120 and 10 a second.
var (
	DefaultShareLinkLimit = auth.RateLimit{Burst: 300, Every: 20 * time.Millisecond}
	DefaultShareIPLimit   = auth.RateLimit{Burst: 120, Every: 100 * time.Millisecond}
)

// allowShare takes one request from the share link buckets: the client IP's
// first (it bounds how many link keys one caller can mint), then the link's.
// It answers 429 itself when either is empty. A nil RateLimits allows
// everything.
func (rl *RateLimits) allowShare(ctx *fasthttp.RequestCtx, mode schemas.Mode, token, ip string) bool {
	if rl == nil {
		return true
	}
	if ok, wait := rl.shareIP.Allow("ip:" + ip); !ok {
		tooMany(ctx, mode, wait, "too many share link requests from this address")
		return false
	}
	if ok, wait := rl.shareLink.Allow("share:" + shareLinkID(token)); !ok {
		tooMany(ctx, mode, wait, "too many requests for this share link")
		return false
	}
	return true
}

// shareLinkID is the org and link id of a share token (org.id.signature),
// without the signature, so the bucket key stays short. An opaque (s_) token
// is keyed by "s_" and the first 16 hex of its hash (fix wave 3, N293).
func shareLinkID(token string) string {
	if IsShareTokenV2(token) {
		return shareV2Prefix + db.ShareTokenHash(token)[:16]
	}
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return clipKey(token)
	}
	return clipKey(parts[0] + "." + parts[1])
}

func clipKey(s string) string {
	if len(s) > 96 {
		return s[:96]
	}
	return s
}

// shareViewWindow is how long one link, client IP and route stay deduped.
const shareViewWindow = time.Hour

// maxShareViews bounds the dedupe memory; when full, expired keys are dropped
// first and, if none have expired, the oldest window is forgotten.
const maxShareViews = 50_000

// shareViews dedupes share view audit rows.
type shareViews struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// first reports whether key was not seen within the window, and marks it.
func (v *shareViews) first(key string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.seen == nil {
		v.seen = map[string]time.Time{}
	}
	if at, ok := v.seen[key]; ok && now.Sub(at) < shareViewWindow {
		return false
	}
	if len(v.seen) >= maxShareViews {
		for k, at := range v.seen {
			if now.Sub(at) >= shareViewWindow {
				delete(v.seen, k)
			}
		}
		if len(v.seen) >= maxShareViews {
			v.seen = map[string]time.Time{}
		}
	}
	v.seen[key] = now
	return true
}

// shareViewKind names a share route for the audit row.
func shareViewKind(pattern string) string {
	switch {
	case strings.HasSuffix(pattern, "/badge.svg"):
		return "badge"
	case strings.HasSuffix(pattern, "/export"):
		return "export"
	}
	return "view"
}

// auditShareView writes the shares.view row of a successful share link read,
// once per link, client IP and route an hour.
func (d *Dispatcher) auditShareView(ctx *fasthttp.RequestCtx, orgID, pattern, token string) {
	if d.c.Audit == nil {
		return
	}
	id := shareLinkID(token)
	_, shareID, _ := strings.Cut(id, ".")
	ip := ClientIP(ctx)
	kind := shareViewKind(pattern)
	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	if !d.views.first(id+"|"+ip+"|"+kind, now) {
		return
	}
	if IsShareTokenV2(token) {
		// The row names the share id from the index, never the token.
		shareID = ""
		if ix, ok := d.c.Dir.(ShareIndex); ok {
			lctx, cancel := workContext()
			_, shareID, _ = ix.ShareByToken(lctx, token)
			cancel()
		}
	}
	details := map[string]any{"route": "GET " + pattern, "kind": kind, "dedupe": "one row per link, address and route an hour"}
	if ua := string(ctx.UserAgent()); ua != "" {
		if len(ua) > 200 {
			ua = ua[:200]
		}
		details["user_agent"] = ua
	}
	if ref := string(ctx.Referer()); ref != "" && len(ref) <= 300 {
		details["referer"] = ref
	}
	e := EntryFor(ctx, orgID, "shares.view", "share", shareID, details)
	e.ActorType, e.ActorID = "anonymous", ""
	actx, cancel := workContext()
	defer cancel()
	if err := d.c.Audit.Audit(actx, e); err != nil {
		d.c.Logger.Error("audit write failed", "org", orgID, "action", e.Action, "error", err.Error())
	}
}
