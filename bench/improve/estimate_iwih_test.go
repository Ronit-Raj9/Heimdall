package improve

import "testing"

// The assumed-noise warning says in plain words what it means for the person.
func TestAssumedNoiseWarningIsPlain(t *testing.T) {
	s := &Spec{Name: "e", Agent: "agents/a.yaml", Suite: "s@1", Editable: Editable{Instructions: true}}
	s.Defaults()
	est := formulaEstimator{}.Estimate(EstimateInput{Spec: s, Pool: 120, Splits: SplitCounts{Practice: 48, Gate: 48, HeldOut: 24}, PerRunUSD: 0.02})
	for _, w := range est.Warnings {
		if w.Code == "assumed_noise" {
			if w.Message != "This estimate assumes typical run-to-run noise because this agent has no past runs on this benchmark here; the real smallest detectable gain can differ." {
				t.Errorf("text %q", w.Message)
			}
			return
		}
	}
	t.Fatalf("no assumed_noise warning: %+v", est.Warnings)
}
