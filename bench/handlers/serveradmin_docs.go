package handlers

import "github.com/valyala/fasthttp"

// Server admin routes (hosted only, web parity L23).
func init() {
	a := APIPrefix + "/admin"
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "GET", Path: a + "/overview", Tag: "admin", Summary: "Server totals and kill switch state (server admin)", Response: AdminOverview{}},
		RouteDoc{Method: "GET", Path: a + "/users", Tag: "admin", Summary: "Every user on the server (server admin)", Response: AdminUserList{}},
		RouteDoc{Method: "POST", Path: a + "/users/{id}/disable", Tag: "admin", Summary: "Disable or re-enable a user (server admin)", Request: AdminUserDisableRequest{}, Response: AdminUserDisableRequest{}},
		RouteDoc{Method: "GET", Path: a + "/orgs", Tag: "admin", Summary: "Every organization with its quota (server admin)", Response: AdminOrgList{}},
		RouteDoc{Method: "GET", Path: a + "/orgs/{id}/quota", Tag: "admin", Summary: "One organization's quota (server admin)", Response: AdminQuota{}},
		RouteDoc{Method: "POST", Path: a + "/orgs/{id}/quota", Tag: "admin", Summary: "Set one organization's quota (server admin)", Request: AdminLimits{}, Response: AdminQuota{}},
		RouteDoc{Method: "GET", Path: a + "/killswitch", Tag: "admin", Summary: "Platform kill switch state (server admin)", Response: AdminKillSwitch{}},
		RouteDoc{Method: "POST", Path: a + "/killswitch", Tag: "admin", Summary: "Turn the kill switch on (no new runs leased) or off (server admin)", Request: AdminKillSwitch{}, Response: AdminKillSwitch{}, Status: fasthttp.StatusOK},
		RouteDoc{Method: "GET", Path: a + "/containers", Tag: "admin", Summary: "Runners across every organization and the run slots they hold (server admin)", Response: AdminContainerList{}},
	)
}
