package handlers

import "github.com/maximhq/bifrost/bench/investigator"

// Wave 4 lane W4-42: findings investigations, the reports of the job
// findings.investigate.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/investigations", Tag: "findings",
			Summary:  "The investigations of failed runs, newest first, at most 50; exp keeps those of one evaluation; running lists the investigation jobs still queued or running (absent when none)",
			Query:    []QueryDoc{{Name: "exp", Description: "Only the investigations of this evaluation"}},
			Response: InvestigationList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/investigations/{id}", Tag: "findings",
			Summary:  "One investigation: the likely causes a model wrote, each with quotes checked against the run's recorded steps",
			Response: investigator.Report{}},
	)
}
