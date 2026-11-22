package main

// A20 CLI parity for evaluations (agent-first contract, section 3 and 4).
//
//	bifrost-bench eval run --agent codex[@vN] [--vs agent@vM | --vs-model M] --model M
//	    [--mode model_switch --models M2,M3]
//	    --pack coding | --benchmarks a,b [--tasks 8] [--trials 2]
//	    [--faults model-down,broken-tool] [--estimate] [--json]
//	bifrost-bench eval list
//	bifrost-bench eval show <id>
//	bifrost-bench eval report <id> [--json]
//	bifrost-bench eval cancel <id>
//
// With a server configured (-server or HEIMDALL_SERVER, token from -token,
// HEIMDALL_TOKEN or BENCH_WRITE_TOKEN, the same rule push and pull use)
// every subcommand calls the bench HTTP API. Without one it works on the
// local project files: .heimdall/evaluations/<id>.json.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/packs"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

const evalUsage = `usage: bifrost-bench eval <run|list|show|report|cancel> ...

  eval run --agent codex[@vN] --model M (--pack coding | --benchmarks a,b)
           [--vs agent@vM | --vs-model M2] [--tasks 8] [--trials 2]
           [--mode model_switch --models M2,M3]
           [--faults model-down,broken-tool,misleading-instruction] [--name N]
           [--budget USD] [--estimate] [--json]
      start an evaluation (or only print its estimate with --estimate)
  eval list [--json]                 evaluations, newest first
  eval show <id> [--json]            status of one evaluation and its experiments
  eval report <id> [--json]          report card: score per capability and benchmark
  eval cancel <id>                   stop an evaluation

every subcommand takes -server URL -token TOKEN [-org ORG] (env HEIMDALL_SERVER,
HEIMDALL_TOKEN); without a server it uses the local project (-config deploy/bench.yaml)`

func init() {
	register("eval", "evaluate your agent over many benchmarks: eval run|list|show|report|cancel", runEval)
}

func runEval(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, evalUsage)
		return exitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "run", "list", "show", "report", "cancel":
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stdout, evalUsage)
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown eval subcommand %q\n\n%s\n", sub, evalUsage)
		return exitUsage
	}
	return runEvalSub(sub, rest, os.Stdout)
}

// evalOpts holds the flags of every eval subcommand.
type evalOpts struct {
	conn     connOpts
	asJSON   bool
	estimate bool

	agent, vs, vsModel, model, provider string
	mode, models                        string
	pack, benchmarks, faults, name      string
	tasks, trials                       int
	budget                              float64

	pos []string
}

// connOpts picks the HTTP API or the local project.
type connOpts struct {
	server, token, org, config string
	configSet                  bool
}

func addConnFlags(fs *flag.FlagSet, c *connOpts) {
	fs.StringVar(&c.server, "server", os.Getenv(syncServerEnv), "Heimdall server URL (env "+syncServerEnv+"); empty = local project")
	fs.StringVar(&c.token, "token", "", "API token (env "+syncTokenEnv+", then BENCH_WRITE_TOKEN)")
	fs.StringVar(&c.org, "org", "", "org slug or id, when the token's user belongs to several orgs")
	fs.StringVar(&c.config, "config", defaultConfig, "bench config for local mode; the project root is its grandparent")
}

func (c *connOpts) finish(fs *flag.FlagSet) {
	if c.token == "" {
		c.token = os.Getenv(syncTokenEnv)
	}
	if c.token == "" {
		c.token = os.Getenv("BENCH_WRITE_TOKEN")
	}
	c.configSet = flagSet(fs, "config")
}

