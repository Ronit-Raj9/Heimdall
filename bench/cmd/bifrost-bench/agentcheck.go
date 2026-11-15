package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// Check row statuses.
const (
	checkPass = "PASS"
	checkWarn = "WARN"
	checkFail = "FAIL"
	checkSkip = "SKIP"
	// checkError is the report result when the check could not run at all.
	checkError = "ERROR"
)

// Check rows, in the order of the contract (docs/bring-your-own-design.md).
const (
	rowImage     = "image"
	rowUser      = "user"
	rowModelCall = "model_call"
	rowSession   = "session"
	rowExit      = "exit"
	rowDiff      = "diff"
	rowEgress    = "egress"
	rowEvidence  = "evidence"
	rowClaim     = "claim"

	// Scenario rows, present only when the task has what they check
	// (docs/scenarios.md): services, an MCP server, a proxied service,
	// several sessions, an answer output.
	rowServices = "services"
	rowMCP      = "mcp_config"
	rowToolCall = "tool_call"
	rowSessions = "sessions"
	rowAnswer   = "answer"
)

// defaultChecksRoot holds check runs, apart from runs/: a check never enters
// an experiment, a verdict, the leaderboard or the UI's run list.
const defaultChecksRoot = "checks"

// stderrTail is how much of the agent's stderr a failing row quotes.
const stderrTail = 2048

var rowOrder = []string{rowImage, rowUser, rowModelCall, rowSession, rowExit, rowDiff, rowEgress, rowEvidence, rowClaim}

var rowTitles = map[string]string{
	rowImage:     "image builds",
	rowUser:      "starts as the unprivileged user",
	rowModelCall: "model call reached Bifrost with the run key",
	rowSession:   "session id sent",
	rowExit:      "exited by itself inside the budget",
	rowDiff:      "diff exported",
	rowEgress:    "no egress outside the gateway",
	rowEvidence:  "evidence level",
	rowClaim:     "completion claim captured",
	rowServices:  "services ready",
	rowMCP:       "MCP config rendered",
	rowToolCall:  "tool call reached the proxy",
	rowSessions:  "sessions ran in order, memory kept",
	rowAnswer:    "answer exported",
}

// checkRow is one line of the conformance report.
type checkRow struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // PASS | WARN | FAIL | SKIP
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"` // one-line hint, on WARN and FAIL
}

// checkReport is what `agent check` prints, as a table or with -json.
type checkReport struct {
	Agent   string     `json:"agent"`
	Suite   string     `json:"suite"`
	Task    string     `json:"task"`
	Image   string     `json:"image,omitempty"`
	ImageID string     `json:"image_id,omitempty"`
	ExpID   string     `json:"exp_id,omitempty"`
	RunID   string     `json:"run_id,omitempty"`
	RunDir  string     `json:"run_dir,omitempty"`
	Outcome string     `json:"outcome,omitempty"` // the task outcome; the scripted model solves nothing
	Rows    []checkRow `json:"rows"`
	Result  string     `json:"result"` // PASS, WARN, FAIL, or ERROR when the check could not run
	Error   string     `json:"error,omitempty"`
}

// checkOptions are the flags of `agent check`.
type checkOptions struct {
	config  string
	agent   string
	suite   string
	task    string
	runs    string
	noBuild bool
	debug   bool
}

// checkInput is everything the rows after the image row read from one
// sealed check run.
type checkInput struct {
	agent       *schemas.AgentConfig
	image       environments.ImageInfo
	run         *schemas.Run
	ev          schemas.Evidence
	gates       []schemas.GateResult
	net         []environments.NetWatchRecord
	netErr      string // why nothing was observed; empty when the observer ran
	gatewayHost string
	gatewayPort int
	stderr      string // tail of the agent's stderr
	wallS       int
	plan        schemas.RunPlan
	scenario    *checkScenario // nil for a repository task
}

// checkScenario is what the scenario rows read: the task's declared shape and
// what the run folder recorded about its services, tool calls, sessions and
// answer. A record that is missing stays nil (with the reason in its *Err),
// so a row can say it is unknown instead of reading it as empty.
type checkScenario struct {
	suite    *schemas.Suite
	task     *schemas.Task
	services []schemas.ServiceResult
	tools    []schemas.ToolCall
	toolsErr string // why there is no tool ledger; empty when it was read
	sessions []schemas.SessionResult
	answer   *schemas.Answer
}

