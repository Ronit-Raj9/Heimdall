package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/migration"
	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/schemas"
)

const migrateUsage = `usage: bifrost-bench migrate [-config deploy/bench.yaml] -agent agents/<config>.yaml -from <model> -to <model>[,<model>...] -suite <id@version> [-trials 3] [-conditions clean,...] [-tasks t01,t02] [-provider <p>] [-budget-usd n] [-policy policies/default.yaml] [-out <dir>] [-allow-unpriced] [-yes]
       bifrost-bench migrate report [-config deploy/bench.yaml] -exp <id> [-from <model|arm>] [-to <model|arm>[,...]] [-condition <id>] [-alpha 0.05] [-grade-source original] [-policy <file>] [-suite-tasks=false] [-out <dir>] [-json]
migrate plans a model matrix experiment (the agent on the old model and each new one), prints the plan and the cost and time forecast, and with -yes runs it and writes the migration report (docs/model-migration.md). Without -yes nothing runs.
migrate report writes the report for an existing matrix experiment from its sealed runs: verdict per model pair overall and per stratum, cost per verified success and why it moved, behaviour differences, flipped tasks and a recommendation that follows the release policy. It reads the run folder only; with -out it writes report.md and report.json there.`

func init() {
	register("migrate", "plan and run a model migration matrix (-yes), or write the migration report of one (migrate report)", runMigrate)
}

func runMigrate(args []string) int {
	if len(args) > 0 && args[0] == "report" {
		return runMigrateReport(args[1:])
	}
	return runMigratePlan(args)
}

