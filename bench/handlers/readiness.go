package handlers

// Deploy area: process liveness and readiness for container health checks,
// load balancers and the hosted compose stack (deploy/hosted/,
// docs/hosting.md). Both routes sit at the site root, outside /api/bench, so
// a probe needs no session, token or organization.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Readiness routes.
const (
	HealthzPath = "/healthz"
	ReadyzPath  = "/readyz"
)

// readyTimeout bounds every readiness check together. A probe that waits
// longer than this is worse than a "not ready".
const readyTimeout = 5 * time.Second

// ReadyCheck is one condition the server needs before it takes traffic: the
// database answers, its migrations are current, the runs root is writable.
type ReadyCheck struct {
	Name  string
	Check func(ctx context.Context) error
}

// ReadyResult is one check's outcome in the /readyz answer.
type ReadyResult struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Readiness is the /readyz answer. Status is "ready" or "not_ready".
type Readiness struct {
	Status  string        `json:"status"`
	Version string        `json:"version"`
	Checks  []ReadyResult `json:"checks"`
}

// ReadinessHandler serves GET /healthz and GET /readyz.
type ReadinessHandler struct {
	mode    schemas.Mode
	version string
	checks  []ReadyCheck
}

// NewReadinessHandler creates the handler. With no checks /readyz answers
// ready as soon as the process serves.
func NewReadinessHandler(mode schemas.Mode, version string, checks []ReadyCheck) *ReadinessHandler {
	return &ReadinessHandler{mode: mode, version: version, checks: checks}
}

// RegisterRoutes registers GET and HEAD for /healthz and /readyz. Exact
// routes win over the UI's catch-all on the same router.
func (h *ReadinessHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(HealthzPath, lib.ChainMiddlewares(h.getHealthz, middlewares...))
	r.HEAD(HealthzPath, lib.ChainMiddlewares(h.getHealthz, middlewares...))
	r.GET(ReadyzPath, lib.ChainMiddlewares(h.getReadyz, middlewares...))
	r.HEAD(ReadyzPath, lib.ChainMiddlewares(h.getReadyz, middlewares...))
}

// getHealthz answers 200 while the process serves requests. It checks
// nothing else, so a restart loop is never triggered by a slow database.
func (h *ReadinessHandler) getHealthz(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.Health{Status: "ok", Version: h.version})
}

// getReadyz runs every check at once under one deadline and answers 200
// when all pass, 503 otherwise, with each check's outcome.
func (h *ReadinessHandler) getReadyz(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	work, cancel := workContext(ctx, readyTimeout)
	defer cancel()
	out := Readiness{Status: "ready", Version: h.version, Checks: RunReadyChecks(work, h.checks)}
	status := fasthttp.StatusOK
	for _, c := range out.Checks {
		if !c.OK {
			out.Status, status = "not_ready", fasthttp.StatusServiceUnavailable
		}
	}
	lib.SendJSON(ctx, h.mode, status, out)
}

// RunReadyChecks runs checks concurrently and returns their outcomes in the
// order given. A check that panics or outlives ctx counts as failed.
func RunReadyChecks(ctx context.Context, checks []ReadyCheck) []ReadyResult {
	out := make([]ReadyResult, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		out[i] = ReadyResult{Name: c.Name}
		if c.Check == nil {
			out[i].Error = "no check function"
			continue
		}
		wg.Add(1)
		go func(i int, c ReadyCheck) {
			defer wg.Done()
			done := make(chan error, 1)
			go func() {
				defer func() {
					if p := recover(); p != nil {
						done <- fmt.Errorf("check panicked: %v", p)
					}
				}()
				done <- c.Check(ctx)
			}()
			var err error
			select {
			case err = <-done:
			case <-ctx.Done():
				err = ctx.Err()
			}
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].OK = true
		}(i, c)
	}
	wg.Wait()
	return out
}

// RunsRootWritable checks that a file can be created, written, synced and
// removed in dir, the folder sealed runs are written under.
func RunsRootWritable(dir string) ReadyCheck {
	return ReadyCheck{Name: "runs_root", Check: func(context.Context) error {
		if dir == "" {
			return errors.New("runs root is not configured")
		}
		f, err := os.CreateTemp(dir, ".readyz-*")
		if err != nil {
			return fmt.Errorf("runs root %s is not writable: %w", dir, err)
		}
		name := f.Name()
		_, werr := f.WriteString("ok\n")
		if werr == nil {
			werr = f.Sync()
		}
		cerr := f.Close()
		rerr := os.Remove(name)
		if err := errors.Join(werr, cerr, rerr); err != nil {
			return fmt.Errorf("runs root %s is not writable: %w", dir, err)
		}
		return nil
	}}
}
