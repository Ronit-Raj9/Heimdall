package lib

import (
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

// TestStreamGuardThrottlesAndLatches backs audit qc2-17-04: the guard asks
// its check at most once per interval and a refusal is final.
func TestStreamGuardThrottlesAndLatches(t *testing.T) {
	now := time.Unix(0, 0)
	calls, answer := 0, true
	g := throttle(func() bool { calls++; return answer }, 10*time.Second, func() time.Time { return now })
	if !g() || !g() || calls != 1 {
		t.Fatalf("first checks: calls %d", calls)
	}
	answer = false
	now = now.Add(11 * time.Second)
	if g() || calls != 2 {
		t.Fatalf("recheck after interval: calls %d", calls)
	}
	answer = true
	now = now.Add(time.Minute)
	if g() {
		t.Fatal("refusal not final")
	}
	ctx := &fasthttp.RequestCtx{}
	if !StreamGuardOf(ctx)() {
		t.Fatal("request without a guard refused")
	}
	SetStreamGuard(ctx, func() bool { return false })
	if StreamGuardOf(ctx)() {
		t.Fatal("guard not used")
	}
}
