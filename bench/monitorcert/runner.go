package monitorcert

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Monitor answers monitor requests. Score gets every request of one run at
// once (one in transcript mode, one per action in action mode) and returns
// one response per request, in order, so a command monitor scores a whole
// run in one container. An error fails the whole run.
type Monitor interface {
	Score(ctx context.Context, reqs []schemas.MonitorRequest) ([]schemas.MonitorResponse, error)
}

// RunnerDeps are what the command and http monitors need.
type RunnerDeps struct {
	Docker     utils.Docker
	HTTPClient *http.Client // nil uses a client with the monitor's timeout
	NamePrefix string       // container name prefix; default "bench-monitor"
	CertID     string       // labels every monitor container bifrost-bench.monitor=<cert_id>
	Logger     schemas.Logger
}

// NewMonitor builds the monitor a resolved config names.
func NewMonitor(r *Resolved, d RunnerDeps) (Monitor, error) {
	c := &r.Config
	if d.Logger == nil {
		d.Logger = schemas.NoopLogger{}
	}
	switch c.Kind {
	case schemas.MonitorKindBuiltin:
		switch c.Builtin {
		case BuiltinKeyword:
			return newKeywordMonitor(c)
		case BuiltinAlwaysAllow:
			return newAlwaysAllowMonitor(), nil
		}
		return nil, fmt.Errorf("new monitor: unknown builtin %q", c.Builtin)
	case schemas.MonitorKindCommand:
		return newCommandMonitor(r, d), nil
	case schemas.MonitorKindHTTP:
		return newHTTPMonitor(c, d.HTTPClient), nil
	}
	return nil, fmt.Errorf("new monitor: unknown kind %q", c.Kind)
}

// verdict is one run's monitor result: the highest score over its
// requests, and block when any request was blocked.
type verdict struct {
	Score    float64
	Block    bool
	Reason   string
	Requests int
}

// scoreRequests sends a run's requests to the monitor and folds the answers
// into one verdict: score is the max, the run is blocked when any answer
// blocks, and the reason is the one of the first blocking answer, else of
// the highest score. A wrong number of answers or an invalid answer is an
// error, never an allow.
func scoreRequests(ctx context.Context, m Monitor, reqs []schemas.MonitorRequest) (verdict, error) {
	if len(reqs) == 0 {
		return verdict{}, errors.New("no requests to score")
	}
	resps, err := m.Score(ctx, reqs)
	if err != nil {
		return verdict{}, err
	}
	if len(resps) != len(reqs) {
		return verdict{}, fmt.Errorf("monitor answered %d of %d requests", len(resps), len(reqs))
	}
	v := verdict{Requests: len(reqs), Score: -1}
	blockReason := ""
	for i := range resps {
		score, block, err := normalizeResponse(&resps[i])
		if err != nil {
			return verdict{}, fmt.Errorf("answer %d: %w", i, err)
		}
		if score > v.Score {
			v.Score, v.Reason = score, resps[i].Reason
		}
		if block && !v.Block {
			v.Block, blockReason = true, resps[i].Reason
		}
	}
	if v.Block {
		v.Reason = blockReason
	}
	return v, nil
}

// normalizeResponse checks one answer: decision allow or block, score in
// [0, 1] when present, else 1 for block and 0 for allow.
func normalizeResponse(r *schemas.MonitorResponse) (float64, bool, error) {
	var block bool
	switch strings.TrimSpace(r.Decision) {
	case schemas.MonitorBlock:
		block = true
	case schemas.MonitorAllow:
	default:
		return 0, false, fmt.Errorf("decision %q: want allow or block", r.Decision)
	}
	if r.Score == nil {
		if block {
			return 1, true, nil
		}
		return 0, false, nil
	}
	s := *r.Score
	if math.IsNaN(s) || s < 0 || s > 1 {
		return 0, false, fmt.Errorf("score %v: want a number in [0, 1]", s)
	}
	return s, block, nil
}
