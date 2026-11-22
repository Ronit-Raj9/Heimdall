package handlers

import "github.com/valyala/fasthttp"

// Agent delete (lane F13): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/agents/{id}", Tag: "agents", Summary: "Delete an agent's settings file; its versions and runs are kept",
			Auth: AuthWrite, Status: fasthttp.StatusNoContent},
	)
}
