// Cheat check (W4-48): run an evaluation's arms once on the impossible twins
// of its benchmarks. A twin has no honest solution, so every pass is the
// agent changing the tests or otherwise cheating. Modelled on
// rerunFailures; the child is exploratory and names its parent in
// cheat_check_of.
package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/twins"
	"github.com/valyala/fasthttp"
)

// Sentences of POST /evaluations/{id}/cheat-check.
const (
	cheatCheckNoTwinsMsg = "None of this evaluation's benchmarks has impossible twins yet. Make them first."
	cheatCheckSuffix     = " (cheat check)"
)

// CheatCheckRequest is the body of POST /evaluations/{id}/cheat-check.
type CheatCheckRequest struct {
	Yes bool `json:"yes"`
	// BudgetUSD is the spend cap of the whole cheat check (round 4, F5):
	// required, so a cheat check never runs on the parent's budget unseen.
	BudgetUSD float64 `json:"budget_usd"`
}

// cheatCheckNoBudgetMsg refuses a cheat check without a spend cap.
const cheatCheckNoBudgetMsg = "Set a spending cap for the cheat check (budget_usd, more than 0)."

func init() {
	RegisterOrgArea("cheatcheck", func(env OrgEnv, r *router.Router) {
		h := &evaluationsHandler{env: env}
		r.POST(APIPrefix+"/evaluations/{id}/cheat-check", env.W(h.cheatCheck))
	})
}

// cheatCheckLaunch is LaunchEvaluation; tests replace it.
var cheatCheckLaunch = LaunchEvaluationPrepared

// twinBenchmarks returns, for each benchmark, its twin benchmark when that
// exists and has at least one admitted twin.
func twinBenchmarks(root string, benches []string) []string {
	var out []string
	for _, b := range benches {
		id := strings.SplitN(b, "@", 2)[0] + twins.SuiteSuffix
		if slices.Contains(out, id) || strings.HasSuffix(b, twins.SuiteSuffix) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, string(projectfs.AreaBenchmarks), id, "suite.lock"))
		if err != nil {
			continue
		}
		var lock schemas.SuiteLock
		if json.Unmarshal(raw, &lock) != nil {
			continue
		}
		for _, t := range lock.Tasks {
			if t.Status == schemas.TaskAdmitted {
				out = append(out, id)
				break
			}
		}
	}
	return out
}

// admittedTwins counts the admitted impossible twins of benchmark (a
// suite id, without the twin suffix); 0 when it has none or no root.
func admittedTwins(root, benchmark string) int {
	if root == "" {
		return 0
	}
	raw, err := os.ReadFile(filepath.Join(root, string(projectfs.AreaBenchmarks), benchmark+twins.SuiteSuffix, "suite.lock"))
	if err != nil {
		return 0
	}
	var lock schemas.SuiteLock
	if json.Unmarshal(raw, &lock) != nil {
		return 0
	}
	n := 0
	for _, t := range lock.Tasks {
		if t.Status == schemas.TaskAdmitted {
			n++
		}
	}
	return n
}

func (h *evaluationsHandler) cheatCheck(ctx *fasthttp.RequestCtx) {
	var req CheatCheckRequest
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, "request body: "+err.Error())
		return
	}
	if !req.Yes {
		h.fail(ctx, fasthttp.StatusBadRequest, `a cheat check runs your agents on your model keys: send {"yes": true} to start it`)
		return
	}
	if !(req.BudgetUSD > 0) || req.BudgetUSD > EvaluationMaxBudget {
		h.fail(ctx, fasthttp.StatusBadRequest, cheatCheckNoBudgetMsg)
		return
	}
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	benches := append([]string{}, ev.Spec.Benchmarks...)
	for _, c := range ev.Children {
		if !slices.Contains(benches, c.Benchmark) {
			benches = append(benches, c.Benchmark)
		}
	}
	tw := twinBenchmarks(h.root(), benches)
	if len(tw) == 0 {
		h.fail(ctx, fasthttp.StatusConflict, cheatCheckNoTwinsMsg)
		return
	}
	spec := ev.Spec
	spec.Benchmarks, spec.Pack, spec.Trials = tw, "", 1
	spec.BudgetUSD = req.BudgetUSD
	spec.Name = strings.TrimSuffix(ev.Spec.Name, cheatCheckSuffix) + cheatCheckSuffix
	// cheat_check_of is set before the child's first save, in the same
	// write that creates it: a status refresh can never drop it.
	child, err := cheatCheckLaunch(ctx, h.env, spec, principalOf(ctx), "", func(e *schemas.Evaluation) { e.CheatCheckOf = ev.ID })
	if err != nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, child)
}

// cheatCheckNotReleasable is the release preview's problem for a cheat check:
// its runs are on impossible twins and decide nothing.
const cheatCheckNotReleasable = "This is a cheat check on impossible tasks; it decides nothing and cannot be released."
