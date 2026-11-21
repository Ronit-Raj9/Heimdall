package handlers

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Run list paging: the default page and the largest page one request may ask for.
const (
	runListDefaultLimit = 100
	runListMaxLimit     = 500
)

// RunListRow is one run of GET /runs: its slot, outcome and timing, with the
// suite's track and the task's output kind so the UI can pick the right view.
// Cost is read from metrics.json for the rows of the page only.
type RunListRow struct {
	RunID     string          `json:"run_id"`
	ExpID     string          `json:"exp_id"`
	PairID    string          `json:"pair_id"`
	Suite     string          `json:"suite"`
	Track     string          `json:"track,omitempty"`   // the suite's track; empty when the suite could not be loaded
	Output    string          `json:"output,omitempty"`  // diff | answer | state | artifact; empty when the suite could not be loaded
	Grading   string          `json:"grading,omitempty"` // verified | judged: who decided y; empty when the suite could not be loaded
	Task      string          `json:"task"`
	Arm       string          `json:"arm"`
	Condition string          `json:"condition"`
	Trial     int             `json:"trial"`
	Attempt   int             `json:"attempt"`
	Counted   bool            `json:"counted"`
	Sealed    bool            `json:"sealed"`
	Outcome   schemas.Outcome `json:"outcome"`
	Y         schemas.Value   `json:"y"`
	CostUSD   *schemas.Value  `json:"cost_usd,omitempty"` // absent for a run with no metrics.json yet
	ConfigID  string          `json:"config_id"`
	Started   *time.Time      `json:"started,omitempty"`
	SealedAt  *time.Time      `json:"sealed_at,omitempty"`
	Error     string          `json:"error,omitempty"` // the first part failure's error of a non-pass run, cut to 600 bytes; empty when none
}

// RunListFacets are the distinct values of every filter across all runs, so
// the UI can offer them without a second request.
type RunListFacets struct {
	Experiments []string `json:"experiments"`
	Suites      []string `json:"suites"`
	Arms        []string `json:"arms"`
	Tasks       []string `json:"tasks"`
	Conditions  []string `json:"conditions"`
	Outcomes    []string `json:"outcomes"`
	Tracks      []string `json:"tracks"`
}

