package handlers

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

// Run list paging: the default page and the largest page one request may ask for.
const (
	runListDefaultLimit = 100
	runListMaxLimit     = 500
)

// RunListRow is one run of GET /runs: its slot, outcome and timing, with the
// suite's track and the task's output kind so the UI can pick the right view.
// Cost, steps, tool calls and tokens are read from metrics.json for the rows
// of the page only; each is absent while the run has no observed value.
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
	CostUSD   *schemas.Value  `json:"cost_usd,omitempty"`   // absent for a run with no metrics.json yet
	Steps     *float64        `json:"steps,omitempty"`      // metrics.json steps; absent when not observed
	ToolCalls *float64        `json:"tool_calls,omitempty"` // metrics.json tool_calls; absent when not observed
	Tokens    *float64        `json:"tokens,omitempty"`     // metrics.json tokens; absent when not observed
	ConfigID  string          `json:"config_id"`
	Model     string          `json:"model,omitempty"`         // the arm's model from the experiment manifest
	AgentVer  string          `json:"agent_version,omitempty"` // the agent's own version string, when it declares one
	Version   int             `json:"version,omitempty"`       // the registered agent version (v1, v2) whose config hash matches; 0 when none
	Fault     string          `json:"fault,omitempty"`         // the fault injected in this run (model-down, broken-tool, misleading-instruction or the probe family); empty for a clean run
	Started   *time.Time      `json:"started,omitempty"`
	SealedAt  *time.Time      `json:"sealed_at,omitempty"`
	Error     string          `json:"error,omitempty"` // the first part failure's error of a non-pass run, cut to 600 bytes; empty when none
	// Excluded is why a sealed run is left out of scores (schemas.ExcludedReason):
	// out_of_memory, infra_error, invalid_evaluator or unknown; empty when it scores.
	Excluded string `json:"excluded,omitempty"`
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
	Agents      []string `json:"agents"`
	Faults      []string `json:"faults"`
	Models      []string `json:"models"`
}

// RunList is GET /runs: one page of runs in the asked order (newest first by
// default), the number that
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
	root   string // bench root: registered agent versions; "" leaves Version 0
}

// NewRunListHandler creates the run list handler. index may be nil (503);
// suites may be nil, and rows then carry no track or output kind.
func NewRunListHandler(mode schemas.Mode, index ReadIndex, suites SuiteSource) *RunListHandler {
	return &RunListHandler{mode: mode, index: index, suites: suites}
}

// WithRoot sets the bench root, so rows name the registered agent version
// (.heimdall/agent-versions) whose config hash matches the run's.
func (h *RunListHandler) WithRoot(root string) *RunListHandler {
	h.root = root
	return h
}

// RegisterRoutes registers GET /runs.
func (h *RunListHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/runs", lib.ChainMiddlewares(h.listRuns, middlewares...))
}

// runListFilter is the query of GET /runs. An empty field matches anything.
// agent matches the run's agent config id (the arm id for a run with none),
// benchmark the suite id with or without its version, and fault the row's
// fault name ("clean" for a run without one).
type runListFilter struct {
	exp, suite, arm, task, condition, outcome, track string
	agent, benchmark, fault, model                   string
}

func (f runListFilter) match(row RunListRow) bool {
	for _, c := range [][2]string{
		{f.exp, row.ExpID}, {f.suite, row.Suite}, {f.arm, row.Arm}, {f.task, row.Task},
		{f.condition, row.Condition}, {f.outcome, string(row.Outcome)}, {f.track, row.Track},
		{f.agent, rowAgent(row)}, {f.model, row.Model},
	} {
		if c[0] != "" && c[0] != c[1] {
			return false
		}
	}
	if f.benchmark != "" {
		id, _, _ := strings.Cut(row.Suite, "@")
		if f.benchmark != row.Suite && f.benchmark != id {
			return false
		}
	}
	if f.fault != "" && f.fault != cmp.Or(row.Fault, faults.Clean) {
		return false
	}
	return true
}

// rowAgent is the agent a run used: its config id, else its arm id.
func rowAgent(row RunListRow) string { return cmp.Or(row.ConfigID, row.Arm) }

