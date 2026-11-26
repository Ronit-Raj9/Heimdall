package handlers

import "github.com/maximhq/bifrost/bench/issuesync"

// Wave 4 lane I35 (W4-48): weekly issue sync routes.
func init() {
	const tag = "issuebench"
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "GET", Path: APIPrefix + "/issuesync", Tag: tag, Summary: "Repositories with a weekly issue sync, each with its last runs", Response: IssueSyncList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/issuesync", Tag: tag, Auth: AuthWrite, Summary: "Turn on a weekly issue sync for a repository (admin); 422 names the field to fix", Request: IssueSyncRequest{}, Response: issuesync.Repo{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/issuesync/{id}", Tag: tag, Auth: AuthWrite, Summary: "Change or pause a weekly issue sync (admin)", Request: IssueSyncRequest{}, Response: issuesync.Repo{}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/issuesync/{id}", Tag: tag, Auth: AuthWrite, Summary: "Remove a weekly issue sync and its schedule (admin); tasks already made stay", Status: 204, NoContent: true},
		RouteDoc{Method: "POST", Path: APIPrefix + "/issuesync/{id}/run", Tag: tag, Auth: AuthWrite, Summary: "Run a weekly issue sync once now (admin); 409 while one runs", Request: IssueSyncRunRequest{}, Response: issuesync.RunRef{}},
	)
}
