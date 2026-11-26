package handlers

import "github.com/maximhq/bifrost/bench/improve"

// W4 design 5 (A2.9): the pre-run estimate of an improvement run.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/improvements/estimate", Tag: "improvements",
			Summary: "Estimate an improvement run before creating it: task splits, runs, cost, time, the smallest gains it can confirm, and why it would be refused; changes nothing",
			Request: improve.Spec{}, Response: improve.Estimate{}},
	)
}
