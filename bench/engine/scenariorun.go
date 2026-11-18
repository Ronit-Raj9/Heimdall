package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Answer sources (schemas.Answer.Source).
const (
	AnswerFromFile         = "file"
	AnswerFromFinalMessage = "final_message"
	AnswerMissing          = "missing"
)

// Scenario files of the run folder.
const (
	FileSessions = "sessions.json"
	FileServices = "services.json"
	FileAnswer   = "answer.txt"
	FileStream   = "agent.stream.jsonl"
)

// sessionStreamFile is the run folder file holding the raw output of the
// 1-based session n.
func sessionStreamFile(n int) string {
	return "agent.stream." + strconv.Itoa(n) + ".jsonl"
}

// startServices starts the suite's and the task's services after the
// workspace exists, through the environment's ServiceEnvironment. A service
// that fails to start or to pass its ready check fails the prepare stage, so
// the run is aborted_infra with the service named; the agent never starts.
func (p *Pipeline) startServices(ctx context.Context, st *runState) error {
	rc := st.rc
	if len(utils.TaskServices(rc.Suite, rc.Task)) == 0 && len(utils.ExternalAPIs(rc.Suite, rc.Task)) == 0 && len(utils.TeamOf(rc)) == 0 { // External APIs area: external APIs run through the tool proxy; Multi-agent area: a team's mailbox
		return nil
	}
	se, ok := st.env.(schemas.ServiceEnvironment)
	if !ok {
		return wrapPart("environment", st.env, fmt.Errorf("task %s has services, but environment %s cannot run services", rc.Task.ID, st.env.GetName()))
	}
	handles, results, err := se.StartServices(ctx, rc)
	rc.Services, st.ev.Services = handles, results
	bad := notReady(results)
	switch {
	case err != nil && bad != "":
		return wrapPart("environment", st.env, fmt.Errorf("start services: %s: %w", bad, err))
	case err != nil:
		return wrapPart("environment", st.env, fmt.Errorf("start services: %w", err))
	case bad != "":
		return wrapPart("environment", st.env, fmt.Errorf("start services: not ready: %s", bad))
	}
	return nil
}

// notReady names every service that did not come up, with its error.
func notReady(results []schemas.ServiceResult) string {
	var out []string
	for _, r := range results {
		if r.Ready {
			continue
		}
		msg := r.ID
		if r.Error != "" {
			msg += " (" + r.Error + ")"
		}
		out = append(out, msg)
	}
	return strings.Join(out, ", ")
}

// runAgent runs the agent: once for a task without sessions, exactly as
// before scenarios, or once per session in order. A suite with verifier mode
// snapshot then freezes the agent's container file system.
func (p *Pipeline) runAgent(ctx context.Context, st *runState) error {
	var err error
	switch {
	case len(utils.TeamOf(st.rc)) > 0: // Multi-agent area: one container per team member (team.go)
		err = p.runTeam(ctx, st)
	case len(st.rc.Task.Sessions) == 0:
		_, err = p.execOnce(ctx, st, nil)
	default:
		err = p.runSessions(ctx, st)
	}
	if err != nil {
		return err
	}
	return p.snapshot(ctx, st)
}

