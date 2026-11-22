package handlers

import (
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/valyala/fasthttp"
)

// Issues to benchmark (lane G06).
func init() {
	p := issueBenchPrefix
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: p, Tag: "benchmarks", Summary: "Issue runs of this org, newest first, without their logs", Response: IssueRunList{}},
		RouteDoc{Method: "POST", Path: p, Tag: "benchmarks", Summary: "Scan a connected GitHub repository for merged pull requests that close an issue and save the result as a new run (free, no model calls): candidates and the selection funnel",
			Auth: AuthWrite, Request: IssueRunCreate{}, Status: fasthttp.StatusCreated, Response: pipeline.State{}},
		RouteDoc{Method: "GET", Path: p + "/{id}", Tag: "benchmarks", Summary: "One run: per-issue status (built, validated, ready, failed with a plain reason and a fix), the environments built and the live log",
			Response: pipeline.State{}, Query: []QueryDoc{{Name: "since", Description: "only log lines after this sequence number"}}},
		RouteDoc{Method: "POST", Path: p + "/{id}/start", Tag: "benchmarks", Summary: "Build and validate every issue of the run in the background; needs consent: this sends your code to your model provider with your key",
			Auth: AuthWrite, Request: IssueRunStart{}, Status: fasthttp.StatusAccepted, Response: pipeline.State{}},
		RouteDoc{Method: "POST", Path: p + "/{id}/cancel", Tag: "benchmarks", Summary: "Stop a running run; start resumes it where it stopped",
			Auth: AuthWrite, Response: pipeline.State{}},
		RouteDoc{Method: "POST", Path: p + "/{id}/issues/{issue}/review", Tag: "benchmarks", Summary: "Accept, reject or edit the instruction of one ready issue; accepted issues join the org's private benchmark for the repository",
			Auth: AuthWrite, Request: pipeline.Review{}, Response: pipeline.State{}},
		RouteDoc{Method: "POST", Path: p + "/{id}/issues/{issue}/retry", Tag: "benchmarks", Summary: "Retry one failed issue with a fix: add_secret, allow_service, raise_budget or retry",
			Auth: AuthWrite, Request: IssueRetry{}, Status: fasthttp.StatusAccepted, Response: pipeline.State{}},
	)
}
