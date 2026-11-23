package handlers

import (
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
)

// Suite authoring area (web parity L10): route docs.
func init() {
	dirQuery := QueryDoc{Name: "dir", Description: "Folder inside the suite, for example tasks/t01/repo"}
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/suite-templates", Tag: "suites", Summary: "Templates a new suite can start from", Response: SuiteTemplateList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites", Tag: "suites", Summary: "Create benchmarks/<id>/ from a suite template; 409 when the id is taken (an existing or a built-in benchmark)",
			Auth: AuthWrite, Request: CreateSuiteRequest{}, Status: fasthttp.StatusCreated, Response: CreateSuiteResponse{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/tasks", Tag: "suites", Summary: "Add a task folder in the shape of the suite's template and list it in suite.yaml",
			Auth: AuthWrite, Request: CreateTaskRequest{}, Status: fasthttp.StatusCreated, Response: CreateTaskResponse{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/lint", Tag: "suites", Summary: "Lint the suite now: problems, error and warning counts, lock drift; a load failure is load_error with 200", Response: SuiteLintReport{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/files", Tag: "suites", Summary: "Every file of the suite folder, hidden tests included (members only)", Query: []QueryDoc{dirQuery}, Response: SuiteFileList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/files/{path:*}", Tag: "suites", Summary: "One suite file: text content and sha256 (members only)", Response: SuiteFile{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/suites/{id}/files/{path:*}", Tag: "suites", Summary: "Write one suite file (raw body, at most 1 MiB); If-Match: <sha256> answers 409 on a concurrent edit",
			Auth: AuthWrite, RawRequest: "application/octet-stream", Response: projectfs.Entry{}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/suites/{id}/files/{path:*}", Tag: "suites", Summary: "Delete one suite file or folder (never suite.yaml)",
			Auth: AuthWrite, Status: fasthttp.StatusNoContent, NoContent: true},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/suites/{id}", Tag: "suites",
			Summary: "Delete a benchmark of this organization: its folder goes; comparisons, evaluations, runs and their results stay readable. 409 for a built-in benchmark, one still in use by running work, or one a schedule repeats",
			Auth:    AuthWrite, Status: fasthttp.StatusNoContent, NoContent: true},
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/upload", Tag: "suites", Summary: "Extract a zip (raw body, at most 50 MiB) into a folder of the suite, zip-slip safe",
			Auth: AuthWrite, Query: []QueryDoc{dirQuery}, RawRequest: "application/zip", Response: SuiteUploadResult{}},
	)
}
