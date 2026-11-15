package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// fakeReviews is an in-memory ReviewSource over e_test: r_known is a sealed
// run, r_changed a run whose seal does not verify, r_open an unsealed run.
type fakeReviews struct {
	mu    sync.Mutex
	added []schemas.ReviewRequest
}

func (f *fakeReviews) ReviewQueue(_ context.Context, expID, reason string) (*schemas.ReviewQueue, error) {
	if expID != "e_test" {
		return nil, fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	}
	items := []schemas.ReviewQueueItem{{RunID: "r_known", ExpID: expID, Task: "t01", Arm: "b", Reasons: []string{"split_votes"},
		SealedOutcome: schemas.OutcomeFail, Outcome: schemas.OutcomeFail}}
	if reason != "" && reason != "split_votes" {
		items = []schemas.ReviewQueueItem{}
	}
	return &schemas.ReviewQueue{ExpID: expID, Items: items, Reasons: schemas.ReviewReasons, Problems: []string{}}, nil
}

func (f *fakeReviews) ReviewLog(_ context.Context, expID string) (*schemas.ReviewLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &schemas.ReviewLog{ExpID: expID, Reviews: []schemas.Review{}, Verified: true, Problems: []string{}}, nil
}

func (f *fakeReviews) RunReviews(_ context.Context, runID string) (*schemas.RunReviews, error) {
	if runID != "r_known" {
		return nil, fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	}
	return &schemas.RunReviews{RunID: runID, ExpID: "e_test", SealedOutcome: schemas.OutcomeFail, Outcome: schemas.OutcomeFail,
		Reviews: []schemas.Review{}, Reasons: []string{}, Problems: []string{}}, nil
}

func (f *fakeReviews) AddReview(_ context.Context, req *schemas.ReviewRequest) (*schemas.Review, error) {
	switch req.RunID {
	case "r_changed":
		return nil, fmt.Errorf("seal does not verify: %w", schemas.ErrSealMismatch)
	case "r_open":
		return nil, fmt.Errorf("run is not sealed: %w", schemas.ErrReviewInput)
	case "r_none":
		return nil, fmt.Errorf("run r_none: %w", schemas.ErrNotFound)
	case "r_boom":
		return nil, errors.New("disk on fire")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, *req)
	return &schemas.Review{ReviewID: "rv_1", ExpID: "e_test", RunID: req.RunID, Decision: req.Decision, Reason: req.Reason,
		Reviewer: req.Reviewer, SealedOutcome: schemas.OutcomeFail, PreviousOutcome: schemas.OutcomeFail, Hash: "h"}, nil
}

