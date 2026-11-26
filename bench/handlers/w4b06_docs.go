package handlers

import "github.com/maximhq/bifrost/bench/schemas"

// Wave 4 lane I10 (N194): one suite's summary, so a suite page does not load
// the whole list.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}", Tag: "suites",
			Summary:  "One suite with its tasks, the same entry GET /suites lists; 404 when the suite is not listed",
			Response: schemas.SuiteSummary{}},
	)
}
