package handlers

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/docs", Tag: "docs", Summary: "In-app guides embedded in the server (name, title, category, start_rank) and the categories in display order; q searches titles, names and bodies", Response: DocList{},
			Query: []QueryDoc{{Name: "q", Description: "Search text, 2 to 80 characters: lists only the guides whose title, name or body contains it (title matches first), each with a snippet"}}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/docs/{name}", Tag: "docs", Summary: "One in-app guide as markdown", Response: DocBody{}},
	)
}