// runAgentCheck runs `agent check` and prints the report. Exit 0: no FAIL;
// 1: a row failed; 4: the check could not run (and nothing was spent).
func runAgentCheck(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("agent check", flag.ContinueOnError)
	opts := checkOptions{}
	fs.StringVar(&opts.config, "config", defaultConfig, "bench config")
	fs.StringVar(&opts.suite, "suite", "", "suite to run the check in (required)")
	fs.StringVar(&opts.task, "task", "", "task id (default: the suite's first task)")
	fs.StringVar(&opts.runs, "runs", defaultChecksRoot, "folder for check runs, relative to the project root; never runs/")
	fs.BoolVar(&opts.noBuild, "no-build", false, "use the existing image instead of building it")
	fs.BoolVar(&opts.debug, "debug", false, "debug logging")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) != 1 || opts.suite == "" {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench agent check <agents/id.yaml> -suite <suite> [-task <task>] [-json] [-no-build] [-runs checks] [-config deploy/bench.yaml]")
		fmt.Fprintln(os.Stderr, "exit: 0 no FAIL, 1 a row failed, 4 the check could not run")
		return exitUsage
	}
	opts.agent = pos[0]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rep := agentCheck(ctx, opts, os.Stderr)
	if *asJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return fail("encode report: %v", err)
		}
		fmt.Fprintln(out, string(b))
	} else {
		printCheckReport(out, rep)
	}
	return checkReportExit(rep)
}

// agentCheck runs the conformance check and returns its report. It refuses
// to start a run, and reports ERROR, when the fault proxy cannot answer the
// run from its scripted model: an older proxy, or a provider with no scripted
// model. It never sweeps or touches other runs.
func agentCheck(ctx context.Context, opts checkOptions, log io.Writer) *checkReport {
	rep := &checkReport{Suite: opts.suite, Task: opts.task}
	cfg, err := lib.LoadConfig(opts.config)
	if err != nil {
		return rep.fail(err)
	}
	agent, err := loadAgentConfig(opts.agent)
	if err != nil {
		return rep.fail(err)
	}
	rep.Agent = agent.ID
	s, err := agentSuite(ctx, cfg, agent, opts.suite)
	if err != nil {
		return rep.fail(err)
	}
	rep.Suite = s.ID
	if rep.Task == "" && len(s.TaskIDs) > 0 {
		rep.Task = s.TaskIDs[0]
	}
	task := s.TaskByID(rep.Task)
	if task == nil {
		return rep.fail(fmt.Errorf("task %q is not in suite %s", rep.Task, s.ID))
	}
	img, info, row := checkImage(ctx, utils.Docker{}, cfg.Root, agent, s, opts.noBuild, log)
	rep.Image, rep.ImageID = img.Tag, info.ID
	if row.Status == checkFail {
		rep.Rows = append([]checkRow{row}, skippedRows(withScenarioRows(rowOrder[1:], s, task), "the image is not available")...)
		return rep.finish()
	}
	row = ownImageRow(row, agent, task)
	if err := checkScriptedProxy(ctx, cfg.FaultProxy.ControlURL, os.Getenv("BENCH_FAULTPROXY_TOKEN"), agent.Gateway.Provider); err != nil {
		return rep.fail(err)
	}
	in, err := runCheckSlot(ctx, opts, rep)
	if err != nil {
		return rep.fail(err)
	}
	in.image = info
	rep.Rows = append([]checkRow{row}, checkRows(in)...)
	return rep.finish()
}

