package schemas

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// A direction blocked by a stage p of 1 has combined z -Inf; the JSON must
// still encode, carry null and name the field in z_blocked (audit 1-B-04).
func TestExtensionResultJSONBlockedDirection(t *testing.T) {
	r := ExtensionResult{Parent: "p", Child: "c", Decision: DecisionImproved,
		ZImproveLow: 3.1, ZImproveHi: 3.2, ZRegressLow: math.Inf(-1), ZRegressHi: math.Inf(-1), ZCritical: 2.2}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{`"z_regress_low":null`, `"z_regress_high":null`, `"z_blocked":["z_regress_low","z_regress_high"]`, `"z_improve_low":3.1`} {
		if !strings.Contains(s, want) {
			t.Errorf("json %s lacks %s", s, want)
		}
	}
	var back ExtensionResult
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(back.ZRegressLow, -1) || back.ZImproveHi != 3.2 || back.Decision != DecisionImproved {
		t.Errorf("round trip = %+v", back)
	}
}
