package verdict

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// TestNoiseFloor decides experiments with and without an A/A arm and checks
// the variance card, the aa_noise_floor check and what they do to the
// verdict. B is the worked example of DESIGN.md 9.3 item 8 throughout, so
// the A-against-B effect stays inconclusive unless a case says otherwise.
func TestNoiseFloor(t *testing.T) {
	clean := schemas.ConditionClean
	workedA := []int{3, 3, 0, 2, 0, 2, 1, 3}
	workedB := []int{3, 3, 1, 3, 0, 3, 2, 3}
	none := []int{0, 0, 0, 0, 0, 0, 0, 0}
	all := []int{3, 3, 3, 3, 3, 3, 3, 3}
	a2Noise := []int{3, 2, 0, 3, 1, 2, 1, 3} // three tasks differ from workedA, in both directions
	worked := concat(passes(clean, ArmBaseline, 3, workedA...), passes(clean, ArmCandidate, 3, workedB...))
	withHashes := func(a, a2 string) *schemas.Manifest {
		m := testManifest(ArmBaseline, ArmCandidate, ArmAA)
		m.Arms[ArmBaseline] = schemas.ArmManifest{Label: "A", ConfigHash: a}
		m.Arms[ArmAA] = schemas.ArmManifest{Label: "A2", ConfigHash: a2}
		return m
	}
	declaredAA := testManifest(ArmBaseline, ArmCandidate)
	declaredAA.Experiment.AA = true
	oneBoundSD := stats.SD([]float64{1, 1, 1, 1, 1, 1, 0, 0, 0, 0}) // observed A2 minus A of the one-bound case
	tests := []struct {
		name       string
		m          *schemas.Manifest
		runs       []schemas.SealedRun
		source     string
		sd         float64 // the SD the MDE used
		measured   *float64
		aaTasks    int
		aaDecision schemas.Decision // empty: no A/A arm, so no decision
		check      schemas.InstrumentStatus
		decision   schemas.Decision
		action     schemas.Action
		reason     string // prefix of some invalid reason
		detail     string // substring of the card's detail
	}{
		{
			name: "no A2 arm: assumed SD", m: testManifest(ArmBaseline, ArmCandidate), runs: worked,
			source: schemas.VarianceAssumed, sd: 0.30, check: schemas.InstrumentNotApplicable,
			decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, detail: "SD 0.30 assumed: the experiment ran no A/A arm",
		},
		{
			name: "inconclusive A/A: measured SD", m: testManifest(ArmBaseline, ArmCandidate, ArmAA),
			runs:   concat(worked, passes(clean, ArmAA, 3, a2Noise...)),
			source: schemas.VarianceMeasuredAA, sd: aaWantSD(workedA, a2Noise, 3), measured: ptr(aaWantSD(workedA, a2Noise, 3)), aaTasks: 8,
			aaDecision: schemas.DecisionInconclusive, check: schemas.InstrumentPass,
			decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, detail: "measured from the A/A arm",
		},
		{
			name: "A2 better than A on every task: harness noise, invalid", m: testManifest(ArmBaseline, ArmCandidate, ArmAA),
			runs: concat(passes(clean, ArmBaseline, 3, none...), passes(clean, ArmCandidate, 3, workedB...), passes(clean, ArmAA, 3, all...)),
			// Every per-task difference is +1, so the SD is 0 and stays unused.
			source: schemas.VarianceAssumed, sd: 0.30, measured: ptr(0), aaTasks: 8,
			aaDecision: schemas.DecisionImproved, check: schemas.InstrumentFail,
			decision: schemas.DecisionInvalid, action: schemas.ActionFixHarness,
			reason: "harness noise: arm A2 (a copy of arm A) against arm A on clean, verified_success: d_bar +100.0 pp, p = 0.0078 over 8 tasks (8 discordant)",
		},
		{
			name: "A2 worse than A: harness noise, invalid", m: testManifest(ArmBaseline, ArmCandidate, ArmAA),
			runs: concat(passes(clean, ArmBaseline, 3, all...), passes(clean, ArmCandidate, 3, workedB...),
				passes(clean, ArmAA, 3, 0, 0, 1, 0, 1, 0, 0, 0)),
			source: schemas.VarianceMeasuredAA, sd: aaWantSD(all, []int{0, 0, 1, 0, 1, 0, 0, 0}, 3), measured: ptr(aaWantSD(all, []int{0, 0, 1, 0, 1, 0, 0, 0}, 3)), aaTasks: 8,
			aaDecision: schemas.DecisionRegressed, check: schemas.InstrumentFail,
			decision: schemas.DecisionInvalid, action: schemas.ActionFixHarness, reason: "harness noise: arm A2 (a copy of arm A) against arm A on clean, verified_success: d_bar -91.7 pp",
		},
		{
			name: "A2 on one task only: assumed SD", m: testManifest(ArmBaseline, ArmCandidate, ArmAA),
			runs:   concat(worked, passes(clean, ArmAA, 3, 2)),
			source: schemas.VarianceAssumed, sd: 0.30, aaTasks: 1, aaDecision: schemas.DecisionInconclusive, check: schemas.InstrumentPass,
			decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, detail: "an SD needs 2",
		},
		{
			name: "A2 equal to A on every task: SD 0 is not used", m: testManifest(ArmBaseline, ArmCandidate, ArmAA),
			runs:   concat(worked, passes(clean, ArmAA, 3, workedA...)),
			source: schemas.VarianceAssumed, sd: 0.30, measured: ptr(0), aaTasks: 8, aaDecision: schemas.DecisionInconclusive, check: schemas.InstrumentPass,
			decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, detail: "an SD of 0 cannot size an MDE",
		},
		{
			name: "A2 significant at one bound only: unknown, never invalid", m: testManifest(ArmBaseline, ArmCandidate, ArmAA),
			runs: concat(
				pattern(clean, ArmBaseline, append(append(repeat("000", 5), "0uu"), repeat("111", 4)...)...),
				pattern(clean, ArmCandidate, append(append(repeat("000", 5), "0uu"), repeat("111", 4)...)...),
				pattern(clean, ArmAA, append(append(repeat("111", 5), "1uu"), repeat("111", 4)...)...)),
			source: schemas.VarianceMeasuredAA, sd: oneBoundSD, measured: ptr(oneBoundSD), aaTasks: 10, aaDecision: schemas.DecisionInconclusive,
			check: schemas.InstrumentUnknown, decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision,
		},
		{
			name: "aa declared but no A2 run: unknown holds an improvement", m: declaredAA,
			runs:   concat(passes(clean, ArmBaseline, 3, none...), passes(clean, ArmCandidate, 3, all...)),
			source: schemas.VarianceAssumed, sd: 0.30, aaDecision: schemas.DecisionInconclusive, check: schemas.InstrumentUnknown,
			decision: schemas.DecisionImproved, action: schemas.ActionHold, detail: "an SD needs 2",
		},
		{
			name: "declared arm A2 with another config is no A/A arm", m: withHashes("sha256:a", "sha256:other"),
			runs:   concat(worked, passes(clean, ArmAA, 3, all...)),
			source: schemas.VarianceAssumed, sd: 0.30, check: schemas.InstrumentNotApplicable,
			decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision,
		},
		{
			name: "A2 with arm A's config hash is the A/A arm", m: withHashes("sha256:a", "sha256:a"),
			runs:   concat(worked, passes(clean, ArmAA, 3, a2Noise...)),
			source: schemas.VarianceMeasuredAA, sd: aaWantSD(workedA, a2Noise, 3), measured: ptr(aaWantSD(workedA, a2Noise, 3)), aaTasks: 8,
			aaDecision: schemas.DecisionInconclusive, check: schemas.InstrumentPass,
			decision: schemas.DecisionInconclusive, action: schemas.ActionNoDecision,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := decideOrFail(t, tt.m, tt.runs)
			v := a.Verdict
			c := v.Variance
			if c == nil {
				t.Fatal("no variance card")
			}
			if c.Source != tt.source || !near(c.SD, tt.sd, 1e-12) || !near(v.SD, c.SD, 0) || c.AssumedSD != 0.30 {
				t.Fatalf("card source %s sd %v (verdict %v), want %s %v; %+v", c.Source, c.SD, v.SD, tt.source, tt.sd, c)
			}
			if v.MDEAssumedSD != (tt.source != schemas.VarianceMeasuredAA) || c.MDE != v.MDE || !near(v.MDE, mdePP(pairedTasks(v.Effect), tt.sd, 0.05), 1e-9) {
				t.Fatalf("MDE %v (card %v), assumed %v; want it from SD %v", v.MDE, c.MDE, v.MDEAssumedSD, tt.sd)
			}
			if (c.MeasuredSD == nil) != (tt.measured == nil) || (c.MeasuredSD != nil && !near(*c.MeasuredSD, *tt.measured, 1e-12)) {
				t.Fatalf("measured SD %v, want %v", c.MeasuredSD, tt.measured)
			}
			if tt.aaDecision == "" {
				if c.AAArm != "" || c.AAEffect != nil || c.AADecision != nil {
					t.Fatalf("A/A fields set without an A/A arm: %+v", c)
				}
			} else if c.AAArm != ArmAA || c.AATasks != tt.aaTasks || c.AADecision == nil || *c.AADecision != tt.aaDecision ||
				c.AAEffect == nil || c.AAEffect.ArmA != ArmBaseline || c.AAEffect.ArmB != ArmAA || c.AAEffect.Condition != clean {
				t.Fatalf("A/A fields %+v (decision %v), want %d tasks, %s", c, c.AADecision, tt.aaTasks, tt.aaDecision)
			}
			chk := checkNamed(v.Instruments, InstrumentAANoiseFloor)
			if chk == nil || chk.Status != tt.check {
				t.Fatalf("aa_noise_floor %+v, want %s", chk, tt.check)
			}
			if v.Decision != tt.decision || v.Action != tt.action {
				t.Fatalf("verdict %s/%s, want %s/%s; reasons %v", v.Decision, v.Action, tt.decision, tt.action, v.InvalidReasons)
			}
			noise := 0
			for _, r := range v.InvalidReasons {
				if strings.HasPrefix(r, harnessNoisePrefix) {
					noise++
					if tt.reason == "" || !strings.HasPrefix(r, tt.reason) {
						t.Fatalf("invalid reason %q, want prefix %q", r, tt.reason)
					}
				}
			}
			if (noise == 1) != (tt.reason != "") || noise > 1 {
				t.Fatalf("%d harness noise reasons in %q, want one only when %q is set", noise, v.InvalidReasons, tt.reason)
			}
			if tt.detail != "" && !strings.Contains(c.Detail, tt.detail) {
				t.Fatalf("card detail %q lacks %q", c.Detail, tt.detail)
			}
			if v.Effect.ArmA != ArmBaseline || v.Effect.ArmB != ArmCandidate {
				t.Fatalf("primary effect compares %s and %s", v.Effect.ArmA, v.Effect.ArmB)
			}
		})
	}
}

