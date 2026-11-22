package handlers

// Usage and spend area (web parity L25): GET /usage sums the cost_usd and
// tokens metrics of an org's sealed runs over a date range, grouped by day,
// model, agent or key, plus the month spend the keystore records per provider
// key. GET /admin/usage (hosted, server admins only) gives the same totals
// per org for the whole platform.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Usage query limits and names.
const (
	usageDateLayout   = "2006-01-02"
	usageDefaultDays  = 30
	usageMaxDays      = 366
	usageTokensMetric = "tokens"

	UsageByDay   = "day"
	UsageByModel = "model"
	UsageByAgent = "agent"
	UsageByKey   = "key"
)

// UsageBucket is one group of the usage report.
type UsageBucket struct {
	Key             string  `json:"key"`
	Label           string  `json:"label,omitempty"`
	CostUSD         float64 `json:"cost_usd"`
	Tokens          float64 `json:"tokens"`
	Runs            int     `json:"runs"`
	UnknownCostRuns int     `json:"unknown_cost_runs"`
	// NoModelCallRuns counts runs that ended before any model call (an
	// infrastructure abort with no metrics and no cost rows): $0, known.
	NoModelCallRuns int `json:"no_model_call_runs,omitempty"`
}

// UsageKeySpend is one provider key's recorded spend. Never carries the secret.
type UsageKeySpend struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	Provider      string  `json:"provider"`
	Last4         string  `json:"last4,omitempty"`
	BudgetUSD     float64 `json:"budget_usd"`
	MonthSpentUSD float64 `json:"month_spent_usd"`
}

// UsageReport is the body of GET /usage.
type UsageReport struct {
	From            string          `json:"from"`
	To              string          `json:"to"`
	GroupBy         string          `json:"group_by"`
	TotalCostUSD    float64         `json:"total_cost_usd"`
	TotalTokens     float64         `json:"total_tokens"`
	Runs            int             `json:"runs"`
	UnknownCostRuns int             `json:"unknown_cost_runs"`
	NoModelCallRuns int             `json:"no_model_call_runs,omitempty"`
	Experiments     int             `json:"experiments"`
	Buckets         []UsageBucket   `json:"buckets"`
	Keys            []UsageKeySpend `json:"keys"`
	KeysMonthSpent  float64         `json:"keys_month_spent_usd"`
	Note            string          `json:"note,omitempty"`
}

// AdminOrgUsage is one org's row of GET /admin/usage.
type AdminOrgUsage struct {
	OrgID           string  `json:"org_id"`
	Slug            string  `json:"slug"`
	Name            string  `json:"name"`
	CostUSD         float64 `json:"cost_usd"`
	Tokens          float64 `json:"tokens"`
	Runs            int     `json:"runs"`
	UnknownCostRuns int     `json:"unknown_cost_runs"`
	KeysMonthSpent  float64 `json:"keys_month_spent_usd"`
	Error           string  `json:"error,omitempty"`
}