// runSessions runs every session of the task in order. Before each one the
// environment prepares the workspace (BeginSession), and the agent sees a
// shallow copy of the task whose instruction is the session's, with
// rc.Session and rc.SessionIndex set. A session that times out, or that
// exits non-zero before the last session, ends the run there: the later
// sessions never start and sessionsEnded maps the run to that outcome. Each
// session's raw output is kept as agent.stream.<n>.jsonl and all of them,
// in order, as agent.stream.jsonl. After the loop rc.Task is the task again;
// rc.Session stays the last session that ran.
func (p *Pipeline) runSessions(ctx context.Context, st *runState) error {
	rc := st.rc
	se, ok := st.env.(schemas.SessionEnvironment)
	if !ok {
		return wrapPart("environment", st.env, fmt.Errorf("task %s has sessions, but environment %s cannot run sessions", rc.Task.ID, st.env.GetName()))
	}
	orig := rc.Task
	defer func() { rc.Task = orig }()
	sessions := orig.Sessions
	var stream bytes.Buffer
	var last *schemas.ExecResult
	truncated := false
	for i := range sessions {
		sess := &sessions[i]
		if err := se.BeginSession(ctx, rc, i); err != nil {
			return wrapPart("environment", st.env, fmt.Errorf("begin session %s: %w", sess.ID, err))
		}
		task := *orig
		task.Instruction = sess.Instruction
		rc.Task, rc.Session, rc.SessionIndex = &task, sess, i
		res, err := p.execOnce(ctx, st, sess)
		if res != nil {
			truncated = truncated || res.Truncated
			res.Truncated = truncated
			n := i + 1
			out, rerr := p.keepSessionStream(ctx, st, res, n)
			if rerr != nil && err == nil {
				err = rerr
			}
			stream.Write(out)
			st.ev.Sessions = append(st.ev.Sessions, schemas.SessionResult{ID: sess.ID, Index: n, ExitCode: res.ExitCode,
				TimedOut: res.TimedOut, Started: res.Started, Ended: res.Ended, Stdout: sessionStreamFile(n)})
			last = res
		}
		if err != nil {
			return err
		}
		if res.TimedOut || (res.ExitCode != 0 && i < len(sessions)-1) {
			p.logger.Warn("session ended the run", "run", rc.Run.RunID, "session", sess.ID, "exit_code", res.ExitCode, "timed_out", res.TimedOut)
			break
		}
	}
	if err := p.writeFile(ctx, st, FileStream, stream.Bytes()); err != nil {
		return err
	}
	if last != nil {
		res := *last
		res.StdoutPath = filepath.Join(rc.Dir, FileStream)
		rc.Exec, st.ev.Exec = &res, &res
	}
	return nil
}

// execOnce plans and executes one agent process. run.json is checkpointed
// with AgentStarted before the first Exec, so a resumed run can always tell
// whether its agent may have started and must never be rerun. sess is nil
// for a task without sessions; its wall budget, when set, replaces the
// plan's. Scenario variables are added to the plan's environment only when
// the run has any (scenarioEnv), so a plain task's plan is unchanged.
func (p *Pipeline) execOnce(ctx context.Context, st *runState, sess *schemas.SessionSpec) (*schemas.ExecResult, error) {
	if st.rc.Probe != nil && st.rc.Probe.Layer == schemas.LayerAdapter {
		rule := st.rc.Probe.Apply.AdapterFault
		if rule == nil {
			return nil, fmt.Errorf("adapter fault probe has no rule")
		}
		if env, ok := st.env.(schemas.AdapterFaultEnvironment); !ok || !env.SupportsAdapterFaults() {
			return nil, fmt.Errorf("environment does not support adapter fault hooks")
		} else if injector, ok := st.adapter.(schemas.AdapterFaultInjector); !ok {
			return nil, fmt.Errorf("adapter %s does not support fault hooks", st.adapter.GetName())
		} else if err := injector.ValidateFault(st.rc.Agent, *rule); err != nil {
			return nil, fmt.Errorf("adapter fault capability: %w", err)
		}
	}
	// An adapter that implements the harness-neutral contract is validated
	// first and asked for its execution plan; any other adapter is planned
	// through the legacy entry point.
	if validator, ok := st.adapter.(schemas.AdapterValidator); ok {
		if err := validator.Validate(ctx, st.rc); err != nil {
			return nil, wrapPart("adapter", st.adapter, fmt.Errorf("validate: %w", err))
		}
	}
	var plan *schemas.RunPlan
	var err error
	if runner, ok := st.adapter.(schemas.AdapterRunner); ok {
		plan, err = runner.BuildExecutionPlan(ctx, st.rc)
	} else {
		plan, err = st.adapter.Plan(ctx, st.rc)
	}
	if err != nil {
		return nil, wrapPart("adapter", st.adapter, fmt.Errorf("plan: %w", err))
	}
	if plan == nil {
		return nil, wrapPart("adapter", st.adapter, errors.New("plan returned no plan"))
	}
	env, err := scenarioEnv(st.rc)
	if err != nil {
		return nil, err
	}
	if len(env) > 0 {
		if plan.Env == nil {
			plan.Env = map[string]string{}
		}
		for k, v := range env {
			plan.Env[k] = v
		}
	}
	if sess != nil && sess.WallS > 0 {
		plan.WallS = sess.WallS
	}
	if st.rc.Probe != nil && st.rc.Probe.Layer == schemas.LayerAdapter {
		if injector, ok := st.adapter.(schemas.AdapterFaultInjector); ok {
			if err := injector.BindFaultRuntime(st.rc, plan); err != nil {
				return nil, fmt.Errorf("bind adapter fault runtime: %w", err)
			}
		}
	}
	st.plan = plan
	run := st.rc.Run
	first := !st.agentStarted
	if first {
		started := p.now().UTC()
		run.Timing.AgentStarted = &started
		if err := p.writeJSON(ctx, st, "run.json", run); err != nil {
			run.Timing.AgentStarted = nil
			return nil, fmt.Errorf("checkpoint before agent start: %w", err)
		}
		st.agentStarted = true
	}
	if st.rc.Probe != nil && st.rc.Probe.Layer == schemas.LayerAdapter {
		sessionID := "default"
		if sess != nil {
			sessionID = sess.ID
		}
		seen := false
		for _, id := range st.rc.FaultSessions {
			if id == sessionID {
				seen = true
				break
			}
		}
		if !seen {
			st.rc.FaultSessions = append(st.rc.FaultSessions, sessionID)
		}
	}
	res, err := st.env.Exec(ctx, st.rc, plan)
	exited := p.now().UTC()
	run.Timing.AgentExited = &exited
	if res != nil {
		st.rc.Exec, st.ev.Exec = res, res
		code := res.ExitCode
		run.Agent.ExitCode = &code
		run.Agent.TimedOut = res.TimedOut
		if first && !res.Started.IsZero() {
			s := res.Started.UTC()
			run.Timing.AgentStarted = &s
		}
		if !res.Ended.IsZero() {
			e := res.Ended.UTC()
			run.Timing.AgentExited = &e
		}
	}
	if err != nil {
		return res, wrapPart("environment", st.env, fmt.Errorf("exec agent: %w", err))
	}
	if res == nil {
		return nil, wrapPart("environment", st.env, errors.New("exec returned no result"))
	}
	return res, nil
}

