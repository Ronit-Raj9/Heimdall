package handlers

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// twoSuiteBoardIndex is agentBoardIndex with a second experiment on another
// suite, so one agent board request builds three boards (all suites, then
// each suite).
func twoSuiteBoardIndex() *fakeIndex {
	idx := agentBoardIndex()
	m := *idx.manifests[testExp]
	m.ExpID, m.SuiteID = "e_two", "hd-other"
	idx.manifests["e_two"] = &m
	for _, rv := range append([]RunView(nil), idx.runs...) {
		rv.Slot.ExpID, rv.Slot.Suite, rv.Run.ExpID = "e_two", "hd-other", "e_two"
		id := rv.Run.RunID + "_two"
		idx.files[id] = idx.files[rv.Run.RunID]
		rv.Run.RunID = id
		idx.runs = append(idx.runs, rv)
	}
	return idx
}

// The agent board request reads the runs once (one leaderboard.Session),
// however many boards it builds.
func TestAgentBoardCollectsOnce(t *testing.T) {
	idx := twoSuiteBoardIndex()
	h := NewLeaderboardHandler(testMode, idx).WithAgentBoard(t.TempDir(), nil)
	if _, err := h.buildAgentBoard(context.Background(), "", "", ""); err != nil {
		t.Fatal(err)
	}
	perRequest := ledgerReads(idx)

	one := twoSuiteBoardIndex()
	src := PricedLeaderboardSource(one, KeyPriceLookup(context.Background(), nil, ""))
	first, err := leaderboard.Build(context.Background(), src, leaderboard.Query{Condition: schemas.ConditionClean, Now: h.now()})
	if err != nil || len(first.Suites) != 2 {
		t.Fatalf("suites %v %v", first, err)
	}
	perBoard := ledgerReads(one)
	if perBoard == 0 || perRequest != perBoard {
		t.Fatalf("ledger reads (the collect step): request with 3 boards %d, one board %d", perRequest, perBoard)
	}
}

// ledgerReads counts the run ledgers read, which the collect step of a
// leaderboard reads once per run.
func ledgerReads(idx *fakeIndex) int {
	n := 0
	for _, name := range idx.asked {
		if name == "proxy.ledger.jsonl" {
			n++
		}
	}
	return n
}