// AdminUsageReport is the body of GET /admin/usage.
type AdminUsageReport struct {
	From         string          `json:"from"`
	To           string          `json:"to"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	TotalTokens  float64         `json:"total_tokens"`
	Runs         int             `json:"runs"`
	Orgs         []AdminOrgUsage `json:"orgs"`
}

// UsageQuery is a parsed date range plus grouping. To is inclusive.
type UsageQuery struct {
	From, To time.Time
	GroupBy  string
}

// ParseUsageQuery reads from, to (YYYY-MM-DD, UTC) and group_by. Defaults:
// the last 30 days ending today, grouped by day.
func ParseUsageQuery(args *fasthttp.Args, now time.Time) (UsageQuery, error) {
	q := UsageQuery{GroupBy: UsageByDay}
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	q.To = today
	if s := strings.TrimSpace(string(args.Peek("to"))); s != "" {
		t, err := time.Parse(usageDateLayout, s)
		if err != nil {
			return q, errBadUsage("to must be a date like 2026-11-21")
		}
		q.To = t
	}
	q.From = q.To.AddDate(0, 0, -(usageDefaultDays - 1))
	if s := strings.TrimSpace(string(args.Peek("from"))); s != "" {
		t, err := time.Parse(usageDateLayout, s)
		if err != nil {
			return q, errBadUsage("from must be a date like 2026-11-01")
		}
		q.From = t
	}
	if q.From.After(q.To) {
		return q, errBadUsage("from must be on or before to")
	}
	if q.To.Sub(q.From) > time.Duration(usageMaxDays-1)*24*time.Hour {
		return q, errBadUsage("the range can be at most 366 days; narrow from and to")
	}
	if s := strings.TrimSpace(string(args.Peek("group_by"))); s != "" {
		switch s {
		case UsageByDay, UsageByModel, UsageByAgent, UsageByKey:
			q.GroupBy = s
		default:
			return q, errBadUsage("group_by must be one of day, model, agent, key")
		}
	}
	return q, nil
}

type usageError string

func (e usageError) Error() string { return string(e) }
func errBadUsage(msg string) error { return usageError(msg) }

// runUsageTime is when a run counts: its seal time, else its start time.
func runUsageTime(rv RunView) time.Time {
	if rv.Run.Timing.Sealed != nil {
		return rv.Run.Timing.Sealed.UTC()
	}
	return rv.Run.Timing.Started.UTC()
}

// ComputeUsage builds the usage report of one org over index and its keys.
// Only sealed runs inside [From, To] count. A run whose cost is unknown only
// for want of a price is costed at read time from the org's key prices or
// the server's price list (keyprices.go); the sealed run is never changed. A
// run with no metrics.json and no cost rows never reached a model and costs
// a known $0 (NoModelCallRuns). Any other run whose cost is unknown is
// counted in UnknownCostRuns and adds nothing to the cost total.
func ComputeUsage(index ReadIndex, keys []keystore.Key, q UsageQuery) UsageReport {
	lookup := priceLookup(keys)
	rep := UsageReport{From: q.From.Format(usageDateLayout), To: q.To.Format(usageDateLayout), GroupBy: q.GroupBy,
		Buckets: []UsageBucket{}, Keys: []UsageKeySpend{}}
	end := q.To.AddDate(0, 0, 1)
	buckets := map[string]*UsageBucket{}
	if q.GroupBy == UsageByDay {
		for d := q.From; d.Before(end); d = d.AddDate(0, 0, 1) {
			k := d.Format(usageDateLayout)
			buckets[k] = &UsageBucket{Key: k}
		}
	}
	if index != nil {
		for _, expID := range index.Experiments() {
			var inRange []RunView
			for _, rv := range index.Runs(expID) {
				if !rv.Sealed {
					continue
				}
				at := runUsageTime(rv)
				if at.Before(q.From) || !at.Before(end) {
					continue
				}
				inRange = append(inRange, rv)
			}
			if len(inRange) == 0 {
				continue
			}
			rep.Experiments++
			m, _ := index.Manifest(expID)
			metrics := runMetrics(index, inRange)
			for _, rv := range inRange {
				cost, costKnown, tokens, noCall := usageOfRunPriced(index, rv.Run.RunID, metrics[rv.Run.RunID], lookup)
				rep.Runs++
				if noCall {
					rep.NoModelCallRuns++
				}
				rep.TotalCostUSD += cost
				rep.TotalTokens += tokens
				if !costKnown {
					rep.UnknownCostRuns++
				}
				if q.GroupBy == UsageByKey {
					continue
				}
				k, label := usageGroupKey(q.GroupBy, rv, m)
				b := buckets[k]
				if b == nil {
					b = &UsageBucket{Key: k, Label: label}
					buckets[k] = b
				}
				b.CostUSD += cost
				b.Tokens += tokens
				b.Runs++
				if noCall {
					b.NoModelCallRuns++
				}
				if !costKnown {
					b.UnknownCostRuns++
				}
			}
		}
	}
	for _, k := range keys {
		ks := UsageKeySpend{ID: k.ID, Label: k.Label, Provider: k.Provider, Last4: k.Last4, BudgetUSD: k.BudgetUSD, MonthSpentUSD: k.MonthSpentUSD}
		rep.Keys = append(rep.Keys, ks)
		rep.KeysMonthSpent += k.MonthSpentUSD
		if q.GroupBy == UsageByKey {
			label := k.Label
			if label == "" {
				label = k.Provider
			}
			buckets[k.ID] = &UsageBucket{Key: k.ID, Label: label, CostUSD: k.MonthSpentUSD}
		}
	}
	if q.GroupBy == UsageByKey {
		rep.Note = "Per-key spend is the current month's spend the keystore records for each provider key; run costs are not split by key."
	}
	for _, b := range buckets {
		rep.Buckets = append(rep.Buckets, *b)
	}
	sort.Slice(rep.Buckets, func(i, j int) bool {
		a, b := rep.Buckets[i], rep.Buckets[j]
		if q.GroupBy == UsageByDay {
			return a.Key < b.Key
		}
		if a.CostUSD != b.CostUSD {
			return a.CostUSD > b.CostUSD
		}
		return a.Key < b.Key
	})
	return rep
}

// usageOfRunPriced is usageOfRun plus the read-time fixes of ComputeUsage:
// an unpriced cost repriced with lookup, and a run that never called a model
// (no metrics.json, no cost rows) costed at a known $0 with noCall true.
func usageOfRunPriced(index ReadIndex, runID string, mr runMetricsRead, lookup func(model string) (evidence.ModelPrice, bool)) (cost float64, known bool, tokens float64, noCall bool) {
	readCost := func() ([]byte, error) {
		data, found, err := readRunFile(index, runID, FileCost)
		if err == nil && !found {
			err = fs.ErrNotExist
		}
		return data, err
	}
	if !mr.err && !mr.found {
		data, err := readCost()
		if errors.Is(err, fs.ErrNotExist) || (err == nil && emptyCostRows(data)) {
			return 0, true, 0, true
		}
		return 0, false, 0, false
	}
	cost, known, tokens = usageOfRun(mr)
	if !known && mr.found {
		if v, ok := repricedCost(mr.values, readCost, lookup); ok && v.V != nil {
			cost, known = *v.V, true
		}
	}
	return cost, known, tokens, false
}

// emptyCostRows reports whether a cost.json holds no rows.
func emptyCostRows(data []byte) bool {
	var rows []schemas.CostRow
	return json.Unmarshal(data, &rows) == nil && len(rows) == 0
}

// usageOfRun reads cost and tokens from one run's metrics.
func usageOfRun(mr runMetricsRead) (cost float64, known bool, tokens float64) {
	if mr.err || !mr.found {
		return 0, false, 0
	}
	if v, ok := mr.values[costMetric]; ok && v.State == schemas.ValueObserved && v.V != nil {
		cost, known = *v.V, true
	}
	if v, ok := mr.values[usageTokensMetric]; ok && v.V != nil {
		tokens = *v.V
	}
	return cost, known, tokens
}

// usageGroupKey names the bucket of a run.
func usageGroupKey(groupBy string, rv RunView, m *schemas.Manifest) (key, label string) {
	switch groupBy {
	case UsageByModel:
		if m != nil {
			if arm, ok := m.Arms[rv.Slot.Arm]; ok && arm.Config.Model != "" {
				return arm.Config.Model, ""
			}
		}
		return "unknown", "model not recorded"
	case UsageByAgent:
		if rv.Run.Agent.ConfigID != "" {
			return rv.Run.Agent.ConfigID, ""
		}
		if m != nil {
			if arm, ok := m.Arms[rv.Slot.Arm]; ok && arm.Config.ID != "" {
				return arm.Config.ID, ""
			}
		}
		return "unknown", "agent not recorded"
	default:
		return runUsageTime(rv).Format(usageDateLayout), ""
	}
}

// UsageHandler serves GET /usage for one org.
type UsageHandler struct {
	mode schemas.Mode
	svc  Services
	now  func() time.Time
}

// NewUsageHandler builds the handler over an org's services.
func NewUsageHandler(mode schemas.Mode, svc Services) *UsageHandler {
	return &UsageHandler{mode: mode, svc: svc, now: time.Now}
}

func (h *UsageHandler) get(ctx *fasthttp.RequestCtx) {
	q, err := ParseUsageQuery(ctx.QueryArgs(), h.now())
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if h.svc.Index == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"usage needs the run index; start the server with a runs folder (runs_root in bench.yaml)")
		return
	}
	var keys []keystore.Key
	if h.svc.Keys != nil {
		if ks, err := h.svc.Keys.List(ctx, h.svc.OrgID, keystore.KindProvider); err == nil {
			keys = ks
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ComputeUsage(h.svc.Index, keys, q))
}

// OrgUsageSource lists every org's usage for the admin page (hosted only).
type OrgUsageSource interface {
	OrgsUsage(ctx context.Context, q UsageQuery) ([]AdminOrgUsage, error)
}

// AdminUsageHandler serves GET /admin/usage to server admins.
type AdminUsageHandler struct {
	mode   schemas.Mode
	source OrgUsageSource
	now    func() time.Time
}

// NewAdminUsageHandler builds the platform handler.
func NewAdminUsageHandler(mode schemas.Mode, source OrgUsageSource) *AdminUsageHandler {
	return &AdminUsageHandler{mode: mode, source: source, now: time.Now}
}

// RegisterRoutes mounts GET /admin/usage.
func (h *AdminUsageHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/admin/usage", lib.ChainMiddlewares(h.get, middlewares...))
}

func (h *AdminUsageHandler) get(ctx *fasthttp.RequestCtx) {
	if !auth.IsServerAdmin(auth.PrincipalFrom(ctx)) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"platform usage is for server admins; ask the operator to add your email to HEIMDALL_SERVER_ADMINS")
		return
	}
	q, err := ParseUsageQuery(ctx.QueryArgs(), h.now())
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if h.source == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "platform usage needs the hosted database")
		return
	}
	rows, err := h.source.OrgsUsage(ctx, q)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list org usage: "+err.Error())
		return
	}
	rep := AdminUsageReport{From: q.From.Format(usageDateLayout), To: q.To.Format(usageDateLayout), Orgs: rows}
	if rep.Orgs == nil {
		rep.Orgs = []AdminOrgUsage{}
	}
	for _, o := range rows {
		rep.TotalCostUSD += o.CostUSD
		rep.TotalTokens += o.Tokens
		rep.Runs += o.Runs
	}
	sort.SliceStable(rep.Orgs, func(i, j int) bool { return rep.Orgs[i].CostUSD > rep.Orgs[j].CostUSD })
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
}

func init() {
	RegisterOrgArea("usage", func(env OrgEnv, r *router.Router) {
		h := NewUsageHandler(env.Mode, env.Services)
		r.GET(APIPrefix+"/usage", env.R(h.get))
	})
}