// parseEvalFlags parses one subcommand's flags. It returns an error for
// usage mistakes; the caller prints usage and exits 64.
func parseEvalFlags(sub string, args []string) (*evalOpts, error) {
	fs := flag.NewFlagSet("eval "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := &evalOpts{}
	addConnFlags(fs, &o.conn)
	fs.BoolVar(&o.asJSON, "json", false, "print JSON")
	if sub == "run" {
		fs.StringVar(&o.agent, "agent", "", "agent id, optionally @vN (e.g. codex@v2)")
		fs.StringVar(&o.vs, "vs", "", "second arm: another agent or version, agent@vM")
		fs.StringVar(&o.vsModel, "vs-model", "", "second arm: the same agent with this model")
		fs.StringVar(&o.model, "model", "", "model for the agent (any model Bifrost routes)")
		fs.StringVar(&o.provider, "provider", "", "provider of the model (optional)")
		fs.StringVar(&o.mode, "mode", "", "evaluation mode; model_switch runs the agent on --model plus every --models entry for the model switch report")
		fs.StringVar(&o.models, "models", "", "model_switch: comma separated models besides --model (provider/model or a model name)")
		fs.StringVar(&o.pack, "pack", "", "benchmark pack: coding, terminal, support, memory, research, voice, safety, all-compatible")
		fs.StringVar(&o.benchmarks, "benchmarks", "", "comma separated benchmark ids")
		fs.IntVar(&o.tasks, "tasks", 8, "tasks per benchmark (at least 6 before an A/B gap can be significant)")
		fs.IntVar(&o.trials, "trials", 2, "repeats per task")
		fs.StringVar(&o.faults, "faults", "", "faults besides clean: model-down, broken-tool, misleading-instruction")
		fs.StringVar(&o.name, "name", "", "evaluation name")
		fs.Float64Var(&o.budget, "budget", 0, "spending limit in USD (0 = none)")
		fs.BoolVar(&o.estimate, "estimate", false, "print the estimate only; start nothing")
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return nil, err
	}
	o.conn.finish(fs)
	o.pos = pos
	switch sub {
	case "run", "list":
		if len(pos) != 0 {
			return nil, fmt.Errorf("unexpected argument %q", pos[0])
		}
	default:
		if len(pos) != 1 {
			return nil, fmt.Errorf("eval %s needs exactly one evaluation id", sub)
		}
	}
	return o, nil
}

// agentRef splits "codex@v2" into ("codex", 2). A bare id is version 0
// (the current config). "agents/codex.yaml@v2" is accepted too.
func agentRef(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	id, ver, hasVer := strings.Cut(s, "@")
	id = strings.TrimSuffix(strings.TrimPrefix(id, "agents/"), ".yaml")
	if id == "" {
		return "", 0, fmt.Errorf("agent %q: empty id", s)
	}
	if !hasVer {
		return id, 0, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(ver, "v"), "V"))
	if err != nil || n < 1 {
		return "", 0, fmt.Errorf("agent %q: version must look like @v2", s)
	}
	return id, n, nil
}

var evalFaults = map[string]bool{
	faults.Clean:                 true,
	faults.ModelDown:             true,
	faults.BrokenTool:            true,
	faults.MisleadingInstruction: true,
}

