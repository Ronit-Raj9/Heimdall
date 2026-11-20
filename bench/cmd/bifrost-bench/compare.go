package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/ci"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/quickstart"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
)

// First workflow: `compare` turns two refs, two agent configs or two
// models into one experiment file, shows its forecast, asks before spending,
// then plans, runs and checks it and prints the verdict card. It exits with
// check's codes.

func init() {
	register("compare", "one command to a verdict: compare --baseline <ref> --candidate <ref> | --a agents/x.yaml --b agents/y.yaml | --a model=m1 --b model=m2", runCompare)
}

const compareUsage = `usage: bifrost-bench compare --baseline <ref> --candidate <ref> [--base agents/<agent>.yaml] [--paths AGENTS.md,CLAUDE.md,...]
       bifrost-bench compare --a agents/x.yaml --b agents/y.yaml
       bifrost-bench compare --a model=<m1> --b model=<m2> [--base agents/<agent>.yaml]   (any key=value override, comma-separated)
   common: [--suite <suite>] [--trials 3] [--fault model-outage]... [--tier <tier>] [--budget-usd <usd, required unless every arm is scripted>] [--yes]
           [--name <experiment name>] [--concurrency 2] [--policy policies/default.yaml|none] [--fail-on regressed,invalid] [--write-only] [--config ...]
exit: 0 ok, 1 regressed, 2 inconclusive, 3 invalid (each only when listed in --fail-on), 4 infrastructure error, 5 blocked by the release policy, 64 usage`

// listFlag is a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	*l = append(*l, splitList(v)...)
	return nil
}

// compareFlags are the parsed flags of compare.
type compareFlags struct {
	config, baseline, candidate, a, b, base, suite, tier, name, policy, failOn, paths, repo string
	trials, concurrency                                                                     int
	budget                                                                                  float64
	faults                                                                                  listFlag
	yes, writeOnly                                                                          bool
}

func runCompare(args []string) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	var f compareFlags
	fs.StringVar(&f.config, "config", defaultConfig, "bench config (default: deploy/bench.yaml here, else the repository's .heimdall project)")
	fs.StringVar(&f.baseline, "baseline", "", "git ref of the baseline agent files (arm A)")
	fs.StringVar(&f.candidate, "candidate", "", "git ref of the candidate agent files (arm B)")
	fs.StringVar(&f.a, "a", "", "arm A: an agent config file, or key=value overrides of --base")
	fs.StringVar(&f.b, "b", "", "arm B: an agent config file, or key=value overrides of --base")
	fs.StringVar(&f.base, "base", "", "agent config both arms start from (default: the first agent heimdall init found)")
	fs.StringVar(&f.suite, "suite", "", "suite (default: the suite heimdall init wrote)")
	fs.StringVar(&f.tier, "tier", "", "suite tier to run (needs a build with suite tiers)")
	fs.StringVar(&f.name, "name", "", "experiment name and file name (default: compare-<time>)")
	fs.StringVar(&f.policy, "policy", "", "release policy file, or none (default: policies/default.yaml)")
	fs.StringVar(&f.failOn, "fail-on", "regressed,invalid", "comma-separated verdicts that exit non-zero")
	fs.StringVar(&f.paths, "paths", strings.Join(scaffold.DefaultAgentPaths, ","), "--baseline/--candidate: the agent's files and folders to read at each ref")
	fs.StringVar(&f.repo, "repo", ".", "--baseline/--candidate: the git repository")
	fs.IntVar(&f.trials, "trials", quickstart.DefaultTrials, "trials per task, arm and condition")
	fs.IntVar(&f.concurrency, "concurrency", 2, "runs at once")
	fs.Float64Var(&f.budget, "budget-usd", 0, "total spend cap in USD; the scheduler stops leasing runs past it. Required (> 0) unless every arm is a scripted agent: 0 would leave the run uncapped")
	fs.Var(&f.faults, "fault", "a fault condition (probe id) to add, e.g. model-outage; repeatable")
	fs.BoolVar(&f.yes, "yes", false, "run without asking, even when the forecast says it costs money")
	fs.BoolVar(&f.writeOnly, "write-only", false, "write the experiment file (and its fixtures) and stop; nothing is planned or run")
	pos, err := parseArgs(fs, args)
	refs := f.baseline != "" || f.candidate != ""
	arms := f.a != "" || f.b != ""
	if err != nil || len(pos) != 0 || refs == arms || (refs && (f.baseline == "" || f.candidate == "")) || (arms && (f.a == "" || f.b == "")) {
		fmt.Fprintln(os.Stderr, compareUsage)
		return exitUsage
	}
	if _, err := parseDecisions(f.failOn); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitUsage
	}
	f.config = projectConfig(f.config, flagSet(fs, "config"))
	if !f.writeOnly {
		loadProjectEnv(f.config) // the stack's tokens; --write-only needs no stack
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return compare(ctx, f, os.Stdin, os.Stdout)
}

