package bifrostfeatures

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func row(id, cacheID string, hit bool) schemas.GatewayRequest {
	return schemas.GatewayRequest{ID: id, Kind: "message", Status: "success", CacheID: cacheID, CacheHit: hit}
}

func TestCacheReplays(t *testing.T) {
	runs := []RunRows{
		{RunID: "r_1", Task: "t01", Arm: "B", Rows: []schemas.GatewayRequest{row("a", "c1", false), row("b", "c1", true), row("c", "c2", false)}},
		{RunID: "r_2", Task: "t01", Arm: "B", Rows: []schemas.GatewayRequest{row("d", "c2", true)}},
		{RunID: "r_3", Task: "t02", Arm: "B", Rows: []schemas.GatewayRequest{row("e", "c1", true), row("f", "", true), row("g", "c9", true), row("h", "", false)}},
	}
	rep := CacheReplays(runs)
	if rep.Hits != 5 || rep.SameRun != 1 || rep.SameTask != 1 || rep.CrossTask != 1 || rep.Unattributed != 2 {
		t.Fatalf("report = %+v", rep)
	}
	for _, r := range rep.Replays {
		if r.Row == "e" && (r.Scope != "cross_task" || r.StoredRun != "r_1" || r.StoredTask != "t01") {
			t.Fatalf("cross-task replay = %+v", r)
		}
	}
	if got := CacheReplays(nil); got.Hits != 0 || len(got.Replays) != 0 {
		t.Fatalf("no runs: %+v", got)
	}
}
