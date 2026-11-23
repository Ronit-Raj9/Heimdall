package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fix wave 3, N61 (group B): the run command names the release rule set by
// its file, as Releases > Rules lists it, not by the name inside the file.
func TestPrintPolicyNamesRuleSetByFile(t *testing.T) {
	r := &schemas.PolicyResult{PolicyName: "enterprise-default", PolicyFile: "strict-release", PolicyHash: "5c1e0f9a12", Decision: schemas.PolicyBlock}
	_, out := captureOutput(t, func() int { printPolicy(r); return 0 })
	if !strings.HasPrefix(out, "policy strict-release (") || strings.Contains(out, "enterprise-default") {
		t.Fatalf("printPolicy = %q, want the file name strict-release only", out)
	}
	// A result saved before PolicyFile existed still names its rule set.
	r.PolicyFile = ""
	if _, out = captureOutput(t, func() int { printPolicy(r); return 0 }); !strings.HasPrefix(out, "policy enterprise-default (") {
		t.Fatalf("printPolicy without a file = %q", out)
	}
}