// scenarioEnv is what the engine adds to the agent's environment: the MCP
// servers when services run, the answer path for an answer task, and the
// session variables and memory folder for a multi-session task. It is empty
// for a task with none of these.
func scenarioEnv(rc *schemas.RunContext) (map[string]string, error) {
	env := map[string]string{}
	if len(rc.Services) > 0 {
		key := ""
		if rc.Key != nil {
			key = rc.Key.Value
		}
		servers, err := utils.MCPServersJSONFor(rc.Services, key) // B2 MCP through Bifrost: the run key as an auth header for gateway-served servers
		if err != nil {
			return nil, err
		}
		env[utils.EnvMCPServers] = servers
		if err := utils.AddExternalEnv(env, rc.Services); err != nil { // External APIs area: BENCH_EXTERNAL_APIS
			return nil, err
		}
	}
	if utils.OutputKind(rc.Suite, rc.Task) == schemas.OutputAnswer {
		env[utils.EnvAnswerPath] = utils.AnswerContainerPath(rc.Suite, rc.Task)
	}
	if rc.Session != nil {
		env[utils.EnvSessionID] = rc.Session.ID
		env[utils.EnvSessionIndex] = strconv.Itoa(rc.SessionIndex + 1)
		env[utils.EnvSessionCount] = strconv.Itoa(len(rc.Task.Sessions))
		env[utils.EnvMemoryDir] = utils.DefaultMemoryDir
	}
	return env, nil
}

// keepSessionStream copies one session's raw output to
// agent.stream.<n>.jsonl, unless the environment already wrote it there, and
// returns it. A session that wrote nothing has an empty stream.
func (p *Pipeline) keepSessionStream(ctx context.Context, st *runState, res *schemas.ExecResult, n int) ([]byte, error) {
	if res.StdoutPath == "" {
		return nil, nil
	}
	b, err := os.ReadFile(res.StdoutPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session %d output: %w", n, err)
	}
	if filepath.Base(res.StdoutPath) == sessionStreamFile(n) {
		return b, nil
	}
	return b, p.writeFile(ctx, st, sessionStreamFile(n), b)
}

