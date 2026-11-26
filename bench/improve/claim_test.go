package improve

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

// claimCase builds held-out means where B minus A is d_t on task t.
func claimCase(d []float64) (a, b map[string]float64, tasks []string) {
	a, b = map[string]float64{}, map[string]float64{}
	for i, v := range d {
		t := fmt.Sprintf("t%03d", i)
		tasks = append(tasks, t)
		if v >= 0 {
			a[t], b[t] = 0, v
		} else {
			a[t], b[t] = -v, 0
		}
	}
	return a, b, tasks
}

func repeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func claimSpec() *Spec {
	return &Spec{Commit: CommitSpec{Alpha: 0.10}, Claim: ClaimSpec{Alpha: 0.05, ExtensionAlpha: 0.02, TargetGain: 0.05, Extension: true}}
}

func TestClaimStates(t *testing.T) {
	mixed := func(pos, neg, zero int) []float64 {
		return append(append(repeat(0.5, pos), repeat(-0.5, neg)...), repeat(0, zero)...)
	}
	cases := []struct {
		name      string
		d         []float64
		committed bool
		approved  bool
		want      string
	}{
		{"no round committed", repeat(1, 10), false, false, VerdictNoChange},
		{"approval of a paused check", repeat(1, 10), true, true, VerdictNotClaimable},
		{"supported", repeat(1, 10), true, false, VerdictSupported},
		{"worse", repeat(-1, 10), true, false, VerdictWorse},
		{"insufficient sample", repeat(1, 3), true, false, VerdictInsufficientSample},
		{"likely better", mixed(21, 13, 166), true, false, VerdictLikelyBetter},
		{"no evidence", mixed(17, 17, 166), true, false, VerdictNoEvidence},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b, tasks := claimCase(c.d)
			v := signFlipClaim{}.Decide(ClaimInput{Spec: claimSpec(), A: a, B: b, Tasks: tasks, Committed: c.committed, Approved: c.approved, ApprovedRound: 2})
			if v.State != c.want {
				t.Fatalf("state %s, want %s (%+v)", v.State, c.want, v)
			}
			if v.N != len(c.d) || !near(v.Level, 0.03) {
				t.Fatalf("n %d level %v", v.N, v.Level)
			}
			if v.SealedGain != v.Gain || v.P == nil || *v.P != v.PUpper {
				t.Fatalf("old fields not kept: %+v", v)
			}
			if _, err := json.Marshal(v); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClaimNumbers(t *testing.T) {
	a, b, tasks := claimCase(repeat(1, 10))
	v := signFlipClaim{}.Decide(ClaimInput{Spec: claimSpec(), A: a, B: b, Tasks: tasks, Committed: true})
	if v.Gain != 1 || v.NNonzero != 10 || v.PUpper != 1.0/1024 || v.PLower != 1 {
		t.Fatalf("%+v", v)
	}
	// sd is 0, so the floor 0.10 is used: (t(0.97, 9) + t(0.80, 9)) * 0.10 / sqrt(10).
	if math.IsNaN(v.MDE) || v.MDE <= 0 || v.MDE > 0.15 {
		t.Fatalf("mde %v", v.MDE)
	}
	if v.CI95[0] != 1 || v.CI95[1] != 1 {
		t.Fatalf("ci95 %v", v.CI95)
	}
}

func TestClaimOldRecordAlpha(t *testing.T) {
	// An old record has no claim block: the claim reads at the commit alpha.
	s := &Spec{Commit: CommitSpec{Alpha: 0.05}}
	a, b, tasks := claimCase(repeat(1, 10))
	v := signFlipClaim{}.Decide(ClaimInput{Spec: s, A: a, B: b, Tasks: tasks, Committed: true})
	if v.Level != 0.05 || v.State != VerdictSupported {
		t.Fatalf("%+v", v)
	}
	// An explicit level wins (the second look).
	v = signFlipClaim{}.Decide(ClaimInput{Spec: claimSpec(), A: a, B: b, Tasks: tasks, Committed: true, Level: 0.02})
	if v.Level != 0.02 {
		t.Fatalf("level %v", v.Level)
	}
}

func TestClaimCallsWording(t *testing.T) {
	old := Wording
	defer func() { Wording = old }()
	var got *Record
	rec := &Record{ID: "imp_x"}
	Wording = func(v *Verdict, r *Record) { got = r; v.Headline = "worded" }
	a, b, tasks := claimCase(repeat(1, 10))
	v := signFlipClaim{}.Decide(ClaimInput{Spec: claimSpec(), Rec: rec, A: a, B: b, Tasks: tasks, Committed: true})
	if v.Headline != "worded" || got != rec {
		t.Fatalf("wording not called: %+v", v)
	}
}

func TestOldVerdictStatesRead(t *testing.T) {
	for _, s := range []string{VerdictNotSupported, VerdictInconclusive} {
		var v Verdict
		if err := json.Unmarshal([]byte(`{"state":"`+s+`","sealed_gain":0.1}`), &v); err != nil || v.State != s || v.SealedGain != 0.1 {
			t.Fatalf("%s: %+v %v", s, v, err)
		}
	}
}
