package verdict

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// withGate adds a fired gate result to the first n runs of arm.
func withGate(runs []schemas.SealedRun, arm string, n int, g schemas.GateResult) []schemas.SealedRun {
	out := append([]schemas.SealedRun(nil), runs...)
	for i := range out {
		if n == 0 {
			break
		}
		if out[i].Slot.Arm == arm {
			out[i].Gates = append(append([]schemas.GateResult(nil), out[i].Gates...), g)
			n--
		}
	}
	return out
}

func TestInstrumentGates(t *testing.T) {
	clean := schemas.ConditionClean
	counts := []int{3, 3, 0, 2, 0, 2, 1, 3}
	base := concat(passes(clean, ArmBaseline, 3, counts...), passes(clean, ArmCandidate, 3, counts...))
	served := schemas.GateResult{Gate: "served_model", Effect: schemas.GateInvalidate, Fired: true}
	egress := schemas.GateResult{Gate: GateEgress, Effect: schemas.GateInvalidate, Fired: true}
	report := schemas.GateResult{Gate: "g8_false_completion", Effect: schemas.GateReport, Fired: true}

	egressUncounted := withGate(passes(clean, "compat", 1, 1), "compat", 1, egress)
	egressUncounted[0].Slot.Counted = false

	staleManifest := append([]schemas.SealedRun(nil), base...)
	staleManifest[0].Run.ManifestHash = "other-hash"

	newVersion := append([]schemas.SealedRun(nil), base...)
	newVersion[len(newVersion)-1].Slot.TaskVersion = "1.1.0"

	gapB := pattern(clean, ArmCandidate, "1uu", "111", "111", "111", "111", "111", "111", "111", "111", "111")
	gapA := passes(clean, ArmBaseline, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3)

	tests := []struct {
		name   string
		runs   []schemas.SealedRun
		reason string // empty means no reason may fire
	}{
		{"clean experiment", base, ""},
		{"report gate never invalidates", withGate(base, ArmCandidate, 24, report), ""},
		{"invalidate gate in 1 of 24 runs is tolerated", withGate(base, ArmBaseline, 1, served), ""},
		{"invalidate gate in 2 of 24 runs", withGate(base, ArmBaseline, 2, served), "gate served_model fired in 2 of 24 runs (8.3%) of arm A"},
		{"one egress run", withGate(base, ArmCandidate, 1, egress), "egress gate fired in 1 run(s)"},
		{"egress in an uncounted run", concat(base, egressUncounted), "egress gate fired in 1 run(s)"},
		{"unequal trials", base[:len(base)-1], "unequal trials on task t08 condition clean: arm A 3, arm B 2"},
		{"arm B missing a task", concat(base[:24], passes(clean, ArmCandidate, 3, counts[:7]...)), "unequal trials on task t08 condition clean: arm A 3, arm B 0"},
		{"manifest mismatch", staleManifest, "manifest mismatch: 1 run(s)"},
		{"task version differs", newVersion, "task t08 ran at more than one version: 1.0.0, 1.1.0"},
		{"coverage gap above 5 pp with both above 0.90", concat(gapA, gapB), "graded coverage gap 6.7 pp"},
		{"no runs on the primary condition", nil, "arm A has no counted runs on clean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testManifest(ArmBaseline, ArmCandidate)
			cfg := settingsFor(m)
			got := instrumentGates(m, cfg, newRunIndex(tt.runs), tt.runs)
			if tt.reason == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected reasons %q", got)
				}
				return
			}
			if !strings.Contains(strings.Join(got, "\n"), tt.reason) {
				t.Fatalf("reasons %q do not mention %q", got, tt.reason)
			}
		})
	}
}

func TestCoverageGap(t *testing.T) {
	clean := schemas.ConditionClean
	tests := []struct {
		name  string
		b     []string
		fires bool
	}{
		{"gap of 1 in 30 runs is 3.3 pp", []string{"11u"}, false},
		{"gap of 2 in 30 runs is 6.7 pp", []string{"1uu"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := pattern(clean, ArmCandidate, append(tt.b, repeat("111", 9)...)...)
			a := passes(clean, ArmBaseline, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3)
			idx := newRunIndex(concat(a, b))
			got := strings.Join(coverageReasons(idx, clean, MetricVerifiedSuccess), "\n")
			if strings.Contains(got, "graded coverage gap") != tt.fires {
				t.Fatalf("reasons %q, want gap firing %v", got, tt.fires)
			}
		})
	}
}
