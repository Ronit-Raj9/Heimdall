package handlers

import (
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// doctorTimeout bounds the preflight checks; they call Docker and the gateway.
const doctorTimeout = 60 * time.Second

// HealthHandler serves liveness and the preflight checks.
type HealthHandler struct {
	mode    schemas.Mode
	version string
	doctor  DoctorFunc
	launch  *bool
}

// NewHealthHandler creates the health handler. doctor may be nil.
func NewHealthHandler(mode schemas.Mode, version string, doctor DoctorFunc) *HealthHandler {
	return &HealthHandler{mode: mode, version: version, doctor: doctor}
}

// WithLaunch records whether this server launches experiments, so the UI can
// hide the authoring workflow on an offline server.
func (h *HealthHandler) WithLaunch(launch bool) *HealthHandler {
	h.launch = &launch
	return h
}

// RegisterRoutes registers GET /health and GET /doctor.
func (h *HealthHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/health", lib.ChainMiddlewares(h.getHealth, middlewares...))
	r.GET(APIPrefix+"/doctor", lib.ChainMiddlewares(h.getDoctor, middlewares...))
}

// getHealth handles GET /api/bench/health.
func (h *HealthHandler) getHealth(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.Health{Status: "ok", Version: h.version, Launch: h.launch})
}

// getDoctor handles GET /api/bench/doctor. It always answers 200: a failed
// check is data for the preflight card, not a server error.
func (h *HealthHandler) getDoctor(ctx *fasthttp.RequestCtx) {
	if h.doctor == nil {
		sendUnavailable(ctx, h.mode, "doctor")
		return
	}
	work, cancel := workContext(ctx, doctorTimeout)
	defer cancel()
	checks := h.doctor(work)
	if checks == nil {
		checks = []schemas.DoctorCheck{}
	}
	ok := true
	for _, c := range checks {
		if c.Status == schemas.DoctorFail {
			ok = false
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.Doctor{OK: ok, Checks: checks})
}
