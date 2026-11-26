package handlers

// W4 feature 3: the new model watch of an evaluation schedule.
//
//	GET  /gateway/model-watch/{schedule}         the watch's state file
//	POST /gateway/model-watch/{schedule}/retry   forget one failed or skipped model
//
// The schedule itself is created and edited with the schedules routes; the
// watch runs in the schedule runner (cmd/bifrost-bench/schedule_newmodel.go).

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/valyala/fasthttp"
)

// modelWatchLockFresh matches the runner's lock: a lock file younger than
// this means a check of the watch is running.
const modelWatchLockFresh = 10 * time.Minute

type modelWatchHandler struct{ env OrgEnv }

// ModelWatchRetry is the body of POST /gateway/model-watch/{schedule}/retry.
type ModelWatchRetry struct {
	Model string `json:"model"` // "<provider>/<model>"
}

func init() {
	RegisterOrgArea("modelwatch", func(env OrgEnv, r *router.Router) {
		h := &modelWatchHandler{env: env}
		r.GET(APIPrefix+"/gateway/model-watch/{schedule}", env.R(h.get))
		r.POST(APIPrefix+"/gateway/model-watch/{schedule}/retry", env.W(h.retry))
	})
}

func (h *modelWatchHandler) root() string { return h.env.Cfg.Root }

// watchSchedule finds the {schedule} schedule with a new model watch; it
// has sent the error and returns false when there is none.
func (h *modelWatchHandler) watchSchedule(ctx *fasthttp.RequestCtx) (string, bool) {
	name, _ := ctx.UserValue("schedule").(string)
	if name == "" {
		name, _ = ctx.UserValue("name").(string)
	}
	f, err := LoadWebSchedules(h.root())
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return "", false
	}
	for _, s := range f.Schedules {
		if s.Name == name && s.NewModels != nil {
			return name, true
		}
	}
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "this schedule does not watch for new models")
	return "", false
}

func (h *modelWatchHandler) load(ctx *fasthttp.RequestCtx, name string) (*schedule.WatchState, bool) {
	st, err := schedule.LoadWatchState(h.root(), name)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return nil, false
	}
	if st == nil {
		st = &schedule.WatchState{Schedule: name, Baseline: map[string][]string{}, Seen: map[string]schedule.WatchItem{}}
	}
	return st, true
}

func (h *modelWatchHandler) get(ctx *fasthttp.RequestCtx) {
	name, ok := h.watchSchedule(ctx)
	if !ok {
		return
	}
	st, ok := h.load(ctx, name)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, st)
}

func (h *modelWatchHandler) retry(ctx *fasthttp.RequestCtx) {
	name, ok := h.watchSchedule(ctx)
	if !ok {
		return
	}
	var body ModelWatchRetry
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil || body.Model == "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, `the body must name the model as {"model": "<provider>/<model>"}`)
		return
	}
	if fi, err := os.Stat(schedule.WatchStatePath(h.root(), name) + ".lock"); err == nil && time.Since(fi.ModTime()) < modelWatchLockFresh {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "a check of this watch is running; try again in a minute")
		return
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	st, ok := h.load(ctx, name)
	if !ok {
		return
	}
	it, found := st.Seen[body.Model]
	if !found {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "the watch has no record of this model")
		return
	}
	if it.State != schedule.WatchFailed && it.State != schedule.WatchSkipped {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "only a model that failed or was skipped can be tried again")
		return
	}
	delete(st.Seen, body.Model)
	if err := schedule.SaveWatchState(h.root(), st); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, st)
}
