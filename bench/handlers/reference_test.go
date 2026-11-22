package handlers_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/reference"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type refSource struct {
	b   *reference.Board
	err error
}

func (s refSource) Load() (*reference.Board, error) { return s.b, s.err }

func getBoard(t *testing.T, src handlers.ReferenceBoardSource, query string) (int, handlers.ReferenceBoard) {
	t.Helper()
	r := router.New()
	handlers.NewReferenceHandler(schemas.ModeRecorded, src).RegisterRoutes(r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(handlers.APIPrefix + "/reference/board" + query)
	r.Handler(ctx)
	var out handlers.ReferenceBoard
	if ctx.Response.StatusCode() == fasthttp.StatusOK {
		if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return ctx.Response.StatusCode(), out
}

func TestReferenceBoardEmpty(t *testing.T) {
	for _, src := range []handlers.ReferenceBoardSource{nil, reference.Open(t.TempDir())} {
		code, b := getBoard(t, src, "")
		if code != 200 || b.Entries == nil || len(b.Entries) != 0 || b.Org != reference.OrgName || b.UpdatedAt != nil {
			t.Fatalf("empty board %d %+v", code, b)
		}
	}
}

func TestReferenceBoardFilter(t *testing.T) {
	now := time.Now().UTC()
	b := &reference.Board{UpdatedAt: &now, Shares: map[string]string{"ev_a": "shr_secret"}, Entries: []reference.Entry{
		{ID: "ev_a:tau2:A", EvaluationID: "ev_a", Benchmark: "tau2", Agent: "codex", Score: 0.4, SealHash: "sha256:x"},
		{ID: "ev_a:hd-swe-mini:A", EvaluationID: "ev_a", Benchmark: "hd-swe-mini", Agent: "codex", Score: 0.2},
	}}
	code, got := getBoard(t, refSource{b: b}, "?benchmark=tau2@1.0.0")
	if code != 200 || len(got.Entries) != 1 || got.Entries[0].SealHash != "sha256:x" || len(got.Benchmarks) != 2 || got.Benchmark != "tau2@1.0.0" {
		t.Fatalf("filtered %d %+v", code, got)
	}
	if code, _ := getBoard(t, refSource{err: errors.New("disk")}, ""); code != 500 {
		t.Fatalf("broken board answered %d", code)
	}
}
