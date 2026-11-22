package handlers

// Benchmark data policy (lane Y15): per benchmark, how personal data in
// imported rows is handled (warn, mask, redact or block) and who in the
// organization may see its task content (everyone, or admins, owners and a
// list of people). BenchmarkAccessGuard enforces the access list on every
// /suites/{id} route of an org router before the route's handler runs; the
// policy lives outside the benchmark folder (datapolicy), so no file edit
// can widen it.

import (
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/datapolicy"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/pii"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// DataPolicyView answers GET and PUT /suites/{id}/data-policy.
type DataPolicyView struct {
	Benchmark string            `json:"benchmark"`
	Policy    datapolicy.Policy `json:"policy"`
	// CanEditAccess is true for callers who may change the access list
	// (org admins and owners).
	CanEditAccess bool `json:"can_edit_access"`
	// Modes and Kinds list the choices.
	Modes []pii.Mode `json:"modes"`
	Kinds []pii.Kind `json:"kinds"`
}

// DataPolicyRequest is the body of PUT /suites/{id}/data-policy. A missing
// part keeps its current value.
type DataPolicyRequest struct {
	PII    *pii.Policy        `json:"pii,omitempty"`
	Access *datapolicy.Access `json:"access,omitempty"`
}

// DataPolicyList answers GET /data-policies: the benchmarks whose policy
// is not the default, and whether the caller may open each.
type DataPolicyList struct {
	Benchmarks []DataPolicyEntry `json:"benchmarks"`
}

// DataPolicyEntry is one benchmark in DataPolicyList.
type DataPolicyEntry struct {
	Benchmark  string `json:"benchmark"`
	PIIMode    string `json:"pii_mode"`
	Restricted bool   `json:"restricted"`
	Allowed    bool   `json:"allowed"`
}

// caller is who sends a request, for the Y15 areas.
type caller struct {
	UserID   string
	Email    string
	Manager  bool // org admin or owner, or the local single-user principal
	SignedIn bool // a signed-in person (a browser session) or the local principal
}

func callerOf(ctx *fasthttp.RequestCtx) caller {
	p := auth.PrincipalFrom(ctx)
	if p == nil {
		return caller{}
	}
	c := caller{UserID: p.UserID, Email: p.Email}
	switch p.Kind {
	case auth.KindLocal:
		c.UserID, c.Manager, c.SignedIn = string(auth.KindLocal), true, true
		if c.Email == "" {
			c.Email = "local"
		}
	case auth.KindUser:
		c.SignedIn = p.UserID != ""
	}
	if p.Role != "" && p.Role.AtLeast(auth.RoleAdmin) {
		c.Manager = true
	}
	if c.Email == "" {
		c.Email = c.UserID
	}
	return c
}

func (c caller) viewer() datapolicy.Viewer {
	return datapolicy.Viewer{UserID: c.UserID, Manager: c.Manager}
}

// benchmarkIDOf reads the benchmark id of a /suites/{id} path, "" for any
// other path.
func benchmarkIDOf(path string) string {
	rest, ok := strings.CutPrefix(path, APIPrefix+"/suites/")
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, "/")
	if !validID(id) {
		return ""
	}
	return id
}

// BenchmarkAccessGuard refuses /suites/{id} routes of a restricted
// benchmark to org members who are not on its access list. root is the
// org's project folder.
func BenchmarkAccessGuard(mode schemas.Mode, root string) lib.Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		if root == "" {
			return next
		}
		st := datapolicy.Open(root)
		return func(ctx *fasthttp.RequestCtx) {
			id := benchmarkIDOf(string(ctx.Path()))
			if id == "" {
				next(ctx)
				return
			}
			p, err := st.Get(id)
			if err != nil {
				lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the access list of benchmark "+id+": "+err.Error())
				return
			}
			if !p.Allows(callerOf(ctx).viewer()) {
				sendRestricted(ctx, mode, id)
				return
			}
			next(ctx)
		}
	}
}

func sendRestricted(ctx *fasthttp.RequestCtx, mode schemas.Mode, id string) {
	lib.SendError(ctx, mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
		"benchmark "+id+" is restricted to the people on its access list; ask an admin of your organization for access")
}

type dataPolicyHandler struct {
	env  OrgEnv
	now  func() time.Time
	root string
}

func init() {
	RegisterOrgArea("datapolicy", func(env OrgEnv, r *router.Router) {
		h := &dataPolicyHandler{env: env, now: time.Now}
		if env.Cfg != nil {
			h.root = env.Cfg.Root
		}
		r.GET(APIPrefix+"/data-policies", env.R(h.list))
		r.GET(APIPrefix+"/suites/{id}/data-policy", env.R(h.get))
		r.PUT(APIPrefix+"/suites/{id}/data-policy", env.W(refuseBuiltin(env.Mode, env.Services.Suites, h.put)))
	})
}

func (h *dataPolicyHandler) store(ctx *fasthttp.RequestCtx) *datapolicy.Store {
	if h.root == "" {
		sendUnavailable(ctx, h.env.Mode, "benchmark data policies")
		return nil
	}
	return datapolicy.Open(h.root)
}

func (h *dataPolicyHandler) view(ctx *fasthttp.RequestCtx, id string, p datapolicy.Policy) DataPolicyView {
	return DataPolicyView{Benchmark: id, Policy: p, CanEditAccess: callerOf(ctx).Manager, Modes: pii.Modes(), Kinds: pii.Kinds()}
}

func (h *dataPolicyHandler) list(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	out := DataPolicyList{Benchmarks: []DataPolicyEntry{}}
	ids, err := st.IDs()
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read benchmark policies: "+err.Error())
		return
	}
	v := callerOf(ctx).viewer()
	for _, id := range ids {
		p, err := st.Get(id)
		if err != nil {
			continue
		}
		out.Benchmarks = append(out.Benchmarks, DataPolicyEntry{Benchmark: id, PIIMode: string(p.PII.Effective()),
			Restricted: p.Access.Mode == datapolicy.AccessRestricted, Allowed: p.Allows(v)})
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *dataPolicyHandler) get(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "benchmark")
	if !ok {
		return
	}
	st := h.store(ctx)
	if st == nil {
		return
	}
	p, err := st.Get(id)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read policy: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.view(ctx, id, p))
}

func (h *dataPolicyHandler) put(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "benchmark")
	if !ok {
		return
	}
	st := h.store(ctx)
	if st == nil {
		return
	}
	var req DataPolicyRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			`body must be {"pii"?:{"mode","kinds"?},"access"?:{"mode":"org"|"restricted","users"?:[user ids]}}: `+err.Error())
		return
	}
	who := callerOf(ctx)
	cur, err := st.Get(id)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read policy: "+err.Error())
		return
	}
	if req.Access != nil {
		if !who.Manager {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
				"only org admins and owners change who may see a benchmark")
			return
		}
		cur.Access = *req.Access
	}
	if req.PII != nil {
		cur.PII = *req.PII
	}
	p, err := st.Set(id, cur, who.Email, h.now())
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.view(ctx, id, p))
}
