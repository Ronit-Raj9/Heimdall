package handlers

import "github.com/maximhq/bifrost/bench/schedule"

// W4 feature 3 (D3 P7): the new model watch of an evaluation schedule.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/model-watch/{schedule}", Tag: "gateway",
			Summary:  "The new model watch of a schedule: the models known at the first check and what happened to each new one; 404 when the schedule does not watch for new models",
			Response: schedule.WatchState{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/gateway/model-watch/{schedule}/retry", Tag: "gateway",
			Summary: "Forget one new model that failed or was skipped, so the next check or Run now starts it",
			Request: ModelWatchRetry{}, Response: schedule.WatchState{}},
	)
}
