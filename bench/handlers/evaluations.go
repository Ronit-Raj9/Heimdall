package handlers

// Evaluations area (agent-first contract, section 3): one agent, or two arms
// for an A/B, across many benchmarks at once. POST /evaluations resolves the
// pack, drops benchmarks the agent cannot run, and builds ONE normal
// experiment per benchmark. Each child goes through the same path QuickRun
// uses: experiments/<name>.yaml written through the project store, then the
// launcher's Plan, Create and Start (local runs, or hosted jobs when the
// launcher is the job runner). The evaluation itself is a JSON file under
// .heimdall/evaluations/ (evalstore), and its status is read live from the
// children.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/packs"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// Evaluation defaults and limits.
const (
	EvaluationDefaultTasks  = 8
	EvaluationDefaultTrials = 2
	EvaluationMaxTasks      = 50
	EvaluationMaxTrials     = 10
	EvaluationMaxBenchmarks = 40
	EvaluationMaxBudget     = 1000.0
	// evaluationLaunchTimeout bounds planning and starting every child.
	evaluationLaunchTimeout = 3 * time.Minute
)

// EvaluationList is GET /evaluations.
type EvaluationList struct {
	Evaluations []*schemas.Evaluation `json:"evaluations"`
}

func init() {
	RegisterOrgArea("evaluations", func(env OrgEnv, r *router.Router) {
		h := &evaluationsHandler{env: env, now: time.Now}
		p := APIPrefix + "/evaluations"
		r.GET(p, env.R(h.list))
		r.POST(p, env.W(h.create))
		r.GET(p+"/{id}", env.R(h.get))
		r.POST(p+"/{id}/cancel", env.W(h.cancel))
	})
}

type evaluationsHandler struct {
	env OrgEnv
	now func() time.Time
}

// evalCompat is the compatibility check; tests replace it.
var evalCompat = compat.Check

var evalArmIDRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{0,15}$`)

func (h *evaluationsHandler) root() string {
	if p := h.env.Services.Projects; p != nil {
		return p.Root()
	}
	if h.env.Cfg != nil {
		return h.env.Cfg.Root
	}
	return ""
}

func (h *evaluationsHandler) store() *evalstore.Store { return evalstore.Open(h.root()) }

func (h *evaluationsHandler) fail(ctx *fasthttp.RequestCtx, status int, msg string) {
	t := lib.ErrTypeBadRequest
	switch status {
	case fasthttp.StatusServiceUnavailable:
		t = lib.ErrTypeUnavailable
	case fasthttp.StatusNotFound:
		t = lib.ErrTypeNotFound
	case fasthttp.StatusConflict:
		t = lib.ErrTypeConflict
	case fasthttp.StatusInternalServerError:
		t = lib.ErrTypeInternal
	}
	lib.SendError(ctx, h.env.Mode, status, t, msg)
}

// load reads the {id} evaluation of this org, or answers the error.
func (h *evaluationsHandler) load(ctx *fasthttp.RequestCtx) (*schemas.Evaluation, bool) {
	id, _ := ctx.UserValue("id").(string)
	if !evalstore.ValidID(id) {
		h.fail(ctx, fasthttp.StatusBadRequest, "evaluation id must look like ev_<letters and digits>")
		return nil, false
	}
	ev, err := h.store().Load(id)
	if err == nil && h.env.Services.OrgID != "" && ev.Org != "" && ev.Org != h.env.Services.OrgID {
		err = evalstore.ErrNotFound
	}
	if errors.Is(err, evalstore.ErrNotFound) {
		sendNotFound(ctx, h.env.Mode, "evaluation", id)
		return nil, false
	}
	if err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, err.Error())
		return nil, false
	}
	return ev, true
}

func (h *evaluationsHandler) list(ctx *fasthttp.RequestCtx) {
	evs, err := h.store().List()
	if err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "list evaluations: "+err.Error())
		return
	}
	out := EvaluationList{Evaluations: []*schemas.Evaluation{}}
	for _, ev := range evs {
		if h.env.Services.OrgID != "" && ev.Org != "" && ev.Org != h.env.Services.OrgID {
			continue
		}
		h.refresh(ev)
		out.Evaluations = append(out.Evaluations, ev)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *evaluationsHandler) get(ctx *fasthttp.RequestCtx) {
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	h.refresh(ev)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ev)
}

// cancel stops every child that still runs. A child that is not running is
// left as it is.
func (h *evaluationsHandler) cancel(ctx *fasthttp.RequestCtx) {
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	h.refresh(ev)
	if terminalEvaluation(ev.Status) {
		h.fail(ctx, fasthttp.StatusConflict, "evaluation "+ev.ID+" already ended ("+ev.Status+")")
		return
	}
	rc, _ := h.env.Services.Launcher.(RunController)
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	var errs []string
	for i := range ev.Children {
		c := &ev.Children[i]
		if c.ExperimentID == "" || terminalChild(c.Status) {
			continue
		}
		if rc != nil {
			if err := rc.Cancel(withPrincipal(work, ctx), c.ExperimentID); err != nil && !errors.Is(err, ErrNotRunning) {
				errs = append(errs, c.Benchmark+": "+err.Error())
				continue
			}
		}
		c.Status = string(schemas.ExperimentCancelled)
	}
	ev.Status = schemas.EvaluationCancelled
	if err := h.store().Save(ev); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "save evaluation: "+err.Error())
		return
	}
	if len(errs) > 0 {
		h.fail(ctx, fasthttp.StatusBadGateway, "some experiments could not be cancelled: "+strings.Join(errs, "; "))
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, ev)
}

func terminalChild(s string) bool {
	switch s {
	case string(schemas.ExperimentDone), string(schemas.ExperimentFailed), string(schemas.ExperimentCancelled), schemas.EvaluationChildSkipped:
		return true
	}
	return false
}

func terminalEvaluation(s string) bool {
	return s == schemas.EvaluationDone || s == schemas.EvaluationFailed || s == schemas.EvaluationCancelled
}

// refresh reads each child's live state from the run index and derives the
// evaluation's status; a changed status is saved.
func (h *evaluationsHandler) refresh(ev *schemas.Evaluation) {
	s := h.env.Services
	changed := false
	if s.Index != nil {
		eh := &ExperimentsHandler{mode: h.env.Mode, index: s.Index, launcher: s.Launcher}
		for i := range ev.Children {
			c := &ev.Children[i]
			if c.ExperimentID == "" || c.Status == schemas.EvaluationChildSkipped {
				continue
			}
			m, ok := s.Index.Manifest(c.ExperimentID)
			if !ok {
				continue
			}
			st := eh.status(m, s.Index.Runs(c.ExperimentID), false)
			if string(st.State) != c.Status && !(c.Status == string(schemas.ExperimentCancelled) && !terminalChild(string(st.State))) {
				c.Status, changed = string(st.State), true
			}
		}
	}
	if next := evaluationStatus(ev); next != ev.Status {
		ev.Status, changed = next, true
	}
	if changed {
		_ = h.store().Save(ev) // a failed save only delays the stored status
	}
}

// evaluationStatus is the evaluation's state from its children. Cancelled
// sticks. Running while any child is not finished; done when at least one
// child finished; failed when none did.
func evaluationStatus(ev *schemas.Evaluation) string {
	if ev.Status == schemas.EvaluationCancelled {
		return ev.Status
	}
	launched, done := 0, 0
	for _, c := range ev.Children {
		if c.Status == schemas.EvaluationChildSkipped {
			continue
		}
		launched++
		if !terminalChild(c.Status) {
			if c.Status == string(schemas.ExperimentCreatedState) && ev.Status == schemas.EvaluationQueued {
				continue
			}
			return schemas.EvaluationRunning
		}
		if c.Status == string(schemas.ExperimentDone) {
			done++
		}
	}
	switch {
	case launched == 0:
		return schemas.EvaluationFailed
	case done > 0:
		return schemas.EvaluationDone
	case ev.Status == schemas.EvaluationQueued:
		return ev.Status
	}
	return schemas.EvaluationFailed
}

func (h *evaluationsHandler) create(ctx *fasthttp.RequestCtx) {
	s := h.env.Services
	if s.Projects == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "the project file store is not available on this server: an evaluation needs it to save its experiment files")
		return
	}
	if s.Launcher == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "no experiment launcher on this server: hosted runs use the shared worker, ask your server owner to enable it")
		return
	}
	var spec schemas.EvaluationSpec
	dec := json.NewDecoder(strings.NewReader(string(ctx.PostBody())))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, "parse evaluation: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, evaluationLaunchTimeout)
	defer cancel()
	if err := h.normalize(work, &spec); err != nil {
		h.fail(ctx, statusOf(err), err.Error())
		return
	}
	if s.Keys != nil { // G10: refuse a model whose org keys are all over budget before anything is written
		var provs, ids []string
		for _, a := range spec.Arms {
			provs, ids = append(provs, a.Provider), append(ids, "")
		}
		if err := KeyBudgetRefusal(work, s.Keys, quickOrgID(ctx, s.OrgID), provs, ids); err != nil {
			h.fail(ctx, fasthttp.StatusConflict, err.Error())
			return
		}
	}
	actor := auth.PrincipalFrom(ctx)
	who := ""
	if actor != nil {
		who = actor.Email
		if who == "" {
			who = actor.UserID
		}
	}
	now := h.now().UTC()
	ev := &schemas.Evaluation{
		ID:  "ev_" + now.Format("20060102_150405") + "_" + randHex(3),
		Org: s.OrgID, Spec: spec, Status: schemas.EvaluationQueued,
		Children: []schemas.EvaluationChild{}, CreatedAt: now, CreatedBy: who,
	}
	if err := h.store().Save(ev); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "save evaluation: "+err.Error())
		return
	}
	agents := h.agentConfigs(work)
	budget := childBudget(spec)
	for _, b := range spec.Benchmarks {
		ev.Children = append(ev.Children, h.launchChild(work, ctx, ev, b, agents, budget, who))
	}
	ev.Status = evaluationStatus(&schemas.Evaluation{Status: schemas.EvaluationRunning, Children: ev.Children})
	if err := h.store().Save(ev); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "save evaluation: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, ev)
}

// normalize validates spec, fills defaults and resolves the pack into the
// benchmark list.
func (h *evaluationsHandler) normalize(ctx context.Context, spec *schemas.EvaluationSpec) error {
	spec.Name = strings.TrimSpace(spec.Name)
	if len(spec.Name) > 120 {
		return badf("name is longer than 120 characters")
	}
	if len(spec.Arms) == 0 {
		return badf("arms is empty: pick an agent and a model")
	}
	if spec.Mode == "" {
		spec.Mode = schemas.EvaluationModeScore
		if len(spec.Arms) == 2 {
			spec.Mode = schemas.EvaluationModeModels
		}
	}
	switch spec.Mode {
	case schemas.EvaluationModeScore:
		if len(spec.Arms) != 1 {
			return badf("mode score takes 1 arm, got %d", len(spec.Arms))
		}
	case schemas.EvaluationModeVersions, schemas.EvaluationModeModels, schemas.EvaluationModeOpponent:
		if len(spec.Arms) != 2 {
			return badf("mode %s takes 2 arms, got %d", spec.Mode, len(spec.Arms))
		}
	default:
		return badf("mode %q is not one of score, versions, models, opponent", spec.Mode)
	}
	for i := range spec.Arms {
		a := &spec.Arms[i]
		if a.Label == "" {
			a.Label = string(rune('A' + i))
		}
		if !evalArmIDRe.MatchString(a.Label) {
			return badf("arm label %q must start with a capital letter and hold only letters and digits", a.Label)
		}
		if i == 1 && a.Label == spec.Arms[0].Label {
			return badf("the two arms have the same label %q", a.Label)
		}
		a.Model, a.Provider = strings.TrimSpace(a.Model), strings.TrimSpace(a.Provider)
		if !quickModelRe.MatchString(a.Model) {
			return badf("arm %s: model %q is not a valid model name", a.Label, a.Model)
		}
		if a.Provider != "" && !quickNameRe.MatchString(a.Provider) {
			return badf("arm %s: provider %q is not a valid provider name", a.Label, a.Provider)
		}
		if a.Version < 0 {
			return badf("arm %s: version must be 1 or more", a.Label)
		}
		path, err := normalizeAgent(a.Agent)
		if err != nil {
			return err
		}
		a.Agent = strings.TrimSuffix(strings.TrimPrefix(path, "agents/"), ".yaml")
		if _, _, err := h.env.Services.Projects.Read(ctx, projectfs.AreaAgents, a.Agent+".yaml"); err != nil {
			if errors.Is(err, projectfs.ErrNotFound) {
				return fmt.Errorf("%w: agent config %s does not exist: register your agent on the Agents page first", errQuickMissing, path)
			}
			return fmt.Errorf("read agent config %s: %w", path, err)
		}
	}
	if spec.TasksPerBenchmark == 0 {
		spec.TasksPerBenchmark = EvaluationDefaultTasks
	}
	if spec.Trials == 0 {
		spec.Trials = EvaluationDefaultTrials
	}
	if spec.TasksPerBenchmark < 1 || spec.TasksPerBenchmark > EvaluationMaxTasks {
		return badf("tasks_per_benchmark must be between 1 and %d", EvaluationMaxTasks)
	}
	if spec.Trials < 1 || spec.Trials > EvaluationMaxTrials {
		return badf("trials must be between 1 and %d", EvaluationMaxTrials)
	}
	if spec.BudgetUSD < 0 || spec.BudgetUSD > EvaluationMaxBudget {
		return badf("budget_usd must be between 0 and %v", EvaluationMaxBudget)
	}
	fs := []string{faults.Clean}
	for _, f := range spec.Faults {
		if f = strings.TrimSpace(f); f != "" && !contains(fs, f) {
			fs = append(fs, f)
		}
	}
	spec.Faults = fs

	var benches []string
	for _, b := range spec.Benchmarks {
		if b = strings.TrimSpace(b); b != "" && !contains(benches, b) {
			if !quickSuiteRe.MatchString(b) {
				return badf("benchmark %q is not a valid benchmark id", b)
			}
			benches = append(benches, b)
		}
	}
	if spec.Pack = strings.TrimSpace(spec.Pack); spec.Pack != "" {
		var available []string
		if src := h.env.Services.Suites; src != nil {
			ids, err := src.SuiteIDs(ctx)
			if err != nil {
				return fmt.Errorf("list benchmarks: %w", err)
			}
			available = ids
		}
		got := packs.Resolve(spec.Pack, available)
		if len(got) == 0 {
			return badf("pack %q has no benchmark on this server", spec.Pack)
		}
		for _, b := range got {
			if !contains(benches, b) {
				benches = append(benches, b)
			}
		}
	}
	if len(benches) == 0 {
		return badf("pick at least one benchmark or a pack")
	}
	if len(benches) > EvaluationMaxBenchmarks {
		return badf("at most %d benchmarks per evaluation, got %d", EvaluationMaxBenchmarks, len(benches))
	}
	spec.Benchmarks = benches
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// childBudget splits the evaluation budget over the benchmarks; with none
// set, each child gets the QuickRun default.
func childBudget(spec schemas.EvaluationSpec) float64 {
	if spec.BudgetUSD <= 0 || len(spec.Benchmarks) == 0 {
		return QuickRunDefaultBudget
	}
	return float64(int(spec.BudgetUSD/float64(len(spec.Benchmarks))*100)) / 100
}

// agentConfigs maps agents/<id>.yaml to its decoded config, when the server
// has an agent source.
func (h *evaluationsHandler) agentConfigs(ctx context.Context) map[string]*schemas.AgentConfig {
	out := map[string]*schemas.AgentConfig{}
	if h.env.Services.Agents == nil {
		return out
	}
	files, err := h.env.Services.Agents.AgentConfigs(ctx)
	if err != nil {
		return out
	}
	for _, f := range files {
		if f.Err == nil && f.Config != nil {
			out[f.Path] = f.Config
		}
	}
	return out
}

// launchChild builds, saves, plans, creates and starts the experiment of one
// benchmark. Every problem becomes a skipped child; it never fails the
// evaluation as a whole.
func (h *evaluationsHandler) launchChild(work context.Context, ctx *fasthttp.RequestCtx, ev *schemas.Evaluation, bench string, agents map[string]*schemas.AgentConfig, budget float64, who string) schemas.EvaluationChild {
	s := h.env.Services
	child := schemas.EvaluationChild{Benchmark: bench}
	skip := func(format string, a ...any) schemas.EvaluationChild {
		child.Status, child.Skipped = schemas.EvaluationChildSkipped, fmt.Sprintf(format, a...)
		return child
	}
	spec := ev.Spec
	suiteRef := bench
	suiteID, _, _ := strings.Cut(bench, "@")
	var st *schemas.Suite
	if s.Suites != nil {
		loaded, err := s.Suites.LoadSuite(work, suiteID)
		if err != nil {
			if errors.Is(err, schemas.ErrNotFound) {
				return skip("benchmark %s does not exist on this server", suiteID)
			}
			return skip("load benchmark %s: %v", suiteID, err)
		}
		st = loaded
		if !strings.Contains(bench, "@") && st.Version != "" {
			suiteRef = suiteID + "@" + st.Version
		}
	}
	for _, a := range spec.Arms {
		ag := agents["agents/"+a.Agent+".yaml"]
		if ag == nil || st == nil {
			continue // nothing to check here; the planner refuses an unknown agent or suite
		}
		if ok, missing, reason := evalCompat(ag, st); !ok {
			if reason == "" {
				reason = compat.Reason(a.Agent, suiteID, missing)
			}
			return skip("%s", reason)
		}
	}
	tasks := schemas.TaskSelector{All: true}
	if st != nil {
		ids := admittedTasks(st)
		if len(ids) == 0 {
			return skip("benchmark %s has no admitted task yet", suiteID)
		}
		tasks = schemas.TaskSelector{IDs: spreadPick(ids, spec.TasksPerBenchmark)}
	}
	condIDs, fskips := faults.Conditions(spec.Faults, st)
	if len(condIDs) == 0 || condIDs[0] != schemas.ConditionClean {
		condIDs = append([]string{schemas.ConditionClean}, condIDs...)
	}
	conds := make([]quickCondition, 0, len(condIDs))
	for _, id := range condIDs {
		conds = append(conds, quickCondition{ID: id, Tasks: tasks, Trials: spec.Trials})
	}

	slug := strings.NewReplacer(".", "-", "_", "-").Replace(suiteID)
	name := "eval-" + strings.TrimPrefix(ev.ID, "ev_") + "-" + slug
	name = strings.ReplaceAll(name, "_", "-")
	f := quickFile{
		Name: name, Suite: suiteRef, Base: "agents/" + spec.Arms[0].Agent + ".yaml",
		Arms:        evalArms(spec),
		Primary:     schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:      []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions:  conds,
		Budget:      schemas.BudgetSpec{TotalUSD: budget, ReserveUSD: quickReserve(budget, 2*len(tasks.IDs)*spec.Trials*len(conds))},
		Verdict:     quickVerdict{Policy: "paired_signflip"},
		Seed:        ev.CreatedAt.Unix(),
		Concurrency: 2, ManifestMode: "agent_exposure",
	}
	if _, _, err := s.Projects.Read(work, projectfs.AreaPolicies, strings.TrimPrefix(quickRunPolicy, "policies/")); err == nil {
		f.Policy = quickRunPolicy
	}
	body, err := marshalQuickFile(f)
	if err == nil {
		body, err = appendEvalID(body, ev.ID)
	}
	if err != nil {
		return skip("encode experiment: %v", err)
	}
	var notes []string
	for _, fs := range fskips {
		notes = append(notes, fs.Fault+": "+fs.Reason)
	}
	head := fmt.Sprintf("# Evaluation %s: benchmark %s, %d task(s) x %d trial(s).\n", ev.ID, suiteRef, len(tasks.IDs), spec.Trials)
	if len(notes) > 0 {
		head += "# Faults skipped: " + strings.Join(notes, "; ") + "\n"
	}
	data := append([]byte(head), body...)
	exp, _, err := DecodeExperiment("application/yaml", data)
	if err != nil {
		return skip("built an unreadable experiment: %v", err)
	}
	m, err := s.Launcher.Plan(work, exp)
	if err == nil && m == nil {
		err = errors.New("the planner returned no manifest")
	}
	if err != nil {
		return skip("plan experiment: %v", err)
	}
	if plan := BuildPlan(m); len(plan.Errors) > 0 {
		return skip("plan experiment: %s", strings.Join(plan.Errors, "; "))
	}
	if err := os.MkdirAll(filepath.Join(s.Projects.Root(), "experiments"), 0o755); err != nil {
		return skip("create the experiments folder: %v", err)
	}
	if _, err := s.Projects.Write(work, projectfs.AreaExperiments, name+".yaml", data,
		projectfs.WriteOpts{Actor: who, Note: "evaluation " + ev.ID}); err != nil {
		return skip("save experiments/%s.yaml: %v", name, err)
	}
	if err := s.Launcher.Create(work, m); err != nil {
		return skip("create experiment %s: %v", m.ExpID, err)
	}
	tenancy.SetCreated(ctx, m.ExpID)
	child.ExperimentID, child.Status = m.ExpID, string(schemas.ExperimentCreatedState)
	if err := s.Launcher.Start(withPrincipal(work, ctx), m.ExpID); err != nil {
		child.Status, child.Skipped = string(schemas.ExperimentFailed), "start experiment (it was created; start it from its page): "+err.Error()
		return child
	}
	child.Status = string(schemas.ExperimentRunning)
	return child
}

// appendEvalID adds the evaluation_id line marshalQuickFile cannot carry.
func appendEvalID(body []byte, id string) ([]byte, error) {
	if !evalstore.ValidID(id) {
		return nil, fmt.Errorf("bad evaluation id %q", id)
	}
	return append(body, []byte("evaluation_id: "+id+"\n")...), nil
}

// evalArms turns the spec's arms into experiment arms. Score mode runs one
// arm twice (A and a marked repeat B) because the planner wants two arms
// with different config hashes; the repeat measures the noise floor.
func evalArms(spec schemas.EvaluationSpec) map[string]schemas.ArmSpec {
	out := map[string]schemas.ArmSpec{}
	base := spec.Arms[0].Agent
	for _, a := range spec.Arms {
		arm := armFor(&QuickRunModel{Provider: a.Provider, Model: a.Model})
		agentLabel := a.Agent
		if a.Version > 0 {
			agentLabel += fmt.Sprintf(" v%d", a.Version)
		}
		arm.Label = agentLabel + " on " + arm.Label
		if a.Agent != base || a.Version > 0 {
			arm.Agent = "agents/" + a.Agent + ".yaml"
			if a.Version > 0 {
				arm.Agent += fmt.Sprintf("@v%d", a.Version)
			}
		}
		out[a.Label] = arm
	}
	if len(spec.Arms) == 1 {
		a := out[spec.Arms[0].Label]
		rep := schemas.ArmSpec{Label: a.Label + " (repeat)", Agent: a.Agent, Set: map[string]any{}}
		for k, v := range a.Set {
			rep.Set[k] = v
		}
		rep.Set["env.HEIMDALL_QUICKRUN_ARM"] = "B"
		id := "B"
		if spec.Arms[0].Label == "B" {
			id = "A"
		}
		out[id] = rep
	}
	return out
}

// admittedTasks is the suite's admitted task ids, sorted (the planner
// refuses a draft task in a locked suite).
func admittedTasks(st *schemas.Suite) []string {
	ids := append([]string(nil), st.TaskIDs...)
	if st.Lock != nil {
		ids = ids[:0]
		for _, t := range st.Tasks {
			if summarizeTask(st, t, nil).Admitted {
				ids = append(ids, t.ID)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// spreadPick takes n ids spread evenly over the sorted list, so the tasks
// come from across the benchmark rather than its first few.
func spreadPick(ids []string, n int) []string {
	if n >= len(ids) {
		return append([]string(nil), ids...)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ids[i*len(ids)/n])
	}
	return out
}