// RunList is GET /runs: one page of runs, newest first, the number that
// matched the filters, and the facets.
type RunList struct {
	Runs   []RunListRow  `json:"runs"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
	Facets RunListFacets `json:"facets"`
}

// RunListHandler serves the run list across experiments.
type RunListHandler struct {
	mode   schemas.Mode
	index  ReadIndex
	suites SuiteSource
}

// NewRunListHandler creates the run list handler. index may be nil (503);
// suites may be nil, and rows then carry no track or output kind.
func NewRunListHandler(mode schemas.Mode, index ReadIndex, suites SuiteSource) *RunListHandler {
	return &RunListHandler{mode: mode, index: index, suites: suites}
}

// RegisterRoutes registers GET /runs.
func (h *RunListHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/runs", lib.ChainMiddlewares(h.listRuns, middlewares...))
}

// runListFilter is the query of GET /runs. An empty field matches anything.
type runListFilter struct {
	exp, suite, arm, task, condition, outcome, track string
}

func (f runListFilter) match(row RunListRow) bool {
	for _, c := range [][2]string{
		{f.exp, row.ExpID}, {f.suite, row.Suite}, {f.arm, row.Arm}, {f.task, row.Task},
		{f.condition, row.Condition}, {f.outcome, string(row.Outcome)}, {f.track, row.Track},
	} {
		if c[0] != "" && c[0] != c[1] {
			return false
		}
	}
	return true
}

// listRuns handles GET /api/bench/runs?exp=&suite=&arm=&task=&condition=&outcome=&track=&limit=&offset=.
// Every run of every experiment the index holds, attempts included, newest
// start first. A filter value that is not a valid id is a 400; limit is
// capped at runListMaxLimit.
func (h *RunListHandler) listRuns(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	args := ctx.QueryArgs()
	var f runListFilter
	for name, dst := range map[string]*string{
		"exp": &f.exp, "suite": &f.suite, "arm": &f.arm, "task": &f.task,
		"condition": &f.condition, "outcome": &f.outcome, "track": &f.track,
	} {
		v := strings.TrimSpace(string(args.Peek(name)))
		if v != "" && !validID(v) {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, name+" is not a valid id")
			return
		}
		*dst = v
	}
	limit, ok := h.intArg(ctx, "limit", runListDefaultLimit)
	if !ok {
		return
	}
	offset, ok := h.intArg(ctx, "offset", 0)
	if !ok {
		return
	}
	limit = min(max(limit, 1), runListMaxLimit)

	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	suiteInfo := h.suiteInfo(work)

	var all []RunListRow
	views := map[string]RunView{}
	for _, expID := range h.index.Experiments() {
		for _, rv := range h.index.Runs(expID) {
			row := runListRow(rv, suiteInfo)
			all = append(all, row)
			views[row.RunID] = rv
		}
	}
	slices.SortFunc(all, func(a, b RunListRow) int {
		return cmp.Or(compareTimeDesc(a.Started, b.Started), cmp.Compare(a.ExpID, b.ExpID), cmp.Compare(a.RunID, b.RunID))
	})

	out := RunList{Runs: []RunListRow{}, Limit: limit, Offset: offset, Facets: runListFacets(all)}
	for _, row := range all {
		if !f.match(row) {
			continue
		}
		if out.Total >= offset && len(out.Runs) < limit {
			out.Runs = append(out.Runs, row)
		}
		out.Total++
	}
	page := make([]RunView, 0, len(out.Runs))
	for _, row := range out.Runs {
		page = append(page, views[row.RunID])
	}
	costs := runCosts(h.index, page)
	for i := range out.Runs {
		if c, found := costs[out.Runs[i].RunID]; found {
			out.Runs[i].CostUSD = &c
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// intArg reads a non-negative integer query argument, def when absent.
func (h *RunListHandler) intArg(ctx *fasthttp.RequestCtx, name string, def int) (int, bool) {
	raw := strings.TrimSpace(string(ctx.QueryArgs().Peek(name)))
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, name+" must be a non-negative integer")
		return 0, false
	}
	return n, true
}

// suiteFacts is what a run row borrows from its suite: the track and the
// output kind of the suite and of each task that overrides it.
type suiteFacts struct {
	track   string
	output  string
	grading string
	tasks   map[string]string // task id -> output kind, only for tasks that set their own
}

// suiteInfo loads every suite once per request. A suite that fails to load
// is left out: its rows carry no track or output, and the list still answers.
func (h *RunListHandler) suiteInfo(ctx context.Context) map[string]suiteFacts {
	out := map[string]suiteFacts{}
	if h.suites == nil {
		return out
	}
	ids, err := h.suites.SuiteIDs(ctx)
	if err != nil {
		return out
	}
	for _, id := range ids {
		s, err := h.suites.LoadSuite(ctx, id)
		if err != nil || s == nil {
			continue
		}
		facts := suiteFacts{track: cmp.Or(s.Track, schemas.TrackCoding), output: "diff", grading: cmp.Or(s.Grading, "verified"), tasks: map[string]string{}}
		if s.Output != nil && s.Output.Kind != "" {
			facts.output = s.Output.Kind
		}
		for _, t := range s.Tasks {
			if t != nil && t.Output != nil && t.Output.Kind != "" {
				facts.tasks[t.ID] = t.Output.Kind
			}
		}
		out[cmp.Or(s.ID, id)] = facts
	}
	return out
}

// runListRow builds one row from a run view. It is pure.
func runListRow(rv RunView, suites map[string]suiteFacts) RunListRow {
	row := RunListRow{
		RunID: rv.Run.RunID, ExpID: cmp.Or(rv.Run.ExpID, rv.Slot.ExpID), PairID: cmp.Or(rv.Run.PairID, rv.Slot.PairID),
		Suite: rv.Slot.Suite, Task: rv.Slot.Task, Arm: rv.Slot.Arm, Condition: rv.Slot.Condition, Trial: rv.Slot.Trial,
		Attempt: rv.Run.Attempt, Counted: rv.Slot.Counted, Sealed: rv.Sealed, Outcome: rv.Run.Outcome, Y: rv.Run.Y,
		ConfigID: rv.Run.Agent.ConfigID, SealedAt: rv.Run.Timing.Sealed,
	}
	if t := rv.Run.Timing.Started; !t.IsZero() {
		row.Started = &t
	}
	if rv.Run.Outcome != schemas.OutcomePass {
		for _, f := range rv.Run.Failures {
			if msg := strings.TrimSpace(f.Error); msg != "" {
				if len(msg) > 600 {
					msg = strings.ToValidUTF8(msg[:600], "")
				}
				row.Error = msg
				break
			}
		}
	}
	if facts, ok := suites[rv.Slot.Suite]; ok {
		row.Track = facts.track
		row.Grading = facts.grading
		row.Output = cmp.Or(facts.tasks[rv.Slot.Task], facts.output)
	}
	return row
}

// runListFacets collects the distinct, sorted filter values of rows.
func runListFacets(rows []RunListRow) RunListFacets {
	sets := make([]map[string]bool, 7)
	for i := range sets {
		sets[i] = map[string]bool{}
	}
	for _, r := range rows {
		for i, v := range []string{r.ExpID, r.Suite, r.Arm, r.Task, r.Condition, string(r.Outcome), r.Track} {
			if v != "" {
				sets[i][v] = true
			}
		}
	}
	sorted := func(set map[string]bool) []string {
		out := make([]string, 0, len(set))
		for v := range set {
			out = append(out, v)
		}
		slices.Sort(out)
		return out
	}
	return RunListFacets{
		Experiments: sorted(sets[0]), Suites: sorted(sets[1]), Arms: sorted(sets[2]), Tasks: sorted(sets[3]),
		Conditions: sorted(sets[4]), Outcomes: sorted(sets[5]), Tracks: sorted(sets[6]),
	}
}

// compareTimeDesc orders newer times first and missing times last.
func compareTimeDesc(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return b.Compare(*a)
}
