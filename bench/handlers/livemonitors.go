package handlers

// Lane Z22: live synthetic monitoring (package livemon,
// docs/live-monitors.md). A monitor runs a few real tasks of a ready
// benchmark against a deployed agent on a timetable. It is a scheduled
// evaluation like any other scheduled check: MonitorSchedules adds one
// schedule per running monitor (live-<id>, repeating
// .heimdall/monitors/<id>/evaluation.json, capped by the monitor's daily
// budget), the server ticker fires it as schedule.fire, and
// CollectMonitors (livemonitorscollect.go) reads each finished evaluation
// back into a sample, runs the incident rules and sends the notices.
//
//	GET    /live-monitors                        every monitor with its status and last 24 hours
//	POST   /live-monitors                        create one ("Monitor this agent")
//	GET    /live-monitors/{id}                   one monitor: samples, incidents, chosen tasks
//	PUT    /live-monitors/{id}                   change it
//	DELETE /live-monitors/{id}                   delete it (its evaluations and runs stay)
//	POST   /live-monitors/{id}/pause|resume      stop or restart its checks
//	POST   /live-monitors/{id}/run               queue one check now
//	POST   /live-monitors/{id}/badge             turn the public status badge on (new link) or off
//	GET    /live-monitor-badges/{token}/badge.svg  anyone with the link: the status badge

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livemon"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// LiveMonitorsPath is the monitors collection.
const LiveMonitorsPath = APIPrefix + "/live-monitors"

// LiveMonitorBadgePath is the public badge route; {token} names the org.
const LiveMonitorBadgePath = APIPrefix + "/live-monitor-badges/{token}/badge.svg"

// MonitorDetailSamples is how many samples GET /live-monitors/{id} returns.
const MonitorDetailSamples = 500

// MonitorStats is a monitor's numbers over a time window.
type MonitorStats struct {
	Checks      int      `json:"checks"`       // checks that ran (ok or infra)
	OKChecks    int      `json:"ok_checks"`    // checks whose runs were graded
	InfraChecks int      `json:"infra_checks"` // checks that failed for infrastructure reasons or could not run
	Passed      int      `json:"passed"`       // passing runs
	Scored      int      `json:"scored"`       // graded runs
	PassRate    *float64 `json:"pass_rate"`    // nil with no graded run
	PassLow     *float64 `json:"pass_low"`     // 95% Wilson range of the pass rate
	PassHigh    *float64 `json:"pass_high"`    //
	P50MS       *int64   `json:"p50_ms"`       // run latency percentiles
	P95MS       *int64   `json:"p95_ms"`       //
	CostUSD     float64  `json:"cost_usd"`     // measured spend
	CostUnknown int      `json:"cost_unknown"` // runs whose cost was not measured
	Errors      int      `json:"errors"`       // runs or checks that ended in an infrastructure error
	Uptime      *float64 `json:"uptime"`       // share of checks that were not infra checks
}

// MonitorRow is one monitor as the list shows it.
type MonitorRow struct {
	livemon.Monitor
	// Status: paused, down (an infra incident is open), degraded (a quality
	// or latency incident is open), learning (too few graded runs for the
	// rule yet), healthy, or no_data (no check yet).
	Status         string             `json:"status"`
	Schedule       string             `json:"schedule"`
	Cron           string             `json:"cron"`
	Next           string             `json:"next,omitempty"` // next check, RFC 3339; empty when paused
	CheckBudgetUSD float64            `json:"check_budget_usd"`
	SpentTodayUSD  float64            `json:"spent_today_usd"`
	Last           *livemon.Sample    `json:"last,omitempty"`
	Day            MonitorStats       `json:"day"`  // the last 24 hours
	Week           MonitorStats       `json:"week"` // the last 7 days
	Open           []livemon.Incident `json:"open_incidents"`
	BadgePath      string             `json:"badge_path,omitempty"` // public badge link, when on
	Problem        string             `json:"problem,omitempty"`    // why its checks would not run now
}

// MonitorList is GET /live-monitors.
type MonitorList struct {
	Monitors       []MonitorRow `json:"monitors"`
	Intervals      []int        `json:"intervals"`
	MaxTasks       int          `json:"max_tasks"`
	MinIntervalMin int          `json:"min_interval_min"`
	Ticker         bool         `json:"ticker"` // checks can fire on this server
}

