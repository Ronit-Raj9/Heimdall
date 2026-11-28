package handlers

import (
	"context"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/plaintext"
)

// Round 6 G1: a feature test's line says the state of the newest test of
// that feature (running with progress, no verdict and why, broken and why,
// or its result), and an older result only when it still holds under the
// current rules, labelled with its date.

// cardOlderLimit bounds how many older tests of one feature the card reads
// to find a result that still holds.
const cardOlderLimit = 5

// GatewayCardOlder is an older test's result, shown beside a newest test
// that has none.
type GatewayCardOlder struct {
	Result     string    `json:"result"`
	Headline   string    `json:"headline"`
	ExpID      string    `json:"exp_id"`
	MeasuredAt time.Time `json:"measured_at"` // when that test was made
}

// gatewayFacts is what the launcher knows of a comparison: whether it runs
// now, and why no run could start.
func gatewayFacts(env OrgEnv) gwfeature.Facts {
	var f gwfeature.Facts
	l := env.Services.Launcher
	if ar, ok := l.(ActivityReporter); ok {
		f.Active = ar.Active
	}
	if rp, ok := l.(RunProblemReporter); ok {
		f.Problem = func(expID string) string {
			p, _ := rp.RunProblem(expID)
			return plaintext.Hosted(p)
		}
	}
	return f
}

// cardPick is the effect a feature line shows and an older result.
type cardPick struct {
	current gwfeature.Effect
	at      time.Time // when the newest test was made
	older   *GatewayCardOlder
}

// cardEffects returns, per feature, the newest feature test's effect and,
// when it has no result, the newest older result that still holds. Nil
// without a run index.
func cardEffects(ctx context.Context, env OrgEnv, src leaderboard.Source, root string) map[string]cardPick {
	if src == nil {
		return nil
	}
	type test struct {
		id string
		at time.Time
	}
	byFeature := map[string][]test{}
	for _, id := range src.Experiments() {
		m, ok := src.Manifest(id)
		if !ok || m == nil || m.Experiment.GatewayTest == nil {
			continue
		}
		f := m.Experiment.GatewayTest.Feature
		byFeature[f] = append(byFeature[f], test{id, m.CreatedAt})
	}
	facts := gatewayFacts(env)
	out := map[string]cardPick{}
	for f, tests := range byFeature {
		slices.SortStableFunc(tests, func(a, b test) int { return b.at.Compare(a.at) })
		e, err := gwfeature.ComputeWith(ctx, src, root, tests[0].id, false, facts)
		if err != nil {
			continue
		}
		p := cardPick{current: e, at: tests[0].at}
		if e.Result == "" {
			for i, t := range tests[1:] {
				if i == cardOlderLimit || ctx.Err() != nil {
					break
				}
				o, err := gwfeature.ComputeWith(ctx, src, root, t.id, false, facts)
				if err == nil && o.State == gwfeature.StateMeasured && o.Result != "" {
					p.older = &GatewayCardOlder{Result: o.Result, Headline: o.Headline, ExpID: o.ExpID, MeasuredAt: t.at.UTC()}
					break
				}
			}
		}
		out[f] = p
	}
	return out
}
