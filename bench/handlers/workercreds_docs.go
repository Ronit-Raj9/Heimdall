package handlers

import "github.com/maximhq/bifrost/bench/schemas"

func init() {
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: LeaseCredentialsPath, Tag: "workers",
		Summary: "Provider keys of the lease's organization, for the runner holding the lease",
		Auth:    AuthWorker, Response: schemas.LeaseCredentials{}})
}
