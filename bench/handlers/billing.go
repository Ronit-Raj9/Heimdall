package handlers

// Plans, metering and billing (lane Y16, audit G4-10): platform routes of
// the hosted server.
//
//	GET  /billing              the org's plan, meters against its limits, features
//	GET  /billing/usage        metered usage per day (shared worker minutes, runs,
//	                           evaluations, storage, judge calls on operator keys)
//	POST /billing/checkout     a Stripe Checkout URL for the Team plan (admins)
//	POST /billing/portal       a Stripe customer portal URL (admins)
//	GET  /billing/invoices     the org's invoices (admins)
//	POST /billing/seats/sync   set the paid seats to the member count (admins)
//	POST /billing/webhook      Stripe events, signature verified (public)
//	PUT  /admin/billing/{id}   set an org's plan by hand (server admins)
//	GET  /api/public/pricing   the plans, for the public pricing page (public)
//
// Billing is off unless the operator sets HEIMDALL_BILLING=on: the reads
// answer as usual (limits shown, not enforced), the Stripe routes answer
// 503 with the reason.

import (
	"bytes"
	"errors"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/billing"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// PublicPricingPath is the public pricing route.
const PublicPricingPath = "/api/public/pricing"

// BillingLink is the app page that shows the plan and the upgrade button.
const BillingLink = "/settings?tab=billing"

// ErrTypePlanLimit is the error type of a refusal by the org's plan.
const ErrTypePlanLimit = "plan_limit"

// BillingHandler serves the billing routes.
type BillingHandler struct {
	mode    schemas.Mode
	svc     *billing.Service
	appURL  string
	audit   AuditFunc
	isAdmin func(*auth.Principal) bool
	now     func() time.Time
}

// NewBillingHandler returns the handler. svc nil answers 503 on every org
// route (no database); the pricing route still answers.
func NewBillingHandler(mode schemas.Mode, svc *billing.Service, appURL string, audit AuditFunc) *BillingHandler {
	return &BillingHandler{mode: mode, svc: svc, appURL: strings.TrimRight(appURL, "/"), audit: audit, isAdmin: auth.IsServerAdmin, now: time.Now}
}

// WithAdminCheck replaces the server admin check (tests).
func (h *BillingHandler) WithAdminCheck(f func(*auth.Principal) bool) *BillingHandler {
	h.isAdmin = f
	return h
}

// CheckoutResponse is the body of checkout and portal.
type CheckoutResponse struct {
	URL string `json:"url"`
}

// InvoiceList is the body of GET /billing/invoices.
type InvoiceList struct {
	Invoices []billing.Invoice `json:"invoices"`
}

// SetPlanBody is the body of PUT /admin/billing/{id}.
type SetPlanBody struct {
	Plan string `json:"plan"`
}

// AdminBillingAccount is the answer of PUT /admin/billing/{id}.
type AdminBillingAccount struct {
	OrgID         string `json:"org_id"`
	Plan          string `json:"plan"`
	EffectivePlan string `json:"effective_plan"`
	Status        string `json:"status"`
}

// RegisterRoutes implements server.RouteRegistrar.
func (h *BillingHandler) RegisterRoutes(r *router.Router, mw ...lib.Middleware) {
	c := func(f fasthttp.RequestHandler) fasthttp.RequestHandler { return lib.ChainMiddlewares(f, mw...) }
	b := APIPrefix + "/billing"
	r.GET(b, c(h.member(auth.RoleViewer, h.view)))
	r.GET(b+"/usage", c(h.member(auth.RoleViewer, h.usage)))
	r.POST(b+"/checkout", c(h.member(auth.RoleAdmin, h.checkout)))
	r.POST(b+"/portal", c(h.member(auth.RoleAdmin, h.portal)))
	r.GET(b+"/invoices", c(h.member(auth.RoleAdmin, h.invoices)))
	r.POST(b+"/seats/sync", c(h.member(auth.RoleAdmin, h.syncSeats)))
	r.POST(b+"/webhook", c(h.webhook))
	r.PUT(APIPrefix+"/admin/billing/{id}", c(h.setPlan))
	r.GET(PublicPricingPath, c(h.pricing))
}

func (h *BillingHandler) unavailable(ctx *fasthttp.RequestCtx) bool {
	if h.svc == nil || h.svc.Store == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "billing is part of the hosted platform and needs its database")
		return true
	}
	return false
}