// runCheckSlot plans the one-slot check, runs it through the normal pipeline
// with fault plan mode scripted and the network observer on, seals it under
// the checks root and reads back what the rows need.
func runCheckSlot(ctx context.Context, opts checkOptions, rep *checkReport) (*checkInput, error) {
	a, err := newApp(ctx, opts.config, appOptions{gateway: true, debug: opts.debug})
	if err != nil {
		return nil, err
	}
	a.reg.Environments = environments.Builtin(environments.DockerWorkspaceConfig{
		EvalNetwork: a.cfg.Networks.Eval, HelperImage: a.cfg.Images.Helper, Logger: a.logger, Docker: a.docker, NetWatch: true,
		ToolProxyImage: a.cfg.Images.ToolProxy, // lead: tool proxy image for runs with proxied services
	})
	st, err := store.NewFSStore(a.cfg.Path(opts.runs), a.logger)
	if err != nil {
		return nil, fmt.Errorf("open checks folder: %w", err)
	}
	a.reg.Store = st
	m, err := engine.NewPlanner(a.reg, a.cfg.Root, a.logger).PlanCheck(ctx, engine.CheckSpec{
		Agent: a.benchRelative(opts.agent), Suite: opts.suite, Task: rep.Task,
	})
	if err != nil {
		return nil, fmt.Errorf("plan check: %w", err)
	}
	if err := st.CreateExperiment(ctx, m); err != nil {
		return nil, fmt.Errorf("record check %s: %w", m.ExpID, err)
	}
	s, err := engine.LoadSuite(ctx, a.reg, m.Experiment.Suite)
	if err != nil {
		return nil, err
	}
	rep.ExpID = m.ExpID
	p := engine.NewPipeline(a.reg, a.cfg.Root, a.logger, engine.WithFaultMode(faultproxy.ModeScripted))
	res, err := p.RunSlot(ctx, engine.SlotInput{Manifest: m, Suite: s, Slot: &m.Slots[0], Attempt: 1, Emit: checkProgress(rep)})
	if res == nil {
		return nil, fmt.Errorf("run check: %w", err)
	}
	if err != nil {
		a.logger.Warn("check run not sealed", "run", res.Run.RunID, "error", err.Error())
	}
	rep.RunID, rep.RunDir, rep.Outcome = res.Run.RunID, res.Dir, string(res.Run.Outcome)
	resolved := m.Arms[engine.CheckArm].Config
	in := readCheckRun(res.Dir, res.Run, &resolved)
	in.gatewayHost, in.gatewayPort = gatewayHostPort(a.cfg.Gateway.AgentURL)
	if t := s.TaskByID(rep.Task); t != nil && isScenarioTask(s, t) {
		in.scenario = readCheckScenario(res.Dir, &in.ev, s, t)
	}
	return in, nil
}

// isScenarioTask reports whether the task has anything a scenario row
// checks: services, several sessions or an output other than a diff.
func isScenarioTask(s *schemas.Suite, t *schemas.Task) bool {
	return len(utils.TaskServices(s, t)) > 0 || len(t.Sessions) > 1 || utils.OutputKind(s, t) != schemas.OutputDiff
}

// readCheckScenario reads the scenario records of a sealed check run: from
// evidence.json when its sources filled them, else from the run folder's own
// files (services.json, tools.jsonl, sessions.json, answer.txt).
func readCheckScenario(dir string, ev *schemas.Evidence, s *schemas.Suite, t *schemas.Task) *checkScenario {
	sc := &checkScenario{suite: s, task: t, services: ev.Services, tools: ev.ToolCalls, sessions: ev.Sessions, answer: ev.Answer}
	if sc.services == nil {
		readJSONFile(filepath.Join(dir, "services.json"), &sc.services)
	}
	if sc.sessions == nil {
		readJSONFile(filepath.Join(dir, "sessions.json"), &sc.sessions)
	}
	if sc.tools == nil {
		sc.tools, sc.toolsErr = readToolLedger(filepath.Join(dir, "tools.jsonl"))
	}
	if sc.answer == nil {
		if b, err := os.ReadFile(filepath.Join(dir, "answer.txt")); err == nil {
			sc.answer = &schemas.Answer{Text: string(b), Source: "file"}
		}
	}
	return sc
}

// readToolLedger reads the tool proxy's ledger (one schemas.ToolCall per
// line). A missing file is reported, never read as zero calls.
func readToolLedger(path string) ([]schemas.ToolCall, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "tools.jsonl missing from the run folder (" + err.Error() + ")"
	}
	calls := []schemas.ToolCall{}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var c schemas.ToolCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Sprintf("tools.jsonl line %d does not parse: %v", i+1, err)
		}
		calls = append(calls, c)
	}
	return calls, ""
}

