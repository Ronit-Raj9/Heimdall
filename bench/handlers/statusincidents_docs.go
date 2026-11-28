package handlers

import "github.com/maximhq/bifrost/bench/statuspage"

// Lane L10: incident notes written from the website by server admins.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: StatusIncidentsPath, Tag: "status", Request: OpenIncidentBody{},
			Summary: "Open an incident on the public status page (server admins)", Response: statuspage.Incident{}},
		RouteDoc{Method: "POST", Path: StatusIncidentsPath + "/{id}", Tag: "status", Request: UpdateIncidentBody{},
			Summary: "Add a note to an incident, change its status or impact, or resolve it (server admins)", Response: statuspage.Incident{}},
	)
}
