package handlers

import (
	"github.com/maximhq/bifrost/bench/policybench"
	"github.com/valyala/fasthttp"
)

// Policy document to compliance benchmark (lane Z23): route docs.
func init() {
	base := APIPrefix + "/suites/{id}/policy-docs"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: base, Tag: "suites",
			Summary: "Policy documents uploaded to a support benchmark, with their clause and draft counts; a viewer gets ids, titles and upload times only (limited), to open the report", Response: PolicyDocList{}},
		RouteDoc{Method: "POST", Path: base, Tag: "suites",
			Summary: "Upload a policy or SOP (PDF, DOCX, Markdown or text, base64 or pasted) and split it into numbered clauses; dry_run previews and writes nothing",
			Auth:    AuthWrite, Request: PolicyDocUpload{}, Status: fasthttp.StatusCreated, Response: PolicyDocView{}},
		RouteDoc{Method: "GET", Path: base + "/{doc}", Tag: "suites",
			Summary: "One policy document: its clauses, drafted tasks with their review state, generation records and the tools the drafts may call (member)", Response: PolicyDocView{}},
		RouteDoc{Method: "DELETE", Path: base + "/{doc}", Tag: "suites",
			Summary: "Delete a policy document and its pending drafts; accepted tasks stay in the benchmark", Auth: AuthWrite, Status: fasthttp.StatusNoContent},
		RouteDoc{Method: "PUT", Path: base + "/{doc}/clauses", Tag: "suites",
			Summary: "Replace a policy document's clauses after a person fixes the split or unticks clauses with nothing to test",
			Auth:    AuthWrite, Request: PolicyClausesRequest{}, Response: PolicyDocView{}},
		RouteDoc{Method: "GET", Path: base + "/{doc}/drafts/{task}", Tag: "suites",
			Summary: "The files of one drafted task (task.yaml, checks.yaml, simulated customer, seed, reference and violating controls) for review (member)", Response: PolicyDraftFiles{}},
		RouteDoc{Method: "POST", Path: base + "/{doc}/review", Tag: "suites",
			Summary: "Accept drafted tasks into the benchmark (one suite.yaml write, optionally switching to grader checks with a pinned reply judge) or reject them",
			Auth:    AuthWrite, Request: PolicyReviewRequest{}, Response: PolicyReviewResult{}},
		RouteDoc{Method: "GET", Path: base + "/{doc}/report", Tag: "suites",
			Summary: "Compliance report of a policy document: per-clause pass rate with 95% ranges per agent, plain-words findings with example runs; format=json, markdown or pdf",
			Query: []QueryDoc{{Name: "format", Description: "json (default), markdown or pdf"}, {Name: "exp", Description: "only the runs of this evaluation"}},
			Response: policyReportDoc{}, AlsoRaw: "application/pdf"},
	)
}

// policyReportDoc documents the report's JSON shape.
type policyReportDoc = policybench.Report
