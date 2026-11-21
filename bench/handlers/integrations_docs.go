package handlers

import (
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

// Route docs of the integrations area (integrations.go, web parity L19).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/integrations/webhooks", Tag: "integrations", Summary: "Outgoing webhooks of the org (secrets reduced to their last four characters), the events they can subscribe to and each one's last delivery", Response: WebhookList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/integrations/webhooks", Tag: "integrations", Summary: "Add a webhook (admin): url, events (default experiment.finished), description. The signing secret is returned once; hosted servers deliver only to public https URLs",
			Auth: AuthWrite, Request: webhooks.Input{}, Status: fasthttp.StatusCreated, Response: WebhookCreated{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/integrations/webhooks/{id}", Tag: "integrations", Summary: "Change a webhook's url, events, description or enabled flag (admin); omitted fields are kept",
			Auth: AuthWrite, Request: webhooks.Input{}, Response: webhooks.View{}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/integrations/webhooks/{id}", Tag: "integrations", Summary: "Delete a webhook (admin)", Auth: AuthWrite, Status: fasthttp.StatusNoContent},
		RouteDoc{Method: "POST", Path: APIPrefix + "/integrations/webhooks/{id}/test", Tag: "integrations", Summary: "Send a signed ping event to the webhook now (one attempt) and return the delivery (member)",
			Auth: AuthWrite, Response: webhooks.Delivery{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/integrations/deliveries", Tag: "integrations", Summary: "Recent webhook deliveries, newest first (the last 200 are kept)",
			Query: []QueryDoc{{Name: "limit", Description: "1..200, default 50"}}, Response: WebhookDeliveries{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/integrations/exports", Tag: "integrations", Summary: "Export files written by integrations.export jobs (OTLP traces, EEE records, run tables, Harbor tars), newest first", Response: IntegrationExportList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/integrations/exports/{name}", Tag: "integrations", Summary: "Download one export file", Raw: "application/octet-stream"},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/integrations/exports/{name}", Tag: "integrations", Summary: "Delete one export file (member)", Auth: AuthWrite, Status: fasthttp.StatusNoContent},
		RouteDoc{Method: "GET", Path: APIPrefix + "/integrations/ci/workflow", Tag: "integrations", Summary: "Preview the CI workflow: the GitHub Actions file the CI install pull request adds, or (mode hosted) the GitHub Actions or GitLab CI file that evaluates every pull request on this Heimdall; no secret is ever written in it",
			Query:    []QueryDoc{{Name: "mode", Description: "recorded (default), live, or hosted (evaluate each pull request on this Heimdall with an org API token)"}, {Name: "agent", Description: "live: agent id both arms start from; hosted: the agent to test"}, {Name: "source_ref", Description: "Heimdall ref to build, default main"},
				{Name: "provider", Description: "hosted: github (default) or gitlab"}, {Name: "source", Description: "hosted: config (default), image or endpoint"}, {Name: "config", Description: "hosted: agent config path in the repository, default agents/<agent>.yaml"},
				{Name: "url", Description: "hosted: this Heimdall's address; empty reads the HEIMDALL_URL CI variable"}, {Name: "pack", Description: "hosted: benchmark pack"}, {Name: "benchmarks", Description: "hosted: comma-separated benchmark ids"},
				{Name: "faults", Description: "hosted: comma-separated faults"}, {Name: "fail_on", Description: "hosted: outcomes that fail, default regressed,failed"}},
			Response: CIWorkflowPreview{}},
	)
}
