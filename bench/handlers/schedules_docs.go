package handlers

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/schedules", Tag: "schedules", Summary: "Web schedules (policies/schedules.yaml) with scheduled canaries, next fire, last trigger, spend today and the alert events and org secrets alerts can name", Response: SchedulesResponse{}},
		RouteDoc{Auth: AuthWrite, Method: "PUT", Path: APIPrefix + "/schedules", Tag: "schedules", Summary: "Replace the web schedules file (JSON file or YAML text); validated against the project, saved through project files", Request: SchedulesPut{}, Response: SchedulesResponse{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: APIPrefix + "/schedules/test-alert", Tag: "schedules", Summary: "Send a test webhook alert to a receiver (url_env names an org secret or, locally, an environment variable)", Request: TestAlertRequest{}, Response: TestAlertResponse{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: APIPrefix + "/schedules/{name}/run", Tag: "schedules", Summary: "Queue one schedule now as a job (schedule.fire, or canary.run on a hosted server)", Status: 202, Response: ScheduleRunResponse{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: APIPrefix + "/canaries", Tag: "canaries", Summary: "Create a drift canary definition (canaries/<name>.yaml); force overwrites", Request: CanaryInitRequest{}, Status: 201, Response: CanaryInitResponse{}},
		RouteDoc{Auth: AuthWrite, Method: "DELETE", Path: APIPrefix + "/canaries/{name}", Tag: "canaries", Summary: "Delete a drift canary definition (its looks and baselines stay)", Status: 204},
	)
}
