package handlers

import (
	"github.com/maximhq/bifrost/bench/annotate"
	"github.com/valyala/fasthttp"
)

// Lane Y15 route docs: annotation queues, the dataset API, platform
// imports and the benchmark data policy.
func init() {
	q := APIPrefix + "/annotation/queues"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: q, Tag: "annotation", Summary: "List the organization's annotation queues with their counts and what the caller can label",
			Response: AnnotationQueueList{}},
		RouteDoc{Method: "POST", Path: q, Tag: "annotation", Summary: "Create an annotation queue: runs or judge decisions to label, reviewers per item, blind mode, assignment and the benchmark whose judge the labels calibrate",
			Auth: AuthWrite, Request: AnnotationQueueRequest{}, Status: fasthttp.StatusCreated, Response: AnnotationQueueView{}},
		RouteDoc{Method: "GET", Path: q + "/{id}", Tag: "annotation", Summary: "Read one annotation queue", Response: AnnotationQueueView{}},
		RouteDoc{Method: "PUT", Path: q + "/{id}", Tag: "annotation", Summary: "Change a queue's settings (its creator or an org admin)",
			Auth: AuthWrite, Request: AnnotationQueueRequest{}, Response: AnnotationQueueView{}},
		RouteDoc{Method: "DELETE", Path: q + "/{id}", Tag: "annotation", Summary: "Delete a queue (its creator or an org admin); calibration labels already written stay",
			Auth: AuthWrite, Response: map[string]any{}},
		RouteDoc{Method: "POST", Path: q + "/{id}/items", Tag: "annotation", Summary: "Add items: the sealed runs of an experiment (optionally a stratified sample) or items given directly",
			Auth: AuthWrite, Request: AnnotationItemsRequest{}, Status: fasthttp.StatusCreated, Response: AnnotationItemsResult{}},
		RouteDoc{Method: "GET", Path: q + "/{id}/items", Tag: "annotation", Summary: "List a queue's items as the caller may see them (blind mode and label order applied)",
			Query:    []QueryDoc{{Name: "state", Description: "open, labelling, disputed or done"}, {Name: "offset", Description: "items to skip"}, {Name: "limit", Description: "at most this many (default 100, max 500)"}},
			Response: AnnotationItemList{}},
		RouteDoc{Method: "POST", Path: q + "/{id}/assign", Tag: "annotation", Summary: "Assign every unresolved item to the queue's reviewers, round robin, so each gets its reviewers per item",
			Auth: AuthWrite, OptionalBody: true, Response: AnnotationAssignResult{}},
		RouteDoc{Method: "POST", Path: q + "/{id}/next", Tag: "annotation", Summary: "Reserve and return the next item for the signed-in caller to label (none when done); an optional body {\"skip\": [item ids]} passes on those items and frees their reservations",
			Auth: AuthWrite, OptionalBody: true, Response: AnnotationNext{}},
		RouteDoc{Method: "POST", Path: q + "/{id}/items/{item}/labels", Tag: "annotation", Summary: "Label an item correct or incorrect as the signed-in caller; agreeing labels resolve it and write a calibration label",
			Auth: AuthWrite, Request: AnnotationLabelRequest{}, Status: fasthttp.StatusCreated, Response: AnnotationLabelResult{}},
		RouteDoc{Method: "POST", Path: q + "/{id}/items/{item}/resolve", Tag: "annotation", Summary: "Decide an item's final label (a disagreement or an override; the queue's creator or an org admin)",
			Auth: AuthWrite, Request: AnnotationLabelRequest{}, Response: AnnotationLabelResult{}},
		RouteDoc{Method: "GET", Path: q + "/{id}/agreement", Tag: "annotation", Summary: "Agreement between labellers (percent, Cohen's kappa per pair, Fleiss' kappa) and with the judge",
			Response: AnnotationAgreement{Agreement: annotate.Agreement{}}},

		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/dataset/tasks", Tag: "suites", Summary: "Add or replace a question-and-answer benchmark's tasks from rows (input or chat messages, expected answers, rubric, metadata, attachments); each change is a new version; the benchmark's personal data policy runs first; dry_run previews",
			Auth: AuthWrite, Request: DatasetTasksRequest{}, Status: fasthttp.StatusCreated, Response: DatasetTasksResult{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/dataset/tasks", Tag: "suites", Summary: "List a benchmark's tasks with their metadata, attachments and row hash (expected answers are never listed)",
			Query: []QueryDoc{{Name: "offset", Description: "tasks to skip"}, {Name: "limit", Description: "at most this many (default 100, max 500)"}}, Response: DatasetTaskList{}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/suites/{id}/dataset/tasks/{task}", Tag: "suites", Summary: "Remove a task made from rows; a new version",
			Auth: AuthWrite, Response: DatasetTaskDeleted{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/dataset/versions", Tag: "suites", Summary: "The version log of a benchmark's rows, newest first", Response: DatasetVersionList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/dataset/versions/{n}", Tag: "suites", Summary: "One version: its changes and the row hash of every task", Response: DatasetVersionDetail{}},

		RouteDoc{Method: "GET", Path: APIPrefix + "/imports/platforms", Tag: "imports", Summary: "The platforms datasets and traces import from (LangSmith, Langfuse, Braintrust, Datadog) and which secrets the org has",
			Response: PlatformImportList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/imports/platforms", Tag: "imports", Summary: "Import a dataset or recent traces from a platform with the org's keys: into a benchmark as tasks, or into the failure inbox; dry_run previews",
			Auth: AuthWrite, Request: PlatformImportRequest{}, Response: PlatformImportResult{}},

		RouteDoc{Method: "GET", Path: APIPrefix + "/data-policies", Tag: "suites", Summary: "Benchmarks with a data policy: personal data mode, whether access is restricted and whether the caller may open each",
			Response: DataPolicyList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/data-policy", Tag: "suites", Summary: "A benchmark's data policy: how personal data in imported rows is handled and who may see its task content",
			Response: DataPolicyView{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/suites/{id}/data-policy", Tag: "suites", Summary: "Set a benchmark's personal data mode (warn, mask, redact, block, off) and, as an org admin, its access list",
			Auth: AuthWrite, Request: DataPolicyRequest{}, Response: DataPolicyView{}},
	)
}
