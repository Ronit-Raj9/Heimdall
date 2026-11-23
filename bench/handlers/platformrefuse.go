package handlers

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Fix wave 3 (N216): an evaluation whose runs go to a cloud sandbox platform
// is refused before anything is saved when the platform cannot run one of
// its benchmarks, or cannot pull Heimdall's images at all, instead of every
// slot failing after launch in internal words.

// platformTargetResolver tells whether an evaluation of org whose resolved
// runner pool is worker runs on a sandbox platform: the platform's name
// ("" for the shared worker or an own worker) and whether that platform can
// pull Heimdall's images (an image registry is saved on its card). nil off
// the hosted platform.
var platformTargetResolver func(ctx context.Context, orgID, worker string) (platform string, imagesReady bool, err error)

// SetPlatformTargetResolver installs the hosted resolver
// (cmd/bifrost-bench/platformrefuse_hosted.go).
func SetPlatformTargetResolver(f func(ctx context.Context, orgID, worker string) (platform string, imagesReady bool, err error)) {
	platformTargetResolver = f
}

// platformBenchmarkRefusal is one refused benchmark of the 422 answer.
type platformBenchmarkRefusal struct {
	ID      string   `json:"id"`
	Reasons []string `json:"reasons"`
}

// platformRefusalBody is the 422 answer: the usual error object (so every
// client shows its message) plus each refused benchmark with its reasons.
type platformRefusalBody struct {
	Error      lib.ErrorField             `json:"error"`
	Benchmarks []platformBenchmarkRefusal `json:"benchmarks"`
}

// errTypeNotOnPlatform is the error type of the 422 answer.
const errTypeNotOnPlatform = "not_on_platform"

func init() {
	platformRefusal = refuseOnPlatform
}

// refuseOnPlatform is the platformRefusal hook. spec is normalized and its
// Worker already resolved to a runner pool.
func refuseOnPlatform(ctx context.Context, h *evaluationsHandler, spec *schemas.EvaluationSpec) (int, any, bool) {
	f := platformTargetResolver
	if f == nil || h == nil || spec == nil || h.env.Services.OrgID == "" {
		return 0, nil, false
	}
	platform, ready, err := f(ctx, h.env.Services.OrgID, spec.Worker)
	if err != nil {
		return fasthttp.StatusServiceUnavailable, platformRefusalBody{Error: lib.ErrorField{Type: lib.ErrTypeUnavailable,
			Message: "Could not check where this evaluation's runs go: " + err.Error()}, Benchmarks: []platformBenchmarkRefusal{}}, true
	}
	if platform == "" {
		return 0, nil, false
	}
	var out []platformBenchmarkRefusal
	if !ready {
		why := sandbox.NeedsRegistry(platform)
		for _, b := range spec.Benchmarks {
			out = append(out, platformBenchmarkRefusal{ID: benchID(b), Reasons: []string{why}})
		}
		return platformRefused(why, out)
	}
	agents := h.agentConfigs(ctx)
	cfgs := evalArmConfigs(spec.Arms, agents)
	for _, b := range spec.Benchmarks {
		if reasons := h.platformReasons(ctx, b, spec, cfgs); len(reasons) > 0 {
			out = append(out, platformBenchmarkRefusal{ID: benchID(b), Reasons: reasons})
		}
	}
	if len(out) == 0 {
		return 0, nil, false
	}
	first := out[0]
	msg := sandbox.PlatformTitle(platform) + " cannot run " + first.ID + ": " + first.Reasons[0] + ". Run it on the shared worker or your own worker."
	return platformRefused(msg, out)
}

func platformRefused(msg string, out []platformBenchmarkRefusal) (int, any, bool) {
	return fasthttp.StatusUnprocessableEntity, platformRefusalBody{Error: lib.ErrorField{Type: errTypeNotOnPlatform, Message: msg}, Benchmarks: out}, true
}

// benchID is a benchmark reference without its "@version".
func benchID(ref string) string {
	id, _, _ := strings.Cut(ref, "@")
	return id
}

// platformReasons is the plain reasons a sandbox cannot run the tasks the
// evaluation would pick from benchmark ref with the arms' agents, the same
// pick launchChild makes. A benchmark that does not load or has no task to
// run is left to the launch, which skips it with its reason.
func (h *evaluationsHandler) platformReasons(ctx context.Context, ref string, spec *schemas.EvaluationSpec, cfgs []*schemas.AgentConfig) []string {
	src := h.env.Services.Suites
	if src == nil {
		return nil
	}
	st, err := src.LoadSuite(ctx, benchID(ref))
	if err != nil || st == nil {
		return nil
	}
	ids, _ := runnableTasks(cfgs, st, admittedTasks(st, evalSplit(spec.Split)).IDs)
	ids = spreadPick(ids, spec.TasksPerBenchmark)
	var raw []string
	for _, id := range ids {
		t := st.TaskByID(id)
		for _, cfg := range cfgs {
			raw = append(raw, sandbox.Refuse(environments.DriverSlot{Suite: st, Task: t, Agent: cfg})...)
		}
		if len(cfgs) == 0 {
			raw = append(raw, sandbox.Refuse(environments.DriverSlot{Suite: st, Task: t})...)
		}
	}
	return sandbox.PlainRefusals(raw)
}
