package leaderboard

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestBoardSkipsTwinBenchmarks: runs on impossible twins (a cheat check)
// never rank: a pass there is cheating, not a score.
func TestBoardSkipsTwinBenchmarks(t *testing.T) {
	f := newFake()
	f.exp("e", "shop-twins@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h", "command")})
	f.pass("e", "t1-twin", "A", "clean", 1, 0.1)
	lb, err := Build(context.Background(), f, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lb.Entries) != 0 {
		t.Fatalf("twin benchmark ranked: %+v", lb.Entries)
	}
}
