package handlers

// QuickRun area (web parity L28): one call from a suite, an agent and a model
// to a running experiment. POST /quickrun writes experiments/quick-<ts>.yaml
// through the project store, plans it, estimates it, creates it and starts
// it. The file stays in the project so the run can be repeated or edited.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// QuickRun defaults and limits.
const (
	QuickRunDefaultSuite  = "coding-core"
	QuickRunDefaultAgent  = "agents/codex.yaml"
	QuickRunDefaultTasks  = 5
	QuickRunDefaultTrials = 1
	QuickRunDefaultBudget = 5.0
	QuickRunMaxTasks      = 50
	QuickRunMaxTrials     = 5
	QuickRunMaxBudget     = 100.0
	quickRunPolicy        = "policies/default.yaml"
)

// QuickRunModel is the UI's ModelRef: a provider, a model and optionally the
// org key to use. KeyID never carries the secret itself.
type QuickRunModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	KeyID    string `json:"keyId,omitempty"`
}

// QuickRunRequest is the body of POST /quickrun. Every field but model has a
// default. CompareModel, when set, becomes arm B; otherwise arm B repeats arm
// A (an A/A run: the verdict then measures the noise floor of one model).
type QuickRunRequest struct {
	Suite        string         `json:"suite,omitempty"`
	Agent        string         `json:"agent,omitempty"`
	Model        *QuickRunModel `json:"model"`
	CompareModel *QuickRunModel `json:"compare_model,omitempty"`
	Tasks        int            `json:"tasks,omitempty"`
	Trials       int            `json:"trials,omitempty"`
	BudgetUSD    float64        `json:"budget_usd,omitempty"`
	// Start false stops after create: the experiment is planned and frozen
	// but nothing runs.
	Start *bool `json:"start,omitempty"`
}

// QuickRunResponse is the answer of POST /quickrun.
type QuickRunResponse struct {
	ExpID        string                    `json:"exp_id"`
	Path         string                    `json:"path"` // project path of the written experiment file
	ManifestHash string                    `json:"manifest_hash"`
	Started      bool                      `json:"started"`
	Plan         schemas.ExperimentPlan    `json:"plan"`
	Estimate     *schemas.Forecast         `json:"estimate"` // null when no forecast is wired
	Status       *schemas.ExperimentStatus `json:"status,omitempty"`
}

var (
	quickForecastMu sync.Mutex
	quickForecasts  = map[string]ForecastSource{}
)

// SetQuickRunForecast wires the forecast of the org whose project root is
// root. cmd/bifrost-bench calls it from a service hook; without one the
// QuickRun answer carries a null estimate.
func SetQuickRunForecast(root string, f ForecastSource) {
	quickForecastMu.Lock()
	defer quickForecastMu.Unlock()
	if f == nil {
		delete(quickForecasts, root)
		return
	}
	quickForecasts[root] = f
}

func quickRunForecast(root string) ForecastSource {
	quickForecastMu.Lock()
	defer quickForecastMu.Unlock()
	return quickForecasts[root]
}

func init() {
	RegisterOrgArea("quickrun", func(env OrgEnv, r *router.Router) {
		h := &quickRunHandler{env: env, now: time.Now}
		r.POST(APIPrefix+"/quickrun", env.W(h.post))
	})
}

type quickRunHandler struct {
	env OrgEnv
	now func() time.Time
}

var (
	quickNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	quickSuiteRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}(@[A-Za-z0-9._-]{1,32})?$`)
	quickModelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,127}$`)
)

var quickProviders = map[string]bool{"openai": true, "anthropic": true, "gemini": true, "openrouter": true, "groq": true, "sarvam": true, "openai-compatible": true}

