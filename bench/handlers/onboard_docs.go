package handlers

import (
	"github.com/maximhq/bifrost/bench/onboard"
	"github.com/valyala/fasthttp"
)

// Add benchmark (lane Z20): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/onboard/upload", Tag: "onboard",
			Summary: "Upload a file to add as a benchmark (multipart field \"file\", or the raw body with ?filename=): a zip is unpacked into a folder; pass the returned upload as input.upload",
			Auth:    AuthWrite, RawRequest: "multipart/form-data", Response: OnboardUploadResult{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/onboard/detect", Tag: "onboard",
			Summary: "Say what an input is (file, Hugging Face, GitHub or Harbor link, upload) with no model call; best match first",
			Auth:    AuthWrite, Request: OnboardInputRequest{}, Response: OnboardDetectResult{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/onboard/inspect", Tag: "onboard",
			Summary: "Describe an input before anything is written: units, five redacted samples, columns, licence, warnings, estimate and a suggested plan",
			Auth:    AuthWrite, Request: OnboardInputRequest{}, Response: onboard.Inventory{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/onboard/drafts", Tag: "onboard",
			Summary:  "List the organization's draft sets, newest first",
			Response: OnboardDraftList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/onboard/drafts", Tag: "onboard",
			Summary: "Draft tasks from an input with a plan into a staging area outside benchmarks/; runs the cheap gates (static validation, duplicates, split)",
			Auth:    AuthWrite, Request: OnboardDraftRequest{}, Status: fasthttp.StatusCreated, Response: onboard.DraftSet{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/onboard/drafts/{set}", Tag: "onboard",
			Summary:  "One draft set: each task's instruction, checks as sentences, controls, gates, review state and readiness with its control catch rate",
			Response: onboard.DraftSet{}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/onboard/drafts/{set}", Tag: "onboard",
			Summary: "Delete a draft set (published benchmarks are kept)",
			Auth:    AuthWrite, Status: fasthttp.StatusNoContent, NoContent: true},
		RouteDoc{Method: "PATCH", Path: APIPrefix + "/onboard/drafts/{set}/tasks/{task}", Tag: "onboard",
			Summary: "Accept, reject, hold out or drop one draft task",
			Auth:    AuthWrite, Request: onboard.TaskEdit{}, Response: onboard.DraftSet{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/onboard/drafts/{set}/ready", Tag: "onboard",
			Summary: "Start the readiness checks on a draft (job onboard.ready): the reference must pass and the empty answer, wrong answers and exploit must fail, in the real sandbox",
			Auth:    AuthWrite, Request: OnboardReadyRequest{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: OnboardJobResult{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/onboard/drafts/{set}/publish", Tag: "onboard",
			Summary: "Publish a draft's accepted, ready tasks privately into its benchmark (job onboard.publish): stamps the canary on held-out tasks, carries readiness records into the lock, writes onboard.yaml",
			Auth:    AuthWrite, Request: onboard.PublishOptions{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: OnboardJobResult{}},
	)
}
