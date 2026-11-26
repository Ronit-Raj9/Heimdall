package handlers

import "github.com/maximhq/bifrost/bench/improve"

// Wave 4 lane I43 (D5 A4.3): route docs of the improvement version explain.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/improvements/{id}/versions/{n}/explain", Tag: "improvements",
			Summary:  "What changed in one version of an improvement run and why: file diffs, edits with reasons and results, gates and the decision; 501 when the server cannot explain versions",
			Response: improve.Explain{}},
	)
}