// MonitorDetail is GET /live-monitors/{id}.
type MonitorDetail struct {
	MonitorRow
	Samples   []livemon.Sample   `json:"samples"`   // oldest first, at most MonitorDetailSamples
	Incidents []livemon.Incident `json:"incidents"` // newest first
	Tasks     []string           `json:"tasks"`     // the tasks each check runs
	TasksNote string             `json:"tasks_note,omitempty"`
}

// MonitorInput is POST /live-monitors and PUT /live-monitors/{id}. On PUT a
// nil field keeps its value; agent and benchmark are fixed at create.
type MonitorInput struct {
	Name            *string       `json:"name,omitempty"`
	Agent           string        `json:"agent,omitempty"`
	Version         *int          `json:"version,omitempty"`
	Model           *string       `json:"model,omitempty"`
	Provider        *string       `json:"provider,omitempty"`
	Benchmark       string        `json:"benchmark,omitempty"` // empty on create: the agent's most recently evaluated benchmark with ready tasks
	Tasks           *int          `json:"tasks,omitempty"`
	IntervalMin     *int          `json:"interval_min,omitempty"`
	BudgetUSDPerDay *float64      `json:"budget_usd_per_day,omitempty"`
	Worker          *string       `json:"worker,omitempty"`
	Rule            *livemon.Rule `json:"rule,omitempty"`
}

// MonitorBadgeRequest is POST /live-monitors/{id}/badge.
type MonitorBadgeRequest struct {
	Enabled bool `json:"enabled"`
}

func init() {
	RegisterOrgArea("live-monitors", func(env OrgEnv, r *router.Router) {
		h := &liveMonitorsHandler{env: env, now: time.Now}
		r.GET(LiveMonitorsPath, env.R(h.list))
		r.POST(LiveMonitorsPath, env.W(h.create))
		r.GET(LiveMonitorsPath+"/{id}", env.R(h.get))
		r.PUT(LiveMonitorsPath+"/{id}", env.W(h.update))
		r.DELETE(LiveMonitorsPath+"/{id}", env.W(h.remove))
		r.POST(LiveMonitorsPath+"/{id}/pause", env.W(h.pause))
		r.POST(LiveMonitorsPath+"/{id}/resume", env.W(h.resume))
		r.POST(LiveMonitorsPath+"/{id}/run", env.W(h.runNow))
		r.POST(LiveMonitorsPath+"/{id}/badge", env.W(h.badgeSet))
		r.GET(LiveMonitorBadgePath, env.R(h.badge))
	})
}

type liveMonitorsHandler struct {
	env OrgEnv
	now func() time.Time
}

func (h *liveMonitorsHandler) root() string          { return h.env.Cfg.Root }
func (h *liveMonitorsHandler) store() *livemon.Store { return livemon.Open(h.root()) }
func (h *liveMonitorsHandler) id(ctx *fasthttp.RequestCtx) string {
	s, _ := ctx.UserValue("id").(string)
	return s
}

func (h *liveMonitorsHandler) fail(ctx *fasthttp.RequestCtx, err error) {
	status, typ := fasthttp.StatusInternalServerError, lib.ErrTypeInternal
	switch {
	case errors.Is(err, livemon.ErrNotFound):
		status, typ = fasthttp.StatusNotFound, lib.ErrTypeNotFound
	case errors.Is(err, livemon.ErrInvalid):
		status, typ = fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest
	}
	lib.SendError(ctx, h.env.Mode, status, typ, strings.TrimPrefix(err.Error(), livemon.ErrInvalid.Error()+": "))
}

