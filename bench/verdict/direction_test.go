package verdict

import "testing"

func TestMetricDirection(t *testing.T) {
	tests := []struct {
		metric string
		higher bool
		rate   bool
	}{
		{MetricVerifiedSuccess, true, true},
		{MetricFalseCompletion, false, true},
		{MetricCostUSD, false, false},
		{MetricTokens, false, false},
		{MetricWallTime, false, false},
		{MetricSteps, false, false},
		{"secret_exposed", false, true},
		{"reward_correctness", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			if HigherIsBetter(tt.metric) != tt.higher || RateMetric(tt.metric) != tt.rate {
				t.Fatalf("higher %v rate %v, want %v %v", HigherIsBetter(tt.metric), RateMetric(tt.metric), tt.higher, tt.rate)
			}
			want := 1.0
			if !tt.higher {
				want = -1
			}
			if better(tt.metric) != want {
				t.Fatalf("better %v, want %v", better(tt.metric), want)
			}
		})
	}
	if len(LowerIsBetterMetrics()) != len(lowerIsBetter) || len(RateMetrics()) != len(rateMetrics) {
		t.Fatal("exported lists differ from the tables")
	}
}
