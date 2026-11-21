package handlers

// API docs of web parity lane L18 (liveinbox_webhook.go).
func init() {
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: APIPrefix + "/inbox/webhook", Tag: "inbox",
		Summary:  "How to push failures into the inbox: the ingest URL, the token variable, whether it is set, a curl example and the sources.yaml snippet; never the token",
		Response: InboxWebhookInfo{}})
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: APIPrefix + "/liveinbox/pull", Tag: "inbox",
		Summary:  "Whether a live or inbox pull can run now: the credentials it reads (names only, set or not) and each missing step with its link",
		Response: PullReadiness{}})
}