func runMigratePlan(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", defaultConfig, "bench config")
	agent := fs.String("agent", "", "agent config path, relative to the bench root")
	from := fs.String("from", "", "the model the agent uses now")
	to := fs.String("to", "", "the model or models to move to, comma-separated")
	suiteRef := fs.String("suite", "", "suite id@version")
	trials := fs.Int("trials", 3, "trials per task and condition")
	conditions := fs.String("conditions", schemas.ConditionClean, "conditions, comma-separated (clean or probe ids)")
	tasks := fs.String("tasks", "", "task ids, comma-separated (default: all public tasks)")
	provider := fs.String("provider", "", "gateway provider for the new models when it differs from the agent config's")
	budget := fs.Float64("budget-usd", 0, "budget.total_usd: refuse to run when the forecast says it is exceeded")
	policyFile := fs.String("policy", "policies/default.yaml", "release policy the recommendation follows (empty for none)")
	out := fs.String("out", "", "folder for the experiment file and the report (default migrations/<from>-to-<to>)")
	allowUnpriced := fs.Bool("allow-unpriced", false, "plan models the price snapshot does not list")
	yes := fs.Bool("yes", false, "run the experiment after the forecast; without it nothing runs")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *agent == "" || *from == "" || *to == "" || *suiteRef == "" || *trials < 1 {
		fmt.Fprintln(os.Stderr, migrateUsage)
		return exitUsage
	}
	toModels := splitList(*to)
	dir := *out
	if dir == "" {
		dir = filepath.Join("migrations", slug(*from+"-to-"+strings.Join(toModels, "-")))
	}
	text := migrationExperiment(*agent, *suiteRef, *from, toModels, *provider, splitList(*conditions), splitList(*tasks), *trials, *budget, *policyFile, *allowUnpriced)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail("migrate: %v", err)
	}
	expPath := filepath.Join(dir, "experiment.yaml")
	if err := os.WriteFile(expPath, []byte(text), 0o644); err != nil {
		return fail("migrate: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{gateway: true})
	if err != nil {
		return fail("%v", err)
	}
	exp, err := loadExperiment(expPath)
	if err != nil {
		return fail("%v", err)
	}
	if err := a.validatePolicy(exp); err != nil {
		return fail("%v", err)
	}
	m, err := a.newPlanner().Plan(ctx, exp)
	if err != nil {
		return fail("plan: %v", err)
	}
	printPlan(m)
	f, err := a.forecast(ctx, m)
	if err != nil {
		return fail("forecast: %v", err)
	}
	printForecast(os.Stdout, f)
	fmt.Printf("experiment file: %s\n", expPath)
	if *budget > 0 && (f.Fit == schemas.FitExceeds || f.Fit == schemas.FitLikelyExceeds) {
		return fail("migrate: the forecast says the budget of $%.2f is exceeded (%s: %s); raise -budget-usd, cut -tasks or -trials, or run fewer models", *budget, f.Fit, f.FitReason)
	}
	if !*yes {
		fmt.Printf("not run: pass -yes to run these %d runs, or run `%s run %s` yourself\n", countedSlots(m), progName, expPath)
		return exitOK
	}
	if *budget > 0 && f.Fit != schemas.FitFits {
		a.logger.Warn("running with a budget the forecast cannot confirm", "fit", f.Fit, "reason", f.FitReason)
	}
	if err := a.store.CreateExperiment(ctx, m); err != nil {
		return fail("freeze %s: %v", m.ExpID, err)
	}
	if code := a.execute(ctx, m, 0); code != exitOK {
		return code
	}
	if _, _, err := a.analyze(context.WithoutCancel(ctx), m.ExpID, ""); err != nil {
		a.logger.Warn("analysis failed; the migration report is still written", "experiment", m.ExpID, "error", err)
	}
	// The report names the frozen arms, not the model strings: one model
	// through two providers would resolve to both arms on both sides
	// (audit 5-C-07). migrationExperiment lists the old model first, and
	// the matrix gives its arms the ids A, B, C... in that order.
	q := migration.Query{From: "A"}
	for i := range toModels {
		q.To = append(q.To, string(rune('B'+i)))
	}
	reportPolicy := *policyFile
	if reportPolicy == "" {
		reportPolicy = "none"
	}
	rep, err := a.migrationReport(context.WithoutCancel(ctx), m.ExpID, schemas.GradeSourceOriginal, reportPolicy, true, q)
	if err != nil {
		return fail("migration report %s: %v", m.ExpID, err)
	}
	if err := writeMigrationReport(dir, rep); err != nil {
		return fail("migration report %s: %v", m.ExpID, err)
	}
	fmt.Print(migration.Markdown(rep))
	fmt.Printf("\nreport: %s, %s\n", filepath.Join(dir, "report.md"), filepath.Join(dir, "report.json"))
	return exitOK
}

func runMigrateReport(args []string) int {
	fs := flag.NewFlagSet("migrate report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", defaultConfig, "bench config")
	expID := fs.String("exp", "", "experiment id")
	from := fs.String("from", "", "old model or arm (default: arm A)")
	to := fs.String("to", "", "new models or arms, comma-separated (default: every other arm of the same agent)")
	condition := fs.String("condition", "", "condition of the overall verdict (default: the experiment's primary)")
	alpha := fs.Float64("alpha", leaderboard.DefaultAlpha, "significance level")
	gradeSource := fs.String("grade-source", schemas.GradeSourceOriginal, "original, or a regrade id of the experiment")
	policyFile := fs.String("policy", "", "release policy file (default: the experiment's own, else policies/default.yaml; \"none\" for none)")
	suiteTasks := fs.Bool("suite-tasks", true, "read the suite's tasks for the kind, capability, tag and declared difficulty strata")
	out := fs.String("out", "", "write report.md and report.json into this folder")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *expID == "" {
		fmt.Fprintln(os.Stderr, migrateUsage)
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	q := migration.Query{From: *from, To: splitList(*to), Condition: *condition, Alpha: *alpha}
	rep, err := a.migrationReport(ctx, *expID, *gradeSource, *policyFile, *suiteTasks, q)
	if errors.Is(err, migration.ErrBadQuery) {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, migrateUsage)
		return exitUsage
	}
	if err != nil {
		return fail("migrate report %s: %v", *expID, err)
	}
	if *out != "" {
		if err := writeMigrationReport(*out, rep); err != nil {
			return fail("migrate report %s: %v", *expID, err)
		}
	}
	if *asJSON {
		return printJSON(rep, "migrate report "+*expID)
	}
	fmt.Print(migration.Markdown(rep))
	return exitOK
}

// migrationReport builds the report of one experiment from its sealed runs,
// the suite's tasks and the release policy. policyFile "" takes the
// experiment's own policy, else policies/default.yaml when it exists;
// "none" evaluates none. A From left empty is arm A.
func (a *app) migrationReport(ctx context.Context, expID, gradeSource, policyFile string, withTasks bool, q migration.Query) (*migration.Report, error) {
	m, err := a.store.LoadManifest(ctx, expID)
	if err != nil {
		return nil, err
	}
	runs, err := a.store.SealedRuns(ctx, expID, gradeSource)
	if err != nil {
		return nil, err
	}
	synthetic, err := leaderboard.SyntheticArms(ctx, m, runs, sealedRunFile)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(q.From) == "" {
		q.From = firstArm(m)
	}
	in := migration.Input{Manifest: m, Runs: runs, Read: costexplain.RunFileReader(sealedRunFile), Prices: a.prices, Synthetic: synthetic}
	if withTasks {
		in.Tasks = a.suiteTasks(ctx, m)
	}
	if in.Policy, in.PolicyHash, in.PolicyFrozen, err = a.migrationPolicy(m, policyFile); err != nil {
		return nil, err
	}
	if in.Invalid, err = a.releaseInvalid(ctx, m, runs); err != nil {
		return nil, err
	}
	return migration.Build(ctx, in, q)
}

// releaseInvalid is the release verdict's invalid reasons for m, computed
// the way analyze does (policy decision, then the seal chain) without
// saving an analysis. Audit 5-C-01: a migration recommendation never
// outranks an INVALID release verdict.
func (a *app) releaseInvalid(ctx context.Context, m *schemas.Manifest, runs []schemas.SealedRun) ([]string, error) {
	name := m.Experiment.Verdict.Policy
	if name == "" {
		name = "paired_signflip"
	}
	var vp schemas.VerdictPolicy
	for _, p := range a.reg.Policies {
		if p.GetName() == name {
			vp = p
		}
	}
	if vp == nil {
		return nil, fmt.Errorf("verdict policy %q is not registered", name)
	}
	an, err := vp.Decide(ctx, m, runs)
	if err != nil {
		return nil, fmt.Errorf("decide: %w", err)
	}
	reasons := an.Verdict.InvalidReasons
	if err := a.store.VerifyChain(m.ExpID); err != nil {
		if !errors.Is(err, schemas.ErrSealMismatch) {
			return nil, fmt.Errorf("verify seal chain: %w", err)
		}
		reasons = append(reasons, "evidence: seals.log chain does not verify: "+err.Error())
	}
	return reasons, nil
}

// firstArm is the experiment's first arm id in order.
func firstArm(m *schemas.Manifest) string {
	first := ""
	for id := range m.Arms {
		if first == "" || id < first {
			first = id
		}
	}
	return first
}

// suiteTasks loads the experiment's suite for its task metadata; a suite
// that no longer loads leaves the metadata strata out, with a warning.
func (a *app) suiteTasks(ctx context.Context, m *schemas.Manifest) map[string]*schemas.Task {
	st, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), m.SuiteID+"@"+m.SuiteVersion)
	if err != nil {
		a.logger.Warn("suite not loaded; task kind, capability, tag and declared difficulty strata left out", "suite", m.SuiteID, "error", err)
		return nil
	}
	out := make(map[string]*schemas.Task, len(st.Tasks))
	for _, t := range st.Tasks {
		out[t.ID] = t
	}
	return out
}

