package handlers

// Route docs of the compliance packs area (compliancepacks.go, gap G08).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/compliance-packs", Tag: "releases", Summary: "Compliance packs written by release.compliance jobs (exports/compliance/compliance-<release_id>.md and .json), newest first", Response: CompliancePackFiles{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/compliance-packs/{name}", Tag: "releases", Summary: "Download one compliance pack file", Raw: "application/octet-stream"},
	)
}
