package handlers

import "github.com/maximhq/bifrost/bench/schemas"

// Fix wave 2 lane G14 (N11): a server admin removes an offline worker's
// record from an organization.
func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: APIPrefix + "/admin/containers/{org}/{id}/remove", Tag: "admin",
			Summary:  "Remove an offline worker's record from an organization (server admin); 409 while the worker is online",
			Response: schemas.RunnerView{}},
	)
}
