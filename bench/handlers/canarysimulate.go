package handlers

// Round 5 lane L8: "What if" on a drift monitor. POST /canaries/simulate runs
// canary.Simulate (the Monte Carlo study behind `canary simulate`) with
// bounds that keep one request to about a second. It reads no stored data:
// it is a study of the alarm rule for a monitor of this shape.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// Web bounds of the study.
const (
	simMaxTasks    = 50
	simMaxTrials   = 5
	simTriggers    = 60
	simDefaultReps = 200
)

// CanarySimulateRequest is the body of POST /canaries/simulate.
type CanarySimulateRequest struct {
	Tasks       int     `json:"tasks"`
	Trials      int     `json:"trials,omitempty"`
	Sensitivity string  `json:"sensitivity,omitempty"`
	Drop        float64 `json:"drop,omitempty"`         // true pass rate drop on every task, 0 to 1
	LatencyRise float64 `json:"latency_rise,omitempty"` // true relative latency rise, 0 to 5
}

func init() {
	RegisterOrgArea("canarysimulate", func(env OrgEnv, r *router.Router) {
		r.POST(APIPrefix+"/canaries/simulate", env.W(func(ctx *fasthttp.RequestCtx) { canarySimulateRoute(ctx, env) }))
	})
	APIRoutes = append(APIRoutes,
		RouteDoc{Auth: AuthWrite, Method: "POST", Path: APIPrefix + "/canaries/simulate", Tag: "canaries", Summary: "What if: simulate the alarm rule of a drift monitor of this shape (false alarm chance with no drift, detection with a drop); no stored data is read and nothing runs", Request: CanarySimulateRequest{}, Response: canary.SimResult{}},
	)
}

// simConfig validates the body and turns it into the study's config.
func simConfig(in CanarySimulateRequest) (canary.SimConfig, error) {
	if in.Trials == 0 {
		in.Trials = 1
	}
	switch {
	case in.Tasks < 1 || in.Tasks > simMaxTasks:
		return canary.SimConfig{}, fmt.Errorf("tasks must be 1 to %d", simMaxTasks)
	case in.Trials < 1 || in.Trials > simMaxTrials:
		return canary.SimConfig{}, fmt.Errorf("trials must be 1 to %d", simMaxTrials)
	case in.Drop < 0 || in.Drop > 1:
		return canary.SimConfig{}, fmt.Errorf("drop must be 0 to 1")
	case in.LatencyRise < 0 || in.LatencyRise > 5:
		return canary.SimConfig{}, fmt.Errorf("latency_rise must be 0 to 5")
	case in.Sensitivity != "" && !slices.Contains(canary.Sensitivities, in.Sensitivity):
		return canary.SimConfig{}, fmt.Errorf("sensitivity must be one of %s", strings.Join(canary.Sensitivities, ", "))
	}
	return canary.SimConfig{Tasks: in.Tasks, Trials: in.Trials, Triggers: simTriggers, Reps: simDefaultReps,
		Sensitivity: in.Sensitivity, DropPoints: in.Drop, ShiftRel: in.LatencyRise, Seed: 1}, nil
}

func canarySimulateRoute(ctx *fasthttp.RequestCtx, env OrgEnv) {
	var in CanarySimulateRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		lib.SendError(ctx, env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	c, err := simConfig(in)
	if err != nil {
		lib.SendError(ctx, env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, env.Mode, fasthttp.StatusOK, canary.Simulate(c))
}
