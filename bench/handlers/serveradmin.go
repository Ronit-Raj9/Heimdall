package handlers

// Server admin area (web parity L23): the platform routes under
// /api/bench/admin/ that the server's own operators use. A server admin is
// a signed-in user whose email is listed in HEIMDALL_SERVER_ADMINS
// (auth.IsServerAdmin); everyone else gets a 403. The policy is userOnly()
// (tenancy/policy_serveradmin.go) and every handler checks the admin list.
//
// Routes: users (list, disable, enable), organizations with their quotas,
// the platform kill switch and the containers running on runners. Image
// approval (/admin/images) belongs to the shared pool area (L03) and usage
// (/admin/usage) to the usage area (L25); the admin page calls them.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// AdminUser is one user as a server admin sees it.
type AdminUser struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	Disabled    bool      `json:"disabled"`
	GitHub      bool      `json:"github"`
	Orgs        int       `json:"orgs"`
	CreatedAt   time.Time `json:"created_at"`
	ServerAdmin bool      `json:"server_admin"`
}

// AdminOrg is one organization as a server admin sees it.
type AdminOrg struct {
	ID         string       `json:"id"`
	Slug       string       `json:"slug"`
	Name       string       `json:"name"`
	PublicRead bool         `json:"public_read"`
	Members    int          `json:"members"`
	CreatedAt  time.Time    `json:"created_at"`
	Quota      *AdminLimits `json:"quota,omitempty"`
}

// AdminLimits is quota.Limits on the wire.
type AdminLimits struct {
	MaxConcurrentRuns int     `json:"max_concurrent_runs"`
	MaxRunsPerDay     int     `json:"max_runs_per_day"`
	MaxSpendUSDPerDay float64 `json:"max_spend_usd_per_day"`
	MaxImageBytes     int64   `json:"max_image_bytes"`
	AllowCustomImages bool    `json:"allow_custom_images"`
	SharedPool        bool    `json:"shared_pool"`
	Runtime           string  `json:"runtime"`
}

// AdminContainer is one run slot a runner holds now.
type AdminContainer struct {
	OrgID    string    `json:"org_id"`
	OrgSlug  string    `json:"org_slug"`
	RunnerID string    `json:"runner_id"`
	Runner   string    `json:"runner"`
	Pool     string    `json:"pool"`
	Host     string    `json:"host"`
	State    string    `json:"state"`
	Live     bool      `json:"live"`
	Slots    int       `json:"slots"`
	Running  []string  `json:"running"`
	LastSeen time.Time `json:"last_seen"`
}

// Admin answers.
type (
	AdminUserList struct {
		Users []AdminUser `json:"users"`
	}
	AdminOrgList struct {
		Orgs []AdminOrg `json:"orgs"`
	}
	AdminContainerList struct {
		Runners []AdminContainer `json:"runners"`
		// RunningSlots counts the run slots held across every runner.
		RunningSlots int `json:"running_slots"`
	}
	AdminKillSwitch struct {
		On bool `json:"on"`
	}
	AdminQuota struct {
		OrgID     string      `json:"org_id"`
		Limits    AdminLimits `json:"limits"`
		Defaults  AdminLimits `json:"defaults"`
		Persisted bool        `json:"persisted"` // false: the quota store is the in-memory stub; edits do not stick
	}
	AdminOverview struct {
		Users          int  `json:"users"`
		Orgs           int  `json:"orgs"`
		RunningSlots   int  `json:"running_slots"`
		KillSwitch     bool `json:"kill_switch"`
		QuotaPersisted bool `json:"quota_persisted"`
	}
	AdminUserDisableRequest struct {
		Disabled bool `json:"disabled"`
	}
)

// AdminDirectory reads users and organizations across the whole server.
type AdminDirectory interface {
	Users(ctx context.Context) ([]AdminUser, error)
	SetUserDisabled(ctx context.Context, userID string, disabled bool) error
	Orgs(ctx context.Context) ([]AdminOrg, error)
}

// AdminRunners lists the runners of one org (runners.Service.ListRunners).
type AdminRunners interface {
	ListRunners(ctx context.Context, orgID string) ([]schemas.RunnerView, error)
}

// ErrAdminNotFound is what an AdminDirectory returns for an unknown user.
var ErrAdminNotFound = errors.New("not found")

