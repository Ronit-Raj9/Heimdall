package handlers

import (
	"github.com/maximhq/bifrost/bench/tracebulk"
	"github.com/valyala/fasthttp"
)

// Maxim production failures to regression tests (lane Z06, maximtasks.go).
func init() {
	base := APIPrefix + "/integrations/maxim/batches"
	task := base + "/{id}/tasks/{suite}/{task}"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: base, Tag: "integrations", Summary: "The org's Maxim failure imports, newest first, and whether Maxim is connected", Response: MaximBatchList{}},
		RouteDoc{Method: "POST", Path: base, Tag: "integrations", Summary: "Pull failed, errored or low-scored traces from the connected Maxim log repository into the failure inbox and group them into failure clusters", Auth: AuthWrite, Request: MaximBatchCreate{}, Status: fasthttp.StatusCreated, Response: tracebulk.Batch{}},
		RouteDoc{Method: "GET", Path: base + "/{id}", Tag: "integrations", Summary: "One Maxim failure import: counts, clusters, drafted tasks and their readiness", Response: tracebulk.Batch{}},
		RouteDoc{Method: "POST", Path: base + "/{id}/draft", Tag: "integrations", Summary: "Draft one task per failure cluster into a benchmark, with generated checks, pending review", Auth: AuthWrite, Request: MaximDraftRequest{}, Response: MaximDraftResult{}},
		RouteDoc{Method: "POST", Path: task + "/accept", Tag: "integrations", Summary: "Say what should have happened for a drafted task (the right answer or a rubric), accept it and queue its readiness check", Auth: AuthWrite, Request: MaximReviewRequest{}, Response: MaximTaskResult{}},
		RouteDoc{Method: "POST", Path: task + "/reject", Tag: "integrations", Summary: "Reject a drafted task; its folder is removed from the benchmark", Auth: AuthWrite, Request: MaximRejectRequest{}, Response: MaximTaskResult{}},
		RouteDoc{Method: "POST", Path: task + "/readiness", Tag: "integrations", Summary: "Run an accepted task's readiness check again", Auth: AuthWrite, Response: MaximTaskResult{}},
	)
}