// newReviewsClient mounts the reviews handler in mode the way the server
// does.
func newReviewsClient(t *testing.T, mode schemas.Mode, reviews ReviewSource) *testClient {
	t.Helper()
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found")
	}
	write := lib.RequireWriteToken(mode, testToken)
	NewReviewsHandler(mode, reviews, write).RegisterRoutes(r, lib.BodyLimit(mode, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Shutdown(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestReviewRoutes(t *testing.T) {
	f := &fakeReviews{}
	c := newReviewsClient(t, schemas.ModeLive, f)
	post := APIPrefix + "/reviews"
	good := `{"run_id":"r_known","decision":"pass","reason":"judge misread the answer","reviewer":"ana","evidence":["grade.json"]}`
	tests := []struct {
		name, method, path, body string
		headers                  map[string]string
		status                   int
		contains                 string
	}{
		{"queue", "GET", APIPrefix + "/experiments/e_test/reviews/queue", "", nil, 200, `"run_id":"r_known"`},
		{"queue filtered", "GET", APIPrefix + "/experiments/e_test/reviews/queue?reason=veto", "", nil, 200, `"items":[]`},
		{"queue bad reason", "GET", APIPrefix + "/experiments/e_test/reviews/queue?reason=nope", "", nil, 400, "reason must be one of"},
		{"queue unknown experiment", "GET", APIPrefix + "/experiments/e_none/reviews/queue", "", nil, 404, "not_found"},
		{"log", "GET", APIPrefix + "/experiments/e_test/reviews", "", nil, 200, `"verified":true`},
		{"run reviews", "GET", APIPrefix + "/runs/r_known/reviews", "", nil, 200, `"sealed_outcome":"fail"`},
		{"run reviews unknown", "GET", APIPrefix + "/runs/r_none/reviews", "", nil, 404, "not_found"},
		{"no token", "POST", post, good, map[string]string{"Content-Type": "application/json"}, 401, "unauthorized"},
		{"created", "POST", post, good, authed("application/json"), 201, `"review_id":"rv_1"`},
		{"missing reason", "POST", post, `{"run_id":"r_known","decision":"pass","reason":" ","reviewer":"ana"}`, authed("application/json"), 400, "reason is required"},
		{"missing reviewer", "POST", post, `{"run_id":"r_known","decision":"fail","reason":"x"}`, authed("application/json"), 400, "reviewer is required"},
		{"bad decision", "POST", post, `{"run_id":"r_known","decision":"maybe","reason":"x","reviewer":"ana"}`, authed("application/json"), 400, "decision must be"},
		{"missing run", "POST", post, `{"decision":"pass","reason":"x","reviewer":"ana"}`, authed("application/json"), 400, "run_id is required"},
		{"unknown field", "POST", post, `{"run_id":"r_known","decision":"pass","reason":"x","reviewer":"ana","admin":true}`, authed("application/json"), 400, "unknown field"},
		{"unsealed run", "POST", post, `{"run_id":"r_open","decision":"pass","reason":"x","reviewer":"ana"}`, authed("application/json"), 400, "not sealed"},
		{"changed run", "POST", post, `{"run_id":"r_changed","decision":"pass","reason":"x","reviewer":"ana"}`, authed("application/json"), 409, "conflict"},
		{"unknown run", "POST", post, `{"run_id":"r_none","decision":"pass","reason":"x","reviewer":"ana"}`, authed("application/json"), 404, "not_found"},
		{"failure", "POST", post, `{"run_id":"r_boom","decision":"pass","reason":"x","reviewer":"ana"}`, authed("application/json"), 500, "disk on fire"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			if tc.body != "" {
				body = []byte(tc.body)
			}
			resp := c.do(t, tc.method, tc.path, body, tc.headers)
			if resp.StatusCode() != tc.status || !strings.Contains(string(resp.Body()), tc.contains) {
				t.Fatalf("%s %s = %d %s, want %d containing %q", tc.method, tc.path, resp.StatusCode(), resp.Body(), tc.status, tc.contains)
			}
		})
	}
	if len(f.added) != 1 || f.added[0].Reviewer != "ana" || f.added[0].Evidence[0] != "grade.json" {
		t.Fatalf("recorded = %+v, want exactly the one valid review", f.added)
	}
}

func TestReviewWriteRefusedOutsideLiveMode(t *testing.T) {
	for _, mode := range []schemas.Mode{schemas.ModeRecorded, schemas.ModeSynthetic} {
		t.Run(string(mode), func(t *testing.T) {
			f := &fakeReviews{}
			c := newReviewsClient(t, mode, f)
			resp := c.do(t, "POST", APIPrefix+"/reviews",
				[]byte(`{"run_id":"r_known","decision":"pass","reason":"x","reviewer":"ana"}`), authed("application/json"))
			if resp.StatusCode() != fasthttp.StatusForbidden || !strings.Contains(string(resp.Body()), "refused in "+string(mode)+" mode") {
				t.Fatalf("POST in %s mode = %d %s, want 403", mode, resp.StatusCode(), resp.Body())
			}
			if len(f.added) != 0 {
				t.Fatalf("a review was recorded in %s mode", mode)
			}
			if resp := c.do(t, "GET", APIPrefix+"/experiments/e_test/reviews/queue", nil, nil); resp.StatusCode() != 200 {
				t.Fatalf("reads must still work in %s mode: %d %s", mode, resp.StatusCode(), resp.Body())
			}
		})
	}
}

func TestReviewRoutesWithoutSource(t *testing.T) {
	c := newReviewsClient(t, schemas.ModeLive, nil)
	for _, tc := range []struct{ method, path string }{
		{"GET", APIPrefix + "/experiments/e_test/reviews/queue"},
		{"GET", APIPrefix + "/runs/r_known/reviews"},
		{"POST", APIPrefix + "/reviews"},
	} {
		h := map[string]string(nil)
		if tc.method == "POST" {
			h = authed("application/json")
		}
		if resp := c.do(t, tc.method, tc.path, nil, h); resp.StatusCode() != fasthttp.StatusServiceUnavailable {
			t.Fatalf("%s %s = %d, want 503", tc.method, tc.path, resp.StatusCode())
		}
	}
}

// TestReviewContract checks every review response against the key lists
// bench.ts mirrors (testdata/contract).
func TestReviewContract(t *testing.T) {
	c := newReviewsClient(t, schemas.ModeLive, &fakeReviews{})
	tests := []struct {
		typ, method, path, body string
		nested                  map[string]string // object path -> key list
	}{
		{"ReviewQueue", "GET", APIPrefix + "/experiments/e_test/reviews/queue", "", map[string]string{"items[]": "ReviewQueueItem"}},
		{"ReviewLog", "GET", APIPrefix + "/experiments/e_test/reviews", "", nil},
		{"RunReviews", "GET", APIPrefix + "/runs/r_known/reviews", "", nil},
		{"Review", "POST", APIPrefix + "/reviews", `{"run_id":"r_known","decision":"pass","reason":"x","reviewer":"ana"}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			var body []byte
			var h map[string]string
			if tt.body != "" {
				body, h = []byte(tt.body), authed("application/json")
			}
			resp := c.do(t, tt.method, tt.path, body, h)
			if resp.StatusCode() >= 300 {
				t.Fatalf("%s %s = %d %s", tt.method, tt.path, resp.StatusCode(), resp.Body())
			}
			obj := decodeBody(t, resp)
			checkKeys(t, tt.typ, obj, loadKeys(t, tt.typ), true)
			for path, typ := range tt.nested {
				objs := objectsAt(t, obj, path)
				if len(objs) == 0 {
					t.Fatalf("%s: nothing at %s", tt.typ, path)
				}
				for i, o := range objs {
					checkKeys(t, fmt.Sprintf("%s.%s #%d", tt.typ, path, i), o, loadKeys(t, typ), false)
				}
			}
		})
	}
}