// ServerAdminHandler serves /api/bench/admin/{users,orgs,containers,killswitch,overview}.
type ServerAdminHandler struct {
	mode      schemas.Mode
	dir       AdminDirectory
	runners   AdminRunners
	quota     quota.Store
	persisted bool
	audit     AuditFunc
	isAdmin   func(*auth.Principal) bool
}

// NewServerAdminHandler builds the handler. persisted says whether q keeps
// its writes (false for quota.NewStub). Any dependency may be nil: its routes
// answer 503.
func NewServerAdminHandler(mode schemas.Mode, dir AdminDirectory, rs AdminRunners, q quota.Store, persisted bool, audit AuditFunc) *ServerAdminHandler {
	return &ServerAdminHandler{mode: mode, dir: dir, runners: rs, quota: q, persisted: persisted, audit: audit, isAdmin: auth.IsServerAdmin}
}

// WithAdminCheck replaces auth.IsServerAdmin (tests).
func (h *ServerAdminHandler) WithAdminCheck(f func(*auth.Principal) bool) *ServerAdminHandler {
	h.isAdmin = f
	return h
}

// RegisterRoutes registers the server admin routes.
func (h *ServerAdminHandler) RegisterRoutes(r *router.Router, mw ...lib.Middleware) {
	p := APIPrefix + "/admin"
	g := func(f fasthttp.RequestHandler) fasthttp.RequestHandler {
		return lib.ChainMiddlewares(h.guard(f), mw...)
	}
	r.GET(p+"/overview", g(h.overview))
	r.GET(p+"/users", g(h.listUsers))
	r.POST(p+"/users/{id}/disable", g(h.disableUser))
	r.GET(p+"/orgs", g(h.listOrgs))
	r.GET(p+"/orgs/{id}/quota", g(h.getQuota))
	r.POST(p+"/orgs/{id}/quota", g(h.setQuota))
	r.GET(p+"/killswitch", g(h.getKill))
	r.POST(p+"/killswitch", g(h.setKill))
	r.GET(p+"/containers", g(h.containers))
}

// guard answers 403 to anyone who is not a server admin.
func (h *ServerAdminHandler) guard(f fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if !h.isAdmin(auth.PrincipalFrom(ctx)) {
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
				"server admins only: add your email to HEIMDALL_SERVER_ADMINS on the server and sign in again")
			return
		}
		f(ctx)
	}
}

func (h *ServerAdminHandler) write(ctx *fasthttp.RequestCtx, orgID, action, tt, tid string, d map[string]any) {
	if h.audit != nil {
		h.audit(ctx, orgID, action, tt, tid, d)
	}
}

func actorOf(ctx *fasthttp.RequestCtx) string {
	if p := auth.PrincipalFrom(ctx); p != nil {
		if p.Email != "" {
			return p.Email
		}
		return p.UserID
	}
	return ""
}

