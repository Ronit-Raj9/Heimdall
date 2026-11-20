package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestOfflineModeNeverLive is the regression test for audit finding 7-B-03:
// `serve -offline` replays recorded runs, so a bench.yaml that says live is
// served as recorded and a strict client refuses it.
func TestOfflineModeNeverLive(t *testing.T) {
	tests := []struct {
		configured schemas.Mode
		offline    bool
		want       schemas.Mode
	}{
		{schemas.ModeLive, true, schemas.ModeRecorded},
		{schemas.ModeLive, false, schemas.ModeLive},
		{schemas.ModeRecorded, true, schemas.ModeRecorded},
		{schemas.ModeSynthetic, true, schemas.ModeSynthetic},
	}
	for _, tt := range tests {
		if got := offlineMode(tt.configured, tt.offline); got != tt.want {
			t.Errorf("offlineMode(%s, %v) = %s, want %s", tt.configured, tt.offline, got, tt.want)
		}
	}
}
