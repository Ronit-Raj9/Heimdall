package handlers

// API docs of web parity lane L18 (liveinbox_webhook.go).
func init() {
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: APIPrefix + "/inbox/webhook", Tag: "inbox",
		Summary:  "How to push failures into the inbox: the ingest URL, the token variable, whether it is set, a curl example and the sources.yaml snippet; never the token",
		Response: InboxWebhookInfo{}})
}