func (h *ServerAdminHandler) overview(ctx *fasthttp.RequestCtx) {
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out := AdminOverview{QuotaPersisted: h.persisted && h.quota != nil}
	if h.dir != nil {
		if us, err := h.dir.Users(wctx); err == nil {
			out.Users = len(us)
		}
		if orgs, err := h.dir.Orgs(wctx); err == nil {
			out.Orgs = len(orgs)
			out.RunningSlots = h.countSlots(wctx, orgs)
		}
	}
	if h.quota != nil {
		out.KillSwitch, _ = h.quota.KillSwitch(wctx)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *ServerAdminHandler) countSlots(ctx context.Context, orgs []AdminOrg) int {
	if h.runners == nil {
		return 0
	}
	n := 0
	for _, o := range orgs {
		rs, err := h.runners.ListRunners(ctx, o.ID)
		if err != nil {
			continue
		}
		for _, r := range rs {
			n += r.RunningSlots
		}
	}
	return n
}

func (h *ServerAdminHandler) listUsers(ctx *fasthttp.RequestCtx) {
	if h.dir == nil {
		sendUnavailable(ctx, h.mode, "user directory (database)")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	us, err := h.dir.Users(wctx)
	if err != nil {
		sendDepError(ctx, h.mode, "list users", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AdminUserList{Users: us})
}

func (h *ServerAdminHandler) disableUser(ctx *fasthttp.RequestCtx) {
	if h.dir == nil {
		sendUnavailable(ctx, h.mode, "user directory (database)")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "user")
	if !ok {
		return
	}
	var req AdminUserDisableRequest
	if !decodeStrict(ctx, h.mode, &req, "disable user") {
		return
	}
	if p := auth.PrincipalFrom(ctx); p != nil && p.UserID == id && req.Disabled {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"you can not disable your own account: ask another server admin")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := h.dir.SetUserDisabled(wctx, id, req.Disabled); err != nil {
		if errors.Is(err, ErrAdminNotFound) {
			sendNotFound(ctx, h.mode, "user", id)
			return
		}
		sendDepError(ctx, h.mode, "disable user", err)
		return
	}
	action := "admin.user.enable"
	if req.Disabled {
		action = "admin.user.disable"
	}
	h.write(ctx, "", action, "user", id, nil)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, map[string]any{"id": id, "disabled": req.Disabled})
}

func (h *ServerAdminHandler) listOrgs(ctx *fasthttp.RequestCtx) {
	if h.dir == nil {
		sendUnavailable(ctx, h.mode, "organization directory (database)")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	orgs, err := h.dir.Orgs(wctx)
	if err != nil {
		sendDepError(ctx, h.mode, "list organizations", err)
		return
	}
	if h.quota != nil {
		for i := range orgs {
			if l, err := h.quota.Get(wctx, orgs[i].ID); err == nil {
				w := limitsOut(l)
				orgs[i].Quota = &w
			}
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AdminOrgList{Orgs: orgs})
}

// orgExists reports whether id names an organization.
func (h *ServerAdminHandler) orgExists(ctx context.Context, id string) (bool, error) {
	if h.dir == nil {
		return true, nil
	}
	orgs, err := h.dir.Orgs(ctx)
	if err != nil {
		return false, err
	}
	for _, o := range orgs {
		if o.ID == id {
			return true, nil
		}
	}
	return false, nil
}

func (h *ServerAdminHandler) quotaTarget(ctx *fasthttp.RequestCtx, wctx context.Context) (string, bool) {
	if h.quota == nil {
		sendUnavailable(ctx, h.mode, "quota store")
		return "", false
	}
	id, ok := idParam(ctx, h.mode, "id", "organization")
	if !ok {
		return "", false
	}
	found, err := h.orgExists(wctx, id)
	if err != nil {
		sendDepError(ctx, h.mode, "find organization", err)
		return "", false
	}
	if !found {
		sendNotFound(ctx, h.mode, "organization", id)
		return "", false
	}
	return id, true
}

func (h *ServerAdminHandler) getQuota(ctx *fasthttp.RequestCtx) {
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	id, ok := h.quotaTarget(ctx, wctx)
	if !ok {
		return
	}
	l, err := h.quota.Get(wctx, id)
	if err != nil {
		sendDepError(ctx, h.mode, "read quota", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AdminQuota{OrgID: id, Limits: limitsOut(l), Defaults: limitsOut(h.quota.Defaults()), Persisted: h.persisted})
}

// Upper bounds a quota edit may set; they catch typos, not policy.
const (
	maxQuotaConcurrent = 1000
	maxQuotaRunsPerDay = 100000
	maxQuotaSpendUSD   = 100000
	maxQuotaImageBytes = int64(64) << 30
)

// ValidateAdminLimits checks a quota edit and returns the limits to store.
func ValidateAdminLimits(in AdminLimits) (quota.Limits, error) {
	var bad []string
	if in.MaxConcurrentRuns < 0 || in.MaxConcurrentRuns > maxQuotaConcurrent {
		bad = append(bad, fmt.Sprintf("max_concurrent_runs: 0 to %d", maxQuotaConcurrent))
	}
	if in.MaxRunsPerDay < 0 || in.MaxRunsPerDay > maxQuotaRunsPerDay {
		bad = append(bad, fmt.Sprintf("max_runs_per_day: 0 to %d", maxQuotaRunsPerDay))
	}
	if in.MaxSpendUSDPerDay < 0 || in.MaxSpendUSDPerDay > maxQuotaSpendUSD || in.MaxSpendUSDPerDay != in.MaxSpendUSDPerDay {
		bad = append(bad, fmt.Sprintf("max_spend_usd_per_day: 0 to %d", maxQuotaSpendUSD))
	}
	if in.MaxImageBytes < 0 || in.MaxImageBytes > maxQuotaImageBytes {
		bad = append(bad, "max_image_bytes: 0 to 64 GiB")
	}
	rt := strings.TrimSpace(in.Runtime)
	if rt == "" {
		rt = "runc"
	}
	if rt != "runc" && rt != "runsc" {
		bad = append(bad, `runtime: "runc" or "runsc" (gVisor)`)
	}
	if len(bad) > 0 {
		return quota.Limits{}, errors.New(strings.Join(bad, "; "))
	}
	return quota.Limits{
		MaxConcurrentRuns: in.MaxConcurrentRuns, MaxRunsPerDay: in.MaxRunsPerDay,
		MaxSpendUSDPerDay: in.MaxSpendUSDPerDay, MaxImageBytes: in.MaxImageBytes,
		AllowCustomImages: in.AllowCustomImages, SharedPool: in.SharedPool, Runtime: rt,
	}, nil
}

func limitsOut(l quota.Limits) AdminLimits {
	rt := l.Runtime
	if rt == "" {
		rt = "runc"
	}
	return AdminLimits{
		MaxConcurrentRuns: l.MaxConcurrentRuns, MaxRunsPerDay: l.MaxRunsPerDay,
		MaxSpendUSDPerDay: l.MaxSpendUSDPerDay, MaxImageBytes: l.MaxImageBytes,
		AllowCustomImages: l.AllowCustomImages, SharedPool: l.SharedPool, Runtime: rt,
	}
}

func (h *ServerAdminHandler) setQuota(ctx *fasthttp.RequestCtx) {
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	id, ok := h.quotaTarget(ctx, wctx)
	if !ok {
		return
	}
	var req AdminLimits
	if !decodeStrict(ctx, h.mode, &req, "set quota") {
		return
	}
	l, err := ValidateAdminLimits(req)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "set quota: "+err.Error())
		return
	}
	if err := h.quota.Set(wctx, id, l, actorOf(ctx)); err != nil {
		sendDepError(ctx, h.mode, "set quota", err)
		return
	}
	got, err := h.quota.Get(wctx, id)
	if err != nil {
		got = l
	}
	out := limitsOut(got)
	h.write(ctx, id, "admin.quota.set", "org", id, map[string]any{"limits": out})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AdminQuota{OrgID: id, Limits: out, Defaults: limitsOut(h.quota.Defaults()), Persisted: h.persisted})
}

func (h *ServerAdminHandler) getKill(ctx *fasthttp.RequestCtx) {
	if h.quota == nil {
		sendUnavailable(ctx, h.mode, "quota store")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	on, err := h.quota.KillSwitch(wctx)
	if err != nil {
		sendDepError(ctx, h.mode, "read kill switch", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AdminKillSwitch{On: on})
}

func (h *ServerAdminHandler) setKill(ctx *fasthttp.RequestCtx) {
	if h.quota == nil {
		sendUnavailable(ctx, h.mode, "quota store")
		return
	}
	var req AdminKillSwitch
	if !decodeStrict(ctx, h.mode, &req, "kill switch") {
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := h.quota.SetKillSwitch(wctx, req.On, actorOf(ctx)); err != nil {
		sendDepError(ctx, h.mode, "set kill switch", err)
		return
	}
	on, err := h.quota.KillSwitch(wctx)
	if err != nil {
		on = req.On
	}
	h.write(ctx, "", "admin.killswitch.set", "platform", "killswitch", map[string]any{"on": on})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AdminKillSwitch{On: on})
}

func (h *ServerAdminHandler) containers(ctx *fasthttp.RequestCtx) {
	if h.dir == nil || h.runners == nil {
		sendUnavailable(ctx, h.mode, "runner directory")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	orgs, err := h.dir.Orgs(wctx)
	if err != nil {
		sendDepError(ctx, h.mode, "list organizations", err)
		return
	}
	out := AdminContainerList{Runners: []AdminContainer{}}
	for _, o := range orgs {
		rs, err := h.runners.ListRunners(wctx, o.ID)
		if err != nil {
			sendDepError(ctx, h.mode, "list runners of "+o.Slug, err)
			return
		}
		for _, r := range rs {
			running := r.Running
			if running == nil {
				running = []string{}
			}
			out.Runners = append(out.Runners, AdminContainer{
				OrgID: o.ID, OrgSlug: o.Slug, RunnerID: r.ID, Runner: r.Name, Pool: r.Pool, Host: r.Host,
				State: r.State, Live: r.Live, Slots: r.Slots, Running: running, LastSeen: r.LastSeen,
			})
			out.RunningSlots += r.RunningSlots
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
