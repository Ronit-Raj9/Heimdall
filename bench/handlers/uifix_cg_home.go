package handlers

import (
	"encoding/json"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Home summary (fix wave M2-14, design in notes/G3.md): GET /home/summary
// answers the Home tiles in one call. It is built from the org router's own
// read routes, called in process with the caller's request (same
// credentials, same organization, same role checks), so every number is
// the one those routes answer:
//
//   - counts: total of GET /agents, /suites, /experiments and /evaluations
//     (limit=1 or 5);
//   - latest_evaluations: GET /evaluations?fields=light&limit=5;
//   - latest_comparisons: GET /experiments?limit=5;
//   - running: total of GET /evaluations?status=queued,running;
//   - spend_month_usd: total_cost_usd of GET /usage from the first day of
//     this UTC month to today (the Usage page's own sum), with its
//     unknown_cost_runs.
//
// A part whose route fails or is not on this server is left out.
const HomeSummaryPath = APIPrefix + "/home/summary"

// HomeSummary is GET /home/summary. Every field is optional.
type HomeSummary struct {
	Counts            map[string]int  `json:"counts"`
	LatestEvaluations json.RawMessage `json:"latest_evaluations,omitempty"`
	LatestComparisons json.RawMessage `json:"latest_comparisons,omitempty"`
	Running           *int            `json:"running,omitempty"`
	SpendMonthUSD     *float64        `json:"spend_month_usd,omitempty"`
	SpendMonthUnknown *int            `json:"spend_month_unknown_cost_runs,omitempty"`
	SpendMonthFrom    string          `json:"spend_month_from,omitempty"`
}

type homeHandler struct {
	mode schemas.Mode
	r    *router.Router
	now  func() time.Time
}

func init() {
	RegisterOrgArea("uifix_cg_home", func(env OrgEnv, r *router.Router) {
		h := &homeHandler{mode: env.Mode, r: r, now: time.Now}
		r.GET(HomeSummaryPath, env.R(h.get))
	})
}

// sub calls one GET route of this router with the caller's request and
// decodes its body into out. false when it did not answer 200.
func (h *homeHandler) sub(ctx *fasthttp.RequestCtx, uri string, out any) bool {
	return subGet(h.r, ctx, uri, out)
}

// subGet calls one GET route of r in process with the caller's request
// (headers, credentials and user values, so the same organization and role
// checks apply) and decodes a 200 body into out.
func subGet(r *router.Router, ctx *fasthttp.RequestCtx, uri string, out any) bool {
	var s fasthttp.RequestCtx
	ctx.Request.CopyTo(&s.Request)
	s.Request.Header.SetMethod(fasthttp.MethodGet)
	s.Request.SetRequestURI(uri)
	s.Request.SetBody(nil)
	ctx.VisitUserValuesAll(func(k, v any) { s.SetUserValue(k, v) })
	r.Handler(&s)
	if s.Response.StatusCode() != fasthttp.StatusOK {
		return false
	}
	return json.Unmarshal(s.Response.Body(), out) == nil
}

func (h *homeHandler) get(ctx *fasthttp.RequestCtx) {
	out := HomeSummary{Counts: map[string]int{}}
	var page struct {
		Total int `json:"total"`
	}
	for name, uri := range map[string]string{"agents": APIPrefix + "/agents?limit=1", "benchmarks": APIPrefix + "/suites?limit=1"} {
		page.Total = 0
		if h.sub(ctx, uri, &page) {
			out.Counts[name] = page.Total
		}
	}
	var evs struct {
		Total       int             `json:"total"`
		Evaluations json.RawMessage `json:"evaluations"`
	}
	if h.sub(ctx, APIPrefix+"/evaluations?fields=light&limit=5", &evs) {
		out.Counts["evaluations"] = evs.Total
		out.LatestEvaluations = evs.Evaluations
	}
	var exps struct {
		Total       int             `json:"total"`
		Experiments json.RawMessage `json:"experiments"`
	}
	if h.sub(ctx, APIPrefix+"/experiments?limit=5", &exps) {
		out.Counts["comparisons"] = exps.Total
		out.LatestComparisons = exps.Experiments
	}
	page.Total = 0
	if h.sub(ctx, APIPrefix+"/evaluations?fields=light&limit=1&status=queued,running", &page) {
		n := page.Total
		out.Running = &n
	}
	now := h.now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format(usageDateLayout)
	var usage UsageReport
	if h.sub(ctx, APIPrefix+"/usage?from="+from+"&to="+now.Format(usageDateLayout), &usage) {
		cost, unknown := usage.TotalCostUSD, usage.UnknownCostRuns
		out.SpendMonthUSD, out.SpendMonthUnknown, out.SpendMonthFrom = &cost, &unknown, from
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func init() {
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: HomeSummaryPath, Tag: "home",
		Summary:  "The Home tiles in one call: counts of agents, benchmarks, comparisons and evaluations, the 5 newest evaluations (light) and comparisons, how many evaluations are queued or running, and this UTC month's spend as GET /usage sums it; a part whose route fails is left out",
		Response: HomeSummary{}})
}