// compareSetup is the experiment file compare writes and what it knows
// about the arms.
type compareSetup struct {
	path    string // experiment file, absolute
	rel     string // relative to the project root
	opts    quickstart.CompareOptions
	subject string
}

// buildCompare writes the experiment file for the flags. It needs only the
// config (no gateway), so tests call it directly.
func buildCompare(ctx context.Context, f compareFlags, cfg *lib.Config) (_ *compareSetup, retErr error) {
	pf, _ := quickstart.LoadProjectFile(cfg.Root)
	suiteID := f.suite
	if suiteID == "" && pf != nil {
		suiteID = pf.Suite
	}
	if suiteID == "" {
		return nil, fmt.Errorf("no suite: pass --suite (or run %s init in the repository first)", progName)
	}
	name := f.name
	if name == "" {
		name = "compare-" + time.Now().UTC().Format("20060102-150405")
	}
	name = quickstart.ExperimentName(name)
	o := quickstart.CompareOptions{
		Name: name, Suite: suiteID, Base: f.base, Trials: f.trials, Faults: f.faults, Tier: f.tier,
		BudgetUSD: f.budget, Concurrency: f.concurrency, Policy: f.policy,
		Command: progName + " compare " + strings.Join(os.Args[2:], " "),
	}
	if o.Base == "" && pf != nil && len(pf.Agents) > 0 && (f.baseline != "" || !strings.HasSuffix(f.a, ".yaml") || !strings.HasSuffix(f.b, ".yaml")) {
		o.Base = pf.Agents[0].Config
	}
	setup := &compareSetup{}
	setup.rel = path.Join("experiments", name+".yaml")
	setup.path = filepath.Join(cfg.Root, filepath.FromSlash(setup.rel))
	// Reserve the name before anything is written, so a refused comparison
	// can be retried under the same --name (audit 5-B-11).
	if _, err := os.Stat(setup.path); err == nil {
		return nil, fmt.Errorf("%s exists; pass another --name", setup.path)
	}
	if f.baseline != "" {
		if o.Base == "" {
			return nil, fmt.Errorf("--baseline/--candidate need --base agents/<agent>.yaml: the agent whose files differ between the refs")
		}
		fixtures := filepath.Join(cfg.Root, "experiments", "fixtures", name)
		if _, err := os.Stat(fixtures); err == nil {
			return nil, fmt.Errorf("%s exists; pass another --name", fixtures)
		}
		defer func() {
			if retErr != nil { // the fixture tree belongs to the experiment this call did not write
				if err := os.RemoveAll(fixtures); err != nil {
					fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", fixtures, err)
				}
			}
		}()
		a, b, subject, err := refArms(ctx, f, cfg.Root, name, o.Base)
		if err != nil {
			return nil, err
		}
		o.A, o.B, setup.subject = a, b, subject
	} else {
		var err error
		if o.A, err = quickstart.ParseArm(f.a); err != nil {
			return nil, fmt.Errorf("--a: %w", err)
		}
		if o.B, err = quickstart.ParseArm(f.b); err != nil {
			return nil, fmt.Errorf("--b: %w", err)
		}
		setup.subject = fmt.Sprintf("A = %s, B = %s", o.A.Describe(), o.B.Describe())
	}
	o.Smoke = allScripted(cfg.Root, o)
	if !o.Smoke && !(o.BudgetUSD > 0) {
		// The scheduler treats a zero budget as no cap, so a comparison that
		// can call a real model must state a positive one.
		return nil, fmt.Errorf("an arm can call a real model: pass --budget-usd with a positive spend cap (0 means uncapped, not free)")
	}
	raw, err := quickstart.ExperimentYAML(o)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(setup.path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(setup.path, raw, 0o644); err != nil {
		return nil, err
	}
	if _, err := loadExperiment(setup.path); err != nil {
		_ = os.Remove(setup.path)
		return nil, fmt.Errorf("the written experiment does not load (%s): %w", setup.path, err)
	}
	setup.opts = o
	return setup, nil
}

// errNoAgentChange is a refs comparison whose two refs have the same agent
// files: a verified "nothing to gate", not a failure. compare exits
// ci.ExitNoAgentChange for it, so the generated CI job can skip the gate
// for a pull request that changes only suite data or the workflow (audit
// 5-B-14).
var errNoAgentChange = errors.New("no agent change between the refs")

// refArms copies the agent's files at both refs under
// experiments/fixtures/<name>/ and turns them into arm overrides: the notes
// file as notes.user_file, skill folders as skills.extra, and a committed
// .heimdall agent config as the arm's agent.
func refArms(ctx context.Context, f compareFlags, root, name, base string) (quickstart.ArmInput, quickstart.ArmInput, string, error) {
	paths := splitList(f.paths)
	var arms [2]quickstart.ArmInput
	var exported [2]*scaffold.ExportedRef
	var dsts [2]string
	used := map[string]bool{} // repository paths some arm feeds to its runs
	for i, ref := range []string{f.baseline, f.candidate} {
		id := []string{"A", "B"}[i]
		rel := path.Join("experiments", "fixtures", name, id)
		dst := filepath.Join(root, filepath.FromSlash(rel))
		ex, err := scaffold.ExportAgentFiles(ctx, f.repo, ref, paths, dst)
		if err != nil {
			return arms[0], arms[1], "", fmt.Errorf("read agent files at %s: %w", ref, err)
		}
		exported[i], dsts[i] = ex, dst
		arm := quickstart.ArmInput{Label: fmt.Sprintf("%s (%s)", ref, ex.Commit[:12]), Set: map[string]any{}}
		if notes := pickNotes(ex.Files, root, base); notes != "" {
			arm.Set["notes.user_file"] = path.Join(rel, notes)
			used[notes] = true
		}
		if skills := skillFolders(ex.Files); len(skills) > 0 {
			var extra []any
			for _, s := range skills {
				extra = append(extra, path.Join(rel, s))
				for _, f := range ex.Files {
					if strings.HasPrefix(f, s+"/") {
						used[f] = true
					}
				}
			}
			arm.Set["skills.extra"] = extra
		}
		committed := path.Join(".heimdall", base)
		for _, p := range ex.Files {
			if p == committed {
				arm.Agent = path.Join(rel, committed)
				used[p] = true
			}
		}
		if len(arm.Set) == 0 {
			arm.Set = nil
		}
		arms[i] = arm
	}
	if exported[0].Hash == exported[1].Hash {
		return arms[0], arms[1], "", fmt.Errorf("%s and %s have the same agent files under %s; nothing to compare: %w", f.baseline, f.candidate, strings.Join(paths, ","), errNoAgentChange)
	}
	if len(exported[0].Files) == 0 && len(exported[1].Files) == 0 {
		return arms[0], arms[1], "", fmt.Errorf("no agent files under %s at either ref", strings.Join(paths, ","))
	}
	// Only the notes file, skill folders and the committed agent config reach
	// the runs. A changed file outside them (a prompt file, another agent's
	// config) would leave both arms running the same inputs while the report
	// credits the change, so it is refused (audit 5-B-01).
	if ignored := unusedChanges(exported, dsts, used); len(ignored) > 0 {
		return arms[0], arms[1], "", fmt.Errorf("%s and %s differ in %s, which neither arm feeds to its runs (only the notes file, skill folders and .heimdall/%s are used); narrow --paths or reference the file from the agent config", f.baseline, f.candidate, strings.Join(ignored, ", "), base)
	}
	subject := fmt.Sprintf("agent files at `%s` (%s, %d files) vs `%s` (%s, %d files)", f.baseline, exported[0].Commit[:12], len(exported[0].Files),
		f.candidate, exported[1].Commit[:12], len(exported[1].Files))
	return arms[0], arms[1], subject, nil
}

// unusedChanges lists the exported files that differ between the two refs
// (added, removed or changed) and that no arm uses, sorted.
func unusedChanges(ex [2]*scaffold.ExportedRef, dsts [2]string, used map[string]bool) []string {
	all := map[string]bool{}
	for _, e := range ex {
		for _, f := range e.Files {
			all[f] = true
		}
	}
	var out []string
	for f := range all {
		if used[f] {
			continue
		}
		a, errA := os.ReadFile(filepath.Join(dsts[0], filepath.FromSlash(f)))
		b, errB := os.ReadFile(filepath.Join(dsts[1], filepath.FromSlash(f)))
		if (errA == nil) != (errB == nil) || !bytes.Equal(a, b) {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// pickNotes chooses the notes file for the base agent: its own project file
// name (CLAUDE.md for Claude Code) when the ref has it, else AGENTS.md, then
// CLAUDE.md, then GEMINI.md.
func pickNotes(files []string, root, base string) string {
	have := map[string]bool{}
	for _, f := range files {
		have[f] = true
	}
	order := []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}
	if raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(base))); err == nil {
		var cfg struct {
			Notes struct {
				ProjectFile string `yaml:"project_file"`
			} `yaml:"notes"`
		}
		if yaml.Unmarshal(raw, &cfg) == nil && cfg.Notes.ProjectFile != "" {
			order = append([]string{cfg.Notes.ProjectFile}, order...)
		}
	}
	for _, n := range order {
		if have[n] {
			return n
		}
	}
	return ""
}

// skillFolders lists each skill folder (a folder holding SKILL.md) among the
// exported files.
func skillFolders(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		if path.Base(f) == "SKILL.md" {
			if d := path.Dir(f); !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}

// allScripted reports whether every arm runs a scripted agent ($0 and never
// a finding).
func allScripted(root string, o quickstart.CompareOptions) bool {
	for _, arm := range []quickstart.ArmInput{o.A, o.B} {
		cfgPath := arm.Agent
		if cfgPath == "" {
			cfgPath = o.Base
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cfgPath)))
		if err != nil {
			return false
		}
		var c struct {
			Adapter string `yaml:"adapter"`
		}
		if yaml.Unmarshal(raw, &c) != nil || c.Adapter != "scripted" {
			return false
		}
		if v, ok := arm.Set["adapter"]; ok && v != "scripted" {
			return false
		}
	}
	return true
}

