package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// activeOp is a run operator whose experiment is still running.
type activeOp struct{ logLauncher }

func (*activeOp) Active(string) bool { return true }

func matrixOf(n int) *schemas.MatrixSpec {
	ms := &schemas.MatrixSpec{}
	for i := range n {
		ms.Models = append(ms.Models, schemas.MatrixModel{Model: "m" + string(rune('a'+i))})
	}
	return ms
}

// TestRunControlsRerunNotAllowedOnMatrix: GET /controls says whether Rerun
// failures can work and why not (fix wave 3, N285): never on a model matrix,
// never while running, never when nothing failed.
func TestRunControlsRerunNotAllowedOnMatrix(t *testing.T) {
	h := &ExperimentsHandler{}
	ctx := context.Background()
	failed := []RunView{rcRun("s1", "t1", "clean", 1, schemas.OutcomeFail, false)}
	passed := []RunView{rcRun("s1", "t1", "clean", 1, schemas.OutcomePass, false)}

	m := &schemas.Manifest{ExpID: "e_m", Experiment: schemas.Experiment{Matrix: matrixOf(3)}}
	out, err := h.controlsView(ctx, &logLauncher{}, m, failed)
	if err != nil {
		t.Fatal(err)
	}
	want := "Rerun failures works on a comparison of two setups. For this comparison of 3 models, start a new comparison with the failed tasks."
	if out.RerunFailures == nil || out.RerunFailures.Allowed || out.RerunFailures.Reason != want {
		t.Fatalf("matrix: %+v", out.RerunFailures)
	}

	plain := &schemas.Manifest{ExpID: "e_p"}
	if out, _ = h.controlsView(ctx, &logLauncher{}, plain, failed); out.RerunFailures == nil || !out.RerunFailures.Allowed || out.RerunFailures.Reason != "" {
		t.Fatalf("two setups with a failure: %+v", out.RerunFailures)
	}
	if out, _ = h.controlsView(ctx, &logLauncher{}, plain, passed); out.RerunFailures.Allowed || out.RerunFailures.Reason != "Nothing failed in this comparison." {
		t.Fatalf("nothing failed: %+v", out.RerunFailures)
	}
	if out, _ = h.controlsView(ctx, &activeOp{}, plain, failed); out.RerunFailures.Allowed || !strings.Contains(out.RerunFailures.Reason, "still running") {
		t.Fatalf("running: %+v", out.RerunFailures)
	}
}

// TestRerunMatrixRefusalIsPlain: POST /rerun-failures on a model matrix
// answers 409 with the plain sentence, without "invalid run control: " or
// the "rerun failures of ...: " prefix (fix wave 3, N285).
func TestRerunMatrixRefusalIsPlain(t *testing.T) {
	idx := testIndex()
	idx.manifests[testExp].Experiment.Matrix = matrixOf(3)
	h := NewExperimentsHandler(testMode, idx, nil, &runLauncher{cancelled: map[string]bool{}}, nil, passThrough)
	defer h.Close()
	resp := jobsRequest(t, func(rt *router.Router) { h.RegisterRoutes(rt) }, nil, "POST", "/api/bench/experiments/"+testExp+"/rerun-failures", "")
	if resp.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("status %d %s", resp.StatusCode(), resp.Body())
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("body %s: %v", resp.Body(), err)
	}
	want := "Rerun failures works on a comparison of two setups. For this comparison of 3 models, start a new comparison with the failed tasks."
	if body.Error.Message != want {
		t.Fatalf("message %q", body.Error.Message)
	}
}

// TestPlainErrStripsPrefixes: plainErr drops the internal sentinel prefixes.
func TestPlainErrStripsPrefixes(t *testing.T) {
	if got := plainErr(badf("tasks must be 1 to 50")); got != "tasks must be 1 to 50" {
		t.Fatalf("quickrun: %q", got)
	}
}
