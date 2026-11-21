package handlers

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// API docs of web parity lane L18 (liveinbox_webhook.go) and gap G02's
// hosted-only inbox token and ingest routes (inboxingest.go).
func init() {
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: APIPrefix + "/inbox/webhook", Tag: "inbox",
		Summary:  "How to push failures into the inbox: the ingest URL (hosted: this org's URL), the token variable, whether it is set, a curl example and the sources.yaml snippet; never the token",
		Response: InboxWebhookInfo{}})
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "GET", Path: InboxTokensPath, Tag: "inbox", Summary: "The org's inbox ingest tokens (last 4 characters, created, last used) and its ingest URL; never a token",
			Response: InboxTokenList{}},
		RouteDoc{Method: "POST", Path: InboxTokensPath, Tag: "inbox", Summary: "Create an inbox ingest token (admin); the token is returned once and stored only as its sha256",
			Request: InboxTokenCreateRequest{}, OptionalBody: true, Status: fasthttp.StatusCreated, Response: InboxTokenCreated{}},
		RouteDoc{Method: "DELETE", Path: InboxTokensPath + "/{id}", Tag: "inbox", Summary: "Revoke an inbox ingest token (admin)",
			Status: fasthttp.StatusNoContent},
		RouteDoc{Method: "POST", Path: InboxOrgIngestPath, Tag: "inbox", Auth: AuthInbox,
			Summary: "Push one flagged trace (OTLP, Langfuse or chat JSON) into this org's inbox; the org's ingest token, a 1 MiB cap and a per-org rate limit guard it",
			Status:  fasthttp.StatusCreated, Response: schemas.InboxIngestResult{}},
	)
}
