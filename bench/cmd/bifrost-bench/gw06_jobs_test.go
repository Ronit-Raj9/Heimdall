package main

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// check.run names the rule set by its file, as Releases > Rules lists it,
// and leaves the policy out on a score run (N61).
func TestCheckRunResultPolicyFile(t *testing.T) {
	a, base := analysisFixture(t)
	hash := gw06Policy(t, a)
	pass := map[string]schemas.Outcome{"t01": schemas.OutcomePass, "t02": schemas.OutcomeFail}
	gw06Experiment(t, a, base, "e_pair", []string{"A", "B"}, pass, "policies/default.yaml", hash)
	gw06Experiment(t, a, base, "e_score", []string{"A"}, pass, "policies/default.yaml", hash)
	l := newLauncher(a, nil)
	ctx := context.Background()

	log := &lineLog{}
	out, err := checkRunJob(ctx, l, analysisParams{ExpID: "e_pair"}, log)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(checkRunResult)
	if res.Policy == "" || res.PolicyFile != "default" {
		t.Fatalf("result %+v, want policy_file default", res)
	}
	joined := strings.Join(log.lines, "\n")
	if !strings.Contains(joined, "release rule set default (") {
		t.Fatalf("log does not name the rule set file:\n%s", joined)
	}

	out, err = checkRunJob(ctx, l, analysisParams{ExpID: "e_score"}, &lineLog{})
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(checkRunResult); res.Decision != string(schemas.DecisionScore) || res.Policy != "" || res.PolicyFile != "" {
		t.Fatalf("score run result %+v, want no policy", res)
	}

	if got := policyFileName("policies/my-release-policy.yml"); got != "my-release-policy" {
		t.Fatalf("policyFileName = %q", got)
	}
}

// An infrastructure error is not scored, so blame does not count it as a
// failed run; it is counted apart (N62).
func TestBlameRunJobCountsInfraAsNotScored(t *testing.T) {
	a, base := analysisFixture(t)
	outcomes := map[string]schemas.Outcome{"t01": schemas.OutcomePass, "t02": schemas.OutcomeFail, "t03": schemas.OutcomeInfraError}
	gw06Experiment(t, a, base, "e_blame", []string{"A"}, outcomes, "", "")
	l := newLauncher(a, nil)
	log := &lineLog{}
	out, err := blameRunJob(context.Background(), l, analysisParams{ExpID: "e_blame"}, log)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(blameRunResult)
	if res.Runs != 3 || res.Failed != 1 || res.NotScored != 1 {
		t.Fatalf("blame %+v, want 3 runs, 1 failed, 1 not scored", res)
	}
	if last := log.lines[len(log.lines)-1]; !strings.Contains(last, "1 not scored") {
		t.Fatalf("summary line %q does not count the not scored run", last)
	}
}
