package hostslots

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSize(t *testing.T) {
	for _, c := range []struct {
		cpus int
		mem  int64
		want int
	}{
		{16, 64 << 30, 8}, {16, 16 << 30, 4}, {1, 0, 1}, {8, 0, 4}, {4, 1 << 30, 1},
	} {
		if got := Size(c.cpus, c.mem); got != c.want {
			t.Errorf("Size(%d, %d) = %d, want %d", c.cpus, c.mem, got, c.want)
		}
	}
	if HostSize() < 1 {
		t.Fatal("host size below 1")
	}
}

func acquireNow(t *testing.T, p *Pool, org string) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rel, err := p.Acquire(ctx, org)
	if err != nil {
		t.Fatalf("acquire %s: %v", org, err)
	}
	return rel
}

// waitAsync acquires for org in the background and reports the grant on
// got, keeping the slot.
func waitAsync(p *Pool, org string, got chan<- string) {
	go func() {
		if rel, err := p.Acquire(context.Background(), org); err == nil {
			got <- org
			_ = rel
		}
	}()
}

func TestFairShareWhenContended(t *testing.T) {
	p := New(3)
	// org_big takes the whole host.
	b1, b2, b3 := acquireNow(t, p, "org_big"), acquireNow(t, p, "org_big"), acquireNow(t, p, "org_big")
	got := make(chan string, 4)
	waitAsync(p, "org_big", got)
	time.Sleep(20 * time.Millisecond)
	waitAsync(p, "org_small", got)
	time.Sleep(20 * time.Millisecond)
	// org_big waited first, but org_small holds nothing: it gets the slot.
	b1()
	if g := <-got; g != "org_small" {
		t.Fatalf("first freed slot went to %s, want org_small", g)
	}
	b2()
	if g := <-got; g != "org_big" {
		t.Fatalf("second freed slot went to %s", g)
	}
	b3()
	if total, small := p.InUse("org_small"); total != 2 || small != 1 {
		t.Fatalf("in use %d, org_small %d", total, small)
	}
}

func TestWeightedShare(t *testing.T) {
	p := New(2)
	p.SetWeight("org_heavy", 4)
	h1 := acquireNow(t, p, "org_heavy")
	l1 := acquireNow(t, p, "org_light")
	got := make(chan string, 2)
	waitAsync(p, "org_light", got)
	time.Sleep(20 * time.Millisecond)
	waitAsync(p, "org_heavy", got)
	time.Sleep(20 * time.Millisecond)
	// Both hold 1; per unit of weight org_heavy holds 1/4, org_light 1.
	h1()
	if g := <-got; g != "org_heavy" {
		t.Fatalf("freed slot went to %s, want org_heavy", g)
	}
	l1()
	<-got
}

func TestAcquireCancelAndIdempotentRelease(t *testing.T) {
	p := New(1)
	rel := acquireNow(t, p, "org_a")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Acquire(ctx, "org_b")
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire: %v", err)
	}
	rel()
	rel() // a second release is a no-op
	if total, _ := p.InUse("org_a"); total != 0 {
		t.Fatalf("in use %d after release", total)
	}
	// The cancelled waiter left no ghost: the slot is free for anyone.
	r2 := acquireNow(t, p, "org_c")
	r2()
}

func TestNeverAboveCap(t *testing.T) {
	p := New(4)
	ctx := context.Background()
	var max, cur int
	mu := make(chan struct{}, 1)
	mu <- struct{}{}
	done := make(chan struct{})
	for i := range 40 {
		go func() {
			defer func() { done <- struct{}{} }()
			rel, err := p.Acquire(ctx, []string{"a", "b", "c"}[i%3])
			if err != nil {
				return
			}
			<-mu
			cur++
			if cur > max {
				max = cur
			}
			mu <- struct{}{}
			time.Sleep(time.Millisecond)
			<-mu
			cur--
			mu <- struct{}{}
			rel()
		}()
	}
	for range 40 {
		<-done
	}
	if max > 4 || max < 1 {
		t.Fatalf("held at most %d at once, cap 4", max)
	}
}