// snapshot freezes the agent's container after its last session when the
// suite grades snapshots.
func (p *Pipeline) snapshot(ctx context.Context, st *runState) error {
	if utils.VerifierMode(st.rc.Suite) != schemas.VerifierSnapshot {
		return nil
	}
	sn, ok := st.env.(schemas.SnapshotEnvironment)
	if !ok {
		return wrapPart("environment", st.env, fmt.Errorf("suite %s grades snapshots, but environment %s cannot take one", st.rc.Suite.ID, st.env.GetName()))
	}
	ref, err := sn.Snapshot(ctx, st.rc)
	if err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("snapshot: %w", err))
	}
	st.ev.Snapshot = ref
	return nil
}

// exportScenario reads what the verifier needs beyond the diff: the services'
// state (which also copies the tool ledger) whenever the run had services,
// the answer file of an answer task and the artifact files of an artifact
// task. The answer is finished, with the final-message fallback, after
// collect (finishAnswer). An artifact export that breaks does not fail the
// stage (exportArtifacts).
func (p *Pipeline) exportScenario(ctx context.Context, st *runState) error {
	rc := st.rc
	var errs []error
	if len(utils.TaskServices(rc.Suite, rc.Task)) > 0 || len(utils.ExternalAPIs(rc.Suite, rc.Task)) > 0 || len(utils.TeamOf(rc)) > 0 { // External APIs area: copies the ledger and recorded cassettes; Multi-agent area: the mailbox
		if se, ok := st.env.(schemas.ServiceEnvironment); !ok {
			errs = append(errs, wrapPart("environment", st.env, errors.New("export state: environment cannot run services")))
		} else if files, err := se.ExportState(ctx, rc); err != nil {
			st.ev.State = files
			errs = append(errs, wrapPart("environment", st.env, fmt.Errorf("export state: %w", err)))
		} else {
			st.ev.State = files
		}
	}
	if utils.OutputKind(rc.Suite, rc.Task) == schemas.OutputAnswer {
		if ae, ok := st.env.(schemas.AnswerEnvironment); !ok {
			errs = append(errs, wrapPart("environment", st.env, errors.New("export answer: environment cannot read answers")))
		} else if a, err := ae.ExportAnswer(ctx, rc); err != nil {
			errs = append(errs, wrapPart("environment", st.env, fmt.Errorf("export answer: %w", err)))
		} else {
			st.ev.Answer = a
		}
	}
	if utils.OutputKind(rc.Suite, rc.Task) == schemas.OutputArtifact {
		p.exportArtifacts(ctx, st)
	}
	return errors.Join(errs...)
}

// exportArtifacts copies an artifact task's files into the run folder
// through the environment's ArtifactEnvironment and records them in the
// evidence. When the environment cannot export artifacts, or the export
// itself breaks, the reason is kept in st.artifactErr instead of failing the
// stage: the agent's work is done and the rest of its evidence is still
// collected, and the grade becomes invalid_evaluator with that reason
// (scenarioGradeProblem). The verifier's input is the bench's to produce,
// so its loss is never the agent's fail. A file the agent got wrong is not
// such a break: it is an entry with Error set, judged at grade time.
func (p *Pipeline) exportArtifacts(ctx context.Context, st *runState) {
	ae, ok := st.env.(schemas.ArtifactEnvironment)
	if !ok {
		st.artifactErr = fmt.Sprintf("environment %s cannot export artifacts", st.env.GetName())
		return
	}
	files, err := ae.ExportArtifacts(ctx, st.rc)
	if err != nil {
		st.artifactErr = "export failed: " + err.Error()
		p.logger.Warn("artifact export failed; the run's grade is invalid_evaluator", "run", st.rc.Run.RunID, "err", err)
		return
	}
	st.ev.Artifacts = files
}

