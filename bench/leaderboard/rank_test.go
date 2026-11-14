package leaderboard

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestHolm(t *testing.T) {
	tests := []struct {
		name string
		p    []float64
		want []float64
	}{
		{name: "empty", p: []float64{}, want: []float64{}},
		{name: "one test is unadjusted", p: []float64{0.03}, want: []float64{0.03}},
		// Sorted 0.01, 0.02, 0.04: 3*0.01, 2*0.02, 1*0.04, made monotone.
		{name: "step down", p: []float64{0.04, 0.01, 0.02}, want: []float64{0.04, 0.03, 0.04}},
		{name: "capped at 1", p: []float64{0.5, 0.6}, want: []float64{1, 1}},
		{name: "monotone when a later product is smaller", p: []float64{0.02, 0.011}, want: []float64{0.022, 0.022}},
		{name: "NaN taken as 1", p: []float64{math.NaN(), 0.01}, want: []float64{1, 0.02}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := holm(tt.p)
			if len(got) != len(tt.want) {
				t.Fatalf("holm = %v, want %v", got, tt.want)
			}
			for i := range got {
				if math.Abs(got[i]-tt.want[i]) > 1e-12 {
					t.Errorf("holm = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestHolmProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 200 {
		p := make([]float64, 1+r.IntN(8))
		for j := range p {
			p[j] = r.Float64()
		}
		adj := holm(p)
		for a := range p {
			if adj[a] < p[a] || adj[a] > 1 {
				t.Fatalf("case %d: adjusted %v below raw %v or above 1", i, adj[a], p[a])
			}
			for b := range p {
				if p[a] < p[b] && adj[a] > adj[b] {
					t.Fatalf("case %d: not monotone: raw %v < %v but adjusted %v > %v", i, p[a], p[b], adj[a], adj[b])
				}
			}
		}
	}
}

// calc is an entry with per-task rates; success is their mean.
func calc(hash string, runs int, cost schemas.Value, rates map[string]float64) *entryCalc {
	c := &entryCalc{rates: rates}
	c.e.ConfigHash, c.e.Runs = hash, runs
	c.e.Success = successValue(rates)
	c.e.Metrics = map[string]schemas.Value{MetricCostPerSuccess: cost}
	return c
}

// tasks returns rates t01..tNN, the first ones with rate hi and the rest lo.
func tasks(n, nHi int, hi, lo float64, prefix string) map[string]float64 {
	out := map[string]float64{}
	for i := range n {
		v := lo
		if i < nHi {
			v = hi
		}
		out[fmt.Sprintf("%s%02d", prefix, i+1)] = v
	}
	return out
}

func TestCompareEntries(t *testing.T) {
	cheap, dear, na := obs(0.1), obs(0.5), schemas.NotApplicable()
	calcs := []*entryCalc{
		calc("h_unknown", 9, cheap, map[string]float64{}),
		calc("h_half_na", 9, na, tasks(2, 1, 1, 0, "t")),
		calc("h_half_dear", 1, dear, tasks(2, 1, 1, 0, "t")),
		calc("h_half_cheap_few", 1, cheap, tasks(2, 1, 1, 0, "t")),
		calc("h_half_cheap_many", 5, cheap, tasks(2, 1, 1, 0, "t")),
		calc("h_full_b", 2, dear, tasks(2, 2, 1, 0, "t")),
		calc("h_full_a", 2, dear, tasks(2, 2, 1, 0, "t")),
	}
	slices.SortStableFunc(calcs, compareEntries)
	var got []string
	for _, c := range calcs {
		got = append(got, c.e.ConfigHash)
	}
	want := []string{"h_full_a", "h_full_b", "h_half_cheap_many", "h_half_cheap_few", "h_half_dear", "h_half_na", "h_unknown"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestRankTiers(t *testing.T) {
	c := obs(0.1)
	leader := calc("h_leader", 8, c, tasks(8, 8, 1, 0, "t"))
	behind := calc("h_behind", 8, c, tasks(8, 0, 1, 0, "t"))
	same := calc("h_same", 8, obs(0.2), tasks(8, 8, 1, 0, "t"))
	few := calc("h_few", 8, c, tasks(8, 5, 1, 0, "t"))
	apart := calc("h_apart", 8, c, tasks(3, 0, 1, 0.5, "u"))
	calcs := []*entryCalc{apart, few, behind, same, leader}
	rank(calcs, DefaultAlpha)
	byHash := map[string]*entryCalc{}
	for i, c := range calcs {
		if c.e.Rank != i+1 {
			t.Errorf("%s rank %d at position %d", c.e.ConfigHash, c.e.Rank, i)
		}
		byHash[c.e.ConfigHash] = c
	}
	// Three tested entries: behind has raw p 2/256, Holm multiplies by 3.
	tests := []struct {
		hash, reason string
		tier         int
		p            float64 // -1 for nil
	}{
		{hash: "h_leader", reason: "leader", tier: 1, p: -1},
		{hash: "h_same", reason: "same success as the leader on all 8 common tasks", tier: 1, p: 1},
		{hash: "h_few", reason: "too few differing tasks (3 of 8 common) to separate", tier: 1, p: 0.5},
		{hash: "h_behind", reason: "p 0.023 on 8 common tasks: behind the leader", tier: 2, p: 3 * 2.0 / 256},
		{hash: "h_apart", reason: "no tasks in common with the leader", tier: 1, p: -1},
	}
	for _, tt := range tests {
		t.Run(tt.hash, func(t *testing.T) {
			e := byHash[tt.hash].e
			if e.Tier != tt.tier || e.TierReason != tt.reason {
				t.Errorf("tier %d %q, want %d %q", e.Tier, e.TierReason, tt.tier, tt.reason)
			}
			switch {
			case tt.p < 0 && e.PVsLeader != nil:
				t.Errorf("p = %v, want nil", *e.PVsLeader)
			case tt.p >= 0 && (e.PVsLeader == nil || math.Abs(*e.PVsLeader-tt.p) > 1e-9):
				t.Errorf("p = %v, want %v", e.PVsLeader, tt.p)
			}
		})
	}
	if calcs[0].e.ConfigHash != "h_leader" || calcs[1].e.ConfigHash != "h_same" {
		t.Errorf("leader %s, second %s: equal success breaks on lower cost", calcs[0].e.ConfigHash, calcs[1].e.ConfigHash)
	}
}

func TestRankAheadOnCommonTasks(t *testing.T) {
	// Scope all: the leader wins on its own task set, but on the 8 tasks both
	// ran the other entry passes every one. That is not "behind".
	leaderRates := tasks(8, 0, 1, 0, "t")
	for k, v := range tasks(12, 12, 1, 0, "x") {
		leaderRates[k] = v
	}
	otherRates := tasks(8, 8, 1, 0, "t")
	for k, v := range tasks(20, 0, 1, 0, "y") {
		otherRates[k] = v
	}
	calcs := []*entryCalc{calc("h_other", 28, obs(1), otherRates), calc("h_leader", 20, obs(1), leaderRates)}
	rank(calcs, DefaultAlpha)
	e := calcs[1].e
	if e.ConfigHash != "h_other" || e.Tier != 1 || e.TierReason != "p 0.0078 on 8 common tasks, ahead of the leader on them: the task sets differ" {
		t.Errorf("got %s tier %d %q", e.ConfigHash, e.Tier, e.TierReason)
	}
}

func TestFormatP(t *testing.T) {
	tests := map[float64]string{1: "1.00", 0.62: "0.62", 0.05: "0.05", 0.0078125: "0.0078", 0.023: "0.023"}
	for p, want := range tests {
		if got := formatP(p); got != want {
			t.Errorf("formatP(%v) = %q, want %q", p, got, want)
		}
	}
}