// quickFile is the experiment file QuickRun writes: only the fields it sets,
// in a readable order.
type quickFile struct {
	Name         string                     `yaml:"name"`
	Suite        string                     `yaml:"suite"`
	Base         string                     `yaml:"base"`
	Arms         map[string]schemas.ArmSpec `yaml:"arms"`
	Primary      schemas.Endpoint           `yaml:"primary"`
	Guards       []schemas.GuardSpec        `yaml:"guards"`
	Conditions   []quickCondition           `yaml:"conditions"`
	Budget       schemas.BudgetSpec         `yaml:"budget"`
	Verdict      quickVerdict               `yaml:"verdict"`
	Seed         int64                      `yaml:"seed"`
	Concurrency  int                        `yaml:"concurrency"`
	ManifestMode string                     `yaml:"manifest_mode"`
	Policy       string                     `yaml:"policy,omitempty"`
}

type quickCondition struct {
	ID     string               `yaml:"id"`
	Tasks  schemas.TaskSelector `yaml:"tasks"`
	Trials int                  `yaml:"trials"`
}

type quickVerdict struct {
	Policy string `yaml:"policy"`
}

func (h *quickRunHandler) fail(ctx *fasthttp.RequestCtx, status int, msg string) {
	t := lib.ErrTypeBadRequest
	switch status {
	case fasthttp.StatusServiceUnavailable:
		t = lib.ErrTypeUnavailable
	case fasthttp.StatusNotFound:
		t = lib.ErrTypeNotFound
	case fasthttp.StatusConflict:
		t = lib.ErrTypeConflict
	}
	lib.SendError(ctx, h.env.Mode, status, t, msg)
}

func (h *quickRunHandler) post(ctx *fasthttp.RequestCtx) {
	s := h.env.Services
	if s.Projects == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "the project file store is not available on this server: QuickRun needs it to save the experiment file")
		return
	}
	if s.Launcher == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "no experiment launcher on this server: hosted runs use the shared worker, ask your server owner to enable it")
		return
	}
	var req QuickRunRequest
	if body := ctx.PostBody(); len(body) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(body)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			h.fail(ctx, fasthttp.StatusBadRequest, "parse QuickRun request: "+err.Error())
			return
		}
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()

	data, rel, err := h.build(work, &req)
	if err != nil {
		h.fail(ctx, statusOf(err), err.Error())
		return
	}
	if s.Keys != nil { // G10: a model whose org keys are all over budget is refused before anything is written
		var provs, ids []string
		for _, m := range []*QuickRunModel{req.Model, req.CompareModel} {
			if m != nil {
				provs, ids = append(provs, m.Provider), append(ids, m.KeyID)
			}
		}
		if err := KeyBudgetRefusal(work, s.Keys, quickOrgID(ctx, s.OrgID), provs, ids); err != nil {
			h.fail(ctx, fasthttp.StatusConflict, err.Error())
			return
		}
	}
	exp, _, err := DecodeExperiment("application/yaml", data)
	if err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "QuickRun built an unreadable experiment: "+err.Error())
		return
	}

	m, err := s.Launcher.Plan(work, exp)
	if err == nil && m == nil {
		err = errors.New("the planner returned no manifest")
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			sendDepError(ctx, h.env.Mode, "plan experiment", err)
			return
		}
		h.fail(ctx, fasthttp.StatusBadRequest, "plan experiment: "+err.Error()+". Pick another suite, agent or model, or open the experiment builder for the full form.")
		return
	}
	plan := BuildPlan(m)
	if len(plan.Errors) > 0 {
		h.fail(ctx, fasthttp.StatusBadRequest, "plan experiment: "+strings.Join(plan.Errors, "; "))
		return
	}

	var est *schemas.Forecast
	if f := quickRunForecast(h.env.Cfg.Root); f != nil {
		fe, ferr := f.Estimate(work, exp)
		switch {
		case ferr == nil:
			est = fe
		case errors.Is(ferr, ErrPlanRefused):
			est = RefusedForecast(exp, ferr, h.now())
		default:
			plan.Warnings = append(plan.Warnings, "estimate unavailable: "+ferr.Error())
		}
	}

	// The file is saved once the plan is accepted, so a refused request
	// leaves nothing behind in the project.
	actor := auth.PrincipalFrom(ctx)
	who := actor.Email
	if who == "" {
		who = actor.UserID
	}
	// The Phase 0 projectfs store refuses a write while the area folder
	// itself is missing, so make sure experiments/ exists first.
	if err := os.MkdirAll(filepath.Join(s.Projects.Root(), "experiments"), 0o755); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "create the experiments folder: "+err.Error())
		return
	}
	if _, err := s.Projects.Write(work, projectfs.Area("experiments"), strings.TrimPrefix(rel, "experiments/"), data,
		projectfs.WriteOpts{Actor: who, Note: "quickrun"}); err != nil {
		h.fail(ctx, statusOf(err), "save "+rel+": "+err.Error())
		return
	}
	if err := s.Launcher.Create(work, m); err != nil {
		sendDepError(ctx, h.env.Mode, "create experiment "+m.ExpID, err)
		return
	}
	tenancy.SetCreated(ctx, m.ExpID)
	resp := QuickRunResponse{ExpID: m.ExpID, Path: rel, ManifestHash: m.Hash, Plan: plan, Estimate: est}
	if req.Start == nil || *req.Start {
		if err := s.Launcher.Start(withPrincipal(work, ctx), m.ExpID); err != nil {
			sendDepError(ctx, h.env.Mode, "start experiment "+m.ExpID+" (it was created; start it from its page)", err)
			return
		}
		resp.Started = true
		st := schemas.ExperimentStatus{ExpID: m.ExpID, State: schemas.ExperimentRunning,
			SpentUSD: schemas.Unknown("not_indexed", schemas.EvidenceGateway)}
		resp.Status = &st
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, resp)
}