// finishAnswer settles an answer task's answer after the evidence is
// collected: the exported file when the agent wrote one, else the agent's
// final message, else an empty answer recorded as missing. The text is
// written to answer.txt, where graders and the verifier read it and the seal
// covers it.
func (p *Pipeline) finishAnswer(ctx context.Context, st *runState) error {
	rc := st.rc
	if rc.Task == nil || utils.OutputKind(rc.Suite, rc.Task) != schemas.OutputAnswer {
		return nil
	}
	a := st.ev.Answer
	if a == nil || a.Source == AnswerMissing || a.Source == "" {
		switch c := st.ev.Claim; {
		case c != nil && strings.TrimSpace(c.Text) != "":
			a = &schemas.Answer{Text: c.Text, Source: AnswerFromFinalMessage}
		default:
			a = &schemas.Answer{Source: AnswerMissing}
		}
	} else {
		cp := *a
		a = &cp
	}
	a.SHA256 = utils.SHA256Bytes([]byte(a.Text))
	st.ev.Answer = a
	return p.writeFile(ctx, st, FileAnswer, []byte(a.Text))
}

// scenarioGradeProblem says why a scenario run cannot be graded although
// the agent finished: the state of a state task, the answer of an answer
// task or the artifact files of an artifact task (artifactErr, set by
// exportArtifacts) were not exported. Such a run is invalid_evaluator with
// that reason, never a fail: the missing input is the bench's, not the
// agent's.
func scenarioGradeProblem(rc *schemas.RunContext, ev *schemas.Evidence, artifactErr string) string {
	switch utils.OutputKind(rc.Suite, rc.Task) {
	case schemas.OutputState:
		if len(ev.State) == 0 {
			return "state output: no service state was exported"
		}
		var bad []string
		for _, f := range ev.State {
			if f.Error != "" {
				bad = append(bad, f.Service+"/"+f.ID+": "+f.Error)
			}
		}
		if len(bad) > 0 {
			return "state output: export failed: " + strings.Join(bad, "; ")
		}
	case schemas.OutputAnswer:
		if ev.Answer == nil {
			return "answer output: no answer was exported"
		}
	case schemas.OutputArtifact:
		if artifactErr != "" {
			return "artifact output: " + artifactErr
		}
	}
	return ""
}

// missingArtifactsReason is the fail reason of an artifact task whose
// required artifacts have no usable file, empty when every one has. The
// agent was asked to leave those files, so their absence is the agent's
// fail, graded without running the verifier, the same way an answer task
// with no answer is graded on an empty answer. It is pure.
func missingArtifactsReason(rc *schemas.RunContext, ev *schemas.Evidence) string {
	missing := utils.MissingArtifacts(utils.OutputArtifacts(rc.Suite, rc.Task), ev.Artifacts)
	if len(missing) == 0 {
		return ""
	}
	return "artifact output: required artifact missing: " + strings.Join(missing, "; ")
}

// gradeMode is Grade.Mode for a run that is not a clean-room diff grade.
func gradeMode(rc *schemas.RunContext) string {
	if utils.VerifierMode(rc.Suite) == schemas.VerifierSnapshot {
		return schemas.VerifierSnapshot
	}
	if k := utils.OutputKind(rc.Suite, rc.Task); k != schemas.OutputDiff {
		return k
	}
	return ""
}

// sessionsEnded is the outcome of a multi-session run that a session ended
// before the last one: timeout when it timed out, crash when it exited
// non-zero. It is empty when every session ran, or the task has one.
func sessionsEnded(sessions []schemas.SessionResult, count int) schemas.Outcome {
	if count <= 1 || len(sessions) == 0 {
		return ""
	}
	last := sessions[len(sessions)-1]
	switch {
	case last.Index >= count:
		return ""
	case last.TimedOut:
		return schemas.OutcomeTimeout
	case last.ExitCode != 0:
		return schemas.OutcomeCrash
	default:
		return ""
	}
}

// writeFile writes raw bytes into the run folder through the store.
func (p *Pipeline) writeFile(ctx context.Context, st *runState, name string, b []byte) error {
	if err := p.reg.Store.WriteRunFile(ctx, st.rc.Run, name, b); err != nil {
		return wrapPart("store", p.reg.Store, fmt.Errorf("write %s: %w", name, err))
	}
	return nil
}
