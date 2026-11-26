package environments

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestStopGPUSamplerNilIsNoop guards the nil check a CPU run relies on:
// startGPUSampler returns nil without a GPU, and stopping that nil sampler
// must neither panic nor add a hardware record.
func TestStopGPUSamplerNilIsNoop(t *testing.T) {
	rc := &schemas.RunContext{Run: &schemas.Run{}}
	stopGPUSampler(rc, nil)
	if rc.Run.Hardware != nil {
		t.Fatalf("hardware record added for a CPU run: %+v", rc.Run.Hardware)
	}
	stopGPUSampler(&schemas.RunContext{}, nil)
}
