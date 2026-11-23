package ci

import (
	"strings"
	"testing"
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