// member admits a person or API token acting in an org with at least min.
func (h *BillingHandler) member(min auth.Role, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.unavailable(ctx) {
			return
		}
		p := auth.PrincipalFrom(ctx)
		if !p.IsAuthenticated() || p.OrgID == "" || p.Kind == auth.KindRunner {
			lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in first")
			return
		}
		if !p.Can(min) {
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "this needs the "+string(min)+" role in your organization")
			return
		}
		next(ctx)
	}
}

// sendBillingError maps a billing error to a status.
func (h *BillingHandler) sendBillingError(ctx *fasthttp.RequestCtx, what string, err error) {
	var se *billing.StripeError
	switch {
	case errors.Is(err, billing.ErrBillingOff), errors.Is(err, billing.ErrNoStripe), errors.Is(err, billing.ErrLiveKey):
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, err.Error())
	case errors.Is(err, billing.ErrAlreadyPaid), errors.Is(err, billing.ErrNoCustomer):
		lib.SendErrorLink(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error(), BillingLink)
	case errors.As(err, &se):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, what+": "+err.Error())
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, what+": "+err.Error())
	}
}

func (h *BillingHandler) view(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	v, err := h.svc.View(work, auth.PrincipalFrom(ctx).OrgID)
	if err != nil {
		h.sendBillingError(ctx, "read billing", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, v)
}

func (h *BillingHandler) usage(ctx *fasthttp.RequestCtx) {
	q, err := ParseUsageQuery(ctx.QueryArgs(), h.now())
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	rep, err := h.svc.Usage(work, auth.PrincipalFrom(ctx).OrgID, q.From, q.To)
	if err != nil {
		h.sendBillingError(ctx, "read usage", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
}

func (h *BillingHandler) checkout(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	p := auth.PrincipalFrom(ctx)
	u, err := h.svc.Checkout(work, p.OrgID, p.ActorID(), h.appURL)
	if err != nil {
		h.sendBillingError(ctx, "start checkout", err)
		return
	}
	h.record(ctx, p.OrgID, "billing.checkout", "plan", billing.PlanTeam, nil)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, CheckoutResponse{URL: u})
}

func (h *BillingHandler) portal(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	p := auth.PrincipalFrom(ctx)
	u, err := h.svc.Portal(work, p.OrgID, h.appURL)
	if err != nil {
		h.sendBillingError(ctx, "open the billing portal", err)
		return
	}
	h.record(ctx, p.OrgID, "billing.portal", "org", p.OrgID, nil)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, CheckoutResponse{URL: u})
}

func (h *BillingHandler) invoices(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	list, err := h.svc.Invoices(work, auth.PrincipalFrom(ctx).OrgID)
	if err != nil {
		h.sendBillingError(ctx, "list invoices", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, InvoiceList{Invoices: list})
}

func (h *BillingHandler) syncSeats(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	p := auth.PrincipalFrom(ctx)
	r, err := h.svc.SyncSeats(work, p.OrgID)
	if err != nil {
		h.sendBillingError(ctx, "sync seats", err)
		return
	}
	if r.Changed {
		h.record(ctx, p.OrgID, "billing.seats", "org", p.OrgID, map[string]any{"seats": r.Seats})
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, r)
}

func (h *BillingHandler) webhook(ctx *fasthttp.RequestCtx) {
	if h.unavailable(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	payload := bytes.Clone(ctx.PostBody())
	res, err := h.svc.HandleWebhook(work, payload, string(ctx.Request.Header.Peek(billing.SignatureHeader)))
	switch {
	case err == nil:
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, res)
	case errors.Is(err, billing.ErrSignature):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
	case errors.Is(err, billing.ErrBillingOff):
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, err.Error())
	default:
		var se *billing.StripeError
		if errors.As(err, &se) {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, "apply webhook: "+err.Error())
			return
		}
		if strings.HasPrefix(err.Error(), "decode") || strings.HasPrefix(err.Error(), "stripe event") {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
			return
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "apply webhook: "+err.Error())
	}
}

