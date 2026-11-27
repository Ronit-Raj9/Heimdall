package handlers

import "github.com/valyala/fasthttp"

// Data lifecycle routes (lane X16): org deletion and the audit chain are
// hosted platform routes; deletes, retention and delivery retry are org
// routes.
func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: APIPrefix + "/orgs/{org}/delete", Tag: "orgs", Summary: "Delete the organization and all its data (owner, signed in, typed slug); audit entries are kept", Request: DeleteOrgRequest{}, Status: fasthttp.StatusAccepted, Response: OrgDeletionInfo{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/orgs/{org}/audit/export", Tag: "orgs", Summary: "Download the hash-chained audit log, ?format=jsonl (default) or csv (admin); the optional filters keep only matching rows",
			Query: []QueryDoc{
				{Name: "format", Description: "jsonl or csv"},
				{Name: "action", Description: "Only rows with exactly this action"},
				{Name: "actor_type", Description: "Only rows by this kind of actor: user, token, runner, system or anonymous"},
				{Name: "q", Description: "Only rows whose action, actor id, target type or target id contains this text (any case)"},
			}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/orgs/{org}/audit/verify", Tag: "orgs", Summary: "Check every link of the audit log's hash chain (admin)", Response: AuditVerifyReport{}},
	)
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/experiments/{id}", Tag: "experiments", Summary: "Delete a comparison with its runs, traces and gateway logs (admin; 409 while running)", Response: DeletedInfo{}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/evaluations/{id}", Tag: "evaluations", Summary: "Delete a finished evaluation and its comparisons (admin)", Response: DeletedInfo{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/data/retention", Tag: "data", Summary: "How long comparisons are kept before automatic deletion (0: forever)", Response: RetentionInfo{},
			Query: []QueryDoc{{Name: "preview_days", Description: "1 to 3650 (admin): preview counts the comparisons and runs a retention of this many days would delete; read only"}}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/data/retention", Tag: "data", Summary: "Set the retention in days, 0 to keep forever (admin)", Request: RetentionRequest{}, Response: RetentionInfo{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/integrations/deliveries/{id}/retry", Tag: "integrations", Summary: "Queue a failed webhook delivery again (member)", Status: fasthttp.StatusAccepted, Response: WebhookDeliveryRetried{}},
	)
}
