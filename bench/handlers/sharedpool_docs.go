package handlers

import (
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/valyala/fasthttp"
)

// Shared pool and guardrail routes are hosted-only platform routes.
func init() {
	const tag = "sharedpool"
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "GET", Path: APIPrefix + "/pool", Tag: tag, Summary: "The organization's limits, the kill switch and the shared runner pool status", Response: PoolStatus{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/images", Tag: tag, Summary: "The organization's custom Docker image requests", Response: ImageRequestList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/images", Tag: tag, Summary: "Request a custom Docker image (digest-pinned, within the size limit); a server admin approves it", Request: ImageRequestBody{}, Status: fasthttp.StatusCreated, Response: sharedpool.ImageRequest{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/admin/images", Tag: tag, Summary: "Every image request, newest first (server admin)", Response: ImageRequestList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/admin/images/{id}/approve", Tag: tag, Summary: "Approve an image request (server admin)", Request: ImageDecisionBody{}, OptionalBody: true, Response: sharedpool.ImageRequest{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/admin/images/{id}/reject", Tag: tag, Summary: "Reject an image request (server admin)", Request: ImageDecisionBody{}, OptionalBody: true, Response: sharedpool.ImageRequest{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/admin/quotas/{id}", Tag: tag, Summary: "One organization's limits (server admin)", Response: OrgQuota{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/admin/quotas/{id}", Tag: tag, Summary: "Set one organization's limits: runs, spend, image size, custom images, shared pool, runtime runc or runsc (server admin)", Request: OrgQuota{}.Limits, Response: OrgQuota{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/admin/pool/assignments", Tag: tag, Summary: "The shared runner's assignments (Authorization: Bearer HEIMDALL_SHARED_RUNNER_SECRET)", Auth: "shared runner secret", Response: sharedpool.Assignments{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/admin/pool/assignments/{org}/token", Tag: tag, Summary: "Claim one assigned organization's shared runner token, shown once; revokes its earlier token (Authorization: Bearer HEIMDALL_SHARED_RUNNER_SECRET)", Auth: "shared runner secret", Response: sharedpool.ClaimedToken{}},
	)
}
