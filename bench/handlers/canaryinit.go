package handlers

// Web parity lane L17: create and delete drift canary definitions from the
// website (canaries/<name>.yaml). The baseline and the looks run as jobs
// (canary.baseline, canary.run), queued with POST /jobs.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// CanaryInitRequest is POST /canaries: a definition plus overwrite.
type CanaryInitRequest struct {
	canary.Definition
	Force bool `json:"force,omitempty"`
}

// CanaryInitResponse answers a written definition.
type CanaryInitResponse struct {
	Name string `json:"name"`
	Path string `json:"path"` // relative to the project root
	Next string `json:"next"` // what to do next
}

func init() {
	RegisterOrgArea("canaryinit", func(env OrgEnv, r *router.Router) {
		h := &canaryInitHandler{env: env}
		r.POST(APIPrefix+"/canaries", env.W(h.create))
		r.DELETE(APIPrefix+"/canaries/{name}", env.W(h.remove))
	})
}

type canaryInitHandler struct{ env OrgEnv }

func (h *canaryInitHandler) create(ctx *fasthttp.RequestCtx) {
	var in CanaryInitRequest
	if err := json.Unmarshal(ctx.PostBody(), &in); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be a canary definition as JSON: "+err.Error())
		return
	}
	d := in.Definition
	if !checkCanaryDefinition(ctx, h.env, &d) {
		return
	}
	root := h.env.Cfg.Root
	p := canary.Path(root, d.Name)
	if _, err := os.Stat(p); err == nil && !in.Force {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "canary "+d.Name+" already exists; tick overwrite to replace it")
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "create canaries folder: "+err.Error())
		return
	}
	if err := canary.Save(p, &d); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, CanaryInitResponse{Name: d.Name, Path: canary.DefaultDir + "/" + d.Name + ".yaml",
		Next: "record a baseline (canary.baseline job), then run looks or let the schedule run them"})
}

// CanaryProblemsBody is the 422 answer of a refused drift monitor (create
// and edit): every problem at once, each under its form field, and the same
// sentences joined by newlines in error.message for clients that read only
// the error shape.
type CanaryProblemsBody struct {
	Error    lib.ErrorField   `json:"error"`
	Problems []canary.Problem `json:"problems"`
}

// checkCanaryDefinition normalizes and checks a definition from the website
// (create and edit): the agent file and the benchmark against the project,
// plus every problem of the definition itself (Definition.Problems). It
// answers one 422 that lists every problem and returns false when the
// definition is refused.
func checkCanaryDefinition(ctx *fasthttp.RequestCtx, env OrgEnv, d *canary.Definition) bool {
	d.Name = strings.TrimSpace(d.Name)
	d.Suite = strings.TrimSpace(d.Suite)
	if d.Trials == 0 {
		d.Trials = 1
	}
	var problems []canary.Problem
	root := env.Cfg.Root
	if d.Agent != "" {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(d.Agent)))
		if filepath.IsAbs(d.Agent) || clean == ".." || strings.HasPrefix(clean, "../") {
			problems = append(problems, canary.Problem{Field: "agent", Message: "Pick an agent from the list; the agent must be a file inside the project."})
		} else {
			d.Agent = clean
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(clean))); err != nil {
				problems = append(problems, canary.Problem{Field: "agent", Message: "Agent " + clean + " is not in this project. Create it on the Agents page first."})
			}
		}
	}
	if msg := canaryBenchmarkProblem(ctx, env.Services.Suites, d.Suite); msg != "" {
		problems = append(problems, canary.Problem{Field: "suite", Message: msg})
	}
	problems = append(problems, d.Problems()...)
	if len(problems) == 0 {
		// Problems refuses at least what Validate refuses; this keeps the
		// CLI rule as the last word should the two ever differ.
		if err := d.Validate(); err != nil {
			problems = append(problems, canary.Problem{Field: "definition", Message: err.Error()})
		}
	}
	if len(problems) == 0 {
		return true
	}
	msgs := make([]string, len(problems))
	for i, p := range problems {
		msgs[i] = p.Message
	}
	lib.SendJSON(ctx, env.Mode, fasthttp.StatusUnprocessableEntity, CanaryProblemsBody{
		Error:    lib.ErrorField{Message: strings.Join(msgs, "\n"), Type: lib.ErrTypeBadRequest},
		Problems: problems,
	})
	return false
}

// canaryBenchmarkProblem says why a canary's benchmark ref ("id" or
// "id@version") does not name a benchmark of the org, or "". A path ref, an
// empty ref (Validate names it) and a server without a suite source are not
// checked here.
func canaryBenchmarkProblem(ctx context.Context, src SuiteSource, ref string) string {
	if src == nil || ref == "" || strings.ContainsAny(ref, `/\`) {
		return ""
	}
	id, version, _ := strings.Cut(ref, "@")
	s, err := src.LoadSuite(ctx, id)
	if err != nil || s == nil {
		return "Benchmark " + ref + " does not exist. Pick one from the Benchmarks page."
	}
	if version != "" && s.Version != version {
		return "Benchmark " + id + " has no version " + version + "."
	}
	return ""
}

// canaryStatePath is one canary's saved state (baselines, check records)
// under the runs root, as cmd/bifrost-bench keeps it (.canaries/<name>).
func canaryStatePath(cfg *lib.Config, name string) string {
	return filepath.Join(cfg.Path(cfg.RunsRoot), ".canaries", name)
}

func (h *canaryInitHandler) remove(ctx *fasthttp.RequestCtx) {
	name, _ := ctx.UserValue("name").(string)
	if !canary.ValidName(name) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "canary "+name+": "+schemas.ErrNotFound.Error())
		return
	}
	err := os.Remove(canary.Path(h.env.Cfg.Root, name))
	if errors.Is(err, fs.ErrNotExist) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "canary "+name+" not found")
		return
	}
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "delete canary: "+err.Error())
		return
	}
	// Its baselines and check records go too, so a monitor created again
	// with the same name starts clean.
	if h.env.Cfg.RunsRoot != "" {
		if err := os.RemoveAll(canaryStatePath(h.env.Cfg, name)); err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "delete canary state: "+err.Error())
			return
		}
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}
