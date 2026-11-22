package handlers

// Scheduled checks control (fix wave lane F17): pause and resume a web
// schedule or a drift monitor, and edit a drift monitor in place. A paused
// entry stays in its file with paused: true; the server ticker skips it
// (cmd/bifrost-bench/jobs_canary.go) and Run now still works.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

func init() {
	RegisterOrgArea("canarycontrol", func(env OrgEnv, r *router.Router) {
		h := &canaryControlHandler{env: env}
		r.POST(APIPrefix+"/schedules/{name}/pause", env.W(h.pauseSchedule))
		r.POST(APIPrefix+"/schedules/{name}/resume", env.W(h.resumeSchedule))
		r.POST(APIPrefix+"/canaries/{name}/pause", env.W(h.pauseCanary))
		r.POST(APIPrefix+"/canaries/{name}/resume", env.W(h.resumeCanary))
		r.PUT(APIPrefix+"/canaries/{name}", env.W(h.edit))
	})
}

type canaryControlHandler struct{ env OrgEnv }

func (h *canaryControlHandler) pauseSchedule(ctx *fasthttp.RequestCtx) {
	h.setSchedulePaused(ctx, true)
}
func (h *canaryControlHandler) resumeSchedule(ctx *fasthttp.RequestCtx) {
	h.setSchedulePaused(ctx, false)
}
func (h *canaryControlHandler) pauseCanary(ctx *fasthttp.RequestCtx)  { h.setCanaryPaused(ctx, true) }
func (h *canaryControlHandler) resumeCanary(ctx *fasthttp.RequestCtx) { h.setCanaryPaused(ctx, false) }

// setSchedulePaused pauses or resumes one schedule and answers it. A
// canary-<name> schedule is a drift monitor's: its definition changes.
func (h *canaryControlHandler) setSchedulePaused(ctx *fasthttp.RequestCtx, paused bool) {
	name, _ := ctx.UserValue("name").(string)
	root := h.env.Cfg.Root
	if cn, ok := strings.CutPrefix(name, "canary-"); ok && canary.ValidName(cn) {
		if _, err := os.Stat(canary.Path(root, cn)); err == nil {
			d, ok := h.saveCanaryPaused(ctx, cn, paused)
			if !ok {
				return
			}
			lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, schedule.Schedule{
				Name: d.ScheduleName(), Experiment: canary.DefaultDir + "/" + d.Name + ".yaml", Cron: d.Schedule.Cron,
				Timezone: d.Schedule.Timezone, MaxUSDPerDay: d.Budget.PerDayUSD, Paused: d.Schedule.Paused,
			})
			return
		}
	}
	f, err := LoadWebSchedules(root)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	i := -1
	for k, s := range f.Schedules {
		if s.Name == name {
			i = k
		}
	}
	if i < 0 {
		full := &schedule.File{Schedules: []schedule.Schedule{}}
		if ScheduleExtras != nil {
			ScheduleExtras(root, full)
		}
		if _, ok := full.Find(name); ok {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
				"schedule "+name+" belongs to a live check; pause it on the Live checks tab")
			return
		}
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "schedule "+name+" not found")
		return
	}
	f.Schedules[i].Paused = paused
	data, err := yaml.Marshal(f)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	data = append([]byte("# Web schedules (Schedules page). Alerts name org secrets or environment\n# variables only; never put a webhook URL or a secret in this file.\n"), data...)
	if err := (&schedulesHandler{env: h.env}).write(ctx, data); err != nil {
		sendProjectErr(ctx, h.env.Mode, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, f.Schedules[i])
}

// setCanaryPaused pauses or resumes a drift monitor's schedule and answers
// its definition.
func (h *canaryControlHandler) setCanaryPaused(ctx *fasthttp.RequestCtx, paused bool) {
	name, _ := ctx.UserValue("name").(string)
	if d, ok := h.saveCanaryPaused(ctx, name, paused); ok {
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, d)
	}
}

// saveCanaryPaused loads, changes and saves one definition; it answers the
// error and returns false when it can not.
func (h *canaryControlHandler) saveCanaryPaused(ctx *fasthttp.RequestCtx, name string, paused bool) (*canary.Definition, bool) {
	d, ok := h.load(ctx, name)
	if !ok {
		return nil, false
	}
	if d.Schedule == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			"drift monitor "+name+" has no schedule to pause; edit it and set when it runs")
		return nil, false
	}
	d.Schedule.Paused = paused
	if err := canary.Save(canary.Path(h.env.Cfg.Root, name), d); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "save drift monitor: "+err.Error())
		return nil, false
	}
	return d, true
}

// load reads one definition, answering 404 when it does not exist.
func (h *canaryControlHandler) load(ctx *fasthttp.RequestCtx, name string) (*canary.Definition, bool) {
	if !canary.ValidName(name) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "drift monitor "+name+" not found")
		return nil, false
	}
	p := canary.Path(h.env.Cfg.Root, name)
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "drift monitor "+name+" not found")
		return nil, false
	}
	d, err := canary.Load(p)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return nil, false
	}
	return d, true
}

// edit handles PUT /canaries/{name}: the create body, validated the same
// way; the name in the path wins, the paused state and the saved baselines
// and checks are kept.
func (h *canaryControlHandler) edit(ctx *fasthttp.RequestCtx) {
	name, _ := ctx.UserValue("name").(string)
	old, ok := h.load(ctx, name)
	if !ok {
		return
	}
	var in CanaryInitRequest
	if err := json.Unmarshal(ctx.PostBody(), &in); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be a canary definition as JSON: "+err.Error())
		return
	}
	d := in.Definition
	d.Name = name
	if d.Schedule != nil && old.Schedule != nil {
		d.Schedule.Paused = old.Schedule.Paused
	}
	if !checkCanaryDefinition(ctx, h.env, &d) {
		return
	}
	if err := canary.Save(canary.Path(h.env.Cfg.Root, name), &d); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, d)
}
