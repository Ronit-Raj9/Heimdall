package packs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// apiPrefix is handlers.APIPrefix. It is repeated here so the handlers
// package can import packs (report cards use CapabilityOf).
const apiPrefix = "/api/bench"

// readTimeout bounds one GET /evaluation-packs.
const readTimeout = 30 * time.Second

// SuiteLister is the GET /suites list: every benchmark the organization can
// see, built-in library included. handlers.SuitesHandler satisfies it.
type SuiteLister interface {
	ListSummaries(ctx context.Context) ([]schemas.SuiteSummary, error)
}

// PackView is one pack in GET /evaluation-packs: the pack with its benchmarks
// resolved against what is available and the ready (admitted) task count of
// each.
type PackView struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Capability  string         `json:"capability"`
	Benchmarks  []string       `json:"benchmarks"`
	ReadyTasks  map[string]int `json:"ready_tasks"` // benchmark id to admitted task count
	TotalReady  int            `json:"total_ready"`
}

// Handler serves GET /api/bench/evaluation-packs.
type Handler struct {
	mode   schemas.Mode
	suites SuiteLister
}

// NewHandler creates the evaluation packs handler. suites may be nil.
func NewHandler(mode schemas.Mode, suites SuiteLister) *Handler {
	return &Handler{mode: mode, suites: suites}
}

// RegisterRoutes registers GET /evaluation-packs.
func (h *Handler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(apiPrefix+"/evaluation-packs", lib.ChainMiddlewares(h.getPacks, middlewares...))
}

func (h *Handler) getPacks(ctx *fasthttp.RequestCtx) {
	if h.suites == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"suite loader is not available on this server (mode "+string(h.mode)+")")
		return
	}
	work, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	sums, err := h.suites.ListSummaries(work)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list benchmarks: "+err.Error())
		return
	}
	// The contract and the UI read a bare JSON array, so lib.SendJSON (which
	// merges "mode" into an object) is not used; mode rides in the header.
	body, err := json.Marshal(Views(sums))
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "encode response: "+err.Error())
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json; charset=utf-8")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetBody(append(body, '\n'))
}

// Views resolves every pack against the loadable benchmarks in sums. A
// benchmark that failed to load, or has no ready (admitted) task, is left
// out: a pack never offers what cannot run (the Benchmarks page lists those
// as not ready yet). Every pack is listed, even one with no benchmark
// available, so the UI can show it greyed.
func Views(sums []schemas.SuiteSummary) []PackView {
	ready := make(map[string]int, len(sums))
	ids := make([]string, 0, len(sums))
	for _, s := range sums {
		if s.ID == "" || s.Error != "" || s.AdmittedCount <= 0 {
			continue
		}
		ids = append(ids, s.ID)
		ready[s.ID] = s.AdmittedCount
	}
	out := make([]PackView, 0, len(all))
	for _, p := range All() {
		v := PackView{ID: p.ID, Name: p.Name, Description: p.Description, Capability: p.Capability,
			Benchmarks: Resolve(p.ID, ids), ReadyTasks: map[string]int{}}
		if v.Benchmarks == nil {
			v.Benchmarks = []string{}
		}
		for _, b := range v.Benchmarks {
			v.ReadyTasks[b] = ready[b]
			v.TotalReady += ready[b]
		}
		out = append(out, v)
	}
	return out
}