// listRuns handles GET /api/bench/runs?exp=&suite=&benchmark=&arm=&agent=&model=&task=&condition=&fault=&outcome=&track=&sort=&dir=&limit=&offset=.
// Every run of every experiment the index holds, attempts included, in the
// order of sort and dir (default newest start first), sorted before paging.
// A filter value that is not a valid id, or an unknown sort or dir, is a
// 400; limit is capped at runListMaxLimit.
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
		"agent": &f.agent, "benchmark": &f.benchmark, "fault": &f.fault, "model": &f.model,
	} {
		v := strings.TrimSpace(string(args.Peek(name)))
		if v != "" && !validID(v) && !(name == "model" && validModelFilter(v)) {
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
	order, ok := h.sortArgs(ctx)
	if !ok {
		return
	}

	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	suiteInfo := h.suiteInfo(work)

	var all []RunListRow
	views := map[string]RunView{}
	versions := newVersionIndex(h.root, suite.LibraryDir())
	for _, expID := range h.index.Experiments() {
		m, _ := h.index.Manifest(expID)
		for _, rv := range h.index.Runs(expID) {
			row := runListRow(rv, suiteInfo)
			armFacts(&row, rv, m, versions)
			all = append(all, row)
			views[row.RunID] = rv
		}
	}

	out := RunList{Runs: []RunListRow{}, Limit: limit, Offset: offset, Facets: runListFacets(all)}
	var matched []RunListRow
	for _, row := range all {
		if f.match(row) {
			matched = append(matched, row)
		}
	}
	out.Total = len(matched)
	// A sort on a metric reads metrics.json for every matched run, so the
	// order holds across pages; any other sort reads it for the page only.
	if order.needsMetrics() {
		fillRunMetrics(h.index, matched, views)
	}
	order.sort(matched)
	if offset < len(matched) {
		out.Runs = matched[offset:min(offset+limit, len(matched))]
	}
	if !order.needsMetrics() {
		fillRunMetrics(h.index, out.Runs, views)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// fillRunMetrics sets the cost, steps, tool calls and tokens of rows from
// each run's metrics.json.
func fillRunMetrics(index ReadIndex, rows []RunListRow, views map[string]RunView) {
	page := make([]RunView, 0, len(rows))
	for _, row := range rows {
		page = append(page, views[row.RunID])
	}
	read := runMetrics(index, page)
	costs := costsOf(page, read)
	for i := range rows {
		r := &rows[i]
		if c, found := costs[r.RunID]; found {
			r.CostUSD = &c
		}
		if mr := read[r.RunID]; mr.found {
			r.Steps = observed(mr.values, metrics.Steps)
			r.ToolCalls = observed(mr.values, metrics.ToolCalls)
			r.Tokens = observed(mr.values, metrics.Tokens)
		}
	}
}

// runListSorts are the sort keys GET /runs accepts, in the order the error
// message lists them.
var runListSorts = []string{"started", "duration", "outcome", "cost", "tokens", "steps", "task", "agent"}

// runListOrder is the sort of GET /runs: a key of runListSorts and a
// direction. The default is started, newest first.
type runListOrder struct {
	key  string
	desc bool
}

// sortArgs reads sort and dir. An unknown value is a 400 that names the
// allowed ones.
func (h *RunListHandler) sortArgs(ctx *fasthttp.RequestCtx) (runListOrder, bool) {
	args := ctx.QueryArgs()
	order := runListOrder{key: "started", desc: true}
	if v := strings.TrimSpace(string(args.Peek("sort"))); v != "" {
		if !slices.Contains(runListSorts, v) {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "sort must be one of "+strings.Join(runListSorts, ", "))
			return order, false
		}
		order.key = v
	}
	switch v := strings.TrimSpace(string(args.Peek("dir"))); v {
	case "":
	case "asc":
		order.desc = false
	case "desc":
		order.desc = true
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "dir must be one of asc, desc")
		return order, false
	}
	return order, true
}

// needsMetrics reports whether the sort key is read from metrics.json.
func (o runListOrder) needsMetrics() bool {
	return o.key == "cost" || o.key == "tokens" || o.key == "steps"
}

// sort orders rows in place. A row without a value for the key (no start,
// no cost, an unsealed run's duration) goes last in either direction; ties
// keep the default order, newest start first.
func (o runListOrder) sort(rows []RunListRow) {
	slices.SortStableFunc(rows, func(a, b RunListRow) int {
		var c int
		switch o.key {
		case "started":
			c = compareMaybe(timeKey(a.Started), timeKey(b.Started), o.desc)
		case "duration":
			c = compareMaybe(durationKey(a), durationKey(b), o.desc)
		case "cost":
			c = compareMaybe(costKey(a.CostUSD), costKey(b.CostUSD), o.desc)
		case "tokens":
			c = compareMaybe(a.Tokens, b.Tokens, o.desc)
		case "steps":
			c = compareMaybe(a.Steps, b.Steps, o.desc)
		case "outcome":
			c = flip(cmp.Compare(a.Outcome, b.Outcome), o.desc)
		case "task":
			c = flip(cmp.Compare(a.Task, b.Task), o.desc)
		case "agent":
			c = flip(cmp.Compare(rowAgent(a), rowAgent(b)), o.desc)
		}
		return cmp.Or(c, compareTimeDesc(a.Started, b.Started), cmp.Compare(a.ExpID, b.ExpID), cmp.Compare(a.RunID, b.RunID))
	})
}

// flip negates c for a descending sort.
func flip(c int, desc bool) int {
	if desc {
		return -c
	}
	return c
}

// compareMaybe compares two optional values in the given direction, with a
// missing value after any present one.
func compareMaybe(a, b *float64, desc bool) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return flip(cmp.Compare(*a, *b), desc)
}

// timeKey is t as Unix seconds, nil when absent.
func timeKey(t *time.Time) *float64 {
	if t == nil || t.IsZero() {
		return nil
	}
	v := float64(t.UnixNano()) / 1e9
	return &v
}

