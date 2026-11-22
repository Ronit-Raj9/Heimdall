package handlers

// Fix wave 2 lane G18 route docs: the people a labelling queue can name.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/annotation/people", Tag: "annotation", Summary: "Members who can be named as labellers: user id and display name",
			Response: AnnotationPeople{}},
	)
}
