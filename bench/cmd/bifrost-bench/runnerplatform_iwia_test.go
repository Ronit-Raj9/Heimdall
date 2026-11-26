package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestSandboxGatewayWrapsOnce: when an org's default target is a platform
// and it also has a platform for single evaluations, the platform worker's
// registry starts from one whose gateway already carries the default
// platform's egress probe. The new probe replaces it, so a preflight probes
// egress once, from the platform that runs the runs.
func TestSandboxGatewayWrapsOnce(t *testing.T) {
	var inner schemas.Gateway = &calGateway{}
	first := sandboxGateway(inner, &sandbox.Driver{})
	second := sandboxGateway(first, &sandbox.Driver{})
	sg, ok := second.(*sandbox.Gateway)
	if !ok {
		t.Fatalf("not a sandbox gateway: %T", second)
	}
	if got := sg.UnwrapGateway(); got != inner {
		t.Fatalf("second wrap wraps %T, want the plain gateway", got)
	}
}