// errBad is a request error the user can fix (400); errMissing is a missing
// suite or agent (404).
var (
	errQuickBad     = errors.New("bad quickrun request")
	errQuickMissing = errors.New("not found")
)

func statusOf(err error) int {
	switch {
	case errors.Is(err, errQuickMissing), errors.Is(err, projectfs.ErrNotFound):
		return fasthttp.StatusNotFound
	case errors.Is(err, projectfs.ErrConflict):
		return fasthttp.StatusConflict
	case errors.Is(err, projectfs.ErrTooLarge):
		return fasthttp.StatusRequestEntityTooLarge
	case errors.Is(err, errQuickBad), errors.Is(err, projectfs.ErrOutside), errors.Is(err, projectfs.ErrInvalid):
		return fasthttp.StatusBadRequest
	}
	return fasthttp.StatusInternalServerError
}

func badf(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errQuickBad}, a...)...)
}

// checkModel validates one model reference.
func checkModel(field string, m *QuickRunModel) error {
	if m == nil || strings.TrimSpace(m.Model) == "" {
		return badf("%s is required: pick a provider and a model (add a key on the Models page first)", field)
	}
	m.Model, m.Provider = strings.TrimSpace(m.Model), strings.TrimSpace(m.Provider)
	if !quickModelRe.MatchString(m.Model) {
		return badf("%s.model %q is not a valid model name", field, m.Model)
	}
	if m.Provider != "" && !quickProviders[m.Provider] && !bifrostProviderOK(m.Provider) {
		return badf("%s.provider %q is not supported", field, m.Provider)
	}
	return nil
}

// bifrostProviderOK reports whether p is a built-in Bifrost provider (lane
// A09). A custom provider name is not taken here: an unknown name is more
// likely a typo than a custom endpoint, which agents name as
// "<custom>/<model>" once the org has its key.
func bifrostProviderOK(p string) bool {
	_, known := schemas.LookupBifrostProvider(p)
	return known
}

// armFor is the arm of one model: the model override and, for a named
// provider, the gateway provider.
func armFor(m *QuickRunModel) schemas.ArmSpec {
	if m.Provider == "" { // A09: "<provider>/<model>" names its Bifrost provider
		if p, model := schemas.SplitProviderModel(m.Model); p != "" {
			m = &QuickRunModel{Provider: p, Model: model, KeyID: m.KeyID}
		}
	}
	set := map[string]any{"model": m.Model}
	label := m.Model
	if m.Provider != "" && m.Provider != "openai-compatible" {
		set["gateway.provider"] = m.Provider
		label += " via " + m.Provider
	}
	return schemas.ArmSpec{Label: label, Set: set}
}

