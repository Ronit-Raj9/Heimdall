package reporters

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A sampled p is labelled with its draws; an exact one is a bare number
// (audit 1-B-07).
func TestPValueLabelsSampled(t *testing.T) {
	p := 0.000001
	if got := pValue(&schemas.Effect{PValue: &p, PDraws: 1000000}); !strings.Contains(got, "sampled, 1000000 sign vectors") {
		t.Fatalf("sampled: %q", got)
	}
	q := 0.125
	if got := pValue(&schemas.Effect{PValue: &q}); got != "0.1250" {
		t.Fatalf("exact: %q", got)
	}
}
