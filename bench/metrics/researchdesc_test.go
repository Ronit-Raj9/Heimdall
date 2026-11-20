package metrics

import (
	"strings"
	"testing"
)

// TestResearchDescriptorsNameCorpus covers 7-C-10: searches and
// citations_grounded also read the fixed-corpus search service, and their
// descriptions say so instead of "Tavily only".
func TestResearchDescriptorsNameCorpus(t *testing.T) {
	for _, d := range []string{newSearchesMetric().Descriptor().Description, newCitationsGroundedMetric().Descriptor().Description} {
		if !strings.Contains(d, "fixed-corpus search service") {
			t.Errorf("description does not name the corpus service: %q", d)
		}
	}
}
