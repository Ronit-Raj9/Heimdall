package handlers

import "github.com/valyala/fasthttp"

func init() {
	id := LiveMonitorsPath + "/{id}"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: LiveMonitorsPath, Tag: "live-monitors",
			Summary: "Live monitors of deployed agents: status, open incidents, pass rate, latency, cost and errors over the last 24 hours and 7 days", Response: MonitorList{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: LiveMonitorsPath, Tag: "live-monitors",
			Summary: "Create a live monitor: a few real tasks of a ready benchmark run against the agent on a timetable (default 3 tasks hourly, at least every 15 minutes), under a daily budget",
			Request: MonitorInput{}, Status: fasthttp.StatusCreated, Response: MonitorDetail{}},
		RouteDoc{Method: "GET", Path: id, Tag: "live-monitors",
			Summary: "One live monitor: its checks (oldest first), incidents (newest first) and the tasks each check runs", Response: MonitorDetail{}},
		RouteDoc{Auth: AuthWrite, Method: "PUT", Path: id, Tag: "live-monitors",
			Summary: "Change a live monitor's name, version, model, tasks, timetable, budget or incident rule (agent and benchmark are fixed)",
			Request: MonitorInput{}, Response: MonitorDetail{}},
		RouteDoc{Auth: AuthWrite, Method: "DELETE", Path: id, Tag: "live-monitors",
			Summary: "Delete a live monitor with its checks and incidents; the evaluations and runs it started stay", Status: fasthttp.StatusNoContent},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: id + "/pause", Tag: "live-monitors",
			Summary: "Pause a live monitor: no check fires until it is resumed", Response: MonitorDetail{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: id + "/resume", Tag: "live-monitors",
			Summary: "Resume a paused live monitor", Response: MonitorDetail{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: id + "/run", Tag: "live-monitors",
			Summary: "Queue one check of a live monitor now, as a schedule.fire job (its daily budget still applies)", Status: fasthttp.StatusAccepted, Response: ScheduleRunResponse{}},
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: id + "/badge", Tag: "live-monitors",
			Summary: "Turn a live monitor's public read-only status badge on (a new link each time, the old one stops working) or off",
			Request: MonitorBadgeRequest{}, Response: MonitorDetail{}},
		RouteDoc{Method: "GET", Path: LiveMonitorBadgePath, Tag: "live-monitors",
			Summary: "Public status badge of a live monitor (anyone with the link): its state and pass rate over the last 24 hours", Raw: "image/svg+xml"},
	)
}