func (h *BillingHandler) setPlan(ctx *fasthttp.RequestCtx) {
	if h.unavailable(ctx) {
		return
	}
	p := auth.PrincipalFrom(ctx)
	if h.isAdmin == nil || !h.isAdmin(p) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"server admins only: the server owner lists admin emails in "+auth.ServerAdminsEnv)
		return
	}
	org := pathParam(ctx, "id")
	if !validID(org) {
		sendNotFound(ctx, h.mode, "organization", org)
		return
	}
	var body SetPlanBody
	if err := decodeJSONStrict(bytes.TrimSpace(ctx.PostBody()), &body); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if h.svc.Orgs != nil {
		if _, err := h.svc.Orgs.OrgName(work, org); err != nil {
			sendNotFound(ctx, h.mode, "organization", org)
			return
		}
	}
	a, err := h.svc.SetPlan(work, org, strings.TrimSpace(body.Plan), p.ActorID())
	if err != nil {
		if strings.HasPrefix(err.Error(), "unknown plan") {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
			return
		}
		h.sendBillingError(ctx, "set plan", err)
		return
	}
	h.record(ctx, org, "billing.plan", "org", org, map[string]any{"plan": a.Plan})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AdminBillingAccount{OrgID: org, Plan: a.Plan, EffectivePlan: a.EffectivePlan(), Status: a.Status})
}

func (h *BillingHandler) pricing(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, h.svc.Pricing())
	ctx.Response.Header.Set("Cache-Control", "public, max-age=300")
}

func (h *BillingHandler) record(ctx *fasthttp.RequestCtx, org, action, typ, id string, details map[string]any) {
	if h.audit != nil {
		h.audit(ctx, org, action, typ, id, details)
	}
}

// SendPlanLimit answers a refusal of the org's plan: 402 with type
// plan_limit and a link to Settings, Billing.
func SendPlanLimit(ctx *fasthttp.RequestCtx, mode schemas.Mode, err error) {
	lib.SendErrorLink(ctx, mode, fasthttp.StatusPaymentRequired, ErrTypePlanLimit, err.Error(), BillingLink)
}

// PlanGates maps the routes a plan feature covers (method and router
// pattern, as the tenancy policy keys them) to that feature; the seats
// route is checked against the plan's seats instead.
var PlanGates = map[string]billing.Feature{
	"POST " + APIPrefix + "/suites":                        billing.FeaturePrivateBenchmarks,
	"POST " + APIPrefix + "/suites/{id}/tasks":             billing.FeaturePrivateBenchmarks,
	"POST " + APIPrefix + "/suites/{id}/upload":            billing.FeaturePrivateBenchmarks,
	"PUT " + APIPrefix + "/suites/{id}/files/{path:*}":     billing.FeaturePrivateBenchmarks,
	"POST " + APIPrefix + "/suites/{id}/rows":              billing.FeaturePrivateBenchmarks,
	"PUT " + APIPrefix + "/suites/{id}/grader":             billing.FeaturePrivateBenchmarks,
	"PUT " + APIPrefix + "/suites/{id}/tasks/{task}/split": billing.FeaturePrivateBenchmarks,
	"POST " + APIPrefix + "/github/ci-install":             billing.FeatureCI,
	"PUT " + APIPrefix + "/schedules":                      billing.FeatureAlerts,
	"POST " + APIPrefix + "/schedules/test-alert":          billing.FeatureAlerts,
	"POST " + APIPrefix + "/integrations/webhooks":         billing.FeatureAlerts,
}

// SeatGate is the route that adds a seat (an invitation).
const SeatGate = "POST " + APIPrefix + "/orgs/{org}/invitations"

// PlanGate is the dispatcher gate of the plan limits (tenancy.RouteGate):
// with billing off it never refuses.
func PlanGate(svc *billing.Service) func(ctx *fasthttp.RequestCtx, mode schemas.Mode, orgID, method, pattern string) bool {
	return func(ctx *fasthttp.RequestCtx, mode schemas.Mode, orgID, method, pattern string) bool {
		if svc == nil || !svc.Enforced() {
			return true
		}
		k := method + " " + pattern
		var err error
		if k == SeatGate {
			work, cancel := workContext(ctx, readTimeout)
			err = svc.CheckSeats(work, orgID)
			cancel()
		} else if f, ok := PlanGates[k]; ok {
			work, cancel := workContext(ctx, readTimeout)
			err = svc.CheckFeature(work, orgID, f)
			cancel()
		}
		if err != nil {
			SendPlanLimit(ctx, mode, err)
			return false
		}
		return true
	}
}