// normalizeAgent turns "codex" or "agents/codex.yaml" into the project path
// agents/<id>.yaml.
func normalizeAgent(a string) (string, error) {
	a = strings.TrimSpace(a)
	if a == "" {
		a = QuickRunDefaultAgent
	}
	a = strings.TrimPrefix(a, "agents/")
	if !strings.HasSuffix(a, ".yaml") && !strings.HasSuffix(a, ".yml") {
		a += ".yaml"
	}
	if strings.Contains(a, "..") || strings.HasPrefix(a, "/") || !quickNameRe.MatchString(path.Base(a)) {
		return "", badf("agent %q is not a valid agent config name", a)
	}
	return "agents/" + path.Clean(a), nil
}

// build validates the request, resolves the suite and its tasks and returns
// the experiment file and its project path.
func (h *quickRunHandler) build(ctx context.Context, req *QuickRunRequest) ([]byte, string, error) {
	s := h.env.Services
	if err := checkModel("model", req.Model); err != nil {
		return nil, "", err
	}
	if req.CompareModel != nil {
		if err := checkModel("compare_model", req.CompareModel); err != nil {
			return nil, "", err
		}
	}
	if req.Tasks == 0 {
		req.Tasks = QuickRunDefaultTasks
	}
	if req.Trials == 0 {
		req.Trials = QuickRunDefaultTrials
	}
	if req.BudgetUSD == 0 {
		req.BudgetUSD = QuickRunDefaultBudget
	}
	if req.Tasks < 1 || req.Tasks > QuickRunMaxTasks {
		return nil, "", badf("tasks must be between 1 and %d", QuickRunMaxTasks)
	}
	if req.Trials < 1 || req.Trials > QuickRunMaxTrials {
		return nil, "", badf("trials must be between 1 and %d", QuickRunMaxTrials)
	}
	if req.BudgetUSD < 0 || req.BudgetUSD > QuickRunMaxBudget {
		return nil, "", badf("budget_usd must be between 0 and %v", QuickRunMaxBudget)
	}

	agent, err := normalizeAgent(req.Agent)
	if err != nil {
		return nil, "", err
	}
	if _, _, err := s.Projects.Read(ctx, projectfs.Area("agents"), strings.TrimPrefix(agent, "agents/")); err != nil {
		if errors.Is(err, projectfs.ErrNotFound) {
			return nil, "", fmt.Errorf("%w: agent config %s does not exist: create one on the Agents page or pick another", errQuickMissing, agent)
		}
		return nil, "", fmt.Errorf("read agent config %s: %w", agent, err)
	}

	suite := strings.TrimSpace(req.Suite)
	if suite == "" {
		suite = QuickRunDefaultSuite
	}
	if !quickSuiteRe.MatchString(suite) {
		return nil, "", badf("suite %q is not a valid suite id", suite)
	}
	suiteID, _, _ := strings.Cut(suite, "@")
	tasks := schemas.TaskSelector{All: true}
	if s.Suites != nil {
		st, err := s.Suites.LoadSuite(ctx, suiteID)
		if err != nil {
			if errors.Is(err, schemas.ErrNotFound) {
				return nil, "", fmt.Errorf("%w: suite %s does not exist: add one from the Catalog page", errQuickMissing, suiteID)
			}
			return nil, "", fmt.Errorf("load suite %s: %w", suiteID, err)
		}
		if !strings.Contains(suite, "@") && st.Version != "" {
			suite = suiteID + "@" + st.Version
		}
		// Only admitted tasks: the planner refuses a draft task in the lock.
		ids := append([]string(nil), st.TaskIDs...)
		if st.Lock != nil {
			ids = ids[:0]
			for _, t := range st.Tasks {
				if summarizeTask(st, t, nil).Admitted {
					ids = append(ids, t.ID)
				}
			}
		}
		if len(ids) == 0 && len(st.TaskIDs) > 0 {
			return nil, "", badf("suite %s has no admitted task yet: open the suite, Ops tab, and press Admit all tasks", suiteID)
		}
		if len(ids) == 0 {
			return nil, "", badf("suite %s has no tasks: add tasks on its page first", suiteID)
		}
		sort.Strings(ids)
		if req.Tasks < len(ids) {
			tasks = schemas.TaskSelector{IDs: ids[:req.Tasks]}
		}
	}

	now := h.now().UTC()
	name := "quick-" + now.Format("20060102-150405") + "-" + randHex(2)
	armB := armFor(req.Model)
	if req.CompareModel != nil {
		armB = armFor(req.CompareModel)
	} else {
		// One model: B repeats A as a noise-floor run. The planner refuses arms
		// with the same config hash, so B carries a marker env var the agent
		// ignores; everything that matters (model, provider, prompt) is equal.
		armB.Label += " (repeat)"
		armB.Set["env.HEIMDALL_QUICKRUN_ARM"] = "B"
	}
	f := quickFile{
		Name: name, Suite: suite, Base: agent,
		Arms:         map[string]schemas.ArmSpec{"A": armFor(req.Model), "B": armB},
		Primary:      schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:       []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions:   []quickCondition{{ID: schemas.ConditionClean, Tasks: tasks, Trials: req.Trials}},
		Budget:       schemas.BudgetSpec{TotalUSD: req.BudgetUSD, ReserveUSD: quickReserve(req.BudgetUSD, 2*req.Tasks*req.Trials)},
		Verdict:      quickVerdict{Policy: "paired_signflip"},
		Seed:         now.Unix(),
		Concurrency:  2,
		ManifestMode: "agent_exposure",
	}
	if _, _, err := s.Projects.Read(ctx, projectfs.Area("policies"), strings.TrimPrefix(quickRunPolicy, "policies/")); err == nil {
		f.Policy = quickRunPolicy
	}
	body, err := marshalQuickFile(f)
	if err != nil {
		return nil, "", fmt.Errorf("encode experiment: %w", err)
	}
	head := fmt.Sprintf("# QuickRun %s: %s on %s, %d task(s) x %d trial(s).\n# Written by the Run button; edit it or run it again from the Experiments page.\n",
		now.Format(time.RFC3339), agent, suite, req.Tasks, req.Trials)
	return append([]byte(head), body...), "experiments/" + name + ".yaml", nil
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}

