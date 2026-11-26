package handlers

// W4-44 task categories: the kind of work of each task of a benchmark.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/categories", Tag: "suites",
			Summary:  "Kind of work of each task of a benchmark (bug fix, refactor, ...); a person's label wins over a rule's",
			Response: TaskCategoriesView{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/categories", Tag: "suites",
			Summary: "Label every task without a kind of work by the rules; a task the rules cannot label stays without one",
			Auth:    AuthWrite, Response: TaskCategoriesView{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/suites/{id}/tasks/{task}/category", Tag: "suites",
			Summary: "Set the kind of work of one task by hand; rules never replace it",
			Auth:    AuthWrite, Request: TaskCategoryRequest{}, Response: TaskCategoriesView{}},
	)
}
