package leaderboard

import (
	"context"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// countingSource counts the reads collect and classify make. It embeds the
// fake so the optional RegradeSource methods stay visible.
type countingSource struct {
	*fakeSource
	runs, ledgers int
}

func (c *countingSource) Runs(expID string) []Run { c.runs++; return c.fakeSource.Runs(expID) }

func (c *countingSource) RunFile(runID, name string) ([]byte, error) {
	if name == fileLedger {
		c.ledgers++
	}
	return c.fakeSource.RunFile(runID, name)
}

// agentBoardQueries are the boards one agent board request builds, in its
// order: the first board, then each suite, then a mix of grade sources and
// filters so a board that writes its records is followed by one that must
// not see those writes.
func agentBoardQueries() []Query {
	return []Query{
		{Condition: schemas.ConditionClean, Now: t0},
		{Suite: "s@2", Condition: schemas.ConditionClean, Now: t0},
		{Suite: "s@1", Condition: schemas.ConditionClean, Now: t0},
		{Suite: "s@1", Condition: schemas.ConditionClean, Now: t0, GradeSource: GradeSourceLatest},
		{Suite: "s@1", Condition: schemas.ConditionClean, Now: t0},
		{Suite: "s@2", Condition: schemas.ConditionClean, Now: t0, Synthetic: SyntheticOnly},
		{Suite: "s@2", Condition: "model-outage", Now: t0},
		{Suite: "bad suite", Now: t0},
	}
}

// TestSessionSameBoards: every board a Session builds equals the board Build
// gives alone, in any order, on both fixtures.
func TestSessionSameBoards(t *testing.T) {
	ctx := context.Background()
	for name, mk := range map[string]func() *fakeSource{
		"board":   boardSource,
		"regrade": func() *fakeSource { return regradeSource(t) },
	} {
		src := mk()
		src.files[src.runs["e_one"][0].Run.RunID][fileLedger] = []byte("{\"kind\":\"message\"}\n")
		sess := NewSession(src)
		qs := agentBoardQueries()
		for _, order := range [][]int{{0, 1, 2, 3, 4, 5, 6, 7}, {3, 4, 0, 7, 6, 5, 2, 1}} {
			for _, i := range order {
				want, wantErr := Build(ctx, src, qs[i])
				got, gotErr := sess.Build(ctx, qs[i])
				if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) {
					t.Fatalf("%s query %d: error %v, want %v", name, i, gotErr, wantErr)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s query %d: session board differs\n got %+v\nwant %+v", name, i, got, want)
				}
			}
		}
	}
}

// TestSessionReadsOnce measures the gain: N boards over one Session read each
// experiment's runs and each ledger once, against N times with Build.
func TestSessionReadsOnce(t *testing.T) {
	ctx := context.Background()
	qs := agentBoardQueries()[:3]
	alone := &countingSource{fakeSource: boardSource()}
	for _, q := range qs {
		if _, err := Build(ctx, alone, q); err != nil {
			t.Fatal(err)
		}
	}
	shared := &countingSource{fakeSource: boardSource()}
	sess := NewSession(shared)
	for _, q := range qs {
		if _, err := sess.Build(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	exps := len(shared.manifests)
	if alone.runs != len(qs)*exps || shared.runs != exps {
		t.Fatalf("Runs calls: Build %d, Session %d; want %d and %d", alone.runs, shared.runs, len(qs)*exps, exps)
	}
	if shared.ledgers*len(qs) != alone.ledgers {
		t.Fatalf("ledger reads: Build %d, Session %d", alone.ledgers, shared.ledgers)
	}
	t.Logf("%d boards: Runs calls %d -> %d, ledger reads %d -> %d", len(qs), alone.runs, shared.runs, alone.ledgers, shared.ledgers)
}
