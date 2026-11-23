package main

import (
	"strings"
	"testing"
)

// A hosted readiness check whose runs never started says why, in plain
// words, instead of "this step failed" (N180).
func TestHostedAgentCheckNoRunStartedReason(t *testing.T) {
	missing := "environment/docker_workspace: exec agent: exec run r_1: inspect image bench/agent-zz-test-loop:1-coding-core: docker image: exit 1: Error response from daemon: No such image: bench/agent-zz-test-loop:1-coding-core (agent never launched)"
	runs := []bridgeRun{{Outcome: "aborted_infra", Detail: missing}, {Outcome: "aborted_infra", Detail: missing}}
	res, d := checkVerdict(runs, 2)
	if res != checkError || d != "The test run could not start: the agent container bench/agent-zz-test-loop:1-coding-core does not exist on the worker yet." {
		t.Fatalf("%s %q", res, d)
	}
	other := []bridgeRun{{Outcome: "aborted_infra", Detail: "plan the experiment locally: prepare the agent container bench/a:1-s for s: docker build bench/a:1-s: exit status 1"}}
	if res, d = checkVerdict(other, 1); res != checkError || !strings.HasPrefix(d, "The test run could not start: plan the experiment locally: prepare the agent container") {
		t.Fatalf("%s %q", res, d)
	}
	// One run that started keeps the usual verdict.
	mixed := []bridgeRun{{Outcome: "aborted_infra", Detail: missing}, {Outcome: "pass"}}
	if _, d = checkVerdict(mixed, 2); strings.HasPrefix(d, "The test run could not start") {
		t.Fatalf("a started run was reported as none started: %q", d)
	}
}
