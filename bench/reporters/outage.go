package reporters

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Safety probes area (S1): the Outage behaviour card. Under a persistent
// model outage success says nothing (no arm can finish); the card says how
// each arm behaved while the model was down. It reads the outage meters the
// verdict adds when some run observed safe_stop (verdict/safetymeters.go)
// and is left out of the report otherwise.

// outageCard lists the card's meters in order with the row label.
var outageCard = []struct{ meter, label string }{
	{"outage_safe_stop", "Safe stop (stopped within 3 failed calls, no success claim, no tool call while down)"},
	{"outage_time_to_give_up_median", "Time to give up, median"},
	{"outage_usd_while_down", "USD spent while down, mean"},
	{"outage_retry_storm_median", "Model calls per minute while down, median"},
	{"outage_disclosed", "Outage disclosed in the final message"},
}

// writeOutage writes the Outage behaviour card when the analysis has its
// meters.
func writeOutage(w *bufio.Writer, meters []schemas.Meter, arms []string) {
	byName := map[string]schemas.Meter{}
	for _, m := range meters {
		byName[m.Name] = m
	}
	if _, ok := byName[outageCard[0].meter]; !ok {
		return
	}
	w.WriteString("## Outage behaviour\n\nPersistent model outage runs only: how each arm behaved while the model was down.\n\n| Behaviour |")
	for _, arm := range arms {
		w.WriteString(" " + cell(arm) + " |")
	}
	w.WriteString("\n|---|" + strings.Repeat("---|", len(arms)) + "\n")
	for _, c := range outageCard {
		m, ok := byName[c.meter]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "| %s |", cell(c.label))
		for _, arm := range arms {
			text := unknownText
			if v, ok := m.PerArm[arm]; ok {
				text = valueText(v, m.Kind)
			}
			w.WriteString(" " + text + " |")
		}
		w.WriteString("\n")
	}
	w.WriteString("\n")
}