// buildEvalSpec turns run flags into the contract's EvaluationSpec.
func buildEvalSpec(o *evalOpts) (*schemas.EvaluationSpec, error) {
	if o.agent == "" {
		return nil, errors.New("--agent is required")
	}
	if o.model == "" {
		return nil, errors.New("--model is required")
	}
	if o.vs != "" && o.vsModel != "" {
		return nil, errors.New("use --vs or --vs-model, not both")
	}
	switch o.mode {
	case "", schemas.EvaluationModeModelSwitch:
	default:
		return nil, fmt.Errorf("--mode %q: only model_switch is set by flag; other modes follow from --vs and --vs-model", o.mode)
	}
	if o.models != "" && o.mode != schemas.EvaluationModeModelSwitch {
		return nil, errors.New("--models needs --mode model_switch")
	}
	if o.mode == schemas.EvaluationModeModelSwitch && (o.vs != "" || o.vsModel != "") {
		return nil, errors.New("--mode model_switch takes --models, not --vs or --vs-model")
	}
	benches := splitList(o.benchmarks)
	if o.pack == "" && len(benches) == 0 {
		return nil, errors.New("pick benchmarks: --pack <name> or --benchmarks a,b")
	}
	if o.tasks < 1 || o.trials < 1 {
		return nil, errors.New("--tasks and --trials must be at least 1")
	}
	if o.budget < 0 {
		return nil, errors.New("--budget must not be negative")
	}
	id, ver, err := agentRef(o.agent)
	if err != nil {
		return nil, err
	}
	a := schemas.EvaluationArm{Label: "A", Agent: id, Version: ver, Model: o.model, Provider: o.provider}
	spec := &schemas.EvaluationSpec{
		Name: o.name, Mode: schemas.EvaluationModeScore, Arms: []schemas.EvaluationArm{a},
		Benchmarks: benches, Pack: o.pack, TasksPerBenchmark: o.tasks, Trials: o.trials,
		BudgetUSD: o.budget, Faults: []string{faults.Clean},
	}
	switch {
	case o.vs != "":
		bid, bver, err := agentRef(o.vs)
		if err != nil {
			return nil, err
		}
		if bid == id && bver == ver {
			return nil, errors.New("--vs names the same agent and version as --agent")
		}
		spec.Mode = schemas.EvaluationModeOpponent
		if bid == id {
			spec.Mode = schemas.EvaluationModeVersions
		}
		spec.Arms = append(spec.Arms, schemas.EvaluationArm{Label: "B", Agent: bid, Version: bver, Model: o.model, Provider: o.provider})
	case o.mode == schemas.EvaluationModeModelSwitch:
		if err := modelSwitchArms(spec, a, splitList(o.models)); err != nil {
			return nil, err
		}
	case o.vsModel != "":
		if o.vsModel == o.model {
			return nil, errors.New("--vs-model is the same as --model")
		}
		spec.Mode = schemas.EvaluationModeModels
		b := a
		b.Label, b.Model, b.Provider = "B", o.vsModel, ""
		spec.Arms = append(spec.Arms, b)
	}
	for _, f := range splitList(o.faults) {
		if !evalFaults[f] {
			return nil, fmt.Errorf("unknown fault %q (model-down, broken-tool, misleading-instruction)", f)
		}
		if !evalHas(spec.Faults, f) {
			spec.Faults = append(spec.Faults, f)
		}
	}
	if spec.Name == "" {
		spec.Name = defaultEvalName(spec)
	}
	return spec, nil
}

// modelSwitchArms adds one arm per extra model to a model_switch spec. The
// labels match the web form (current, model-2, ...). "provider/model"
// sets the arm's provider; a bare name keeps the provider empty.
func modelSwitchArms(spec *schemas.EvaluationSpec, a schemas.EvaluationArm, extra []string) error {
	spec.Mode = schemas.EvaluationModeModelSwitch
	a.Label = "current"
	spec.Arms = []schemas.EvaluationArm{a}
	if len(extra) == 0 {
		return errors.New("--mode model_switch needs --models: at least one model besides --model")
	}
	if 1+len(extra) > schemas.EvaluationModelSwitchMaxArms {
		return fmt.Errorf("--mode model_switch takes at most %d models in all, got %d", schemas.EvaluationModelSwitchMaxArms, 1+len(extra))
	}
	seen := map[string]bool{a.Provider + "/" + a.Model: true}
	for i, m := range extra {
		b := a
		b.Label = fmt.Sprintf("model-%d", i+2)
		b.Provider, b.Model = "", m
		if p, rest, ok := strings.Cut(m, "/"); ok && p != "" && rest != "" {
			b.Provider, b.Model = p, rest
		}
		if seen[b.Provider+"/"+b.Model] || (b.Model == a.Model && b.Provider == "") {
			return fmt.Errorf("--models lists %s twice or the same as --model", m)
		}
		seen[b.Provider+"/"+b.Model] = true
		spec.Arms = append(spec.Arms, b)
	}
	return nil
}

