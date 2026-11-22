package handlers

import (
	"github.com/valyala/fasthttp"
)

// Y11 pull-mode agents (pullagent.go, docs/pull-agents.md): served by the
// local server and as hosted platform routes, so described in both tables.
func init() {
	APIRoutes = append(APIRoutes, pullRouteDocs...)
	OrgRouteDocs = append(OrgRouteDocs, pullRouteDocs...)
}

var pullRouteDocs = []RouteDoc{
	{Method: "POST", Path: PullNextPath, Tag: "pull", Summary: "Take the next turn of a pull agent (heimdall.serve): waits up to wait_s (at most 25) for one, 204 when none came; every call counts as the agent's process being connected",
		Auth: AuthWrite, Request: PullNextRequest{}, Query: []QueryDoc{{Name: "wait", Description: "seconds to wait, 0 to 25"}}, Response: PullTurnView{}, NoContent: true},
	{Method: "POST", Path: PullAnswerPath, Tag: "pull", Summary: "Answer a turn this organization's process took: the answer object (text, tool_calls, patch, usage) or an error",
		Auth: AuthWrite, Request: PullAnswerRequest{}, Status: fasthttp.StatusNoContent},
	{Method: "GET", Path: PullAgentsPath, Tag: "pull", Summary: "The organization's pull agents: connected or not, last poll, processes, queued and in-flight turns", Response: PullAgentsResponse{}},
	{Method: "POST", Path: PullRunTurnsPath, Tag: "pull", Summary: "A run's launcher queues a turn for its pull agent (the run's signed pull token); 409 when no process serves the agent",
		Request: PullRunTurnRequest{}, Status: fasthttp.StatusCreated, Response: PullRunTurnCreated{}},
	{Method: "GET", Path: PullRunTurnPath, Tag: "pull", Summary: "A run's launcher waits for a turn's answer (the run's pull token): 200 answered or failed, 202 still pending",
		Query: []QueryDoc{{Name: "wait", Description: "seconds to wait, 0 to 25"}}, Response: PullRunTurnResultDoc{}},
	{Method: "POST", Path: PullRunReposPath, Tag: "pull", Summary: "A run's launcher stores its workspace tarball (gzip, at most 48 MiB) and gets a signed link that expires with the run",
		RawRequest: "application/gzip", Status: fasthttp.StatusCreated, Response: PullRepoCreated{}},
	{Method: "GET", Path: PullRepoPath, Tag: "pull", Summary: "Download a run's workspace tarball through its signed, expiring link (exp and sig)",
		Query: []QueryDoc{{Name: "exp", Description: "expiry, unix seconds"}, {Name: "sig", Description: "the link's signature"}}, Raw: "application/gzip"},
}

// PullRunTurnResultDoc documents the answer of GET /pull/run/turns/{id}
// (pullagent.Result).
type PullRunTurnResultDoc struct {
	Status string         `json:"status"` // pending | leased | answered | failed
	Answer map[string]any `json:"answer,omitempty"`
	Error  string         `json:"error,omitempty"`
}
