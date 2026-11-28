package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The bodies the New improvement form sends (ui improvementSpec): the
// smallest size, and the old Small preset that PAID-19 found at $1,811 to
// $3,884 on a 3641 task benchmark.
const (
	webTrialBody = `{"kind":"improvement","name":"t-improve","agent":"agents/a.yaml","suite":"hd-harbor-bfcl@1.0.0","preset":"trial",` +
		`"editable":{"instructions":true,"files":[]},"improver":{"task":"heimdall-improver@1.1.0:reflect-edit","candidates":1,"keep":1},` +
		`"budget":{"total_usd":3},"rounds":1,"max_tasks":12}`
	webSmallBody = `{"kind":"improvement","name":"s-improve","agent":"agents/a.yaml","suite":"hd-harbor-bfcl@1.0.0","preset":"small",` +
		`"editable":{"instructions":true,"files":[]},"improver":{"task":"heimdall-improver@1.1.0:reflect-edit"},"budget":{"total_usd":2}}`
)

func bigSuite(n int) *schemas.Suite {
	s := &schemas.Suite{}
	for i := range n {
		s.Tasks = append(s.Tasks, &schemas.Task{ID: fmt.Sprintf("t-%04d", i)})
	}
	return s
}

func webEstimate(t *testing.T, body string, perRun float64) improve.Estimate {
	t.Helper()
	spec, err := improve.ParseSpec([]byte(body))
	if err != nil {
		t.Fatalf("the form's body is refused: %v", err)
	}
	in, err := improveEstimatePool(bigSuite(3641), spec)
	if err != nil {
		t.Fatal(err)
	}
	in.Spec, in.PerRunUSD, in.RunSource = spec, perRun, "forecast"
	return improve.DefaultEstimator.Estimate(in)
}

// PAID-19: the smallest size of the web form estimates a few dollars on a
// cheap model, says it cannot prove a gain, and is not refused; the old
// Small size on the same benchmark is still refused by the money rule.
func TestWebTrialBodyEstimatesAFewDollars(t *testing.T) {
	e := webEstimate(t, webTrialBody, 0.01)
	if !e.OK || len(e.Refusals) != 0 {
		t.Fatalf("trial refused: %+v", e.Refusals)
	}
	if e.Pool != 12 || e.Splits.Practice+e.Splits.Gate+e.Splits.HeldOut != 12 {
		t.Fatalf("pool %d splits %+v, want 12 tasks", e.Pool, e.Splits)
	}
	if !e.Trial || e.USD.High > 5 || e.USD.Limit != 3 {
		t.Fatalf("trial %v usd %+v", e.Trial, e.USD)
	}
	found := false
	for _, w := range e.Warnings {
		found = found || (w.Code == "trial" && w.Message == improve.TrialText)
	}
	if !found {
		t.Fatalf("no trial notice: %+v", e.Warnings)
	}
	big := webEstimate(t, webSmallBody, 0.01)
	if big.OK || big.Pool != 3641 || !strings.Contains(big.Refusals[0].Message, "spending limit") {
		t.Fatalf("small preset: pool %d %+v", big.Pool, big.Refusals)
	}
}
