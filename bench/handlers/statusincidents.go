package handlers

// Lane L10: server admins write the public status page's incident notes from
// the website, the same store `bifrost-bench status incident` writes.
//
//	POST /status/incidents        open an incident (server admins)
//	POST /status/incidents/{id}   add a note, change status or impact, resolve

import (
	"bytes"
	"errors"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/statuspage"
	"github.com/valyala/fasthttp"
)

// StatusIncidentsPath is the incident write route.
const StatusIncidentsPath = APIPrefix + "/status/incidents"

// OpenIncidentBody is the body of POST /status/incidents.
type OpenIncidentBody struct {
	Title        string     `json:"title"`
	Message      string     `json:"message"`
	Impact       string     `json:"impact,omitempty"`
	Status       string     `json:"status,omitempty"`
	Components   []string   `json:"components,omitempty"`
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
}

// UpdateIncidentBody is the body of POST /status/incidents/{id}.
type UpdateIncidentBody struct {
	Message string `json:"message"`
	Status  string `json:"status,omitempty"`
	Impact  string `json:"impact,omitempty"`
}

// statusIsAdmin is replaced in tests.
var statusIsAdmin = auth.IsServerAdmin

func (h *StatusPageHandler) registerIncidentRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.POST(StatusIncidentsPath, lib.ChainMiddlewares(h.openIncident, middlewares...))
	r.POST(StatusIncidentsPath+"/{id}", lib.ChainMiddlewares(h.updateIncident, middlewares...))
}

func (h *StatusPageHandler) incidentStore(ctx *fasthttp.RequestCtx) *statuspage.Store {
	if h.svc == nil || h.svc.Store() == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "the status page is not configured on this server")
		return nil
	}
	if !statusIsAdmin(auth.PrincipalFrom(ctx)) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"server admins only: the server owner lists admin emails in "+auth.ServerAdminsEnv)
		return nil
	}
	return h.svc.Store()
}

func (h *StatusPageHandler) openIncident(ctx *fasthttp.RequestCtx) {
	st := h.incidentStore(ctx)
	if st == nil {
		return
	}
	var body OpenIncidentBody
	if err := decodeJSONStrict(bytes.TrimSpace(ctx.PostBody()), &body); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	o := statuspage.Open{Title: body.Title, Message: body.Message, Impact: statuspage.Impact(body.Impact),
		Status: body.Status, Components: body.Components, ScheduledFor: body.ScheduledFor}
	if o.ScheduledFor != nil {
		if o.Status == "" {
			o.Status = statuspage.StatusScheduled
		}
		if o.Impact == "" {
			o.Impact = statuspage.ImpactMaintenance
		}
	}
	in, err := st.Open(o)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, in)
}

func (h *StatusPageHandler) updateIncident(ctx *fasthttp.RequestCtx) {
	st := h.incidentStore(ctx)
	if st == nil {
		return
	}
	id := strings.TrimSpace(pathParam(ctx, "id"))
	var body UpdateIncidentBody
	if err := decodeJSONStrict(bytes.TrimSpace(ctx.PostBody()), &body); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	in, err := st.Update(id, statuspage.Change{Status: body.Status, Impact: statuspage.Impact(body.Impact), Message: body.Message})
	if errors.Is(err, statuspage.ErrNotFound) {
		sendNotFound(ctx, h.mode, "incident", id)
		return
	}
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, in)
}
