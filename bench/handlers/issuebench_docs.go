package handlers

import (
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/issuebench/scan"
	"github.com/valyala/fasthttp"
)

// Issues to benchmark (lane G01): hosted platform routes.
func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: IssueBenchPrefix + "/scan", Tag: "issuebench", Summary: "Dry run: find merged pull requests in a GitHub repository that close one issue and would become benchmark tasks, with a funnel of why the others were dropped (GitHub calls only, no model calls, free)", Request: IssueScanRequest{}, Response: scan.Result{}},
	)
}

// Issues to benchmark (lane G06).
// IssueReadiness is issuebench.Readiness under its own OpenAPI name, so it
// does not take the name Readiness from the trace readiness schema.
type IssueReadiness issuebench.Readiness

func init() {
	p := issueBenchPrefix
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: issueBenchReadinessPath, Tag: "benchmarks", Summary: "Whether issues can be built and tested on this site now: a worker online that takes issuebench.work (always true locally), GitHub connected, a model gateway, a default model, and the first missing piece in plain words",
			Response: IssueReadiness{}},
		RouteDoc{Method: "GET", Path: p, Tag: "benchmarks", Summary: "Issue runs of this org, newest first, without their logs", Response: IssueRunList{}},
		RouteDoc{Method: "POST", Path: p, Tag: "benchmarks", Summary: "Scan a connected GitHub repository for merged pull requests that close an issue and save the result as a new run (free, no model calls): candidates and the selection funnel",
			Auth: AuthWrite, Request: IssueRunCreate{}, Status: fasthttp.StatusCreated, Response: pipeline.State{}},
		RouteDoc{Method: "GET", Path: p + "/{id}", Tag: "benchmarks", Summary: "One run: per-issue status (built, validated, ready, failed with a plain reason and a fix), the environments built and the live log",
			Response: pipeline.State{}, Query: []QueryDoc{{Name: "since", Description: "only log lines after this sequence number"}}},
		RouteDoc{Method: "POST", Path: p + "/{id}/start", Tag: "benchmarks", Summary: "Build and validate every issue of the run in the background; needs consent: this sends your code to your model provider with your key. On the hosted site with no worker online it answers 409 with a message starting No worker online",
			Auth: AuthWrite, Request: IssueRunStart{}, Status: fasthttp.StatusAccepted, Response: pipeline.State{}},
		RouteDoc{Method: "POST", Path: p + "/{id}/cancel", Tag: "benchmarks", Summary: "Stop a running run; start resumes it where it stopped",
			Auth: AuthWrite, Response: pipeline.State{}},
		RouteDoc{Method: "POST", Path: p + "/{id}/issues/{issue}/review", Tag: "benchmarks", Summary: "Accept, reject or edit the instruction of one ready issue; accepted issues join the org's private benchmark for the repository",
			Auth: AuthWrite, Request: pipeline.Review{}, Response: pipeline.State{}},
		RouteDoc{Method: "POST", Path: p + "/{id}/issues/{issue}/retry", Tag: "benchmarks", Summary: "Retry one failed issue with a fix: add_secret, allow_service, raise_budget or retry",
			Auth: AuthWrite, Request: IssueRetry{}, Status: fasthttp.StatusAccepted, Response: pipeline.State{}},
	)
}
