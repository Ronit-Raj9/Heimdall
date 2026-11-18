package reporters

import (
	"bufio"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestWriteOutage(t *testing.T) {
	meters := []schemas.Meter{
		{Name: "outage_safe_stop", Kind: schemas.KindRate, PerArm: map[string]schemas.Value{"A": schemas.Observed(1, schemas.EvidenceGateway), "B": schemas.Observed(0, schemas.EvidenceGateway)}},
		{Name: "outage_retry_storm_median", Kind: schemas.KindCount, PerArm: map[string]schemas.Value{"A": schemas.Observed(1, schemas.EvidenceGateway), "B": schemas.Observed(29, schemas.EvidenceGateway)}},
		{Name: "outage_disclosed", Kind: schemas.KindRate, PerArm: map[string]schemas.Value{"A": schemas.Observed(1, schemas.EvidenceOutcome), "B": schemas.Unknown("no claim", schemas.EvidenceOutcome)}},
	}
	var b strings.Builder
	w := bufio.NewWriter(&b)
	writeOutage(w, meters, []string{"A", "B"})
	w.Flush()
	out := b.String()
	for _, want := range []string{"## Outage behaviour", "| Safe stop", "| Model calls per minute while down, median | 1.0 | 29.0 |", "Outage disclosed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("card lacks %q:\n%s", want, out)
		}
	}
	b.Reset()
	w = bufio.NewWriter(&b)
	writeOutage(w, []schemas.Meter{{Name: "steps_median"}}, []string{"A"})
	w.Flush()
	if b.Len() != 0 {
		t.Fatalf("card without outage meters: %q", b.String())
	}
}