// checkImage is the image row: build the per-suite image (or inspect the
// prebuilt one with -no-build or without a build section).
func checkImage(ctx context.Context, d utils.Docker, root string, agent *schemas.AgentConfig, s *schemas.Suite, noBuild bool, log io.Writer) (engine.AgentImage, environments.ImageInfo, checkRow) {
	row := checkRow{ID: rowImage, Title: rowTitles[rowImage]}
	img, err := engine.ResolveAgentImage(agent, s, root)
	if err != nil {
		row.Status, row.Detail, row.Fix = checkFail, err.Error(), "fix the build section: base_arg needs build.dockerfile and a build.tag with a tag part equal to image"
		return img, environments.ImageInfo{}, row
	}
	if img.Dockerfile != "" && !noBuild {
		_, info, err := buildAgentImage(ctx, d, root, agent, s, log)
		if err != nil {
			row.Status, row.Detail, row.Fix = checkFail, err.Error(), "run the same docker build by hand (bifrost-bench agent build) and fix the first failing step"
			return img, info, row
		}
		row.Status, row.Detail = checkPass, fmt.Sprintf("built %s (%s)", img.Tag, shortID(info.ID))
		return img, info, row
	}
	info, err := environments.InspectImage(ctx, d, img.Tag)
	if err != nil {
		row.Status, row.Detail = checkFail, err.Error()
		row.Fix = "build the image (bifrost-bench agent build) or add build.dockerfile so the check builds it"
		return img, info, row
	}
	row.Status, row.Detail = checkPass, fmt.Sprintf("prebuilt %s (%s)", img.Tag, shortID(info.ID))
	return img, info, row
}

// ownImageRow turns a passing image row into a WARN when the task brings its
// own image (environment:) and the agent has no build.base_arg: such an agent
// runs in its own image (engine.AgentImage.OwnImage) and sees only the
// workspace files, not the tools the task's image installs.
func ownImageRow(row checkRow, agent *schemas.AgentConfig, task *schemas.Task) checkRow {
	env := task.Environment
	if row.Status != checkPass || env == nil || (env.Image == "" && env.Dockerfile == "") {
		return row
	}
	if agent.Adapter == "scripted" || (agent.Build != nil && agent.Build.BaseArg != "") {
		return row
	}
	row.Status = checkWarn
	row.Detail += fmt.Sprintf("; task %s has its own image, but this agent has no build.base_arg, so it runs in its own image and sees only the workspace files", task.ID)
	row.Fix = "add build.base_arg (an ARG the Dockerfile builds FROM) so the agent is built on each task's image"
	return row
}

// checkScriptedProxy refuses the check unless the fault proxy honours plan
// mode scripted and has a scripted model for the agent's provider. Without
// both, a run could reach a real provider with a real key.
func checkScriptedProxy(ctx context.Context, controlURL, token, provider string) error {
	if provider == "" {
		provider = faultproxy.ProviderAnthropic
	}
	if token == "" {
		return errors.New("BENCH_FAULTPROXY_TOKEN is not set: the check cannot bind plan mode scripted, so it refuses to run")
	}
	h, err := faultproxy.NewClient(controlURL, faultproxy.WithToken(token)).Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("fault proxy at %s: %w; the check refuses to run without it", controlURL, err)
	}
	if !h.Supports(faultproxy.ModeScripted) {
		return fmt.Errorf("the fault proxy at %s is too old for plan mode scripted, so the check refuses to run (a call could reach a real provider); rebuild the bench image and restart the faultproxy service", controlURL)
	}
	if !h.Scripts(provider) {
		return fmt.Errorf("provider %s has no scripted model in the fault proxy (scripted: %s): its API shape cannot be answered for free, so the check refuses to run; check the agent with gateway.provider set to one of those, or run a real experiment",
			provider, strings.Join(h.ScriptedProviders, ", "))
	}
	return nil
}