// TestNoiseFloorKeepsA2OutOfTheComparison checks that an A/A arm with a
// veto and a far better score than both arms leaves the primary effect, the
// guards and integrity exactly as they are without it, and shows under its
// own id in the naive scoreboard.
func TestNoiseFloorKeepsA2OutOfTheComparison(t *testing.T) {
	clean := schemas.ConditionClean
	worked := concat(passes(clean, ArmBaseline, 3, 3, 3, 0, 2, 0, 2, 1, 3), passes(clean, ArmCandidate, 3, 3, 3, 1, 3, 0, 3, 2, 3))
	a2 := withVeto(passes(clean, ArmAA, 3, 3, 3, 1, 2, 0, 2, 1, 3), taskID(0), ArmAA)
	with := decideOrFail(t, testManifest(ArmBaseline, ArmCandidate, ArmAA), concat(worked, a2)).Verdict
	without := decideOrFail(t, testManifest(ArmBaseline, ArmCandidate), worked).Verdict
	naive := decideOrFail(t, testManifest(ArmBaseline, ArmCandidate, ArmAA), concat(worked, a2)).Naive
	if with.Effect.DBar != without.Effect.DBar || *with.Effect.PValue != *without.Effect.PValue || with.Effect.NTasks != without.Effect.NTasks {
		t.Fatalf("A2 moved the primary effect: %+v against %+v", with.Effect, without.Effect)
	}
	if with.Integrity != without.Integrity || with.Integrity != schemas.IntegrityClean {
		t.Fatalf("A2's veto reached integrity: %s against %s", with.Integrity, without.Integrity)
	}
	for i := range with.Guards {
		if with.Guards[i].Status != without.Guards[i].Status || with.Guards[i].Detail != without.Guards[i].Detail {
			t.Fatalf("A2 moved guard %d: %+v against %+v", i, with.Guards[i], without.Guards[i])
		}
	}
	if r, ok := naive[ArmAA]; !ok || r.Den != 24 {
		t.Fatalf("naive scoreboard %+v lacks arm A2", naive)
	}
}