func compare(ctx context.Context, f compareFlags, in io.Reader, out io.Writer) int {
	start := time.Now()
	cfg, err := loadConfig(f.config)
	if err != nil {
		return fail("%v", err)
	}
	setup, err := buildCompare(ctx, f, cfg)
	if errors.Is(err, errNoAgentChange) {
		fmt.Fprintf(os.Stderr, "compare: %v\n", err)
		return ci.ExitNoAgentChange
	}
	if err != nil {
		return fail("compare: %v", err)
	}
	fmt.Fprintf(out, "wrote %s\n  %s\n", setup.path, setup.subject)
	if f.writeOnly {
		fmt.Fprintf(out, "--write-only: nothing planned or run; run it with\n  (cd %s && %s check %s)\n", cfg.Root, progName, setup.rel)
		return exitOK
	}
	a, err := newApp(ctx, f.config, appOptions{gateway: true})
	if err != nil {
		return fail("%v", err)
	}
	exp, err := loadExperiment(setup.path)
	if err != nil {
		return fail("%v", err)
	}
	if setup.opts.Smoke {
		a.cfg.Server.Mode = schemas.ModeSynthetic // scripted arms: the report is labelled synthetic, never a finding
	}
	if err := a.validatePolicy(exp); err != nil {
		return fail("%v", err)
	}
	m, err := a.newPlanner().Plan(ctx, exp)
	if err != nil {
		return fail("plan: %v", err)
	}
	printPlan(m)
	fc, ferr := a.forecast(ctx, m)
	if ferr == nil {
		printForecast(out, fc)
	} else {
		fmt.Fprintf(out, "forecast: none (%v)\n", ferr)
	}
	episodes := len(m.Slots)
	switch {
	case setup.opts.Smoke:
		fmt.Fprintf(out, "every arm is a scripted agent: $0, no consent needed; running %d episodes\n", episodes)
	case f.yes:
		fmt.Fprintf(out, "--yes: running %d episodes (budget cap $%.2f)\n", episodes, f.budget)
	default:
		if !interactive(in) {
			fmt.Fprintf(out, "not run: this spends real model money; rerun with --yes, or run it with\n  (cd %s && %s run %s)\n", a.cfg.Root, progName, setup.rel)
			return exitOK
		}
		fmt.Fprintf(out, "run %d agent episodes (budget cap $%.2f)? [y/N] ", episodes, f.budget)
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "not run")
			return exitOK
		}
	}
	if err := a.store.CreateExperiment(ctx, m); err != nil {
		return fail("freeze %s: %v", m.ExpID, err)
	}
	if code := a.execute(ctx, m, 0); code != exitOK {
		return code
	}
	an, report, err := a.analyzeWith(context.WithoutCancel(ctx), m.ExpID, "", "")
	if err != nil {
		return fail("analyze %s: %v", m.ExpID, err)
	}
	unrev, total := a.experimentUnreviewed(ctx, m)
	card := ci.VerdictCard(an, ci.CardInput{
		ArmLabels:  map[string]string{"A": setup.opts.A.Describe(), "B": setup.opts.B.Describe()},
		Unreviewed: unrev, Tasks: total, Subject: setup.subject,
		UILink:   "http://" + a.cfg.Server.Listen + "/verdict?exp=" + m.ExpID,
		Evidence: filepath.Join(a.store.Root(), m.ExpID),
	})
	dir := filepath.Join(a.cfg.Root, "reports", m.ExpID)
	if err := os.MkdirAll(dir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "report.md"), report, 0o644)
		_ = os.WriteFile(filepath.Join(dir, "card.md"), []byte(card), 0o644)
		if b, err := json.MarshalIndent(an, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(dir, "report.json"), b, 0o644)
		}
	}
	fmt.Fprintf(out, "\n%s\nreport: %s (took %s)\n", card, filepath.Join(dir, "report.md"), time.Since(start).Round(time.Second))
	fmt.Fprintf(out, "verdict: %s, action: %s\n", an.Verdict.Decision, an.Verdict.Action)
	printPolicy(an.Policy)
	failOn, _ := parseDecisions(f.failOn) // checked when the flags were parsed
	return checkExitCode(an, failOn)
}