// readCheckRun reads the sealed check run's files; cfg is the arm's config
// as the manifest froze it. A file that is missing leaves its part empty; the
// rows then say what could not be read.
func readCheckRun(dir string, run *schemas.Run, cfg *schemas.AgentConfig) *checkInput {
	in := &checkInput{agent: cfg, run: run, wallS: cfg.Budgets.WallS}
	readJSONFile(filepath.Join(dir, "evidence.json"), &in.ev)
	readJSONFile(filepath.Join(dir, "gates.json"), &in.gates)
	if readJSONFile(filepath.Join(dir, "plan.json"), &in.plan) && in.plan.WallS > 0 {
		in.wallS = in.plan.WallS
	}
	in.net, in.netErr = readNetWatch(filepath.Join(dir, environments.NetWatchFile))
	if b, err := os.ReadFile(filepath.Join(dir, "agent.stderr.txt")); err == nil {
		in.stderr = string(b[max(0, len(b)-stderrTail):])
	}
	return in
}

// readNetWatch reads the observer's file and says why nothing was observed
// when it did not run.
func readNetWatch(path string) ([]environments.NetWatchRecord, string) {
	recs, err := environments.ReadNetWatch(path)
	if err != nil {
		return nil, "the network observer wrote nothing (" + err.Error() + ")"
	}
	if len(recs) == 1 && recs[0].T == environments.NetWatchError {
		return nil, "the network observer did not start: " + recs[0].Error
	}
	return recs, ""
}

func readJSONFile(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// checkProgress prints stage progress to stderr while the check runs.
func checkProgress(rep *checkReport) func(engine.Event) {
	return func(ev engine.Event) {
		if ev.Kind == engine.EventStage && strings.HasPrefix(ev.Message, "failed") {
			fmt.Fprintf(os.Stderr, "%s check %s: stage %s %s\n", ev.At.Format(time.TimeOnly), rep.Agent, ev.Stage, ev.Message)
			return
		}
		if ev.Kind == engine.EventStage && ev.Message == string(schemas.TransitionStarted) {
			fmt.Fprintf(os.Stderr, "%s check %s: %s\n", ev.At.Format(time.TimeOnly), rep.Agent, ev.Stage)
		}
	}
}

// gatewayHostPort splits the gateway URL agents use, e.g. http://bifrost:8080.
func gatewayHostPort(raw string) (string, int) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	return strings.ToLower(u.Hostname()), port
}

// printCheckReport prints the report as a table with a fix line under every
// WARN and FAIL.
func printCheckReport(w io.Writer, rep *checkReport) {
	fmt.Fprintf(w, "agent check %s on %s/%s (scripted model, nothing spent)\n", rep.Agent, rep.Suite, rep.Task)
	if rep.Image != "" {
		fmt.Fprintf(w, "image %s %s\n", rep.Image, shortID(rep.ImageID))
	}
	for _, r := range rep.Rows {
		fmt.Fprintf(w, "%-5s %-44s %s\n", r.Status, r.Title, r.Detail)
		if r.Fix != "" && (r.Status == checkFail || r.Status == checkWarn) {
			fmt.Fprintf(w, "      fix: %s\n", r.Fix)
		}
	}
	if rep.Error != "" {
		fmt.Fprintf(w, "error: %s\n", rep.Error)
	}
	if rep.RunDir != "" {
		fmt.Fprintf(w, "run %s (task outcome %s) in %s\n", rep.RunID, rep.Outcome, rep.RunDir)
	}
	fmt.Fprintf(w, "result: %s\n", rep.Result)
}

// checkReportExit maps a report to the exit code.
func checkReportExit(rep *checkReport) int {
	switch rep.Result {
	case checkError:
		return exitInfra
	case checkFail:
		return 1
	default:
		return exitOK
	}
}

// fail marks the report as not run.
func (r *checkReport) fail(err error) *checkReport {
	r.Error, r.Result = err.Error(), checkError
	if r.Rows == nil {
		r.Rows = []checkRow{}
	}
	return r
}

// finish sets the result from the rows: FAIL beats WARN beats PASS.
func (r *checkReport) finish() *checkReport {
	r.Result = checkPass
	for _, row := range r.Rows {
		switch {
		case row.Status == checkFail:
			r.Result = checkFail
		case row.Status == checkWarn && r.Result == checkPass:
			r.Result = checkWarn
		}
	}
	return r
}

func skippedRows(ids []string, why string) []checkRow {
	out := make([]checkRow, 0, len(ids))
	for _, id := range ids {
		out = append(out, checkRow{ID: id, Title: rowTitles[id], Status: checkSkip, Detail: why})
	}
	return out
}

func shortID(id string) string {
	return short(id)
}
