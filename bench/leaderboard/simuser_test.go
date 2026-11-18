package leaderboard

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSimulatedUserExclusion(t *testing.T) {
	exp := func(mode string) *expInfo {
		m := &schemas.Manifest{}
		if mode != "" {
			m.Simulator = &schemas.SimulatorSpec{Mode: mode, Provider: "anthropic", Model: "claude-sim"}
		}
		return &expInfo{id: "e", m: m}
	}
	pass := schemas.Run{Outcome: schemas.OutcomePass}
	for name, tt := range map[string]struct {
		rec  record
		want string
	}{
		"no simulator":          {record{exp: exp(""), sealed: true, run: pass}, ""},
		"scripted user counts":  {record{exp: exp(schemas.SimulatorScripted), sealed: true, run: pass}, ""},
		"llm user excluded":     {record{exp: exp(schemas.SimulatorLLM), sealed: true, run: pass}, ReasonSimulatedUser},
		"invalid verdict first": {record{exp: &expInfo{id: "e", invalid: true, m: exp(schemas.SimulatorLLM).m}, sealed: true, run: pass}, ReasonVerdictInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			if got := excludeReason(&tt.rec); got != tt.want {
				t.Errorf("excludeReason = %q, want %q", got, tt.want)
			}
		})
	}
}