// migrationPolicy loads the release policy the recommendation follows and
// says whether it is the one the experiment froze. Audit 5-C-02: a policy
// that was asked for (an explicit file, or the one the experiment names)
// and cannot be read is an error, never a silent statistics-only report;
// only the implicit policies/default.yaml may be absent, and "none" is the
// explicit statistics-only choice.
func (a *app) migrationPolicy(m *schemas.Manifest, file string) (*schemas.Policy, string, bool, error) {
	implicit := false
	switch {
	case file == "none":
		return nil, "", false, nil
	case file == "" && m.Experiment.Policy != "":
		file = m.Experiment.Policy
	case file == "":
		file, implicit = "policies/default.yaml", true
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.cfg.Root, file)
	}
	p, raw, err := policy.Load(path)
	switch {
	case err != nil && implicit && errors.Is(err, os.ErrNotExist):
		a.logger.Warn("no release policy; the recommendation follows the statistics alone", "policy", file)
		return nil, "", false, nil
	case err != nil:
		return nil, "", false, fmt.Errorf("release policy %s: %w (pass -policy none for a statistics-only report)", file, err)
	}
	frozen := m.Experiment.Policy != "" && filepath.Clean(file) == filepath.Clean(m.Experiment.Policy)
	return p, policy.Hash(raw), frozen, nil
}

