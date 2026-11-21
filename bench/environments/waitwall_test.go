package environments

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestWaitWallExtendsByHeldTime(t *testing.T) {
	done := make(chan struct{})
	if !waitWall(context.Background(), done, 20*time.Millisecond, nil) {
		t.Fatal("no held clock: the budget must run out")
	}
	var held atomic.Int64
	held.Store(int64(300 * time.Millisecond))
	clock := schemas.HeldClock(func() time.Duration { return time.Duration(held.Load()) })
	go func() { time.Sleep(150 * time.Millisecond); close(done) }()
	if waitWall(context.Background(), done, 20*time.Millisecond, clock) {
		t.Fatal("held time must extend the budget past the agent's exit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !waitWall(ctx, make(chan struct{}), time.Hour, clock) {
		t.Fatal("a cancelled context ends the wait")
	}
}
