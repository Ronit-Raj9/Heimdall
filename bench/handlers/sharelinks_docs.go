package handlers

import "github.com/maximhq/bifrost/bench/sharelinks"

// Share links, exports and reproduce bundles (lane X14) route docs.
func init() {
	formats := []QueryDoc{{Name: "format", Description: "html (default, standalone page with a print stylesheet for Save as PDF), md or json"}, {Name: "inline", Description: "1 shows the document instead of downloading it"}}
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/shares", Tag: "sharing",
			Summary: "Create a read-only share link to a report card, evaluation, verdict, run trace or leaderboard view (expires_in_days 0 means never and is admin only, a member asking for it gets 403; default 7). On the hosted server the token is opaque (s_...) and names no organization",
			Auth:    AuthWrite, Request: sharelinks.NewShare{}, Status: 201, Response: ShareView{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/shares", Tag: "sharing",
			Summary: "List this organization's share links with their state. Without kind or target (every link of the organization) only an admin may list; a filtered list answers anyone who may read the item. A viewer never gets a link's token, path or url, only its metadata", Response: ShareList{},
			Query: []QueryDoc{{Name: "kind", Description: "Only links of this kind"}, {Name: "target", Description: "Only links to this item"}}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/shares/{id}/revoke", Tag: "sharing",
			Summary: "Turn a share link off at once", Auth: AuthWrite, Response: ShareView{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/shared/{token}", Tag: "sharing",
			Summary: "Open a share link: the item, with secrets, file paths and people removed (and prompts, when the link hides them). An old token that names the organization still opens and answers canonical_token, the link's new token. 410 when expired or revoked", Response: SharedItem{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/shared/{token}/badge.svg", Tag: "sharing",
			Summary: "A badge with the shared report card's score or the verdict's action, embeddable anywhere", Raw: "image/svg+xml"},
		RouteDoc{Method: "GET", Path: APIPrefix + "/shared/{token}/export", Tag: "sharing",
			Summary: "The shared evaluation's report card as a standalone document", Raw: "text/html", AlsoRaw: "text/markdown",
			Query: formats[:1]},
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/report/export", Tag: "evaluations",
			Summary: "The report card as a standalone document: HTML (print to PDF), Markdown or JSON", Raw: "text/html", AlsoRaw: "text/markdown",
			Query: formats},
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/reproduce", Tag: "evaluations",
			Summary: "A zip to reproduce the evaluation: spec, experiment files, agent version configs, benchmark locks and commands", Raw: "application/zip"},
	)
}
