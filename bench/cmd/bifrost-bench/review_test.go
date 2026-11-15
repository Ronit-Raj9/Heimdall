package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/valyala/fasthttp"
)

func TestAnalyzeWithReviews(t *testing.T) {
	ctx := context.Background()
	a, m := analysisFixture(t)
	before, _, err := a.analyze(ctx, m.ExpID, "")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	rv, err := a.store.AddReview(ctx, m.ExpID, &schemas.ReviewRequest{RunID: "r_t01_b", Decision: schemas.ReviewFail,
		Reason: "the verifier accepted an empty diff", Reviewer: "ana"})
	if err != nil {
		t.Fatalf("AddReview: %v", err)
	}

	orig, report, err := a.analyze(ctx, m.ExpID, "")
	if err != nil {
		t.Fatalf("analyze original: %v", err)
	}
	if orig.GradeSource != schemas.GradeSourceOriginal || orig.ReviewedRuns != 0 || orig.Naive["B"] != before.Naive["B"] ||
		orig.Verdict.Decision != before.Verdict.Decision {
		t.Fatalf("original analysis moved: %+v naive %v, before %v", orig.Verdict.Decision, orig.Naive, before.Naive)
	}
	if !strings.Contains(string(report), "1 counted runs have a human review that this analysis does not apply") {
		t.Fatalf("original report does not mention the unapplied review:\n%s", report)
	}

	rev, report, err := a.analyze(ctx, m.ExpID, schemas.GradeSourceReviewed)
	if err != nil {
		t.Fatalf("analyze reviewed: %v", err)
	}
	if rev.GradeSource != schemas.GradeSourceReviewed || rev.ReviewedRuns != 1 || rev.ReviewChangedRuns != 1 {
		t.Fatalf("reviewed analysis: source %s reviewed %d changed %d", rev.GradeSource, rev.ReviewedRuns, rev.ReviewChangedRuns)
	}
	if got := rev.Naive["B"]; got.Num != 1 || got.Den != 2 {
		t.Fatalf("naive B = %+v, want 1 of 2 after the fail review", got)
	}
	for _, want := range []string{"## Human review", rv.ReviewID, "evidence level `reviewed`", "| r_t01_b | t01 | B | clean | pass | fail | ana |"} {
		if !strings.Contains(string(report), want) {
			t.Fatalf("reviewed report lacks %q:\n%s", want, report)
		}
	}
	if err := a.store.Verify(ctx, m.ExpID, "r_t01_b"); err != nil {
		t.Fatalf("the reviewed run's seal must still verify: %v", err)
	}
}

func TestReviewMarkdownWithoutReviews(t *testing.T) {
	an := &schemas.Analysis{ExpID: "e_x", GradeSource: schemas.GradeSourceOriginal}
	if got := reviewMarkdown(an, nil, &store.ReviewSet{Verified: true}); got != "" {
		t.Fatalf("section = %q, want none without reviews", got)
	}
	got := reviewMarkdown(an, nil, &store.ReviewSet{Problems: []string{"review rv_1 ignored: changed"}})
	if !strings.Contains(got, "Reviews ignored because they do not verify") || !strings.Contains(got, "rv_1") {
		t.Fatalf("section = %q, want the ignored review listed", got)
	}
}

func TestRunReviewUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"unknown subcommand", []string{"approve"}},
		{"list without exp", []string{"list"}},
		{"show without run", []string{"show"}},
		{"add without decision", []string{"add", "-run", "r_1", "-reason", "x", "-reviewer", "ana"}},
		{"add without reason", []string{"add", "-run", "r_1", "-decision", "pass", "-reviewer", "ana"}},
		{"add bad decision", []string{"add", "-run", "r_1", "-decision", "maybe", "-reason", "x", "-reviewer", "ana"}},
		{"log without exp", []string{"log"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := runReview(tc.args); got != exitUsage {
				t.Fatalf("runReview(%v) = %d, want %d", tc.args, got, exitUsage)
			}
		})
	}
}

func TestReviewCell(t *testing.T) {
	if got := reviewCell("a | b\n c"); got != "a / b c" {
		t.Fatalf("reviewCell = %q", got)
	}
	if got := reviewCell(strings.Repeat("x", 200)); len(got) != 120 || !strings.HasSuffix(got, "...") {
		t.Fatalf("long cell = %d chars", len(got))
	}
}

