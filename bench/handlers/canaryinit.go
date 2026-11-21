package handlers

// Web parity lane L17: create and delete drift canary definitions from the
// website (canaries/<name>.yaml). The baseline and the looks run as jobs
// (canary.baseline, canary.run), queued with POST /jobs.

import (
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
	d.Name = strings.TrimSpace(d.Name)
	if d.Trials == 0 {
		d.Trials = 1
	}
	if !canary.ValidName(d.Name) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			"name must be lowercase letters, digits, - or _, starting with a letter or digit, at most 48 characters")
		return
	}
	root := h.env.Cfg.Root
	if d.Agent != "" {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(d.Agent)))
		if filepath.IsAbs(d.Agent) || clean == ".." || strings.HasPrefix(clean, "../") {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "agent must be a path inside the project, e.g. agents/codex.yaml")
			return
		}
		d.Agent = clean
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(clean))); err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
				"agent "+clean+" not found in the project; create it on the Agents page first")
			return
		}
	}
	if err := d.Validate(); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
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
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}
