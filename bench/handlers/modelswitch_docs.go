package handlers

import "github.com/maximhq/bifrost/bench/modelswitch"

// Model switch area OpenAPI (lane Z21, modelswitch.go).
func init() {
	routing := []QueryDoc{
		{Name: "scope", Description: "Rule scope: global (default), team, customer or virtual_key"},
		{Name: "scope_id", Description: "Id of the team, customer or virtual key; required for a non-global scope"},
		{Name: "prefix", Description: "Rule name prefix (default heimdall-<evaluation id>)"},
		{Name: "header_prefix", Description: "Request header prefix segment rules match (default x-heimdall-)"},
		{Name: "budget_saver_at", Description: "budget_used percent at which the budget saver rule routes to the cheapest model (default 80)"},
		{Name: "base_priority", Description: "Priority of the first rule (default 100)"},
		{Name: "alpha", Description: "Significance level of every comparison (default 0.05)"},
	}
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/model-switch", Tag: "evaluations",
			Summary:  "Model switch report: quality, cost and latency per model with ranges, Pareto frontiers, each model against the best (sign-flip, Holm), per benchmark and per segment, and a plain recommendation",
			Response: modelswitch.Report{}, Query: []QueryDoc{{Name: "alpha", Description: "Significance level (default 0.05)"}}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/model-switch/bifrost-config", Tag: "evaluations",
			Summary:  "Bifrost routing config from the model switch report: segment rules, a weighted default rule, fallbacks by measured quality and availability, a budget saver rule, with why for each; download=1 returns the config.json fragment as a file",
			Response: modelswitch.Export{}, Query: append(routing, QueryDoc{Name: "download", Description: "1 returns the Bifrost config.json fragment alone, as an attachment"})},
		RouteDoc{Method: "POST", Path: APIPrefix + "/evaluations/{id}/model-switch/apply", Tag: "evaluations",
			Summary: "Write the routing rules to the org's own Bifrost through its routing rules API: created or updated by name, placed after the scope's existing rules, disabled unless enable is set, never deleting; dry_run lists the changes",
			Request: ModelSwitchApplyRequest{}, Response: modelswitch.ApplyResult{}},
	)
}
