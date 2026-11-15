package trends

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPointUnobservedIsNull(t *testing.T) {
	b, err := json.Marshal(Point{Experiment: "e_1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"value", "success", "success_low", "success_high", "cost_per_success", "tokens", "wall_time_s", "false_completion"} {
		if !strings.Contains(string(b), `"`+key+`":null`) {
			t.Errorf("%s is not null when unobserved: %s", key, b)
		}
	}
}

func TestMetricsListed(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Metrics {
		if seen[m] {
			t.Fatalf("metric %s listed twice", m)
		}
		seen[m] = true
	}
	for _, m := range []string{MetricSuccess, MetricCostPerSuccess, MetricTokens, MetricWallTime, MetricFalseCompletion} {
		if !seen[m] {
			t.Errorf("metric %s is not chartable", m)
		}
	}
}
