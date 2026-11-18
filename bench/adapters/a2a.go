package adapters

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// B1: agents that speak A2A, reached through Bifrost's agent gateway
// (docs/a2a-agents.md).

// AdapterA2A is the name of the a2a adapter.
const AdapterA2A = "a2a"

const (
	a2aVersion      = "1.0.0"
	a2aLauncherFile = "/tmp/bench/a2a.py"
)

//go:embed a2a.py
var a2aLauncherSource string

// a2aAdapter runs an agent that is a deployed A2A server registered in this
// bench's Bifrost. The container it plans is the bench's own launcher
// (a2a.py) on the evaluation network: it reaches only Bifrost, sends the
// task as an A2A message to the agent's gateway endpoint and writes the
// result. The registration itself is made by the engine before the run
// (engine/a2a.go) and handed over in rc.A2A, so this adapter never calls
// the gateway's management API.
type a2aAdapter struct {
	cfg CommandConfig
}

func newA2AAdapter(cfg CommandConfig) *a2aAdapter { return &a2aAdapter{cfg: cfg} }

// GetName implements schemas.Part.
func (a *a2aAdapter) GetName() string { return AdapterA2A }

// GetVersion implements schemas.Part.
func (a *a2aAdapter) GetVersion() string { return a2aVersion }

// Metadata implements schemas.AdapterMetadata.
func (a *a2aAdapter) Metadata() schemas.AdapterMetadataInfo {
	return schemas.AdapterMetadataInfo{AdapterVersion: a2aVersion}
}

// Validate implements schemas.AdapterValidator.
func (a *a2aAdapter) Validate(_ context.Context, rc *schemas.RunContext) error {
	if err := checkRunContext(rc); err != nil {
		return err
	}
	if rc.Agent.A2A == nil {
		return fmt.Errorf("plan run %s: agent %s has no a2a block", rc.Run.RunID, rc.Agent.ID)
	}
	return utils.CheckA2AAgent(rc.Agent.A2A)
}

// BuildExecutionPlan implements schemas.AdapterRunner.
func (a *a2aAdapter) BuildExecutionPlan(ctx context.Context, rc *schemas.RunContext) (*schemas.ExecutionPlan, error) {
	return a.Plan(ctx, rc)
}

// Cleanup implements schemas.AdapterCleanup. The launcher's files die with
// the run container; the registrations are the engine's to remove.
func (a *a2aAdapter) Cleanup(context.Context, *schemas.RunContext) error { return nil }

// Plan returns the launcher's plan. The run's key reaches the container
// only through env (BENCH_A2A_KEY, redacted in plan.json). The plan asks for
// no egress: the gateway is on the evaluation network and Bifrost, not the
// container, connects to the agent.
func (a *a2aAdapter) Plan(_ context.Context, rc *schemas.RunContext) (*schemas.RunPlan, error) {
	if err := a.Validate(context.Background(), rc); err != nil {
		return nil, err
	}
	spec := rc.Agent.A2A
	kind := utils.OutputKind(rc.Suite, rc.Task)
	if ok, why := utils.A2AOutputAllowed(spec, kind); !ok {
		return nil, fmt.Errorf("plan run %s: adapter a2a cannot run task %s (output %s): %s", rc.Run.RunID, rc.Task.ID, kind, why)
	}
	if kind == schemas.OutputState && rc.RemoteTools == nil {
		return nil, fmt.Errorf("plan run %s: task %s is a state task and a2a.tools is expose, but no tool proxy was published to the agent (the task has no proxied service or external API)", rc.Run.RunID, rc.Task.ID)
	}
	if rc.A2A == nil || rc.A2A.Entry == "" {
		return nil, errors.New("plan: the run has no agent gateway registration (the engine registers the agent before the run)")
	}
	if a.cfg.GatewayAgentURL == "" {
		return nil, errors.New("plan: no gateway agent URL (gateway.agent_url)")
	}
	if rc.Key == nil || rc.Key.Value == "" {
		return nil, errors.New("plan: an a2a run needs the run key to call the agent gateway")
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
	env, err := a2aEnv(rc, spec, strings.TrimRight(a.cfg.GatewayAgentURL, "/"), plan.WallS, kind)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	plan.Argv = []string{"python3", "-I", a2aLauncherFile}
	plan.Env = env
	plan.Files = map[string]string{a2aLauncherFile: a2aLauncherSource, InstructionFile: instruction}
	return plan, nil
}

// a2aEnv is the launcher's environment.
func a2aEnv(rc *schemas.RunContext, spec *schemas.A2AAgent, gateway string, wallS int, kind string) (map[string]string, error) {
	timeout := spec.TimeoutS
	if limit := wallS - remoteTimeoutMargin; timeout <= 0 || (limit > 0 && timeout > limit) {
		timeout = max(limit, 1)
	}
	env := map[string]string{
		"BENCH_A2A_ENDPOINT":    A2AEndpoint(gateway, rc.A2A.Entry),
		"BENCH_A2A_KEY":         rc.Key.Value,
		"BENCH_A2A_TIMEOUT_S":   strconv.Itoa(timeout),
		"BENCH_A2A_INSTRUCTION": InstructionFile,
		"BENCH_A2A_OUTPUT":      kind,
		"BENCH_A2A_FILES_DIR":   Workspace,
		utils.EnvAnswerPath:     utils.AnswerContainerPath(rc.Suite, rc.Task),
	}
	if spec.Streaming {
		env["BENCH_A2A_STREAMING"] = "1"
	}
	if spec.Artifacts == schemas.RemoteArtifactsInline {
		env["BENCH_A2A_ARTIFACTS"] = schemas.RemoteArtifactsInline
	}
	team := map[string]string{}
	for reg, member := range rc.A2A.Members {
		if member != utils.A2ALead {
			team[member] = A2AEndpoint(gateway, reg)
		}
	}
	if len(team) > 0 {
		raw, err := json.Marshal(team)
		if err != nil {
			return nil, fmt.Errorf("encode a2a team: %w", err)
		}
		env["BENCH_A2A_TEAM"] = string(raw)
	}
	if utils.RemoteToolsExposed(rc.Agent) && rc.RemoteTools != nil {
		if urls := utils.RemoteToolURLs(rc.RemoteTools); len(urls) > 0 {
			raw, err := json.Marshal(urls)
			if err != nil {
				return nil, fmt.Errorf("encode remote tools: %w", err)
			}
			env["BENCH_REMOTE_TOOLS"] = string(raw)
			env["BENCH_REMOTE_TOOLS_TOKEN"] = rc.RemoteTools.Token
		}
	}
	if f := utils.A2AFaultOf(rc); f != nil {
		raw, err := json.Marshal(f)
		if err != nil {
			return nil, fmt.Errorf("encode a2a fault: %w", err)
		}
		env["BENCH_A2A_FAULT"] = string(raw)
	}
	return env, nil
}

// A2AEndpoint is a registration's JSON-RPC endpoint on the gateway, as
// Bifrost serves it (/agents/a2a/<name>/json-rpc).
func A2AEndpoint(gateway, name string) string {
	return gateway + "/agents/a2a/" + url.PathEscape(name) + "/json-rpc"
}
