package handlers

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/valyala/fasthttp"
)

// pausingLauncher answers Controls with a paused state while paused is set.
type pausingLauncher struct {
	budgetLauncher
	paused atomic.Bool
}

func (l *pausingLauncher) Controls(context.Context, string) (*runctl.State, []runctl.Entry, error) {
	if l.paused.Load() {
		return &runctl.State{Settings: runctl.Settings{Mode: runctl.ModePaused, PausedBy: "usr_1"}}, nil, nil
	}
	return &runctl.State{Settings: runctl.Settings{Mode: runctl.ModeRunning}}, nil, nil
}

// TestGetEvaluationReportsPaused: GET and the list read the children's run
// controls, so a pause made anywhere (the evaluation page or a comparison's
// run controls) shows on the next poll, and a resume clears it.
func TestGetEvaluationReportsPaused(t *testing.T) {
	l := &pausingLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	created := evalCall(r, "POST", APIPrefix+"/evaluations", `{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":1}`)
	if created.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("create %d: %s", created.Response.StatusCode(), created.Response.Body())
	}
	ev := decodeEval(t, created)
	if ev.Paused {
		t.Fatal("a new evaluation is paused")
	}
	l.paused.Store(true)
	if got := decodeEval(t, evalCall(r, "GET", APIPrefix+"/evaluations/"+ev.ID, "")); !got.Paused {
		t.Fatalf("GET after pause: paused false: %+v", got)
	}
	var list EvaluationList
	if err := json.Unmarshal(evalCall(r, "GET", APIPrefix+"/evaluations", "").Response.Body(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Evaluations) != 1 || !list.Evaluations[0].Paused {
		t.Fatalf("list after pause: %+v", list.Evaluations)
	}
	l.paused.Store(false)
	if got := decodeEval(t, evalCall(r, "GET", APIPrefix+"/evaluations/"+ev.ID, "")); got.Paused {
		t.Fatal("GET after resume: still paused")
	}
}

// TestGetEvaluationWithoutRunControls: a server with no run controls never
// reports a pause and still answers.
func TestGetEvaluationWithoutRunControls(t *testing.T) {
	r, _ := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites()})
	ev := decodeEval(t, evalCall(r, "POST", APIPrefix+"/evaluations", `{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":1}`))
	got := evalCall(r, "GET", APIPrefix+"/evaluations/"+ev.ID, "")
	if got.Response.StatusCode() != fasthttp.StatusOK || decodeEval(t, got).Paused {
		t.Fatalf("GET %d: %s", got.Response.StatusCode(), got.Response.Body())
	}
}
