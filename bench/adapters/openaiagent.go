package adapters

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// A08: agents served as an OpenAI-compatible endpoint (docs/openai-agents.md).

// AdapterOpenAIAgent is the name of the openai_agent adapter.
const AdapterOpenAIAgent = utils.AdapterOpenAIAgent

const (
	openaiAgentVersion = "1.0.0"
	openaiLauncherFile = "/tmp/bench/openaiagent.py"
	openaiSystemFile   = "/tmp/bench/openai-system.md"
	openaiDefaultTurns = 20
	openaiHistoryFile  = utils.DefaultMemoryDir + "/.openai-agent-history.json"
	openaiEnvAPIKey    = "BENCH_OPENAI_API_KEY"
)

//go:embed openaiagent.py
var openaiLauncherSource string

// openaiAgentAdapter runs an agent that is an OpenAI-compatible endpoint
// (chat/completions or Responses). The container it plans is the bench's own
// launcher (openaiagent.py): it sends the task as a user message, offers the
// task's MCP tools as function tools, runs every tool call through the run's
// tool proxy (so each call is recorded like any agent's), loops until the
// endpoint answers without a tool call and writes that text as the answer.
type openaiAgentAdapter struct {
	cfg CommandConfig
}

func newOpenAIAgentAdapter(cfg CommandConfig) *openaiAgentAdapter {
	return &openaiAgentAdapter{cfg: cfg}
}

// GetName implements schemas.Part.
func (a *openaiAgentAdapter) GetName() string { return AdapterOpenAIAgent }

// GetVersion implements schemas.Part.
func (a *openaiAgentAdapter) GetVersion() string { return openaiAgentVersion }

// Metadata implements schemas.AdapterMetadata.
func (a *openaiAgentAdapter) Metadata() schemas.AdapterMetadataInfo {
	return schemas.AdapterMetadataInfo{AdapterVersion: openaiAgentVersion}
}

// Validate implements schemas.AdapterValidator.
func (a *openaiAgentAdapter) Validate(_ context.Context, rc *schemas.RunContext) error {
	if err := checkRunContext(rc); err != nil {
		return err
	}
	if rc.Agent.OpenAI == nil {
		return fmt.Errorf("plan run %s: agent %s has no openai block", rc.Run.RunID, rc.Agent.ID)
	}
	if err := utils.CheckOpenAIAgent(rc.Agent.OpenAI); err != nil {
		return fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	return nil
}

// BuildExecutionPlan implements schemas.AdapterRunner.
func (a *openaiAgentAdapter) BuildExecutionPlan(ctx context.Context, rc *schemas.RunContext) (*schemas.ExecutionPlan, error) {
	return a.Plan(ctx, rc)
}

// Cleanup implements schemas.AdapterCleanup. The launcher's files die with
// the run container.
func (a *openaiAgentAdapter) Cleanup(context.Context, *schemas.RunContext) error { return nil }

// Plan returns the launcher's plan. The API key is never in argv or files:
// BENCH_OPENAI_API_KEY holds a {{secret.NAME}} reference the engine expands
// from the org's secrets (redacted in plan.json). The plan asks for egress to
// exactly the endpoint's host and port, granted through a per-run forwarder.
func (a *openaiAgentAdapter) Plan(ctx context.Context, rc *schemas.RunContext) (*schemas.RunPlan, error) {
	if err := a.Validate(ctx, rc); err != nil {
		return nil, err
	}
	spec := rc.Agent.OpenAI
	kind := utils.OutputKind(rc.Suite, rc.Task)
	if ok, why := utils.OpenAIOutputAllowed(spec, kind); !ok {
		return nil, fmt.Errorf("plan run %s: adapter openai_agent cannot run task %s (output %s): %s", rc.Run.RunID, rc.Task.ID, kind, why)
	}
	servers := utils.MCPServers(rc.Services)
	tools := spec.Tools != schemas.OpenAIToolsOff
	if kind == schemas.OutputState && len(servers) == 0 {
		return nil, fmt.Errorf("plan run %s: task %s is a state task, but the run has no MCP tools to offer the endpoint", rc.Run.RunID, rc.Task.ID)
	}
	if rc.NoPaidTraffic {
		return nil, fmt.Errorf("plan run %s: a smoke, scripted_upstream or agent check run cannot hold an OpenAI-compatible endpoint's own model calls to the scripted model", rc.Run.RunID)
	}
	endpoint, err := utils.OpenAIEndpoint(spec)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	target, err := utils.RemoteEgressTarget(endpoint)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
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
	plan.Env = openaiEnv(rc, spec, endpoint, plan.WallS, kind)
	plan.Files = map[string]string{openaiLauncherFile: openaiLauncherSource, InstructionFile: instruction}
	if spec.System != "" {
		plan.Files[openaiSystemFile] = spec.System
		plan.Env["BENCH_OPENAI_SYSTEM"] = openaiSystemFile
	}
	if tools && len(servers) > 0 {
		js, err := mcpServersJSON(rc)
		if err != nil {
			return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
		}
		plan.Env[utils.EnvMCPServers] = js
	}
	plan.Argv = []string{"python3", "-I", openaiLauncherFile}
	plan.Egress = []string{target}
	return plan, nil
}

// openaiEnv is the launcher's environment.
func openaiEnv(rc *schemas.RunContext, spec *schemas.OpenAIAgent, endpoint string, wallS int, kind string) map[string]string {
	timeout := spec.TimeoutS
	if limit := wallS - remoteTimeoutMargin; timeout <= 0 || (limit > 0 && timeout > limit) {
		timeout = max(limit, 1)
	}
	turns := spec.MaxTurns
	if turns <= 0 {
		turns = rc.Agent.Budgets.Turns
	}
	if turns <= 0 {
		turns = openaiDefaultTurns
	}
	model := spec.Model
	if model == "" {
		model = rc.Agent.Model
	}
	api := spec.API
	if api == "" {
		api = schemas.OpenAIAPIChat
	}
	sessionID, sessionIndex := rc.Run.RunID, 1
	if rc.Session != nil {
		sessionID, sessionIndex = rc.Session.ID, rc.SessionIndex+1
	}
	env := map[string]string{
		"BENCH_OPENAI_URL":           endpoint,
		"BENCH_OPENAI_API":           api,
		"BENCH_OPENAI_MODEL":         model,
		"BENCH_OPENAI_TIMEOUT_S":     strconv.Itoa(timeout),
		"BENCH_OPENAI_MAX_TURNS":     strconv.Itoa(turns),
		"BENCH_OPENAI_INSTRUCTION":   InstructionFile,
		"BENCH_OPENAI_OUTPUT":        kind,
		"BENCH_OPENAI_SESSION_ID":    sessionID,
		"BENCH_OPENAI_SESSION_INDEX": strconv.Itoa(sessionIndex),
		utils.EnvRemoteConnect:       utils.RemoteEgressAddr(rc.Run.RunID),
		utils.EnvAnswerPath:          utils.AnswerContainerPath(rc.Suite, rc.Task),
	}
	if spec.APIKeySecret != "" {
		env[openaiEnvAPIKey] = "{{secret." + spec.APIKeySecret + "}}"
	}
	if len(rc.Task.Sessions) > 0 {
		// Multi-session task: the conversation so far is kept in the run's
		// memory volume, so session N is sent with sessions 1..N-1 as
		// earlier turns, the way a chat agent remembers.
		env["BENCH_OPENAI_HISTORY"] = openaiHistoryFile
	}
	return env
}
