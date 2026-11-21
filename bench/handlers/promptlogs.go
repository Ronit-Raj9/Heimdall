package handlers

// Prompt logging area (lane X03, audit PROD2 N4). Each organization chooses
// whether the shared gateway keeps the prompt and response text of its model
// calls, and for how many days it keeps the organization's gateway log rows.
// The organization can list its own rows (metadata only) and delete all of
// them. Rows are tagged with the organization by the runner
// (gateways/bifrost/orglogs.go), and every gateway call here filters on that
// tag (promptlogs package): an organization never reaches another's rows.

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/promptlogs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// PromptLoggingPath and PromptLogsPath are the routes.
const (
	PromptLoggingPath = APIPrefix + "/prompt-logging"
	PromptLogsPath    = APIPrefix + "/prompt-logs"
)

// PromptLogging is GET and PUT /prompt-logging.
type PromptLogging struct {
	schemas.PromptLogPolicy
	// GatewayLogs is true when this server can reach the shared gateway's
	// log store (list and delete work).
	GatewayLogs      bool `json:"gateway_logs"`
	MaxRetentionDays int  `json:"max_retention_days"`
}

// PromptLogsDeleted is DELETE /prompt-logs.
type PromptLogsDeleted struct {
	Deleted int `json:"deleted"`
}

var (
	promptLogsMu       sync.RWMutex
	promptLogsSettings promptlogs.SettingsStore
	promptLogsClient   *promptlogs.Client
)

// SetPromptLogs installs the settings store the policy lives in and the
// shared gateway's log client (nil: no shared gateway on this server). The
// hosted serve sets both.
func SetPromptLogs(st promptlogs.SettingsStore, c *promptlogs.Client) {
	promptLogsMu.Lock()
	defer promptLogsMu.Unlock()
	promptLogsSettings, promptLogsClient = st, c
}

func promptLogDeps() (promptlogs.SettingsStore, *promptlogs.Client) {
	promptLogsMu.RLock()
	defer promptLogsMu.RUnlock()
	return promptLogsSettings, promptLogsClient
}

// OrgPromptLogPolicy is the organization's policy as the lease credentials
// route hands it to a runner: the stored one, or the default.
func OrgPromptLogPolicy(ctx context.Context, orgID string) schemas.PromptLogPolicy {
	st, _ := promptLogDeps()
	p, err := promptlogs.LoadPolicy(ctx, st, orgID)
	if err != nil {
		return schemas.DefaultPromptLogPolicy()
	}
	return p
}

type promptLogsHandler struct{ env OrgEnv }

func init() {
	RegisterOrgArea("promptlogs", func(env OrgEnv, r *router.Router) {
		h := &promptLogsHandler{env: env}
		r.GET(PromptLoggingPath, env.R(h.getPolicy))
		r.PUT(PromptLoggingPath, env.W(h.putPolicy))
		r.GET(PromptLogsPath, env.R(h.list))
		r.DELETE(PromptLogsPath, env.W(h.purge))
	})
}

func (h *promptLogsHandler) org(ctx *fasthttp.RequestCtx) string {
	if h.env.Services.OrgID != "" {
		return h.env.Services.OrgID
	}
	if p := auth.PrincipalFrom(ctx); p != nil && p.OrgID != "" {
		return p.OrgID
	}
	return auth.LocalOrgID
}

func (h *promptLogsHandler) view(p schemas.PromptLogPolicy) PromptLogging {
	_, c := promptLogDeps()
	return PromptLogging{PromptLogPolicy: p, GatewayLogs: c != nil, MaxRetentionDays: schemas.PromptLogRetentionMax}
}

func (h *promptLogsHandler) getPolicy(ctx *fasthttp.RequestCtx) {
	st, _ := promptLogDeps()
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	p, err := promptlogs.LoadPolicy(work, st, h.org(ctx))
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the prompt logging setting failed")
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.view(p))
}

func (h *promptLogsHandler) putPolicy(ctx *fasthttp.RequestCtx) {
	st, _ := promptLogDeps()
	if st == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"prompt logging settings are not available on this server")
		return
	}
	var in schemas.PromptLogPolicy
	if !decodeStrict(ctx, h.env.Mode, &in, "prompt logging setting") {
		return
	}
	if why := promptlogs.ValidatePolicy(in); why != "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, why)
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := promptlogs.SavePolicy(work, st, h.org(ctx), in); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "save the prompt logging setting failed")
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.view(in))
}

func (h *promptLogsHandler) client(ctx *fasthttp.RequestCtx) *promptlogs.Client {
	_, c := promptLogDeps()
	if c == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"this server keeps no shared gateway logs: runs use your own worker's gateway")
	}
	return c
}

func (h *promptLogsHandler) list(ctx *fasthttp.RequestCtx) {
	c := h.client(ctx)
	if c == nil {
		return
	}
	q := promptlogs.Query{Limit: 50}
	if v, err := strconv.Atoi(string(ctx.QueryArgs().Peek("limit"))); err == nil {
		q.Limit = v
	}
	if v, err := strconv.Atoi(string(ctx.QueryArgs().Peek("offset"))); err == nil {
		q.Offset = v
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	page, err := c.List(work, h.org(ctx), q)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, "the gateway log store did not answer")
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, page)
}

// purgeTimeout bounds one delete request; a larger backlog is finished by
// the retention sweep or a second request.
const purgeTimeout = 2 * time.Minute

func (h *promptLogsHandler) purge(ctx *fasthttp.RequestCtx) {
	c := h.client(ctx)
	if c == nil {
		return
	}
	var before time.Time
	if s := string(ctx.QueryArgs().Peek("before")); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "before must be an RFC 3339 time")
			return
		}
		before = t
	}
	work, cancel := workContext(ctx, purgeTimeout)
	defer cancel()
	n, err := c.Purge(work, h.org(ctx), before)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable,
			"the gateway log store stopped answering after "+strconv.Itoa(n)+" rows were deleted; try again")
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, PromptLogsDeleted{Deleted: n})
}
