package handlers

func init() {
	q := []QueryDoc{{Name: "name", Description: "agent folder name under agents/; defaults to the zip's single top folder"}}
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/uploads/runs", Tag: "uploads",
			Summary: "Import a zip of sealed experiment folders; every seal and seals.log chain is verified before anything is kept",
			Auth:    AuthWrite, RawRequest: "application/zip", Response: UploadRunsReport{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/uploads/project", Tag: "uploads",
			Summary: "Unpack a project zip (agents/, benchmarks/, experiments/, policies/) through the project file store; zip-slip refused",
			Auth:    AuthWrite, RawRequest: "application/zip", Response: UploadFilesReport{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/uploads/agent", Tag: "uploads",
			Summary: "Unpack an agent folder zip into agents/<name>/ through the project file store; zip-slip refused",
			Auth:    AuthWrite, Query: q, RawRequest: "application/zip", Response: UploadFilesReport{}},
	)
}
