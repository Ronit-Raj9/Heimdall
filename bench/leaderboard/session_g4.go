package leaderboard

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Session shares the work every board of one request repeats: collect (every
// experiment's manifest and runs) and classify (the seal check and ledger
// read of every sealed run). The agent board builds one board per suite and
// per simulator pin over the same source; with a Session these reads happen
// once instead of once per board.
//
// A Session holds no lock and is not safe for concurrent use. It is meant to
// live for one request: it never notices runs sealed after its first Build,
// so it must never be kept across requests. Each Build works on its own copy
// of the records, so one board's grade source reads never leak into another.
type Session struct {
	src       Source
	done      bool
	recs      []*record
	synthetic map[string]bool
}

// NewSession starts a Session over src.
func NewSession(src Source) *Session { return &Session{src: src} }

// Build is leaderboard.Build over the Session's shared reads. The board is
// the same as Build(ctx, src, q) gives for the same source.
func (s *Session) Build(ctx context.Context, q Query) (*schemas.Leaderboard, error) {
	return build(ctx, s.src, q, s)
}

// records returns a fresh copy of the collected and classified records and
// the synthetic classes. The first call reads; later calls copy. A read error
// is not kept, so a later call reads again (a canceled request stays an error
// on every call through ctx).
func (s *Session) records(ctx context.Context) ([]*record, map[string]bool, error) {
	if !s.done {
		recs, err := collect(ctx, s.src)
		if err != nil {
			return nil, nil, err
		}
		synthetic, err := classify(ctx, s.src, recs)
		if err != nil {
			return nil, nil, err
		}
		s.recs, s.synthetic, s.done = recs, synthetic, true
	}
	return cloneRecords(s.recs), s.synthetic, nil
}

// cloneRecords copies the records and their experiments, keeping records of
// one experiment on one shared copy as collect made them. Fields a board
// writes later (the grade source reads, the experiment's regrade) are on the
// copies; the manifest, arm and trials stay shared, as nothing writes them.
func cloneRecords(recs []*record) []*record {
	exps := map[*expInfo]*expInfo{}
	out := make([]*record, len(recs))
	for i, r := range recs {
		cp := *r
		e, ok := exps[r.exp]
		if !ok && r.exp != nil {
			ec := *r.exp
			e = &ec
			exps[r.exp] = e
		}
		cp.exp = e
		out[i] = &cp
	}
	return out
}