// writeMigrationReport writes report.md and report.json into dir.
func writeMigrationReport(dir string, rep *migration.Report) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(migration.Markdown(rep)), 0o644); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.json"), append(b, '\n'), 0o644)
}

// migrationExperiment is the experiment file of a migration: a model matrix
// of the agent on the old model first, then each new model, every condition
// on the same tasks and trials, the paired verdict on clean (else on the
// first condition).
func migrationExperiment(agent, suiteRef, from string, to []string, provider string, conditions, tasks []string, trials int, budget float64, policyFile string, allowUnpriced bool) string {
	var b strings.Builder
	b.WriteString("# Written by `migrate` (docs/model-migration.md). Edit and run with `run` if you like.\n")
	fmt.Fprintf(&b, "name: %s\n", quote("migrate "+from+" to "+strings.Join(to, ", ")))
	fmt.Fprintf(&b, "suite: %s\nbase: %s\nmatrix:\n  models:\n    - %s\n", quote(suiteRef), quote(agent), quote(from))
	for _, m := range to {
		if provider != "" {
			fmt.Fprintf(&b, "    - {model: %s, provider: %s}\n", quote(m), quote(provider))
		} else {
			fmt.Fprintf(&b, "    - %s\n", quote(m))
		}
	}
	if allowUnpriced {
		b.WriteString("  allow_unpriced: true\n")
	}
	if len(conditions) == 0 {
		conditions = []string{schemas.ConditionClean}
	}
	// The verdict is on clean when it is run, else on the first condition
	// asked for (audit 5-C-06: a fault-only plan must still be valid).
	primary := conditions[0]
	if slices.Contains(conditions, schemas.ConditionClean) {
		primary = schemas.ConditionClean
	}
	fmt.Fprintf(&b, "primary: {condition: %s, metric: verified_success}\nguards:\n  - {rule: no_new_integrity_violation}\nconditions:\n", quote(primary))
	sel := "all"
	if len(tasks) > 0 {
		q := make([]string, len(tasks))
		for i, t := range tasks {
			q[i] = quote(t)
		}
		sel = "[" + strings.Join(q, ", ") + "]"
	}
	for _, c := range conditions {
		fmt.Fprintf(&b, "  - {id: %s, tasks: %s, trials: %d}\n", quote(c), sel, trials)
	}
	fmt.Fprintf(&b, "budget: {total_usd: %s}\n", strconv.FormatFloat(budget, 'f', -1, 64))
	b.WriteString("verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}\nseed: 20261118\nconcurrency: 2\nmanifest_mode: agent_exposure\n")
	if policyFile != "" && policyFile != "none" {
		fmt.Fprintf(&b, "policy: %s\n", quote(policyFile))
	}
	return b.String()
}

// countedSlots is the counted runs a manifest plans.
func countedSlots(m *schemas.Manifest) int {
	n := 0
	for _, s := range m.Slots {
		if s.Counted {
			n++
		}
	}
	return n
}

var slugRE = regexp.MustCompile(`[^a-z0-9.]+`)

func slug(s string) string {
	return strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// quote writes a YAML double-quoted string.
func quote(s string) string { return strconv.Quote(s) }

// serveW6 is the server's handlers.MigrationSource and
// handlers.CostExplainSource: the same code as `migrate report`, `cost
// explain` and `cost pareto`.
type serveW6 struct {
	a *app
}

var (
	_ handlers.MigrationSource   = serveW6{}
	_ handlers.CostExplainSource = serveW6{}
)

// Migration implements handlers.MigrationSource with the experiment's own
// release policy (else policies/default.yaml) and the suite's tasks.
func (s serveW6) Migration(ctx context.Context, expID, gradeSource string, q migration.Query) (*migration.Report, error) {
	return s.a.migrationReport(ctx, expID, gradeSource, "", true, q)
}

// CostExplain implements handlers.CostExplainSource.
func (s serveW6) CostExplain(ctx context.Context, expID, gradeSource string, q costexplain.Query) (*costexplain.Explanation, error) {
	return s.a.costExplain(ctx, expID, gradeSource, q)
}

// Pareto implements handlers.CostExplainSource.
func (s serveW6) Pareto(ctx context.Context, expID, condition, x string) (*costexplain.Pareto, error) {
	return s.a.matrixPareto(ctx, expID, condition, x)
}
