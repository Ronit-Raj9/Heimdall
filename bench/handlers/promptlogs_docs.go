package handlers

import (
	"github.com/maximhq/bifrost/bench/promptlogs"
	"github.com/maximhq/bifrost/bench/schemas"
)

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: PromptLoggingPath, Tag: "prompt-logs",
			Summary: "The organization's gateway prompt logging setting", Response: PromptLogging{}},
		RouteDoc{Method: "PUT", Path: PromptLoggingPath, Tag: "prompt-logs",
			Summary: "Keep or drop prompt text in the gateway log, and how many days rows are kept (admin)",
			Auth:    AuthWrite, Request: schemas.PromptLogPolicy{}, Response: PromptLogging{}},
		RouteDoc{Method: "GET", Path: PromptLogsPath, Tag: "prompt-logs",
			Summary: "The organization's gateway log rows, newest first (metadata only, never prompt text)",
			Query: []QueryDoc{{Name: "limit", Description: "rows per page, 1 to 200 (default 50)"},
				{Name: "offset", Description: "rows to skip"}},
			Response: promptlogs.Page{}},
		RouteDoc{Method: "DELETE", Path: PromptLogsPath, Tag: "prompt-logs",
			Summary: "Delete the organization's gateway log rows (admin); only this organization's rows are touched",
			Auth:    AuthWrite, Query: []QueryDoc{{Name: "before", Description: "RFC 3339 time: delete only rows older than it"}},
			Response: PromptLogsDeleted{}},
	)
}
