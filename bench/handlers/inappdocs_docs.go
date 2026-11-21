package handlers

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/docs", Tag: "docs", Summary: "In-app guides embedded in the server (name and title)", Response: DocList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/docs/{name}", Tag: "docs", Summary: "One in-app guide as markdown", Response: DocBody{}},
	)
}