func evalHas(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func armName(a schemas.EvaluationArm) string {
	s := a.Agent
	if a.Version > 0 {
		s += fmt.Sprintf("@v%d", a.Version)
	}
	return s + " " + a.Model
}

func defaultEvalName(s *schemas.EvaluationSpec) string {
	what := s.Pack
	if what == "" {
		what = strings.Join(s.Benchmarks, ",")
	}
	names := make([]string, len(s.Arms))
	for i, a := range s.Arms {
		names[i] = armName(a)
	}
	return strings.Join(names, " vs ") + " on " + what
}

// evalBackend is either the HTTP API or the local project files.
type evalBackend interface {
	create(ctx context.Context, s *schemas.EvaluationSpec) (*schemas.Evaluation, error)
	estimate(ctx context.Context, s *schemas.EvaluationSpec) (*evalEstimate, error)
	list(ctx context.Context) ([]schemas.Evaluation, error)
	get(ctx context.Context, id string) (*schemas.Evaluation, error)
	report(ctx context.Context, id string) (*evalReport, error)
	cancel(ctx context.Context, id string) (*schemas.Evaluation, error)
}

func newEvalBackend(c connOpts) (evalBackend, error) {
	if strings.TrimSpace(c.server) != "" {
		cl, err := newSyncClient(&syncOpts{server: c.server, token: c.token, org: c.org})
		if err != nil {
			return nil, err
		}
		return &httpEval{c: cl}, nil
	}
	root, err := projectRoot(projectConfig(c.config, c.configSet))
	if err != nil {
		return nil, err
	}
	return &localEval{root: root}, nil
}

func runEvalSub(sub string, args []string, out io.Writer) int {
	o, err := parseEvalFlags(sub, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, evalUsage)
			return exitOK
		}
		fmt.Fprintf(os.Stderr, "%s eval %s: %v\n\n%s\n", progName, sub, err, evalUsage)
		return exitUsage
	}
	var spec *schemas.EvaluationSpec
	if sub == "run" {
		if spec, err = buildEvalSpec(o); err != nil {
			fmt.Fprintf(os.Stderr, "%s eval run: %v\n\n%s\n", progName, err, evalUsage)
			return exitUsage
		}
	}
	be, err := newEvalBackend(o.conn)
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	emit := func(v any, text func()) int {
		if o.asJSON {
			return writeJSON(out, v)
		}
		text()
		return exitOK
	}
	switch sub {
	case "run":
		if o.estimate {
			est, err := be.estimate(ctx, spec)
			if err != nil {
				return fail("estimate: %v", err)
			}
			return emit(est, func() { renderEstimate(out, est) })
		}
		ev, err := be.create(ctx, spec)
		if err != nil {
			return fail("start evaluation: %v", err)
		}
		return emit(ev, func() {
			renderEvaluation(out, ev)
			fmt.Fprintf(out, "\nfollow: %s eval show %s\nreport: %s eval report %s\n", progName, ev.ID, progName, ev.ID)
		})
	case "list":
		evs, err := be.list(ctx)
		if err != nil {
			return fail("list evaluations: %v", err)
		}
		return emit(evs, func() { renderEvalList(out, evs) })
	case "show":
		ev, err := be.get(ctx, o.pos[0])
		if err != nil {
			return fail("%v", err)
		}
		return emit(ev, func() { renderEvaluation(out, ev) })
	case "report":
		rep, err := be.report(ctx, o.pos[0])
		if err != nil {
			return fail("%v", err)
		}
		return emit(rep, func() { renderReport(out, rep) })
	case "cancel":
		ev, err := be.cancel(ctx, o.pos[0])
		if err != nil {
			return fail("%v", err)
		}
		return emit(ev, func() { fmt.Fprintf(out, "evaluation %s: %s\n", ev.ID, ev.Status) })
	}
	return exitUsage
}

func writeJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

// ---- report and estimate shapes (contract section 3 and 4; client copies) ----

type evalRange struct {
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
	Known bool    `json:"known,omitempty"`
}

type evalEstimate struct {
	Runs         int               `json:"runs"`
	EstCostUSD   evalRange         `json:"est_cost_usd"`
	EstMinutes   evalRange         `json:"est_minutes"`
	PerBenchmark []json.RawMessage `json:"per_benchmark,omitempty"`
}

type evalMetric struct {
	Score     float64 `json:"score"`
	CILow     float64 `json:"ci_low"`
	CIHigh    float64 `json:"ci_high"`
	Runs      int     `json:"runs"`
	CostUSD   float64 `json:"cost_usd"`
	Tokens    int64   `json:"tokens"`
	Steps     float64 `json:"steps"`
	ToolCalls float64 `json:"tool_calls"`
	WallS     float64 `json:"wall_s"`
}

type evalVerdict struct {
	Winner      string  `json:"winner"`
	Delta       float64 `json:"delta"`
	Significant bool    `json:"significant"`
	Summary     string  `json:"summary"`
}

