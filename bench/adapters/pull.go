package adapters

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Lane Y11 (audit G1 P0-9): pull-mode agents (docs/pull-agents.md).

// AdapterPull is the name of the pull adapter.
const AdapterPull = utils.AdapterPull

const (
	pullVersion           = "1.0.0"
	pullLauncherFile      = "/tmp/bench/pullagent.py"
	pullDefaultTurnS      = 300
	pullDefaultToolRounds = 20
)

//go:embed pullagent.py
var pullLauncherSource string

// pullAdapter runs an agent that lives in the customer's own process and
// connects out to Heimdall (heimdall.serve). The container it plans is the
// bench's launcher (pullagent.py): it queues each turn on this server's
// pull endpoint for the agent's name, waits for the process's answer, runs
// every tool call the answer asks for through the run's tool proxy (so the
// proxy records it like any agent's) and loops until the agent answers
// without one. Its only route out is the pull endpoint. With
// pull.conversation driver bench it drives chat tasks turn by turn; with
// pull.repo tarball it runs coding tasks (repo in, patch out).
type pullAdapter struct {
	cfg CommandConfig
}

func newPullAdapter(cfg CommandConfig) *pullAdapter { return &pullAdapter{cfg: cfg} }

// GetName implements schemas.Part.
func (a *pullAdapter) GetName() string { return AdapterPull }

// GetVersion implements schemas.Part.
func (a *pullAdapter) GetVersion() string { return pullVersion }

// Metadata implements schemas.AdapterMetadata.
func (a *pullAdapter) Metadata() schemas.AdapterMetadataInfo {
	return schemas.AdapterMetadataInfo{AdapterVersion: pullVersion}
}

// Validate implements schemas.AdapterValidator.
func (a *pullAdapter) Validate(_ context.Context, rc *schemas.RunContext) error {
	if err := checkRunContext(rc); err != nil {
		return err
	}
	if err := utils.CheckPullAgent(rc.Agent); err != nil {
		return fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	return nil
}

// BuildExecutionPlan implements schemas.AdapterRunner.
func (a *pullAdapter) BuildExecutionPlan(ctx context.Context, rc *schemas.RunContext) (*schemas.ExecutionPlan, error) {
	return a.Plan(ctx, rc)
}

// Cleanup implements schemas.AdapterCleanup. The launcher's files die with
// the run container; turns left on the server expire with their deadline.
func (a *pullAdapter) Cleanup(context.Context, *schemas.RunContext) error { return nil }

// Plan returns the launcher's plan. The run's pull token reaches the
// container only through env (BENCH_PULL_TOKEN, redacted in plan.json).
func (a *pullAdapter) Plan(ctx context.Context, rc *schemas.RunContext) (*schemas.RunPlan, error) {
	if err := a.Validate(ctx, rc); err != nil {
		return nil, err
	}
	spec := rc.Agent.Pull
	kind := utils.OutputKind(rc.Suite, rc.Task)
	if ok, why := utils.PullOutputAllowed(spec, kind); !ok {
		return nil, fmt.Errorf("plan run %s: adapter pull cannot run task %s (output %s): %s", rc.Run.RunID, rc.Task.ID, kind, why)
	}
	servers := utils.MCPServers(rc.Services)
	if kind == schemas.OutputState && len(servers) == 0 {
		return nil, fmt.Errorf("plan run %s: task %s is a state task, but the run has no MCP tools to offer the agent", rc.Run.RunID, rc.Task.ID)
	}
	if rc.NoPaidTraffic {
		return nil, fmt.Errorf("plan run %s: a smoke, scripted_upstream or agent check run cannot hold a pull agent's own model calls to the scripted model", rc.Run.RunID)
	}
	instruction, err := readTaskFile(rc.Task, rc.Task.Instruction)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	network := a.cfg.EvalNetwork
	if network == "" && rc.Handle != nil {
		network = rc.Handle.Network
	}
	if network == "" {
		return nil, fmt.Errorf("plan run %s: no evaluation network", rc.Run.RunID)
	}
	plan, err := basePlan(rc, network)
	if err != nil {
		return nil, err
	}
	name := utils.PullAgentName(rc.Agent)
	env := pullLauncherEnv(rc, spec, name, plan.WallS, kind)
	plan.Files = map[string]string{pullLauncherFile: pullLauncherSource, InstructionFile: instruction}
	withConvHelper(plan)
	if err := pullEnv(a.cfg, rc, plan, env, name); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	if spec.Tools != schemas.OpenAIToolsOff && len(servers) > 0 {
		js, err := mcpServersJSON(rc)
		if err != nil {
			return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
		}
		env[utils.EnvMCPServers] = js
	}
	if _, err := conversationEnv(rc, spec.Conversation, plan, env); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	if err := repoEnv(a.cfg, rc, plan, env, name); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	plan.Env = env
	plan.Argv = []string{"python3", "-I", pullLauncherFile}
	return plan, nil
}

// pullLauncherEnv is the launcher's environment without the pull endpoint.
func pullLauncherEnv(rc *schemas.RunContext, spec *schemas.PullAgent, name string, wallS int, kind string) map[string]string {
	timeout := spec.TimeoutS
	if limit := wallS - remoteTimeoutMargin; timeout <= 0 || (limit > 0 && timeout > limit) {
		timeout = max(limit, 1)
	}
	turnS := spec.TurnTimeoutS
	if turnS <= 0 {
		turnS = pullDefaultTurnS
	}
	rounds := spec.MaxToolRounds
	if rounds <= 0 {
		rounds = pullDefaultToolRounds
	}
	sessionID, sessionIndex := rc.Run.RunID, 1
	if rc.Session != nil {
		sessionID, sessionIndex = rc.Session.ID, rc.SessionIndex+1
	}
	return map[string]string{
		"BENCH_PULL_AGENT":         name,
		"BENCH_PULL_TIMEOUT_S":     strconv.Itoa(timeout),
		"BENCH_PULL_TURN_S":        strconv.Itoa(turnS),
		"BENCH_PULL_TOOL_ROUNDS":   strconv.Itoa(rounds),
		"BENCH_PULL_INSTRUCTION":   InstructionFile,
		"BENCH_PULL_OUTPUT":        kind,
		"BENCH_PULL_SESSION_ID":    sessionID,
		"BENCH_PULL_SESSION_INDEX": strconv.Itoa(sessionIndex),
		"BENCH_PULL_TASK_ID":       rc.Task.ID,
		utils.EnvAnswerPath:        utils.AnswerContainerPath(rc.Suite, rc.Task),
	}
}
