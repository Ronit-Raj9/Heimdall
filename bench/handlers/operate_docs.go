package handlers

import "github.com/valyala/fasthttp"

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: OperateStatusPath, Tag: "operate",
			Summary:  "What the Operate page can run on this server: operate job kinds, evidence mirror, GPU mode, driver",
			Response: OperateStatus{}},
		RouteDoc{Method: "GET", Path: OperateBackupsPath, Tag: "operate",
			Summary: "Evidence backup zips of this organization (evidence.backup jobs), newest first", Response: OperateBackupList{}},
		RouteDoc{Method: "GET", Path: OperateBackupsPath + "/{name}", Tag: "operate",
			Summary: "Download one evidence backup zip", Raw: "application/zip"},
		RouteDoc{Method: "DELETE", Path: OperateBackupsPath + "/{name}", Tag: "operate",
			Summary: "Delete one evidence backup zip (member)", Auth: AuthWrite, Status: fasthttp.StatusNoContent},
	)
}
