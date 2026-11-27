package handlers

import (
	"context"
	"errors"
	"sync"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fullLeaderboardSource is every interface the leaderboard reads from a
// source: leaderboardSource and pricedSource implement all of them.
type fullLeaderboardSource interface {
	leaderboard.Source
	leaderboard.SealSource
	leaderboard.RegradeSource
	leaderboard.AnalysisSource
}

// requestLeaderboardSource remembers, for one request, the run files and
// seal checks the leaderboard reads (UI fix wave G3, M4-106). The agent
// board calls leaderboard.Build once for the suite list, once per suite and
// once per simulator pin, and every Build reads every run of every
// experiment again: the ledger of each sealed run (classify), metrics.json
// (grading) and the seal check. With 20 suites that is 21 reads of the same
// files in one request. The memory lives only as long as the request, so
// nothing is kept between requests and nothing is shared between callers.
type requestLeaderboardSource struct {
	fullLeaderboardSource
	mu    sync.Mutex
	files map[[2]string]fileRead
	seals map[string]error
}

type fileRead struct {
	data []byte
	err  error
}

// memoLeaderboardSource wraps src for one request; a source that does not
// implement every interface is returned as it is, so no optional behaviour
// is added or lost.
func memoLeaderboardSource(src leaderboard.Source) leaderboard.Source {
	full, ok := src.(fullLeaderboardSource)
	if !ok {
		return src
	}
	return &requestLeaderboardSource{fullLeaderboardSource: full, files: map[[2]string]fileRead{}, seals: map[string]error{}}
}

// memoFileMax is the largest run file remembered: metrics.json and grade
// files are small; a gateway ledger can be tens of megabytes and is read
// again rather than held for the whole request.
const memoFileMax = 1 << 20

// RunFile implements leaderboard.Source, reading each small run file once
// per request. The bytes are shared: the leaderboard only decodes them.
func (s *requestLeaderboardSource) RunFile(runID, name string) ([]byte, error) {
	k := [2]string{runID, name}
	s.mu.Lock()
	r, ok := s.files[k]
	s.mu.Unlock()
	if ok {
		return r.data, r.err
	}
	data, err := s.fullLeaderboardSource.RunFile(runID, name)
	if len(data) > memoFileMax {
		return data, err
	}
	s.mu.Lock()
	s.files[k] = fileRead{data: data, err: err}
	s.mu.Unlock()
	return data, err
}

// VerifySeal implements leaderboard.SealSource, checking each run once per
// request. Only a pass or a seal mismatch is remembered; any other error (a
// cancelled request) is returned and asked again next time.
func (s *requestLeaderboardSource) VerifySeal(ctx context.Context, runID string) error {
	s.mu.Lock()
	err, ok := s.seals[runID]
	s.mu.Unlock()
	if ok {
		return err
	}
	err = s.fullLeaderboardSource.VerifySeal(ctx, runID)
	if err == nil || errors.Is(err, schemas.ErrSealMismatch) {
		s.mu.Lock()
		s.seals[runID] = err
		s.mu.Unlock()
	}
	return err
}