// marshalQuickFile encodes f and drops empty values (blank strings, nulls,
// empty maps and lists): the schema's yaml tags have no omitempty and the
// strict validator refuses an empty arm agent or guard metric.
func marshalQuickFile(f quickFile) ([]byte, error) {
	var doc yaml.Node
	if err := doc.Encode(f); err != nil {
		return nil, err
	}
	pruneEmptyYAML(&doc)
	return yaml.Marshal(&doc)
}

func pruneEmptyYAML(n *yaml.Node) {
	switch n.Kind {
	case yaml.MappingNode:
		kept := n.Content[:0]
		for i := 0; i+1 < len(n.Content); i += 2 {
			v := n.Content[i+1]
			pruneEmptyYAML(v)
			if emptyYAML(v) {
				continue
			}
			kept = append(kept, n.Content[i], v)
		}
		n.Content = kept
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			pruneEmptyYAML(c)
		}
	}
}

func emptyYAML(n *yaml.Node) bool {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Tag == "!!null" || (n.Tag == "!!str" && n.Value == "")
	case yaml.MappingNode, yaml.SequenceNode:
		return len(n.Content) == 0
	}
	return false
}

// quickOrgID is the org whose keys a QuickRun uses.
func quickOrgID(ctx *fasthttp.RequestCtx, orgID string) string {
	if orgID != "" {
		return orgID
	}
	if p := auth.PrincipalFrom(ctx); p != nil && p.OrgID != "" {
		return p.OrgID
	}
	return auth.LocalOrgID
}

// quickReserve is the per-run charge of a run whose cost the price table can
// not compute (an unpriced model): the budget split over the planned runs,
// so one unpriced run no longer uses the whole budget and stops the rest.
func quickReserve(budget float64, runs int) float64 {
	if budget <= 0 || runs <= 0 {
		return 0
	}
	r := budget / float64(runs)
	return math.Round(r*100) / 100
}
