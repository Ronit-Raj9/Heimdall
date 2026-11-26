package handlers

import "github.com/valyala/fasthttp"

// API docs of W4-49: the paged step routes (runstepspage_w449.go), the
// validity ledger (validity_w449.go) and the user feedback route (hosted
// only, feedback_w449.go).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/runs/{id}/steps/page", Tag: "runs",
			Summary: "One page of a run's step timeline: rows after after_seq, at most limit (1 to 200, default 50), filtered by kind, agent_id and parent_seq; next_after_seq is set when more rows match",
			Query: []QueryDoc{{Name: "after_seq", Description: "return rows with a higher seq (default 0)"}, {Name: "limit", Description: "rows per page, 1 to 200 (default 50)"},
				{Name: "kind", Description: "only rows of this kind"}, {Name: "agent_id", Description: "only rows of this agent"}, {Name: "parent_seq", Description: "only rows nested under this seq"}},
			Response: RunStepsPage{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/runs/{id}/step/{seq}", Tag: "runs",
			Summary:  "One step of a run with the steps that witnessed it, the step it witnesses and its child steps (at most 50); 404 when the run has no such step",
			Response: RunStep{}},
	)
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: FeedbackPath, Tag: "inbox", Auth: AuthInbox,
			Summary: "Send one user rating (thumbs down or up, or a score from 0 to 10) for a session; with a trace, or a session a Bifrost source can find, it lands in this org's inbox. The org's ingest token, a 64 KiB cap and a per-org rate limit guard it",
			Request: FeedbackRequest{}, Status: fasthttp.StatusCreated, Response: FeedbackResult{}},
	)
}
