package improve

import (
	"strings"
	"testing"
)

func wordingRecord() *Record {
	rec := &Record{Incumbent: Version{Number: 2}, Spec: Spec{Commit: CommitSpec{Alpha: 0.05}}}
	rec.Rounds = []*Round{{N: 1, Decision: DecisionCommitted}, {N: 2, Approvals: []Approval{{By: "x"}}}}
	rec.Controls = &Controls{Retries: &RetriesControl{K: 3, LoopUSD: 12.5, UsedUSD: 9.25}}
	return rec
}

// TestWordingHeadlines is the snapshot of each state's headline (A2.12).
func TestWordingHeadlines(t *testing.T) {
	base := Verdict{N: 40, NNonzero: 12, Gain: 0.125, CI80: [2]float64{0.05, 0.2}, CI95: [2]float64{0.02, 0.23}, PUpper: 0.0123, PLower: 0.0042, MDE: 0.081}
	want := map[string]string{
		VerdictSupported:          "Version 2 is better: +12 points on 40 held-out tasks it never saw (95% interval +2.0 to +23, p 0.012).",
		VerdictLikelyBetter:       "Likely better, not proven: +12 points on 40 held-out tasks (80% interval +5.0 to +20, p 0.012). This many tasks can confirm gains of about 8.1 points or more.",
		VerdictInsufficientSample: "Not enough held-out tasks to tell: 40 tasks, 12 of them with different results for the two versions. Even a clear gain could not be confirmed with this many. Observed: +12 points.",
		VerdictNoEvidence:         "No clear change on the held-out tasks: +12 points on 40 tasks (80% interval +5.0 to +20). Keep version 0 unless you have other reasons.",
		VerdictWorse:              "Version 2 is worse: +12 points on 40 held-out tasks (95% interval +2.0 to +23, p 0.0042). Keep version 0.",
		VerdictNoChange:           "No new version was kept: none passed the gate. The agent stays at version 0.",
		VerdictNotClaimable:       "A person approved a paused check in round 2, so this result cannot be claimed. Observed: +12 points on 40 held-out tasks.",
	}
	for state, w := range want {
		v := base
		v.State = state
		wordVerdict(&v, wordingRecord())
		if v.Headline != w {
			t.Errorf("%s:\n got %q\nwant %q", state, v.Headline, w)
		}
	}
	v := base
	v.State, v.MDE = VerdictLikelyBetter, 0
	wordVerdict(&v, nil)
	if !strings.Contains(v.Headline, "not enough tasks to estimate") || strings.Contains(v.Headline, " 0 points") {
		t.Errorf("zero MDE: %q", v.Headline)
	}
}

// TestWordingLines is the snapshot of the lines under the headline, in order.
func TestWordingLines(t *testing.T) {
	v := &Verdict{State: VerdictLikelyBetter, N: 40, Gain: 0.06, Teacher: "big-model",
		Continue: &ContinueOffer{ExtraTrials: 2, NewTasks: 30, USD: [2]float64{3, 4.5}},
		Null:     &ControlLine{Score: 0.42, Diff: 0.03, CI80: [2]float64{-0.01, 0.07}},
		Retries:  &ControlLine{Score: 0.5, Diff: 0.04, CI80: [2]float64{0.01, 0.07}}}
	wordVerdict(v, wordingRecord())
	want := []string{
		"To confirm a 5.0 point gain: about 30 more held-out tasks, or 2 more tries on each task, about $4.50.",
		"Edits made without looking at failures (same rounds, same budget) scored 42%. Version 2 is +3.0 points ahead of them (80% interval -1.0 to +7.0). So feedback is not shown to matter here.",
		"Retrying version 0 when a run visibly failed, with up to the same money ($12.50, up to 3 tries per task, $9.25 used), scored 50%. Version 2 is +4.0 points ahead (80% interval +1.0 to +7.0).",
		"Teacher-assisted: the improver ran on big-model, not on the agent's own model. Its cost is in the totals.",
		"Each kept version passed a test on the gate tasks. Over the whole run, the chance that any kept version is no better than the one before it is at most 5.0%. The headline is tested separately on held-out tasks the loop never used.",
		"One run of the loop. Run it twice more from version 0 to see whether the gain repeats.",
	}
	if len(v.Sentences) != len(want) {
		t.Fatalf("lines = %q", v.Sentences)
	}
	for i := range want {
		if v.Sentences[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, v.Sentences[i], want[i])
		}
	}
	for _, s := range append(v.Sentences, v.Headline) {
		if strings.ContainsAny(s, "\u2013\u2014") {
			t.Errorf("dash in %q", s)
		}
	}
}

// TestWordingSet: the claim rule words its verdict through Wording.
func TestWordingSet(t *testing.T) {
	if Wording == nil {
		t.Fatal("Wording is not set")
	}
}
