package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
)

// TestOrgPolicyKnowsPlatformPins (SEC-C C2): the shared runner's org policy
// carries the platform's own image names and pins, so a plain platform name
// can stay plain only from the platform's own pin.
func TestOrgPolicyKnowsPlatformPins(t *testing.T) {
	t.Setenv("HEIMDALL_LIBRARY_DIR", "")
	h := &sharedHost{base: &app{cfg: &lib.Config{Root: t.TempDir()}}}
	p := h.orgPolicy("o1", nil, t.TempDir())
	if p.PlatformPin == nil {
		t.Fatal("org policy has no PlatformPin")
	}
	if pin, ok := p.PlatformPin("bench/coding-core-base:1"); !ok || pin != "" {
		t.Fatalf("core base = %q %v, want a platform image without a lock pin", pin, ok)
	}
	if _, ok := p.PlatformPin("bench/task-anything:1"); ok {
		t.Fatal("an unknown name is a platform image")
	}
}