// interactive reports whether in is a terminal a person can answer on.
func interactive(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// experimentUnreviewed counts the experiment's tasks whose instruction a
// tool drafted and nobody reviewed. A suite that cannot be read counts 0 of 0.
func (a *app) experimentUnreviewed(ctx context.Context, m *schemas.Manifest) (int, int) {
	s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), m.SuiteID)
	if err != nil {
		return 0, 0
	}
	seen := map[string]bool{}
	var ids []string
	for _, sl := range m.Slots {
		if !seen[sl.Task] {
			seen[sl.Task] = true
			ids = append(ids, sl.Task)
		}
	}
	return unreviewedCount(s, ids)
}

// unreviewedSection is the report section naming how many of the
// experiment's tasks have unreviewed, tool-drafted instructions; "" when none.
func (a *app) unreviewedSection(ctx context.Context, m *schemas.Manifest, report string) string {
	n, total := a.experimentUnreviewed(ctx, m)
	if n == 0 {
		return report
	}
	sec := fmt.Sprintf("## Task instructions\n\n%d of %d tasks have unreviewed instructions: `tasks mine` drafted them from commit messages and test names, and nobody has read them yet. Review each instruction.md, then run `%s task review <suite>/<task>`.\n",
		n, total, progName)
	return insertSection(report, sec)
}

// loadProjectEnv sets, from deploy/.env beside the config, every non-empty
// variable that is not already in the environment, so the first-workflow commands
// reach the stack without a manual export. Values are never printed.
func loadProjectEnv(config string) {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(config), ".env"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		if !ok || k == "" {
			continue
		}
		if _, set := os.LookupEnv(k); set {
			continue
		}
		if v = strings.Trim(strings.TrimSpace(v), `"'`); v != "" {
			_ = os.Setenv(k, v)
		}
	}
}