type evalReport struct {
	Evaluation   string                  `json:"evaluation"`
	Arms         []schemas.EvaluationArm `json:"arms"`
	Capabilities []struct {
		Capability string                `json:"capability"`
		Benchmarks []string              `json:"benchmarks"`
		Arms       map[string]evalMetric `json:"arms"`
	} `json:"capabilities"`
	Benchmarks []struct {
		Benchmark    string                `json:"benchmark"`
		Capability   string                `json:"capability"`
		ExperimentID string                `json:"experiment_id"`
		Arms         map[string]evalMetric `json:"arms"`
		Verdict      *evalVerdict          `json:"verdict,omitempty"`
	} `json:"benchmarks"`
	Reliability []struct {
		Fault string `json:"fault"`
		Arms  map[string]struct {
			Score float64 `json:"score"`
			Drop  float64 `json:"drop"`
		} `json:"arms"`
	} `json:"reliability"`
	Safety *struct {
		AttackResisted map[string]float64 `json:"attack_resisted"`
		CheatRate      map[string]float64 `json:"cheat_rate"`
	} `json:"safety,omitempty"`
	Alignment *reportcard.Alignment `json:"alignment,omitempty"`
	Overall   *struct {
		Winner  string         `json:"winner"`
		Wins    map[string]int `json:"wins"`
		Summary string         `json:"summary"`
	} `json:"overall,omitempty"`
}

// ---- rendering ----

// alignmentCell is one arm's safety rate, or "not tested".
func alignmentCell(arms map[string]reportcard.AlignmentRate, label string) string {
	r, ok := arms[label]
	if !ok {
		return "not tested"
	}
	return fmt.Sprintf("%.0f%% (%.0f-%.0f%%, %d runs)", r.Rate*100, r.CILow*100, r.CIHigh*100, r.Runs)
}

func renderEstimate(w io.Writer, e *evalEstimate) {
	fmt.Fprintf(w, "runs:    %d\n", e.Runs)
	if e.EstCostUSD.Known {
		fmt.Fprintf(w, "cost:    %s to %s\n", moneyText(e.EstCostUSD.Low), moneyText(e.EstCostUSD.High))
	} else {
		fmt.Fprintln(w, "cost:    unknown (no price for this model)")
	}
	if e.EstMinutes.High > 0 {
		fmt.Fprintf(w, "time:    %.0f to %.0f min\n", e.EstMinutes.Low, e.EstMinutes.High)
	} else {
		fmt.Fprintln(w, "time:    unknown")
	}
}

func renderEvaluation(w io.Writer, ev *schemas.Evaluation) {
	fmt.Fprintf(w, "evaluation %s  %s\n", ev.ID, ev.Status)
	if ev.Spec.Name != "" {
		fmt.Fprintf(w, "name:     %s\n", ev.Spec.Name)
	}
	fmt.Fprintf(w, "mode:     %s\n", ev.Spec.Mode)
	for _, a := range ev.Spec.Arms {
		fmt.Fprintf(w, "arm %s:    %s\n", a.Label, armName(a))
	}
	fmt.Fprintf(w, "tasks:    %d per benchmark, %d trials, faults %s\n", ev.Spec.TasksPerBenchmark, ev.Spec.Trials, strings.Join(ev.Spec.Faults, ","))
	if len(ev.Children) == 0 {
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "BENCHMARK\tEXPERIMENT\tSTATUS\tNOTE")
	for _, c := range ev.Children {
		exp := c.ExperimentID
		if exp == "" {
			exp = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Benchmark, exp, c.Status, c.Skipped)
	}
	tw.Flush()
}

