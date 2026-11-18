package auth

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestLimiterBurstAndRefill(t *testing.T) {
	clk := newFakeClock()
	l := NewLimiter(RateLimit{Burst: 3, Every: 10 * time.Second}, clk.Now)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("attempt %d refused within burst", i)
		}
	}
	ok, wait := l.Allow("k")
	if ok || wait <= 0 || wait > 10*time.Second {
		t.Fatalf("4th attempt = %v, wait %v; want refused with a wait up to 10s", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("another key shares the bucket")
	}
	clk.Advance(10 * time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("no refill after one interval")
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("refilled more than one token in one interval")
	}
	clk.Advance(time.Hour)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatal("refill exceeded or fell short of burst")
		}
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("bucket grew past burst")
	}
	l.Reset("k")
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("Reset did not refill")
	}
	if ok, _ := l.Allow(""); !ok {
		t.Fatal("empty key limited")
	}
	var nilL *Limiter
	if ok, _ := nilL.Allow("x"); !ok {
		t.Fatal("nil limiter limited")
	}
}

func TestLimiterEvictsFullBuckets(t *testing.T) {
	clk := newFakeClock()
	l := NewLimiter(RateLimit{Burst: 2, Every: time.Second}, clk.Now)
	for i := 0; i < 1000; i++ {
		l.Allow("k" + strconv.Itoa(i))
	}
	clk.Advance(time.Minute)
	l.mu.Lock()
	l.evictLocked(clk.Now())
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d refilled buckets kept after eviction", n)
	}
}
