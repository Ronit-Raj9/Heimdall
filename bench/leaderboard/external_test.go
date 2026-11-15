package leaderboard

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestExternalExclusion(t *testing.T) {
	exp := func(external string) *expInfo {
		return &expInfo{id: "e", m: &schemas.Manifest{Experiment: schemas.Experiment{External: external}}}
	}
	pass := schemas.Run{Outcome: schemas.OutcomePass}
	tests := []struct {
		name string
		rec  record
		want string
	}{
		{"no external", record{exp: exp(""), sealed: true, run: pass}, ""},
		{"replay counts normally", record{exp: exp(schemas.ExternalReplay), sealed: true, run: pass}, ""},
		{"live excluded", record{exp: exp(schemas.ExternalLive), sealed: true, run: pass}, ReasonExternalLive},
		{"record excluded", record{exp: exp(schemas.ExternalRecord), sealed: true, run: pass}, ReasonExternalLive},
		{"no manifest", record{exp: &expInfo{id: "e"}, sealed: true, run: pass}, ""},
		{"invalid verdict still wins", record{exp: &expInfo{id: "e", invalid: true, m: exp(schemas.ExternalLive).m}, sealed: true, run: pass}, ReasonVerdictInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := excludeReason(&tt.rec); got != tt.want {
				t.Errorf("excludeReason = %q, want %q", got, tt.want)
			}
		})
	}
}
