package handlers

import "github.com/maximhq/bifrost/bench/schemas"

// Guided inbox accept (lane Y14): route docs. The single accept route keeps
// its entry in openapi.go; its request now carries the guide.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/inbox/accept", Tag: "inbox",
			Summary: "Accept several items into one benchmark with one shared \"what should have happened\" guide (for example one reply rubric, or the recorded sessions were right for goldens); each item is accepted or listed with why not",
			Auth:    AuthWrite, Request: schemas.InboxBulkAcceptRequest{}, Response: schemas.InboxBulkAcceptResult{}},
	)
}
