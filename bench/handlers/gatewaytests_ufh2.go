package handlers

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/gwfeature"
)

// Feature test minimums on the start form (lane H2): GET /gateway/features
// carries each feature's need, and POST /gateway/tests/draft refuses a size
// that can never reach it and notes one that likely will not.

// callsPerRun is this agent's measured model calls per run, 0 without history.
func (h *gatewayTestsHandler) callsPerRun(base string) float64 {
	if base == "" || h.source == nil {
		return 0
	}
	return gwfeature.CallsPerRun(h.source(), base)
}

// needCheck is the need of feature f against the request's size; nil when the
// number of tasks is not known here.
func (h *gatewayTestsHandler) needCheck(feature string, req gwfeature.DraftRequest, tasks int) *gwfeature.NeedCheck {
	if tasks <= 0 {
		return nil
	}
	c := gwfeature.NeedOf(feature, h.callsPerRun(strings.TrimSpace(req.Base))).Check(tasks, req.Trials, strings.TrimSpace(req.Options["poison_probe"]) != "")
	return &c
}

// imageWait is the wait for the test's task images before its first run;
// nil when its benchmark brings no own task images.
func (h *gatewayTestsHandler) imageWait(ctx context.Context, req gwfeature.DraftRequest, taskCount int) *ImageWait {
	src := h.env.Services.Suites
	if src == nil {
		return nil
	}
	id, _, _ := strings.Cut(strings.TrimSpace(req.Suite), "@")
	st, err := src.LoadSuite(ctx, id)
	if err != nil || st == nil {
		return nil
	}
	tasks := req.Tasks
	if len(tasks) == 0 {
		tasks = readyTaskIDs(st)
	}
	if taskCount > 0 && taskCount < len(tasks) {
		tasks = tasks[:taskCount]
	}
	return imageWait(ownImageTasks(st, tasks))
}
