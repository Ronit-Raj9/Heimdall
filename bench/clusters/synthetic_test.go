package clusters

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSynthetic(t *testing.T) {
	live := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{Adapter: "command"}}}}
	smoke := &schemas.Manifest{Experiment: schemas.Experiment{Smoke: true}, Arms: live.Arms}
	scripted := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{Adapter: adapterScripted}}}}
	run := sealed("r", schemas.OutcomeFail)
	unlisted := run
	unlisted.Slot.Arm = "Z"
	unlisted.Run.Agent.Adapter = adapterScripted
	tests := []struct {
		name   string
		m      *schemas.Manifest
		r      schemas.SealedRun
		ledger []schemas.LedgerEntry
		want   bool
	}{
		{name: "live", m: live, r: run, ledger: []schemas.LedgerEntry{{Upstream: schemas.LedgerUpstreamReal}}, want: false},
		{name: "smoke experiment", m: smoke, r: run, want: true},
		{name: "scripted arm", m: scripted, r: run, want: true},
		{name: "scripted run without a manifest arm", m: live, r: unlisted, want: true},
		{name: "scripted model in the ledger", m: live, r: run, ledger: []schemas.LedgerEntry{{Upstream: schemas.LedgerUpstreamReal}, {Upstream: schemas.LedgerUpstreamScripted}}, want: true},
		{name: "no manifest", m: nil, r: run, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Synthetic(tt.m, tt.r, tt.ledger); got != tt.want {
				t.Fatalf("synthetic %v, want %v", got, tt.want)
			}
		})
	}
}
