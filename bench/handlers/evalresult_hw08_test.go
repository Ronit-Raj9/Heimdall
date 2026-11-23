package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// analysisFakeIndex is the leaderboard test index with a settable newest
// analysis id per experiment (AnalysisIndex).
type analysisFakeIndex struct {
	*fakeIndex
	ids map[string]string
}

func (a *analysisFakeIndex) LatestAnalysis(expID string) (string, bool) {
	id, ok := a.ids[expID]
	return id, ok
}

// Fix wave 3, N215: the stored result of a finished evaluation records which
// analysis of each child it was built from, and a newer analysis (a re-score
// after a gate fix) refills it, so the list stops showing the old number.
func TestEvaluationResultRefilledOnNewAnalysis(t *testing.T) {
	h, st, ev := evalResultFixture(t, "ev_20261123_000000_rean", schemas.EvaluationDone, time.Now())
	idx := &analysisFakeIndex{fakeIndex: h.env.Services.Index.(*fakeIndex), ids: map[string]string{testExp: "a_old"}}
	h.env.Services.Index = idx
	get := func() *schemas.Evaluation {
		r := router.New()
		r.GET(APIPrefix+"/evaluations/{id}", h.get)
		var ctx fasthttp.RequestCtx
		ctx.Request.Header.SetMethod(fasthttp.MethodGet)
		ctx.Request.SetRequestURI(APIPrefix + "/evaluations/" + ev.ID)
		r.Handler(&ctx)
		var got schemas.Evaluation
		if err := json.Unmarshal(ctx.Response.Body(), &got); err != nil {
			t.Fatal(err)
		}
		return &got
	}
	got := get()
	if got.Result == nil || got.Result.Analyses[testExp] != "a_old" {
		t.Fatalf("first fill %+v", got.Result)
	}
	first := got.Result.At
	h.now = func() time.Time { return first.Add(time.Minute) }
	if again := get(); !again.Result.At.Equal(first) {
		t.Fatalf("refilled without a new analysis: %v then %v", first, again.Result.At)
	}
	idx.ids[testExp] = "a_new"
	got = get()
	if got.Result.Analyses[testExp] != "a_new" || got.Result.At.Equal(first) {
		t.Fatalf("not refilled on a new analysis: %+v", got.Result)
	}
	saved, _ := st.Load(ev.ID)
	if saved.Result.Analyses[testExp] != "a_new" {
		t.Fatalf("refill not saved: %+v", saved.Result)
	}
	// A result stored before the map existed is refilled once.
	saved.Result.Analyses = nil
	if !needsResult(saved) {
		t.Fatal("a result without analyses needs one refill")
	}
}
