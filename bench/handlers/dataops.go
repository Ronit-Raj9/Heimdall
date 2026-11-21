package handlers

// Data lifecycle inside an organization (lane X16, audit N10): delete a
// comparison (experiment) with every run, trace and gateway log it holds,
// delete an evaluation with its comparisons, and the org's retention
// setting, which the server's sweeper applies (cmd/bifrost-bench
// dataops.go). The audit log is never touched: the dispatcher writes the
// row of each delete.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// DataEraser removes stored data of one org.
type DataEraser interface {
	// EraseExperiment removes the experiment's folder (its runs, traces,
	// gateway logs and evidence), its mirrored copy, and its index rows.
	// schemas.ErrNotFound when it does not exist.
	EraseExperiment(ctx context.Context, expID string) error
}

// Retention limits.
const (
	RetentionMinDays = 1
	RetentionMaxDays = 3650
)

// RetentionInfo is GET and PUT /data/retention. RunDays 0 keeps forever.
type RetentionInfo struct {
	RunDays     int        `json:"run_days"`
	MinDays     int        `json:"min_days"`
	MaxDays     int        `json:"max_days"`
	LastSweepAt *time.Time `json:"last_sweep_at,omitempty"`
	LastDeleted int        `json:"last_deleted"`
	UpdatedBy   string     `json:"updated_by,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

// RetentionRequest is the body of PUT /data/retention.
type RetentionRequest struct {
	RunDays int `json:"run_days"`
}

// DeletedInfo answers a delete.
type DeletedInfo struct {
	Deleted     string   `json:"deleted"`
	Kind        string   `json:"kind"` // experiment | evaluation
	Experiments []string `json:"experiments,omitempty"`
}

// RetentionPath is the org's retention setting file, in the project's
// hidden folder (never served by the files API).
func RetentionPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".heimdall", "retention.json")
}

// ReadRetention reads the setting; a missing file keeps forever.
func ReadRetention(projectRoot string) (RetentionInfo, error) {
	out := RetentionInfo{}
	b, err := os.ReadFile(RetentionPath(projectRoot))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &out); err != nil {
			return out, fmt.Errorf("retention setting: %w", err)
		}
	}
	out.MinDays, out.MaxDays = RetentionMinDays, RetentionMaxDays
	return out, nil
}

// WriteRetention stores the setting atomically.
func WriteRetention(projectRoot string, r RetentionInfo) error {
	p := RetentionPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".retention-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

type dataOpsHandler struct{ env OrgEnv }

func init() {
	RegisterOrgArea("dataops", func(env OrgEnv, r *router.Router) {
		h := &dataOpsHandler{env: env}
		r.DELETE(APIPrefix+"/experiments/{id}", env.W(h.deleteExperiment))
		r.DELETE(APIPrefix+"/evaluations/{id}", env.W(h.deleteEvaluation))
		r.GET(APIPrefix+"/data/retention", env.R(h.getRetention))
		r.PUT(APIPrefix+"/data/retention", env.W(h.putRetention))
	})
}

func (h *dataOpsHandler) root() string {
	if p := h.env.Services.Projects; p != nil {
		return p.Root()
	}
	if h.env.Cfg != nil {
		return h.env.Cfg.Root
	}
	return ""
}

// active reports whether an experiment is still queued or running.
func (h *dataOpsHandler) active(expID string) bool {
	ar, ok := h.env.Services.Launcher.(ActivityReporter)
	return ok && ar.Active(expID)
}

func (h *dataOpsHandler) deleteExperiment(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if h.env.Services.Data == nil {
		sendUnavailable(ctx, h.env.Mode, "data deletion")
		return
	}
	if h.active(id) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "comparison "+id+" is still running; cancel it first, then delete it")
		return
	}
	wctx, cancel := workContext(ctx, 2*time.Minute)
	defer cancel()
	if err := h.env.Services.Data.EraseExperiment(wctx, id); err != nil {
		sendDepError(ctx, h.env.Mode, "delete comparison", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, DeletedInfo{Deleted: id, Kind: "experiment"})
}

func (h *dataOpsHandler) deleteEvaluation(ctx *fasthttp.RequestCtx) {
	ev := (&evaluationsHandler{env: h.env, now: time.Now})
	e, ok := ev.load(ctx)
	if !ok {
		return
	}
	ev.refresh(e)
	if !terminalEvaluation(e.Status) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "evaluation "+e.ID+" is "+e.Status+"; cancel it first, then delete it")
		return
	}
	var exps []string
	for _, c := range e.Children {
		if c.ExperimentID == "" {
			continue
		}
		if h.active(c.ExperimentID) {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "comparison "+c.ExperimentID+" of this evaluation is still running; cancel it first")
			return
		}
		exps = append(exps, c.ExperimentID)
	}
	if len(exps) > 0 && h.env.Services.Data == nil {
		sendUnavailable(ctx, h.env.Mode, "data deletion")
		return
	}
	wctx, cancel := workContext(ctx, 5*time.Minute)
	defer cancel()
	for _, id := range exps {
		if err := h.env.Services.Data.EraseExperiment(wctx, id); err != nil && !errors.Is(err, schemas.ErrNotFound) {
			sendDepError(ctx, h.env.Mode, "delete comparison "+id, err)
			return
		}
	}
	if err := ev.store().Delete(e.ID); err != nil && !errors.Is(err, evalstore.ErrNotFound) {
		sendDepError(ctx, h.env.Mode, "delete evaluation", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, DeletedInfo{Deleted: e.ID, Kind: "evaluation", Experiments: exps})
}

func (h *dataOpsHandler) getRetention(ctx *fasthttp.RequestCtx) {
	root := h.root()
	if root == "" {
		sendUnavailable(ctx, h.env.Mode, "retention (no project folder)")
		return
	}
	r, err := ReadRetention(root)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "retention", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, r)
}

func (h *dataOpsHandler) putRetention(ctx *fasthttp.RequestCtx) {
	root := h.root()
	if root == "" {
		sendUnavailable(ctx, h.env.Mode, "retention (no project folder)")
		return
	}
	var req RetentionRequest
	if !decodeStrict(ctx, h.env.Mode, &req, "retention") {
		return
	}
	if req.RunDays != 0 && (req.RunDays < RetentionMinDays || req.RunDays > RetentionMaxDays) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			fmt.Sprintf("run_days: 0 (keep forever) or %d to %d", RetentionMinDays, RetentionMaxDays))
		return
	}
	r, err := ReadRetention(root)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "retention", err)
		return
	}
	now := time.Now().UTC()
	r.RunDays, r.UpdatedAt = req.RunDays, &now
	if a := actorOf(ctx); a != "" {
		r.UpdatedBy = a
	}
	if err := WriteRetention(root, r); err != nil {
		sendDepError(ctx, h.env.Mode, "retention", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, r)
}