// durationKey is a sealed run's seconds from start to seal, nil when either
// time is missing.
func durationKey(r RunListRow) *float64 {
	if r.Started == nil || r.SealedAt == nil || r.Started.IsZero() || r.SealedAt.IsZero() {
		return nil
	}
	v := r.SealedAt.Sub(*r.Started).Seconds()
	return &v
}

// costKey is an observed cost in USD, nil when the cost is not known.
func costKey(v *schemas.Value) *float64 {
	if v == nil || !v.IsObserved() {
		return nil
	}
	f, ok := v.Float()
	if !ok {
		return nil
	}
	return &f
}

// observed is metric name's value when observed, else nil.
func observed(values map[string]schemas.Value, name string) *float64 {
	v, ok := values[name]
	if !ok {
		return nil
	}
	f, ok := v.Float()
	if !ok || !v.IsObserved() {
		return nil
	}
	return &f
}

// validModelFilter accepts a model name as the model filter: provider
// prefixes and colons are allowed, nothing that could escape a path.
func validModelFilter(v string) bool {
	if len(v) > 200 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-/:@", r):
		default:
			return false
		}
	}
	return !strings.Contains(v, "..")
}

// armFacts fills the model, agent version and registered version of row
// from its run and the experiment manifest (m may be nil).
func armFacts(row *RunListRow, rv RunView, m *schemas.Manifest, versions *versionIndex) {
	row.AgentVer = rv.Run.Agent.AgentVersion
	var agentHash string
	if m != nil {
		if arm, ok := m.Arms[rv.Slot.Arm]; ok {
			row.Model = arm.Config.Model
			row.AgentVer = cmp.Or(row.AgentVer, arm.Config.AgentVersion)
			if row.ConfigID == "" {
				row.ConfigID = arm.Config.ID
			}
			// Versions are matched by agent hash (the config without its
			// model and provider), as the agent leaderboard does.
			agentHash = armAgentHash(arm.Config)
		}
	}
	if versions != nil {
		row.Version = versions.lookup(row.ConfigID, agentHash)
	}
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
	probes  map[string]string // probe id -> fault name (runFault)
}

// runListSuiteBudget is how long the runs list waits for one suite.
const runListSuiteBudget = 2 * time.Second

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
		// A pack still loading (a large library pack after start, cached by
		// the source) is left out rather than holding the whole list.
		one, cancel := context.WithTimeout(ctx, runListSuiteBudget)
		s, err := h.suites.LoadSuite(one, id)
		cancel()
		if err != nil || s == nil {
			continue
		}
		facts := suiteFacts{track: cmp.Or(s.Track, schemas.TrackCoding), output: "diff", grading: cmp.Or(s.Grading, "verified"), tasks: map[string]string{}, probes: map[string]string{}}
		for _, p := range s.Probes {
			if name := familyFault(p.PS2Family); name != "" {
				facts.probes[p.ID] = name
			}
		}
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
	if rv.Sealed {
		row.Excluded = schemas.ExcludedReason(&rv.Run)
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
	facts, ok := suites[rv.Slot.Suite]
	row.Fault = runFault(rv.Slot.Condition, facts.probes)
	if ok {
		row.Track = facts.track
		row.Grading = facts.grading
		row.Output = cmp.Or(facts.tasks[rv.Slot.Task], facts.output)
	}
	return row
}

// familyFault is the evaluation fault name of a probe family: the three
// evaluation faults by their names, any other family with dashes.
func familyFault(f schemas.PS2Family) string {
	switch f {
	case "":
		return ""
	case schemas.FamilyUnavailableModel:
		return faults.ModelDown
	case schemas.FamilyBrokenTool:
		return faults.BrokenTool
	case schemas.FamilyMisleadingInstruction:
		return faults.MisleadingInstruction
	}
	return strings.ReplaceAll(string(f), "_", "-")
}

// runFault is the fault a condition injected: "" for clean, the generic
// probes and the suite's probes by their family, else a fault named in the
// condition id, else the condition id itself.
func runFault(condition string, probes map[string]string) string {
	switch condition {
	case "", schemas.ConditionClean:
		return ""
	case schemas.GenericProbeModelDown:
		return faults.ModelDown
	case schemas.GenericProbeBrokenTool:
		return faults.BrokenTool
	}
	if name := probes[condition]; name != "" {
		return name
	}
	if name := reportcard.DefaultFault(condition); name != "" {
		return name
	}
	return condition
}

// runListFacets collects the distinct, sorted filter values of rows.
func runListFacets(rows []RunListRow) RunListFacets {
	sets := make([]map[string]bool, 10)
	for i := range sets {
		sets[i] = map[string]bool{}
	}
	for _, r := range rows {
		for i, v := range []string{r.ExpID, r.Suite, r.Arm, r.Task, r.Condition, string(r.Outcome), r.Track, r.ConfigID, cmp.Or(r.Fault, faults.Clean), r.Model} {
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
		Agents: sorted(sets[7]), Faults: sorted(sets[8]), Models: sorted(sets[9]),
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
