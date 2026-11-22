package redteam

import (
	"strings"
	"testing"
)

func TestAttackFor(t *testing.T) {
	for _, c := range testPlanCells() {
		cfg := attackFor("s", c, 4)
		if cfg.Goal == "" || len(cfg.Script.Seeds) == 0 || cfg.Script.Opening == "" || cfg.Persona.Goal == "" {
			t.Fatalf("%s: incomplete %+v", TaskID(c), cfg)
		}
		if cfg.Success.Kind == "leak" && (cfg.Canary == "" || strings.Contains(cfg.Goal, cfg.Canary)) {
			t.Fatalf("%s: leak task needs a canary the attacker is not told", TaskID(c))
		}
		if c.Channel != ChannelChat && (cfg.Benign == nil || len(cfg.Carriers) == 0) {
			t.Fatalf("%s: injection task needs carriers and a harmless user", TaskID(c))
		}
	}
	if a, b := canary("P-", "s", "t1"), canary("P-", "s", "t2"); a == b || len(a) != 10 {
		t.Fatalf("canaries %s %s", a, b)
	}
}
