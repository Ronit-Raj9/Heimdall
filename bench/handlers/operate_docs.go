package handlers

func init() {
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: OperateStatusPath, Tag: "operate",
		Summary:  "What the Operate page can run on this server: operate job kinds, evidence mirror, GPU mode, driver",
		Response: OperateStatus{}})
}
