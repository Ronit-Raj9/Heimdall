package reporters

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestExitCode(t *testing.T) {
	regInv := []schemas.Decision{schemas.DecisionRegressed, schemas.DecisionInvalid}
	all := []schemas.Decision{schemas.DecisionRegressed, schemas.DecisionInconclusive, schemas.DecisionInvalid}
	tests := []struct {
		name   string
		d      schemas.Decision
		failOn []schemas.Decision
		want   int
	}{
		{"improved passes", schemas.DecisionImproved, all, 0},
		{"regressed listed", schemas.DecisionRegressed, regInv, 1},
		{"invalid listed", schemas.DecisionInvalid, regInv, 3},
		{"inconclusive not listed", schemas.DecisionInconclusive, regInv, 0},
		{"inconclusive listed", schemas.DecisionInconclusive, all, 2},
		{"nothing listed", schemas.DecisionRegressed, nil, 0},
		{"improved listed is a failure", schemas.DecisionImproved, []schemas.Decision{schemas.DecisionImproved}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.d, tt.failOn); got != tt.want {
				t.Errorf("ExitCode(%s, %v) = %d, want %d", tt.d, tt.failOn, got, tt.want)
			}
		})
	}
	if ExitInfra != 4 {
		t.Errorf("ExitInfra = %d, want 4", ExitInfra)
	}
}