func (h *liveMonitorsHandler) list(ctx *fasthttp.RequestCtx) {
	ms, err := h.store().List()
	if err != nil {
		h.fail(ctx, err)
		return
	}
	out := MonitorList{Monitors: []MonitorRow{}, Intervals: livemon.Intervals, MaxTasks: livemon.MaxTasks,
		MinIntervalMin: livemon.MinIntervalMin, Ticker: h.ticker()}
	for _, m := range ms {
		st, err := h.store().State(m.ID)
		if err != nil {
			st = &livemon.State{}
		}
		out.Monitors = append(out.Monitors, h.row(m, st))
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *liveMonitorsHandler) ticker() bool {
	return h.env.Services.Jobs != nil && slices.Contains(h.env.Services.Jobs.Kinds(), KindScheduleFire)
}

func (h *liveMonitorsHandler) get(ctx *fasthttp.RequestCtx) {
	m, err := h.store().Get(h.id(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	h.sendDetail(ctx, *m, fasthttp.StatusOK)
}

func (h *liveMonitorsHandler) create(ctx *fasthttp.RequestCtx) {
	var in MonitorInput
	if err := abDecodeBody(ctx, &in); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	now := h.now().UTC()
	m := livemon.Monitor{ID: livemon.NewID(), Agent: strings.TrimSpace(in.Agent), Benchmark: strings.TrimSpace(in.Benchmark),
		CreatedAt: now, UpdatedAt: now, CreatedBy: actorName(ctx)}
	applyMonitorInput(&m, in)
	if m.BudgetUSDPerDay == 0 {
		m.BudgetUSDPerDay = defaultMonitorBudget(m)
	}
	if m.Benchmark == "" && m.Agent != "" {
		m.Benchmark = h.defaultBenchmark(ctx, m)
		if m.Benchmark == "" {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
				"pick a benchmark: agent "+m.Agent+" has no finished evaluation to take one from")
			return
		}
	}
	if err := h.check(ctx, &m); err != nil {
		h.fail(ctx, err)
		return
	}
	s := h.store()
	unlock := s.Lock()
	all, err := s.List()
	if err == nil && len(all) >= livemon.MaxMonitors {
		err = fmt.Errorf("%w: at most %d monitors per organization; delete one first", livemon.ErrInvalid, livemon.MaxMonitors)
	}
	if err == nil {
		err = h.checkHostedQuota(m)
	}
	if err == nil {
		err = s.Save(m, MonitorEvaluationSpec(m))
	}
	unlock()
	if err != nil {
		h.fail(ctx, err)
		return
	}
	h.sendDetail(ctx, m, fasthttp.StatusCreated)
}

// defaultMonitorBudget is 0.25 USD per check, the most a few small tasks
// usually cost, so a one-click monitor has a cap from the start.
func defaultMonitorBudget(m livemon.Monitor) float64 {
	n := m
	if n.IntervalMin == 0 {
		n.IntervalMin = livemon.DefaultIntervalMin
	}
	return math.Min(livemon.MaxBudgetUSDPerDay, 0.25*float64(n.ChecksPerDay()))
}

func applyMonitorInput(m *livemon.Monitor, in MonitorInput) {
	if in.Name != nil {
		m.Name = *in.Name
	}
	if in.Version != nil {
		m.Version = *in.Version
	}
	if in.Model != nil {
		m.Model = *in.Model
	}
	if in.Provider != nil {
		m.Provider = *in.Provider
	}
	if in.Tasks != nil {
		m.Tasks = *in.Tasks
	}
	if in.IntervalMin != nil {
		m.IntervalMin = *in.IntervalMin
	}
	if in.BudgetUSDPerDay != nil {
		m.BudgetUSDPerDay = *in.BudgetUSDPerDay
	}
	if in.Worker != nil {
		m.Worker = *in.Worker
	}
	if in.Rule != nil {
		m.Rule = *in.Rule
	}
}

func (h *liveMonitorsHandler) update(ctx *fasthttp.RequestCtx) {
	var in MonitorInput
	if err := abDecodeBody(ctx, &in); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	s := h.store()
	cur, err := s.Get(h.id(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	if (in.Agent != "" && in.Agent != cur.Agent) || (in.Benchmark != "" && in.Benchmark != cur.Benchmark) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			"the agent and benchmark of a monitor are fixed: create a new monitor to watch another one")
		return
	}
	m := *cur
	applyMonitorInput(&m, in)
	m.UpdatedAt = h.now().UTC()
	if err := h.check(ctx, &m); err != nil {
		h.fail(ctx, err)
		return
	}
	if err := h.save(m); err != nil {
		h.fail(ctx, err)
		return
	}
	h.sendDetail(ctx, m, fasthttp.StatusOK)
}

// save writes m under the store lock, re-reading nothing: the last write wins.
func (h *liveMonitorsHandler) save(m livemon.Monitor) error {
	s := h.store()
	unlock := s.Lock()
	defer unlock()
	if _, err := s.Get(m.ID); err != nil {
		return err
	}
	return s.Save(m, MonitorEvaluationSpec(m))
}

func (h *liveMonitorsHandler) remove(ctx *fasthttp.RequestCtx) {
	s := h.store()
	unlock := s.Lock()
	err := s.Delete(h.id(ctx))
	unlock()
	if err != nil {
		h.fail(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h *liveMonitorsHandler) pause(ctx *fasthttp.RequestCtx)  { h.setPaused(ctx, true) }
func (h *liveMonitorsHandler) resume(ctx *fasthttp.RequestCtx) { h.setPaused(ctx, false) }

func (h *liveMonitorsHandler) setPaused(ctx *fasthttp.RequestCtx, paused bool) {
	m, err := h.store().Get(h.id(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	m.Paused, m.UpdatedAt = paused, h.now().UTC()
	if !paused {
		if err := h.checkHostedQuota(*m); err != nil {
			h.fail(ctx, err)
			return
		}
	}
	if err := h.save(*m); err != nil {
		h.fail(ctx, err)
		return
	}
	h.sendDetail(ctx, *m, fasthttp.StatusOK)
}

func (h *liveMonitorsHandler) runNow(ctx *fasthttp.RequestCtx) {
	m, err := h.store().Get(h.id(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	if m.Paused {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "this monitor is paused: resume it to run a check")
		return
	}
	if !h.ticker() {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"this server cannot run scheduled checks: start serve without -offline, or ask your server owner")
		return
	}
	s := monitorSchedule(*m)
	kind, params := FireJobFor(s, h.env.Services.Hosted)
	j, err := h.env.Services.Jobs.Create(ctx, jobs.NewJob{OrgID: h.env.Services.OrgID, Kind: kind, Params: params, CreatedBy: actorName(ctx)})
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, ScheduleRunResponse{JobID: j.ID, Kind: kind})
}

func (h *liveMonitorsHandler) badgeSet(ctx *fasthttp.RequestCtx) {
	var in MonitorBadgeRequest
	if err := abDecodeBody(ctx, &in); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	m, err := h.store().Get(h.id(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	m.Badge, m.BadgeSecret, m.UpdatedAt = in.Enabled, "", h.now().UTC()
	if in.Enabled {
		m.BadgeSecret = livemon.RandHex(16) // a new link each time: turning it on again revokes the old one
	}
	if err := h.save(*m); err != nil {
		h.fail(ctx, err)
		return
	}
	h.sendDetail(ctx, *m, fasthttp.StatusOK)
}

// badge serves the public status badge: anyone holding the link sees the
// monitor's state and pass rate over the last 24 hours, nothing else.
func (h *liveMonitorsHandler) badge(ctx *fasthttp.RequestCtx) {
	token, _ := ctx.UserValue("token").(string)
	parts := strings.Split(token, ".")
	notFound := func() {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "this badge link does not exist")
	}
	if len(parts) != 3 || parts[0] != h.orgID() {
		notFound()
		return
	}
	m, err := h.store().Get(parts[1])
	if err != nil || !m.Badge || m.BadgeSecret == "" || subtle.ConstantTimeCompare([]byte(parts[2]), []byte(m.BadgeSecret)) != 1 {
		notFound()
		return
	}
	st, err := h.store().State(m.ID)
	if err != nil {
		st = &livemon.State{}
	}
	row := h.row(*m, st)
	value, color := badgeValue(row)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("image/svg+xml; charset=utf-8")
	ctx.Response.Header.Set("Cache-Control", "no-cache, max-age=0")
	ctx.Response.Header.Set(lib.CORPHeader, lib.CORPCrossOrigin)
	ctx.SetBodyString(sharelinks.Badge("agent check", value, color))
}

func badgeValue(r MonitorRow) (string, string) {
	const red, grey = "#e05d44", "#9f9f9f"
	rate := ""
	if r.Day.PassRate != nil {
		rate = fmt.Sprintf(" %.0f%%", math.Round(*r.Day.PassRate*100))
	}
	switch r.Status {
	case "down":
		return "outage", red
	case "degraded":
		return "degraded" + rate, red
	case "paused":
		return "paused", grey
	case "no_data":
		return "no data", grey
	}
	if r.Day.PassRate == nil {
		return "passing", sharelinks.BadgeColor(1)
	}
	return "passing" + rate, sharelinks.BadgeColor(*r.Day.PassRate)
}

func (h *liveMonitorsHandler) orgID() string {
	if h.env.Services.OrgID != "" {
		return h.env.Services.OrgID
	}
	return auth.LocalOrgID
}

func (h *liveMonitorsHandler) sendDetail(ctx *fasthttp.RequestCtx, m livemon.Monitor, status int) {
	st, err := h.store().State(m.ID)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	d := MonitorDetail{MonitorRow: h.row(m, st), Samples: st.Samples, Incidents: slices.Clone(st.Incidents), Tasks: []string{}}
	if len(d.Samples) > MonitorDetailSamples {
		d.Samples = d.Samples[len(d.Samples)-MonitorDetailSamples:]
	}
	slices.Reverse(d.Incidents)
	tasks, note := h.pickTasks(ctx, m)
	if tasks != nil {
		d.Tasks = tasks
	}
	d.TasksNote = note
	lib.SendJSON(ctx, h.env.Mode, status, d)
}

// row builds the list row of m: its schedule, spend today and stats.
func (h *liveMonitorsHandler) row(m livemon.Monitor, st *livemon.State) MonitorRow {
	now := h.now()
	s := monitorSchedule(m)
	r := MonitorRow{Monitor: m, Schedule: s.Name, Cron: s.Cron, CheckBudgetUSD: m.CheckBudgetUSD(), Open: []livemon.Incident{}}
	r.BadgeSecret = ""
	if m.Badge && m.BadgeSecret != "" {
		r.BadgePath = strings.Replace(LiveMonitorBadgePath, "{token}", h.orgID()+"."+m.ID+"."+m.BadgeSecret, 1)
	}
	if !m.Paused {
		if c, err := schedule.ParseCron(s.Cron); err == nil {
			if n := c.Next(now.UTC()); !n.IsZero() {
				r.Next = n.Format(time.RFC3339)
			}
		}
		if err := s.Validate(h.root(), nil); err != nil {
			r.Problem = err.Error()
		}
	}
	r.SpentTodayUSD, _ = schedule.SpentToday(ScheduleStateDir(h.root()), s, now)
	if n := len(st.Samples); n > 0 {
		last := st.Samples[n-1]
		r.Last = &last
	}
	r.Day = monitorStats(st.Samples, now.Add(-24*time.Hour))
	r.Week = monitorStats(st.Samples, now.Add(-7*24*time.Hour))
	for _, inc := range st.Incidents {
		if inc.State == livemon.StateOpen {
			r.Open = append(r.Open, inc)
		}
	}
	r.Status = monitorStatus(m, st, r.Open)
	return r
}

func monitorStatus(m livemon.Monitor, st *livemon.State, open []livemon.Incident) string {
	switch {
	case m.Paused:
		return "paused"
	case slices.ContainsFunc(open, func(i livemon.Incident) bool { return i.Kind == livemon.KindInfra }):
		return "down"
	case len(open) > 0:
		return "degraded"
	case len(st.Samples) == 0:
		return "no_data"
	}
	scored := 0
	for _, s := range st.Samples {
		if s.Status == livemon.SampleOK {
			scored += s.Scored
		}
	}
	if scored < livemon.MinBaselineRuns+livemon.MinRecentRuns && m.Rule.MinPassRate == 0 {
		return "learning"
	}
	return "healthy"
}

// monitorStats sums the samples taken after since.
func monitorStats(samples []livemon.Sample, since time.Time) MonitorStats {
	var st MonitorStats
	var lat []int64
	for _, s := range samples {
		if s.At.Before(since) || s.Status == livemon.SampleSkipped {
			continue
		}
		st.Checks++
		if s.Status == livemon.SampleOK {
			st.OKChecks++
			st.Passed += s.Passed
			st.Scored += s.Scored
		} else {
			st.InfraChecks++
		}
		st.CostUSD += s.CostUSD
		st.CostUnknown += s.CostUnknown
		st.Errors += s.Errors
		for _, r := range s.Runs {
			if r.LatencyMS != nil && !r.Infra {
				lat = append(lat, *r.LatencyMS)
			}
		}
	}
	st.CostUSD = math.Round(st.CostUSD*1e6) / 1e6
	if st.Scored > 0 {
		v := float64(st.Passed) / float64(st.Scored)
		lo, hi := livemon.Wilson(st.Passed, st.Scored)
		st.PassRate, st.PassLow, st.PassHigh = &v, &lo, &hi
	}
	if len(lat) > 0 {
		p50, p95 := livemon.Percentile(lat, 50), livemon.Percentile(lat, 95)
		st.P50MS, st.P95MS = &p50, &p95
	}
	if st.Checks > 0 {
		u := float64(st.Checks-st.InfraChecks) / float64(st.Checks)
		st.Uptime = &u
	}
	return st
}

// check validates m against the project: the agent exists and can run
// without a model or names one, and the benchmark has ready tasks the
// agent can run.
func (h *liveMonitorsHandler) check(ctx context.Context, m *livemon.Monitor) error {
	if err := m.Normalize(); err != nil {
		return err
	}
	cfg, err := monitorAgentConfig(ctx, h.env, m.Agent)
	if err != nil {
		return fmt.Errorf("%w: %v", livemon.ErrInvalid, err)
	}
	if m.Model == "" && !utils.OwnsModel(cfg) {
		return fmt.Errorf("%w: pick a model: agent %s runs on the model you give it", livemon.ErrInvalid, m.Agent)
	}
	if h.env.Services.Suites == nil {
		return nil
	}
	tasks, why := h.pickTasks(ctx, *m)
	if len(tasks) == 0 {
		if why == "" {
			why = "it has no ready task yet"
		}
		return fmt.Errorf("%w: benchmark %s cannot be monitored: %s", livemon.ErrInvalid, m.Benchmark, why)
	}
	return nil
}

// pickTasks is the tasks each check runs: the evaluation's own pick (the
// same evenly spread choice of ready tasks every time), with why none.
func (h *liveMonitorsHandler) pickTasks(ctx context.Context, m livemon.Monitor) ([]string, string) {
	src := h.env.Services.Suites
	if src == nil {
		return nil, ""
	}
	id, _, _ := strings.Cut(m.Benchmark, "@")
	st, err := src.LoadSuite(ctx, id)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			return nil, "it does not exist on this server"
		}
		return nil, "it does not load: " + err.Error()
	}
	cfg, _ := monitorAgentConfig(ctx, h.env, m.Agent)
	if cfg != nil {
		if ok, missing, reason := evalCompat(cfg, st); !ok {
			if reason == "" {
				reason = "the agent lacks " + strings.Join(capNames(missing), ", ")
			}
			return nil, reason
		}
	}
	ids, why := evalPickTasks(st, schemas.SplitPublic, m.Tasks, []*schemas.AgentConfig{cfg})
	return ids, why
}

func capNames(cs []schemas.Capability) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c)
	}
	return out
}

// defaultBenchmark is the benchmark of the agent's newest finished
// evaluation that the monitor could run, or "".
func (h *liveMonitorsHandler) defaultBenchmark(ctx context.Context, m livemon.Monitor) string {
	evals, err := evalstore.Open(h.root()).List()
	if err != nil {
		return ""
	}
	slices.SortFunc(evals, func(a, b *schemas.Evaluation) int { return b.CreatedAt.Compare(a.CreatedAt) })
	tried := map[string]bool{}
	for _, ev := range evals {
		if ev.Status != schemas.EvaluationDone || len(ev.Spec.Arms) == 0 || !slices.ContainsFunc(ev.Spec.Arms, func(a schemas.EvaluationArm) bool { return a.Agent == m.Agent }) {
			continue
		}
		for _, c := range ev.Children {
			b, _, _ := strings.Cut(c.Benchmark, "@")
			if c.ExperimentID == "" || tried[b] {
				continue
			}
			tried[b] = true
			n := m
			n.Benchmark = b
			if n.Tasks == 0 {
				n.Tasks = livemon.DefaultTasks
			}
			if tasks, _ := h.pickTasks(ctx, n); len(tasks) > 0 || h.env.Services.Suites == nil {
				return b
			}
		}
	}
	return ""
}

// checkHostedQuota refuses a running monitor that would take an org past
// the hosted schedule cap (monitors count as schedules).
func (h *liveMonitorsHandler) checkHostedQuota(m livemon.Monitor) error {
	if !h.env.Services.Hosted || m.Paused {
		return nil
	}
	f, err := LoadWebSchedules(h.root())
	if err != nil {
		return nil // the schedules page reports a broken file
	}
	if ScheduleExtras != nil {
		ScheduleExtras(h.root(), f)
	}
	if _, ok := f.Find(m.ScheduleName()); !ok {
		f.Schedules = append(f.Schedules, monitorSchedule(m))
	}
	if len(f.Schedules) > HostedMaxSchedules {
		return fmt.Errorf("%w: at most %d scheduled checks per organization on this server (schedules, drift checks and running monitors count together); pause or delete one first",
			livemon.ErrInvalid, HostedMaxSchedules)
	}
	return nil
}

// monitorAgentConfig reads the agent config agents/<id>.yaml.
func monitorAgentConfig(ctx context.Context, env OrgEnv, id string) (*schemas.AgentConfig, error) {
	if src := env.Services.Agents; src != nil {
		if files, err := src.AgentConfigs(ctx); err == nil {
			for _, f := range files {
				if f.Config != nil && f.Config.ID == id {
					return f.Config, nil
				}
			}
		}
	}
	ps := env.Services.Projects
	if ps == nil {
		return nil, errors.New("the project file store is not available on this server")
	}
	for _, ext := range []string{".yaml", ".yml"} {
		data, _, err := ps.Read(ctx, projectfs.AreaAgents, id+ext)
		if err != nil {
			continue
		}
		var cfg schemas.AgentConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("agent %s: could not read its config: %v", id, err)
		}
		if cfg.ID == "" {
			cfg.ID = id
		}
		return &cfg, nil
	}
	return nil, fmt.Errorf("agent %s does not exist: register it on the Agents page first", id)
}

