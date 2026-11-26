package handlers

import "github.com/valyala/fasthttp"

// API docs of W4-49: the user feedback route (hosted only, feedback_w449.go).
func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: FeedbackPath, Tag: "inbox", Auth: AuthInbox,
			Summary: "Send one user rating (thumbs down or up, or a score from 0 to 10) for a session; with a trace, or a session a Bifrost source can find, it lands in this org's inbox. The org's ingest token, a 64 KiB cap and a per-org rate limit guard it",
			Request: FeedbackRequest{}, Status: fasthttp.StatusCreated, Response: FeedbackResult{}},
	)
}
