package tenancy

import (
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/valyala/fasthttp"
)

// TestShareUnknownTokensRateLimitedLikeKnown (fix wave 3, N293 review):
// tokens that open nothing (an unknown opaque token, an old-form token of
// an org that does not exist) are charged to the address bucket exactly as
// a real link is, so guessing gets 429 at the same point and the answers do
// not tell a real org from a missing one.
func TestShareUnknownTokensRateLimitedLikeKnown(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limits := NewRateLimits(RateLimitConfig{
		PerCredential: auth.RateLimit{Burst: 100, Every: time.Second},
		PerOrg:        auth.RateLimit{Burst: 100, Every: time.Hour},
		PerShareLink:  auth.RateLimit{Burst: 100, Every: time.Hour},
		PerShareIP:    auth.RateLimit{Burst: 3, Every: time.Hour},
	}, func() time.Time { return now })
	d := shareDispatcher(t, nil, limits, &now)
	anon := auth.Anonymous()
	for _, tc := range []struct{ ip, first string }{
		{"198.51.100.10", APIPrefix + "/shared/s_" + strings.Repeat("Q", 43)},
		{"198.51.100.11", APIPrefix + "/shared/org_nosuchorg.shr_1.sig"},
		{"198.51.100.12", APIPrefix + "/shared/org_a.shr_1.sig"},
	} {
		var codes []int
		for i := 0; i < 4; i++ {
			codes = append(codes, serveFrom(d, anon, tc.ip, tc.first).Response.StatusCode())
		}
		if codes[3] != fasthttp.StatusTooManyRequests {
			t.Errorf("%s: answers %v, want the 4th refused with 429", tc.first, codes)
		}
	}
}
