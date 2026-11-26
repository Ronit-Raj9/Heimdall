package handlers

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tracedraft"
)

// Wave 4 lane W4-45 (trace to environment): route docs for the session file
// upload and the model's draft of an inbox item.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: InboxImportSessionPath, Tag: "inbox",
			Summary: "Upload one Claude Code or Codex session file (the raw file as the body, its name in X-File-Name, at most 32 MB); it is cleaned of keys, emails and home folder names and kept as an inbox item. 413 over 32 MB, 422 for any other file",
			Auth:    AuthWrite, RawRequest: "application/octet-stream", Response: schemas.InboxIngestResult{}},
		RouteDoc{Method: "GET", Path: InboxProposalPath, Tag: "inbox",
			Summary:  "The model's draft of what should have happened for an item (written by the trace draft job); 404 until there is one",
			Response: tracedraft.Proposal{}},
	)
}
