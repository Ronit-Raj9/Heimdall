package handlers

import "github.com/maximhq/bifrost/bench/issuebench/scan"

// Issues to benchmark (lane G01): hosted platform routes.
func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: IssueBenchPrefix + "/scan", Tag: "issuebench", Summary: "Dry run: find merged pull requests in a GitHub repository that close one issue and would become benchmark tasks, with a funnel of why the others were dropped (GitHub calls only, no model calls, free)", Request: IssueScanRequest{}, Response: scan.Result{}},
	)
}