func renderEvalList(w io.Writer, evs []schemas.Evaluation) {
	if len(evs) == 0 {
		fmt.Fprintln(w, "no evaluations yet; start one with: "+progName+" eval run --agent <id> --model <model> --pack coding")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tMODE\tBENCHMARKS\tCREATED\tNAME")
	for _, ev := range evs {
		n := len(ev.Children)
		if n == 0 {
			n = len(ev.Spec.Benchmarks)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", ev.ID, ev.Status, ev.Spec.Mode, n, ev.CreatedAt.Local().Format("2006-01-02 15:04"), ev.Spec.Name)
	}
	tw.Flush()
}

func metricCell(m evalMetric, ok bool) string {
	if !ok || m.Runs == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%% [%.0f-%.0f] n=%d", m.Score*100, m.CILow*100, m.CIHigh*100, m.Runs)
}

func reportLabels(r *evalReport) []string {
	var labels []string
	for _, a := range r.Arms {
		labels = append(labels, a.Label)
	}
	if len(labels) == 0 {
		seen := map[string]bool{}
		for _, c := range r.Capabilities {
			for l := range c.Arms {
				if !seen[l] {
					seen[l] = true
					labels = append(labels, l)
				}
			}
		}
		sort.Strings(labels)
	}
	return labels
}

// renderReport prints the report card: a capability table, then one row per
// benchmark with the A/B verdict, reliability under faults, safety, overall.
func renderReport(w io.Writer, r *evalReport) {
	labels := reportLabels(r)
	fmt.Fprintf(w, "report card %s\n", r.Evaluation)
	for _, a := range r.Arms {
		fmt.Fprintf(w, "  %s = %s\n", a.Label, armName(a))
	}
	fmt.Fprintln(w)
	head := func(tw io.Writer, first ...string) {
		cols := append([]string{}, first...)
		for _, l := range labels {
			cols = append(cols, l)
		}
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
	}

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	head(tw, "CAPABILITY", "BENCHMARKS")
	for _, c := range r.Capabilities {
		row := []string{c.Capability, strings.Join(c.Benchmarks, ",")}
		for _, l := range labels {
			m, ok := c.Arms[l]
			row = append(row, metricCell(m, ok))
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()

	if len(r.Benchmarks) > 0 {
		fmt.Fprintln(w)
		tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		cols := []string{"BENCHMARK", "CAPABILITY"}
		cols = append(cols, labels...)
		cols = append(cols, "COST", "VERDICT")
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
		for _, b := range r.Benchmarks {
			row := []string{b.Benchmark, b.Capability}
			cost := 0.0
			for _, l := range labels {
				m, ok := b.Arms[l]
				row = append(row, metricCell(m, ok))
				cost += m.CostUSD
			}
			v := "-"
			if b.Verdict != nil {
				v = b.Verdict.Summary
				if v == "" {
					v = b.Verdict.Winner
				}
				if !b.Verdict.Significant && b.Verdict.Winner != "" {
					v += " (not significant)"
				}
			}
			row = append(row, moneyText(cost), v)
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		tw.Flush()
	}

	if len(r.Reliability) > 0 {
		fmt.Fprintln(w, "\nunder faults:")
		tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		head(tw, "FAULT")
		for _, f := range r.Reliability {
			row := []string{f.Fault}
			for _, l := range labels {
				m, ok := f.Arms[l]
				if !ok {
					row = append(row, "-")
					continue
				}
				row = append(row, fmt.Sprintf("%.0f%% (%+.0f)", m.Score*100, m.Drop*100))
			}
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		tw.Flush()
	}

	if r.Safety != nil && (len(r.Safety.AttackResisted) > 0 || len(r.Safety.CheatRate) > 0) {
		fmt.Fprintln(w, "\nsafety:")
		for _, l := range labels {
			parts := []string{}
			if v, ok := r.Safety.AttackResisted[l]; ok {
				parts = append(parts, fmt.Sprintf("attacks resisted %.0f%%", v*100))
			}
			if v, ok := r.Safety.CheatRate[l]; ok {
				parts = append(parts, fmt.Sprintf("cheat rate %.0f%%", v*100))
			}
			if len(parts) > 0 {
				fmt.Fprintf(w, "  %s: %s\n", l, strings.Join(parts, ", "))
			}
		}
	}

	if r.Alignment != nil && len(r.Alignment.Categories) > 0 {
		fmt.Fprintln(w, "\nsafety and alignment (share of tested runs that failed, 95% interval):")
		tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		head(tw, "CATEGORY")
		for _, c := range r.Alignment.Categories {
			row := []string{c.Label}
			for _, l := range labels {
				row = append(row, alignmentCell(c.Arms, l))
			}
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		if len(r.Alignment.ImpossibleFlagged) > 0 {
			row := []string{"Impossible task flagged (higher is better)"}
			for _, l := range labels {
				row = append(row, alignmentCell(r.Alignment.ImpossibleFlagged, l))
			}
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		tw.Flush()
		var tested, untested []string
		for _, c := range r.Alignment.OWASPAgentic {
			if c.Tested {
				tested = append(tested, c.ID)
			} else {
				untested = append(untested, c.ID)
			}
		}
		fmt.Fprintf(w, "OWASP agentic tested: %s; not tested: %s\n", listOrNone(tested), listOrNone(untested))
	}

	if r.Overall != nil && r.Overall.Summary != "" {
		fmt.Fprintf(w, "\noverall: %s\n", r.Overall.Summary)
	}
}

// ---- HTTP backend ----

type httpEval struct{ c *syncClient }

func (h *httpEval) call(ctx context.Context, method, p string, body, out any) error {
	var rd io.Reader
	hdr := map[string]string{"Accept": "application/json"}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = strings.NewReader(string(raw))
		hdr["Content-Type"] = "application/json"
	}
	resp, err := h.c.do(ctx, method, p, rd, hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return apiError(method, p, resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, p, err)
	}
	return nil
}

func evalPath(id string) string { return "/evaluations/" + urlPathEscape(id) }

func urlPathEscape(s string) string {
	return strings.NewReplacer("/", "%2F", "?", "%3F", "#", "%23", " ", "%20").Replace(s)
}

func (h *httpEval) create(ctx context.Context, s *schemas.EvaluationSpec) (*schemas.Evaluation, error) {
	var ev schemas.Evaluation
	return &ev, h.call(ctx, http.MethodPost, "/evaluations", s, &ev)
}

func (h *httpEval) estimate(ctx context.Context, s *schemas.EvaluationSpec) (*evalEstimate, error) {
	var e evalEstimate
	return &e, h.call(ctx, http.MethodPost, "/evaluations/estimate", s, &e)
}

func (h *httpEval) list(ctx context.Context) ([]schemas.Evaluation, error) {
	var raw json.RawMessage
	if err := h.call(ctx, http.MethodGet, "/evaluations", nil, &raw); err != nil {
		return nil, err
	}
	// Accept a bare array or {"evaluations":[...]}.
	var evs []schemas.Evaluation
	if err := json.Unmarshal(raw, &evs); err == nil {
		return evs, nil
	}
	var wrap struct {
		Evaluations []schemas.Evaluation `json:"evaluations"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, fmt.Errorf("decode evaluations: %w", err)
	}
	return wrap.Evaluations, nil
}

func (h *httpEval) get(ctx context.Context, id string) (*schemas.Evaluation, error) {
	var ev schemas.Evaluation
	return &ev, h.call(ctx, http.MethodGet, evalPath(id), nil, &ev)
}

func (h *httpEval) report(ctx context.Context, id string) (*evalReport, error) {
	var r evalReport
	return &r, h.call(ctx, http.MethodGet, evalPath(id)+"/report", nil, &r)
}

func (h *httpEval) cancel(ctx context.Context, id string) (*schemas.Evaluation, error) {
	var ev schemas.Evaluation
	return &ev, h.call(ctx, http.MethodPost, evalPath(id)+"/cancel", struct{}{}, &ev)
}

// ---- local backend (project files) ----

type localEval struct{ root string }

// heimdallDir is the project's .heimdall folder: the root itself when the
// config lives in .heimdall/deploy/bench.yaml, else root/.heimdall.
func heimdallDir(root string) string {
	if filepath.Base(root) == ".heimdall" {
		return root
	}
	return filepath.Join(root, ".heimdall")
}

func (l *localEval) dir() string { return filepath.Join(heimdallDir(l.root), "evaluations") }

func validEvalID(id string) bool {
	if id == "" || len(id) > 80 {
		return false
	}
	for _, r := range id {
		if !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func newEvalID(now time.Time) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return "ev_" + now.UTC().Format("20060102T150405") + "_" + hex.EncodeToString(b)
}

// localBenchmarks lists the benchmark ids in the project's benchmarks/ folder.
func (l *localEval) localBenchmarks() []string {
	ents, err := os.ReadDir(filepath.Join(l.root, "benchmarks"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() {
			out = append(out, n)
		} else if ext := filepath.Ext(n); ext == ".yaml" || ext == ".yml" {
			out = append(out, strings.TrimSuffix(n, ext))
		}
	}
	return out
}

// resolveBenchmarks expands the pack (globs match local benchmark ids) and
// merges explicit benchmark ids.
func (l *localEval) resolveBenchmarks(s *schemas.EvaluationSpec) ([]string, error) {
	var out []string
	add := func(b string) {
		if !evalHas(out, b) {
			out = append(out, b)
		}
	}
	if s.Pack != "" {
		pk, ok := packs.Get(s.Pack)
		if !ok {
			return nil, fmt.Errorf("unknown pack %q", s.Pack)
		}
		// Prefixes ("agentdojo-*", or "*" for all-compatible) match the
		// project's own benchmarks; exact ids stay, since built-in library
		// benchmarks are not in benchmarks/.
		local := l.localBenchmarks()
		for _, id := range pk.Benchmarks {
			if !strings.HasSuffix(id, "*") {
				add(id)
				continue
			}
			for _, b := range packs.Resolve(s.Pack, local) {
				add(b)
			}
		}
	}
	for _, b := range s.Benchmarks {
		add(b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("pack %q has no benchmarks in this project", s.Pack)
	}
	return out, nil
}

func evalRuns(s *schemas.EvaluationSpec, benches int) int {
	return benches * s.TasksPerBenchmark * s.Trials * len(s.Arms) * len(s.Faults)
}

func (l *localEval) estimate(_ context.Context, s *schemas.EvaluationSpec) (*evalEstimate, error) {
	b, err := l.resolveBenchmarks(s)
	if err != nil {
		return nil, err
	}
	// Local mode has no forecast service; cost and time stay unknown.
	return &evalEstimate{Runs: evalRuns(s, len(b))}, nil
}

func (l *localEval) create(_ context.Context, s *schemas.EvaluationSpec) (*schemas.Evaluation, error) {
	benches, err := l.resolveBenchmarks(s)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	ev := &schemas.Evaluation{ID: newEvalID(now), Spec: *s, Status: "queued", CreatedAt: now, CreatedBy: os.Getenv("USER")}
	ev.Spec.Benchmarks = benches
	for _, b := range benches {
		ev.Children = append(ev.Children, schemas.EvaluationChild{Benchmark: b, Status: "queued"})
	}
	if err := l.save(ev); err != nil {
		return nil, err
	}
	return ev, nil
}

func (l *localEval) save(ev *schemas.Evaluation) error {
	if err := os.MkdirAll(l.dir(), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(l.dir(), ev.ID+".json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (l *localEval) list(context.Context) ([]schemas.Evaluation, error) {
	ents, err := os.ReadDir(l.dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []schemas.Evaluation
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" || strings.Contains(e.Name(), ".report.") {
			continue
		}
		ev, err := l.get(context.Background(), strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, *ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (l *localEval) get(_ context.Context, id string) (*schemas.Evaluation, error) {
	if !validEvalID(id) {
		return nil, fmt.Errorf("bad evaluation id %q", id)
	}
	raw, err := os.ReadFile(filepath.Join(l.dir(), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("evaluation %s not found in %s", id, l.dir())
	}
	if err != nil {
		return nil, err
	}
	var ev schemas.Evaluation
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("evaluation %s: %w", id, err)
	}
	return &ev, nil
}

// report reads a report the server wrote beside the evaluation
// (<id>.report.json). Building one from runs needs the server.
func (l *localEval) report(ctx context.Context, id string) (*evalReport, error) {
	if _, err := l.get(ctx, id); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(l.dir(), id+".report.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no report card for %s yet; start a server (%s serve) and pass -server, or wait for the evaluation to finish", id, progName)
	}
	if err != nil {
		return nil, err
	}
	var r evalReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("report %s: %w", id, err)
	}
	return &r, nil
}

func (l *localEval) cancel(ctx context.Context, id string) (*schemas.Evaluation, error) {
	ev, err := l.get(ctx, id)
	if err != nil {
		return nil, err
	}
	switch ev.Status {
	case "done", "failed", "cancelled":
		return ev, nil
	}
	ev.Status = "cancelled"
	for i := range ev.Children {
		switch ev.Children[i].Status {
		case "done", "failed", "skipped":
		default:
			ev.Children[i].Status = "cancelled"
		}
	}
	return ev, l.save(ev)
}
