package handlers

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func stepPagesGet(t *testing.T, index ReadIndex, path string, out any) int {
	t.Helper()
	r := router.New()
	registerStepPages(r, testMode, index)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(path)
	ctx.Request.Header.SetMethod("GET")
	r.Handler(ctx)
	if out != nil && ctx.Response.StatusCode() == 200 {
		if err := json.Unmarshal(ctx.Response.Body(), out); err != nil {
			t.Fatal(err)
		}
	}
	return ctx.Response.StatusCode()
}

func manySteps(n int) []schemas.StepRecord {
	out := make([]schemas.StepRecord, 0, n)
	for i := 1; i <= n; i++ {
		kind := "model_turn"
		if i%2 == 0 {
			kind = "command"
		}
		s := schemas.StepRecord{Seq: i, Kind: kind, Summary: fmt.Sprint("step ", i)}
		if i%3 == 0 {
			s.AgentID = "helper"
			s.ParentSeq = 1
		}
		out = append(out, s)
	}
	return out
}

func TestStepsPageCursor(t *testing.T) {
	steps := manySteps(7)
	page, next, total := PageSteps(steps, StepsPageFilter{Limit: 3})
	if len(page) != 3 || page[0].Seq != 1 || next == nil || *next != 3 || total != 7 {
		t.Fatalf("page 1: %v next %v total %d", page, next, total)
	}
	page, next, _ = PageSteps(steps, StepsPageFilter{AfterSeq: *next, Limit: 3})
	if len(page) != 3 || page[0].Seq != 4 || next == nil || *next != 6 {
		t.Fatalf("page 2: %v next %v", page, next)
	}
	page, next, _ = PageSteps(steps, StepsPageFilter{AfterSeq: *next, Limit: 3})
	if len(page) != 1 || page[0].Seq != 7 || next != nil {
		t.Fatalf("page 3: %v next %v", page, next)
	}
	// Through the route.
	var body RunStepsPage
	if st := stepPagesGet(t, stepsFixture(t), APIPrefix+"/runs/r_t01_A/steps/page?limit=2", &body); st != 200 {
		t.Fatalf("status %d", st)
	}
	if body.RunID != "r_t01_A" || len(body.Steps) != 2 || body.NextAfterSeq == nil || *body.NextAfterSeq != 2 || body.Total != 3 {
		t.Fatalf("route page %+v", body)
	}
	for _, bad := range []string{"limit=0", "limit=201", "after_seq=-1", "limit=x"} {
		if st := stepPagesGet(t, stepsFixture(t), APIPrefix+"/runs/r_t01_A/steps/page?"+bad, nil); st != 400 {
			t.Fatalf("%s: %d", bad, st)
		}
	}
}

func TestStepsPageFilters(t *testing.T) {
	steps := manySteps(12)
	page, _, total := PageSteps(steps, StepsPageFilter{Limit: 50, Kind: "command"})
	if total != 6 || len(page) != 6 {
		t.Fatalf("kind: %d %d", total, len(page))
	}
	page, _, total = PageSteps(steps, StepsPageFilter{Limit: 50, Kind: "command", AgentID: "helper"})
	if total != 2 || page[0].Seq != 6 || page[1].Seq != 12 {
		t.Fatalf("kind and agent: %v", page)
	}
	one := 1
	if _, _, total = PageSteps(steps, StepsPageFilter{Limit: 50, ParentSeq: &one}); total != 4 {
		t.Fatalf("parent: %d", total)
	}
}

func TestStepRouteChildren(t *testing.T) {
	steps := manySteps(9)
	steps[1].WitnessedBy = []int{3}
	steps[2].WitnessOf = 2
	got, ok := StepAround(steps, 1)
	if !ok || len(got.Children) != 3 || got.Children[0].Seq != 3 {
		t.Fatalf("children %+v", got)
	}
	got, _ = StepAround(steps, 2)
	if len(got.Witnesses) != 1 || got.Witnesses[0].Seq != 3 || got.WitnessOf != nil {
		t.Fatalf("witnesses %+v", got)
	}
	got, _ = StepAround(steps, 3)
	if got.WitnessOf == nil || got.WitnessOf.Seq != 2 {
		t.Fatalf("witness_of %+v", got)
	}
	var body RunStep
	if st := stepPagesGet(t, stepsFixture(t), APIPrefix+"/runs/r_t01_A/step/2", &body); st != 200 || body.Step.Seq != 2 || len(body.Witnesses) != 1 {
		t.Fatalf("route: %d %+v", st, body)
	}
	if st := stepPagesGet(t, stepsFixture(t), APIPrefix+"/runs/r_t01_A/step/99", nil); st != 404 {
		t.Fatalf("missing step: %d", st)
	}
}