// MonitorEvaluationSpec is the evaluation each check of m starts: one arm,
// the monitor's tasks of its benchmark, one trial, clean, under the per
// check budget.
func MonitorEvaluationSpec(m livemon.Monitor) schemas.EvaluationSpec {
	name := "Live monitor: " + m.Name
	if len(name) > 120 {
		name = name[:120]
	}
	return schemas.EvaluationSpec{
		Name: name, Mode: schemas.EvaluationModeScore,
		Arms:       []schemas.EvaluationArm{{Label: "A", Agent: m.Agent, Version: m.Version, Model: m.Model, Provider: m.Provider}},
		Benchmarks: []string{m.Benchmark}, TasksPerBenchmark: m.Tasks, Trials: 1, Faults: []string{},
		BudgetUSD: m.CheckBudgetUSD(), Worker: m.Worker,
	}
}

// monitorSchedule is the schedule that fires m's checks.
func monitorSchedule(m livemon.Monitor) schedule.Schedule {
	return schedule.Schedule{Name: m.ScheduleName(), Evaluation: livemon.EvaluationRef(m.ID), Cron: m.Cron(), Timezone: "UTC",
		MaxUSDPerDay: m.BudgetUSDPerDay}
}

// MonitorSchedules appends the schedule of every running monitor of root to
// f (ScheduleExtras). Paused monitors fire nothing.
func MonitorSchedules(root string, f *schedule.File) {
	ms, err := livemon.Open(root).List()
	if err != nil {
		return
	}
	for _, m := range ms {
		if m.Paused {
			continue
		}
		if _, exists := f.Find(m.ScheduleName()); exists {
			continue
		}
		f.Schedules = append(f.Schedules, monitorSchedule(m))
	}
}
