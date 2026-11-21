package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/ci"
	"github.com/maximhq/bifrost/bench/quickstart"
	"github.com/maximhq/bifrost/bench/schemas"
)

// First workflow: `ci install github` writes the pull-request gate into the
// repository; `ci card` renders the verdict card the gate posts.

func init() {
	register("ci", "pull-request gate: ci evaluate (hosted Heimdall); ci install github|gitlab [-mode hosted|recorded|live]; ci card", runCI)
}

const ciUsage = `usage: bifrost-bench ci evaluate -agent <id> -config <file> -pack <id> ...   (run ` + "`ci evaluate -h`" + ` for every flag)
       bifrost-bench ci install github|gitlab -agent <id> [-config agents/<id>.yaml | -image | -endpoint] [-url https://...]
           [-pack coding] [-benchmark <id>]... [-fault model-down]... [-fail-on regressed,failed] [-dry-run] [-force]
       bifrost-bench ci install github -mode recorded|live [-dry-run] [-force] [-base agents/<agent>.yaml] [-trials 3]
           [-fault model-outage]... [-budget-usd 5] [-exp-id <id>] [-data-mode recorded|synthetic]
           [-source <owner/repo>] [-source-ref <ref>] [-repo .]
       bifrost-bench ci card -report <report.json> [-out card.md] [-config ...] [-evidence <text>]`

// heimdallSource is the repository the workflow builds Heimdall from.
const heimdallSource = "Ronit-Raj9/Heimdall"

func runCI(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, ciUsage)
		return exitUsage
	}
	switch args[0] {
	case "install":
		return runCIInstall(args[1:])
	case "card":
		return runCICard(args[1:])
	case "evaluate":
		return runCIEvaluate(args[1:])
	}
	fmt.Fprintln(os.Stderr, ciUsage)
	return exitUsage
}