func TestReviewTextOutput(t *testing.T) {
	rv := schemas.Review{ReviewID: "rv_1", RunID: "r_1", Decision: schemas.ReviewPass, Reviewer: "ana", Reason: "judge misread",
		Evidence: []string{"grade.json"}}
	var b strings.Builder
	writeReviewQueue(&b, &schemas.ReviewQueue{Items: []schemas.ReviewQueueItem{{RunID: "r_1", Task: "t01", Arm: "B", Condition: "clean", Trial: 1,
		Reasons: []string{"split_votes", "flagged"}, SealedOutcome: schemas.OutcomeFail, Outcome: schemas.OutcomePass, Decision: schemas.ReviewPass}},
		Problems: []string{"review rv_0 ignored"}})
	writeRunReviews(&b, &schemas.RunReviews{RunID: "r_1", ExpID: "e_1", SealedOutcome: schemas.OutcomeFail, Outcome: schemas.OutcomePass,
		Current: &rv, Reviews: []schemas.Review{rv}, Reasons: []string{"split_votes"}})
	writeReviewLog(&b, &schemas.ReviewLog{Reviews: []schemas.Review{rv}, Verified: false, Problems: []string{"line 2 malformed"}})
	out := b.String()
	for _, want := range []string{"split_votes,flagged", "1 runs need review", "review rv_0 ignored", "* ", "evidence: grade.json",
		"needs review: split_votes", "chain DOES NOT VERIFY", "line 2 malformed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestReviewAPIOverStore drives the reviews routes through the serve
// adapter on a real store: post a review by run id alone, then read it back
// from the run, the queue and the log.
func TestReviewAPIOverStore(t *testing.T) {
	a, m := analysisFixture(t)
	r := router.New()
	handlers.NewReviewsHandler(schemas.ModeLive, reviewSource{st: a.store}, lib.RequireWriteToken(schemas.ModeLive, "tok")).RegisterRoutes(r)
	do := func(method, path, body string) (int, map[string]any) {
		var ctx fasthttp.RequestCtx
		ctx.Request.SetRequestURI("http://127.0.0.1" + path)
		ctx.Request.Header.SetMethod(method)
		if body != "" {
			ctx.Request.Header.Set("Authorization", "Bearer tok")
			ctx.Request.Header.SetContentType("application/json")
			ctx.Request.SetBodyString(body)
		}
		r.Handler(&ctx)
		out := map[string]any{}
		if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, ctx.Response.Body(), err)
		}
		return ctx.Response.StatusCode(), out
	}
	code, body := do("POST", handlers.APIPrefix+"/reviews", `{"run_id":"r_t02_a","decision":"flag_only","reason":"diff touches the tests","reviewer":"bo"}`)
	if code != fasthttp.StatusCreated || body["exp_id"] != m.ExpID || body["parent_seal"] == "" {
		t.Fatalf("POST = %d %v", code, body)
	}
	id := body["review_id"]
	if code, body = do("GET", handlers.APIPrefix+"/runs/r_t02_a/reviews", ""); code != 200 || body["current"] != nil {
		t.Fatalf("run reviews = %d %v; a flag decides nothing", code, body)
	}
	reviews, _ := body["reviews"].([]any)
	if len(reviews) != 1 || reviews[0].(map[string]any)["review_id"] != id {
		t.Fatalf("run reviews = %v", body)
	}
	if code, body = do("GET", handlers.APIPrefix+"/experiments/"+m.ExpID+"/reviews/queue?reason=flagged", ""); code != 200 {
		t.Fatalf("queue = %d %v", code, body)
	}
	if items, _ := body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["run_id"] != "r_t02_a" {
		t.Fatalf("queue = %v", body)
	}
	if code, body = do("GET", handlers.APIPrefix+"/experiments/"+m.ExpID+"/reviews", ""); code != 200 || body["verified"] != true {
		t.Fatalf("log = %d %v", code, body)
	}
	if code, _ = do("POST", handlers.APIPrefix+"/reviews", `{"run_id":"r_nope","decision":"pass","reason":"x","reviewer":"bo"}`); code != fasthttp.StatusNotFound {
		t.Fatalf("POST unknown run = %d, want 404", code)
	}
}
