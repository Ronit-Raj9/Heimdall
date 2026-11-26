package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/validity"
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

type w449Index struct {
	*fakeIndex
	regrades []leaderboard.Regrade
}

func (x *w449Index) Regrades(context.Context, string) ([]leaderboard.Regrade, error) {
	return x.regrades, nil
}

func (x *w449Index) RegradedRuns(context.Context, string, string) ([]schemas.SealedRun, error) {
	return nil, nil
}

// w449Analyzer answers invalid for the original and inconclusive for any
// regrade, and counts its calls.
type w449Analyzer struct{ calls int }

func (a *w449Analyzer) Analyze(_ context.Context, expID, src string) (*schemas.Analysis, error) {
	a.calls++
	an := &schemas.Analysis{ExpID: expID, GradeSource: src}
	an.Verdict.Decision, an.Verdict.Action = "invalid", "rerun"
	if src != schemas.GradeSourceOriginal {
		an.Verdict.Decision = "inconclusive"
	}
	return an, nil
}

func validityDo(t *testing.T, h *validityHandler, method, path, user string, out any) int {
	t.Helper()
	r := router.New()
	h.register(r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(path)
	ctx.Request.Header.SetMethod(method)
	if user != "" {
		auth.SetPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, UserID: user, OrgID: "org_a", Role: auth.RoleViewer})
	}
	r.Handler(ctx)
	if out != nil {
		if err := json.Unmarshal(ctx.Response.Body(), out); err != nil {
			t.Fatalf("%s: %v %s", path, err, ctx.Response.Body())
		}
	}
	return ctx.Response.StatusCode()
}

func TestFlipEnqueuesWebhook(t *testing.T) {
	root := t.TempDir()
	hooks := webhooks.Open(root)
	url, events, on := "https://hooks.example.com/x", []string{webhooks.EventResultChanged}, true
	if _, err := hooks.Create(webhooks.Input{URL: &url, Events: &events, Enabled: &on}, "u1", false); err != nil {
		t.Fatal(err)
	}
	idx := &w449Index{fakeIndex: testIndex(), regrades: []leaderboard.Regrade{{ID: "rg_1", CreatedAt: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC), Note: "check fixed"}}}
	an := &w449Analyzer{}
	h := &validityHandler{mode: testMode, orgID: "org_a", index: idx, analyzer: an, store: validity.Open(root), hooks: hooks}
	var list ValidityList
	if st := validityDo(t, h, "GET", APIPrefix+"/validity?flipped=1", "u1", &list); st != 200 {
		t.Fatalf("status %d", st)
	}
	if len(list.Entries) != 1 || !list.Entries[0].Flipped || list.Unseen != 1 || list.Entries[0].Before.Decision != "invalid" {
		t.Fatalf("list %+v", list)
	}
	outbox, _ := os.ReadDir(filepath.Join(root, ".heimdall", "webhooks", "outbox"))
	if len(outbox) != 1 {
		t.Fatalf("outbox files %d", len(outbox))
	}
	b, _ := os.ReadFile(filepath.Join(root, ".heimdall", "webhooks", "outbox", outbox[0].Name()))
	if !strings.Contains(string(b), "result.changed") {
		t.Fatalf("outbox %s", b)
	}
	// A second read stores nothing new and queues nothing new.
	calls := an.calls
	validityDo(t, h, "GET", APIPrefix+"/validity", "u1", &list)
	if outbox, _ = os.ReadDir(filepath.Join(root, ".heimdall", "webhooks", "outbox")); len(outbox) != 1 || an.calls != calls {
		t.Fatalf("second read: outbox %d, analyses %d then %d", len(outbox), calls, an.calls)
	}
	// Seen is per user.
	if st := validityDo(t, h, "POST", APIPrefix+"/validity/"+list.Entries[0].ID+"/seen", "u1", nil); st != 204 {
		t.Fatalf("seen %d", st)
	}
	validityDo(t, h, "GET", APIPrefix+"/validity", "u1", &list)
	if list.Unseen != 0 {
		t.Fatalf("u1 unseen %d", list.Unseen)
	}
	validityDo(t, h, "GET", APIPrefix+"/validity", "u2", &list)
	if list.Unseen != 1 {
		t.Fatalf("u2 unseen %d", list.Unseen)
	}
	var one ExperimentValidity
	if st := validityDo(t, h, "GET", APIPrefix+"/experiments/"+testExp+"/validity", "u1", &one); st != 200 || len(one.Entries) != 1 || one.Stale != nil {
		t.Fatalf("experiment %d %+v", st, one)
	}
	if st := validityDo(t, h, "POST", APIPrefix+"/validity/nope/seen", "u1", nil); st != 404 {
		t.Fatalf("unknown seen %d", st)
	}
}