func runCIInstall(args []string) int {
	fs := flag.NewFlagSet("ci install", flag.ContinueOnError)
	mode := fs.String("mode", "", "hosted (evaluate on your hosted Heimdall; the default with -agent and for gitlab), recorded (re-check a sealed experiment) or live (run agents on a self-hosted runner)")
	agent := fs.String("agent", "", "hosted: the agent id on your Heimdall")
	agentConfig := fs.String("config", "", "hosted: the agent config file in this repository (default agents/<agent>.yaml)")
	image := fs.Bool("image", false, "hosted: pass an image the pipeline builds instead of a config file")
	endpoint := fs.Bool("endpoint", false, "hosted: pass a preview deployment URL instead of a config file")
	hostedURL := fs.String("url", "", "hosted: your Heimdall address; empty reads the HEIMDALL_URL variable")
	pack := fs.String("pack", "", "hosted: benchmark pack")
	failOn := fs.String("fail-on", "", "hosted: outcomes that fail the gate (default regressed,failed)")
	tasks := fs.Int("tasks", 0, "hosted: tasks per benchmark (0: the server default)")
	var benches listFlag
	fs.Var(&benches, "benchmark", "hosted: a benchmark id (repeatable)")
	dryRun := fs.Bool("dry-run", false, "print the workflow; write nothing")
	force := fs.Bool("force", false, "replace an existing workflow file")
	base := fs.String("base", "", "live: agent config both arms start from (default: the first agent heimdall init found)")
	trials := fs.Int("trials", 3, "live: trials per task")
	budget := fs.Float64("budget-usd", 5, "live: spend cap of one gate run in USD")
	expID := fs.String("exp-id", "", "recorded: experiment id inside .heimdall/recorded (default: the only one)")
	dataMode := fs.String("data-mode", "recorded", "recorded: label of the replayed data, recorded or synthetic")
	source := fs.String("source", heimdallSource, "GitHub repository Heimdall is built from")
	sourceRef := fs.String("source-ref", "main", "branch, tag or commit of Heimdall to build (pin a commit)")
	repo := fs.String("repo", ".", "the git repository to write the workflow into")
	var faults listFlag
	fs.Var(&faults, "fault", "live: a fault condition to add (repeatable); default model-outage")
	pos, err := parseArgs(fs, args)
	if err == nil && len(pos) == 1 && *mode == "" && (*agent != "" || pos[0] == ci.ProviderGitLab) {
		*mode = ci.ModeHosted
	}
	if *mode == "" {
		*mode = ci.ModeRecorded
	}
	if err == nil && len(pos) == 1 && *mode == ci.ModeHosted && (pos[0] == ci.ProviderGitHub || pos[0] == ci.ProviderGitLab) {
		cfg := *agentConfig
		if cfg == "" && !*image && !*endpoint && *agent != "" {
			cfg = "agents/" + *agent + ".yaml"
		}
		trialsN := 0
		if flagSet(fs, "trials") {
			trialsN = *trials
		}
		budgetN := 0.0
		if flagSet(fs, "budget-usd") {
			budgetN = *budget
		}
		var fl []string
		for _, f := range faults {
			fl = append(fl, f)
		}
		return installHosted(pos[0], ci.HostedOptions{
			Agent: *agent, Config: cfg, Image: *image, Endpoint: *endpoint, URL: *hostedURL,
			Source: *source, SourceRef: *sourceRef, Pack: *pack, Benchmarks: benches, Faults: fl,
			FailOn: *failOn, Trials: trialsN, Tasks: *tasks, BudgetUSD: budgetN,
		}, *repo, *dryRun, *force)
	}
	if err != nil || len(pos) != 1 || pos[0] != "github" {
		fmt.Fprintln(os.Stderr, ciUsage)
		if err == nil && len(pos) == 1 {
			fmt.Fprintf(os.Stderr, "only github is supported, got %q\n", pos[0])
		}
		return exitUsage
	}
	top, err := gitTop(*repo)
	if err != nil {
		return fail("ci install: %s is not inside a git repository: %v", *repo, err)
	}
	if *mode == ci.ModeLive && *base == "" {
		if pf, err := quickstart.LoadProjectFile(filepath.Join(top, quickstart.Dir)); err == nil && len(pf.Agents) > 0 {
			*base = pf.Agents[0].Config
		}
	}
	if len(faults) == 0 {
		faults = listFlag{"model-outage"}
	}
	wf, err := ci.GitHubWorkflow(ci.WorkflowOptions{
		Mode: *mode, Source: *source, SourceRef: *sourceRef, Base: *base, Trials: *trials, Faults: faults,
		BudgetUSD: *budget, ExpID: *expID, DataMode: *dataMode,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s ci install: %v\n", progName, err)
		return exitUsage
	}
	dst := filepath.Join(top, ".github", "workflows", "heimdall.yml")
	if *dryRun {
		fmt.Print(wf)
		fmt.Fprintf(os.Stderr, "-dry-run: would write %s (%d lines)\n", dst, countLines(wf))
		printCINotes(*mode, top)
		return exitOK
	}
	if _, err := os.Stat(dst); err == nil && !*force {
		return fail("%s exists; pass -force to replace it", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fail("%v", err)
	}
	if err := os.WriteFile(dst, []byte(wf), 0o644); err != nil {
		return fail("%v", err)
	}
	fmt.Printf("wrote %s\n", dst)
	printCINotes(*mode, top)
	return exitOK
}

// installHosted writes the hosted workflow for provider into the repository.
func installHosted(provider string, o ci.HostedOptions, repo string, dryRun, force bool) int {
	rel, wf, err := ci.HostedWorkflow(provider, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s ci install: %v\n", progName, err)
		return exitUsage
	}
	top, err := gitTop(repo)
	if err != nil {
		return fail("ci install: %s is not inside a git repository: %v", repo, err)
	}
	dst := filepath.Join(top, filepath.FromSlash(rel))
	notes := func() {
		if provider == ci.ProviderGitLab {
			fmt.Fprintf(os.Stderr, "next: include it from .gitlab-ci.yml (include: - local: %s), then add the CI/CD variables HEIMDALL_TOKEN (an org API token from Heimdall), GITLAB_TOKEN (a project access token with the api scope)%s\n",
				rel, map[bool]string{true: "", false: " and HEIMDALL_URL"}[o.URL != ""])
			return
		}
		fmt.Fprintf(os.Stderr, "next: add the repository secret HEIMDALL_TOKEN (an org API token from Heimdall, Settings > API tokens)%s, then commit %s\n",
			map[bool]string{true: "", false: " and the variable HEIMDALL_URL"}[o.URL != ""], rel)
	}
	if dryRun {
		fmt.Print(wf)
		fmt.Fprintf(os.Stderr, "-dry-run: would write %s (%d lines)\n", dst, countLines(wf))
		notes()
		return exitOK
	}
	if _, err := os.Stat(dst); err == nil && !force {
		return fail("%s exists; pass -force to replace it", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fail("%v", err)
	}
	if err := os.WriteFile(dst, []byte(wf), 0o644); err != nil {
		return fail("%v", err)
	}
	fmt.Printf("wrote %s\n", dst)
	notes()
	return exitOK
}

func printCINotes(mode, top string) {
	if mode == ci.ModeLive {
		fmt.Fprintln(os.Stderr, "live mode: register a self-hosted runner labelled `heimdall` with Docker and the stack up, and set the repository variable HEIMDALL_ENV_FILE to the stack's env file on it")
		return
	}
	rec := filepath.Join(top, quickstart.Dir, "recorded")
	if _, err := os.Stat(rec); err != nil {
		fmt.Fprintln(os.Stderr, recordedCopyNote(rec, filepath.Join(top, quickstart.Dir)))
	}
}

// recordedCopyNote is the recorded-mode setup step. The folder is created
// first: `cp -R <exp> <dest>/` into a missing dest makes dest itself the
// experiment, and CI expects one folder per experiment beneath it (audit
// 5-B-07).
func recordedCopyNote(rec, project string) string {
	return fmt.Sprintf("recorded mode: copy one sealed experiment into %s (mkdir -p %s && cp -R %s/runs/<exp_id> %s/) and commit it",
		rec, rec, project, rec)
}

func countLines(s string) int {
	n := 0
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	return n
}

// runCICard renders the verdict card from a check report (the analysis JSON
// `check -out` writes beside the markdown). With a config whose runs root
// holds the experiment it also counts unreviewed task instructions.
func runCICard(args []string) int {
	fs := flag.NewFlagSet("ci card", flag.ContinueOnError)
	report := fs.String("report", "", "the analysis JSON check wrote (report.json), or its report.md")
	out := fs.String("out", "", "write the card here (default: stdout)")
	config := fs.String("config", defaultConfig, "bench config, to read the experiment's manifest and suite (optional)")
	evidence := fs.String("evidence", "", "where the evidence is, shown on the card")
	uiURL := fs.String("ui-url", "", "base URL of a heimdall serve the reader can open; empty leaves the link out")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 || *report == "" {
		fmt.Fprintln(os.Stderr, ciUsage)
		return exitUsage
	}
	path := *report
	if filepath.Ext(path) == ".md" {
		path = trimExt(path) + ".json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fail("ci card: %v", err)
	}
	var an schemas.Analysis
	if err := json.Unmarshal(raw, &an); err != nil {
		return fail("ci card: %s: %v", path, err)
	}
	in := ci.CardInput{Evidence: *evidence}
	cfgPath := projectConfig(*config, flagSet(fs, "config"))
	if a, err := newApp(context.Background(), cfgPath, appOptions{}); err == nil {
		if m, err := a.store.LoadManifest(context.Background(), an.ExpID); err == nil {
			in.Unreviewed, in.Tasks = a.experimentUnreviewed(context.Background(), m)
			in.ArmLabels = map[string]string{}
			for id, arm := range m.Arms {
				in.ArmLabels[id] = arm.Label
			}
			if *uiURL != "" {
				in.UILink = *uiURL + "/verdict?exp=" + an.ExpID
			}
		}
	}
	card := ci.VerdictCard(&an, in)
	if *out == "" {
		fmt.Print(card)
		return exitOK
	}
	if err := os.WriteFile(*out, []byte(card), 0o644); err != nil {
		return fail("ci card: %v", err)
	}
	return exitOK
}
