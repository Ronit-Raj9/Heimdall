package ci

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fix wave 3, N61 (group B): the CI card names the release rule set by its
// file, as Releases > Rules lists it, not by the name inside the file.
func TestVerdictCardNamesRuleSetByFile(t *testing.T) {
	an := cardAnalysis()
	an.Policy.PolicyFile = "strict-release"
	got := VerdictCard(an, CardInput{Tasks: 10})
	if !strings.Contains(got, "**Policy** strict-release: ") || strings.Contains(got, "enterprise-default") {
		t.Fatalf("card policy line does not name the file:\n%s", got)
	}
}

// Fix wave 3, N244 (group B): a comparison of three or more setups never
// prints a B-against-A effect on the CI card, only the setups result.
func TestVerdictCardSetupsHasNoBAgainstA(t *testing.T) {
	an := cardAnalysis()
	an.Policy = nil
	an.Verdict.Decision, an.Verdict.Action = schemas.DecisionInconclusive, schemas.ActionNoDecision
	an.Verdict.Setups = &schemas.SetupsVerdict{Leader: "B", TopGroup: []string{"B", "A", "C"}, Behind: []string{}, Separable: true,
		Safety: map[string][]string{"B": {"t02"}}}
	got := VerdictCard(an, CardInput{Tasks: 10})
	for _, bad := range []string{"B against A", "worse"} {
		if strings.Contains(got, bad) {
			t.Fatalf("setups card says %q:\n%s", bad, got)
		}
	}
	for _, want := range []string{"**Setups**: cannot tell B, A, C apart yet.", "B broke a safety rule on t02."} {
		if !strings.Contains(got, want) {
			t.Fatalf("setups card lacks %q:\n%s", want, got)
		}
	}
	an.Verdict.Decision = schemas.DecisionLeader
	an.Verdict.Setups.TopGroup, an.Verdict.Setups.Behind = []string{"B"}, []string{"A", "C"}
	if got = VerdictCard(an, CardInput{}); !strings.Contains(got, "**Setups**: B leads every other setup (A, C behind).") {
		t.Fatalf("leader card:\n%s", got)
	}
}
