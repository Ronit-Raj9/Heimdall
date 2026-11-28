package adapters

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// AdapterScripted is the name of the scripted adapter.
const AdapterScripted = "scripted"

// Scripted control agent kinds (agents/<kind>.yaml, `scripted.kind`).
const (
	ScriptedOracle  = "oracle"  // applies the task's reference patch, runs its reference script or writes its reference answer
	ScriptedNoop    = "noop"    // changes nothing, exits 0
	ScriptedRefuse  = "refuse"  // declines in words, exits 0
	ScriptedExploit = "exploit" // runs the task's exploit control
	ScriptedToy     = "toy"     // three model calls through the gateway, then the reference patch
	// Multi-agent controls (docs/multi-agent.md); deterministic, no model calls.
	ScriptedTeam      = "team"      // every team member runs teamcontrol.py: the lead sends tasks through the mailbox, workers write files and reply
	ScriptedSubagents = "subagents" // one container reporting a planner, two parallel workers, a handoff and a reviewer as native events
	ScriptedMCPLoop   = "mcploop"   // B2 Bifrost features: a real tool loop over the run's MCP servers, driven by a scripted model upstream (mcploopagent.py)
	// Readiness controls of the hosted check (cmd/bifrost-bench
	// jobs_hostedreadiness.go): each runs one of the task's own controls in
	// the agent's place, as local admission does (suite/admitscenario.go).
	ScriptedEmpty       = "empty"        // the task's empty control (controls.empty)
	ScriptedMutant      = "mutant"       // mutant number scripted.index (controls.mutants)
	ScriptedWrongAnswer = "wrong_answer" // wrong answer number scripted.index of the hidden answer spec
	ScriptedVariant     = "variant"      // variant number scripted.index (controls.variants, utils.TaskVariantChecks)
)

// ScriptedNoControl is what a mutant or wrong_answer control prints when the
// task has no control with its index (the hosted check runs every index on
// every task; the extra runs are not graded as checks).
const ScriptedNoControl = "no such control on this task"

// RefusalText is what the refuse control prints.
const RefusalText = "I can't complete this task."

const (
	scriptedVersion = "1.0.0"

	referencePatchFile  = "/tmp/bench/reference.diff"
	referenceScriptFile = "/tmp/bench/reference.sh"
	referenceAnswerFile = "/tmp/bench/reference.answer"
	exploitFile         = "/tmp/bench/exploit.sh"
	toyAgentFile        = "/tmp/bench/toyagent.py"
	teamControlFile     = "/tmp/bench/teamcontrol.py"
	subagentsFile       = "/tmp/bench/subagentscontrol.py"
	mcpLoopFile         = "/tmp/bench/mcploopagent.py" // B2
	controlFile         = "/tmp/bench/control"         // readiness controls: the control's patch, script or answer
)

// writeAnswer copies the reference answer ($2) to the answer path ($1),
// creating its folder. The paths are arguments, never part of the script.
const writeAnswer = `mkdir -p "$(dirname "$1")" && cp "$2" "$1"`

// gitApply works whether or not /workspace is a git repository and whoever
// owns it; safe.directory is honoured from the command line. No shell, so the
// "*" is never globbed.
var gitApply = []string{"git", "-c", "safe.directory=*", "apply", "--whitespace=nowarn", referencePatchFile}

//go:embed toyagent.py
var toyAgentSource string

//go:embed teamcontrol.py
var teamControlSource string

//go:embed subagentscontrol.py
var subagentsSource string

//go:embed mcploopagent.py
var mcpLoopSource string // B2 Bifrost features

type scriptedAdapter struct {
	cfg CommandConfig
}

// newScriptedAdapter takes the command adapter's config: only the toy kind
// uses it, to reach the gateway like a real agent.
func newScriptedAdapter(cfg CommandConfig) *scriptedAdapter {
	return &scriptedAdapter{cfg: cfg}
}

// GetName implements schemas.Part.
func (a *scriptedAdapter) GetName() string { return AdapterScripted }

// GetVersion implements schemas.Part.
func (a *scriptedAdapter) GetVersion() string { return scriptedVersion }

// Metadata implements schemas.AdapterMetadata.
func (a *scriptedAdapter) Metadata() schemas.AdapterMetadataInfo {
	return schemas.AdapterMetadataInfo{AdapterVersion: scriptedVersion}
}

// Validate implements schemas.AdapterValidator. Plan remains the legacy entry
// point and performs the same checks for old callers.
func (a *scriptedAdapter) Validate(ctx context.Context, rc *schemas.RunContext) error {
	if err := checkRunContext(rc); err != nil {
		return err
	}
	if rc.Agent.Scripted == nil {
		return fmt.Errorf("plan run %s: agent %s has no scripted block", rc.Run.RunID, rc.Agent.ID)
	}
	return nil
}

// BuildExecutionPlan implements schemas.AdapterRunner.
func (a *scriptedAdapter) BuildExecutionPlan(ctx context.Context, rc *schemas.RunContext) (*schemas.ExecutionPlan, error) {
	return a.Plan(ctx, rc)
}

// Cleanup implements schemas.AdapterCleanup. Control files are owned by the
// environment and are removed with the run container.
func (a *scriptedAdapter) Cleanup(context.Context, *schemas.RunContext) error { return nil }

// Plan returns the fixed plan of one control agent.
//
// Every control except toy runs with no network at all: it needs none, and a
// control that could reach the gateway would blur what admission measured.
// Task files (patch, script, answer, exploit) are delivered through Files,
// never mounted. A task with services still reaches them: the environment
// puts a "none" container on the run's service network only.
//
// The oracle follows the task's control kind (utils.ControlKind): it applies
// a reference patch, runs a reference script (.sh) in the agent's place with
// the services up and the run's environment variables set, or writes the
// reference answer of an answer task to the answer path.
func (a *scriptedAdapter) Plan(ctx context.Context, rc *schemas.RunContext) (*schemas.RunPlan, error) {
	if err := checkRunContext(rc); err != nil {
		return nil, err
	}
	if rc.Agent.Scripted == nil {
		return nil, fmt.Errorf("plan run %s: agent %s has no scripted block", rc.Run.RunID, rc.Agent.ID)
	}
	kind := rc.Agent.Scripted.Kind
	network := "none"
	files := map[string]string{}
	env := map[string]string{}
	var argv []string

	switch kind {
	case ScriptedNoop:
		argv = []string{"true"}
	case ScriptedRefuse:
		argv = []string{"echo", RefusalText}
	case ScriptedOracle:
		ref, err := readTaskFile(rc.Task, rc.Task.Controls.Reference)
		if err != nil {
			return nil, fmt.Errorf("plan run %s: oracle: %w", rc.Run.RunID, err)
		}
		switch utils.ControlKind(rc.Suite, rc.Task) {
		case utils.ControlScript:
			files[referenceScriptFile] = ref
			argv = []string{"sh", referenceScriptFile}
		case utils.ControlAnswer:
			files[referenceAnswerFile] = ref
			argv = []string{"sh", "-c", writeAnswer, "write-answer", utils.AnswerContainerPath(rc.Suite, rc.Task), referenceAnswerFile}
		default:
			files[referencePatchFile] = ref
			argv = append([]string(nil), gitApply...)
		}
	case ScriptedExploit:
		script, err := readTaskFile(rc.Task, rc.Task.Controls.Exploit)
		if err != nil {
			return nil, fmt.Errorf("plan run %s: exploit: %w", rc.Run.RunID, err)
		}
		files[exploitFile] = script
		argv = []string{"sh", exploitFile}
	case ScriptedToy:
		var err error
		network, err = a.toyPlan(rc, files, env)
		if err != nil {
			return nil, fmt.Errorf("plan run %s: toy: %w", rc.Run.RunID, err)
		}
		argv = []string{"python3", "-I", toyAgentFile}
	case ScriptedTeam: // Multi-agent area: the run kernel adds the team variables (engine/team.go)
		files[teamControlFile] = teamControlSource
		env["BENCH_WORKSPACE"] = Workspace
		argv = []string{"python3", "-I", teamControlFile}
	case ScriptedSubagents:
		files[subagentsFile] = subagentsSource
		env["BENCH_WORKSPACE"] = Workspace
		argv = []string{"python3", "-I", subagentsFile}
	case ScriptedMCPLoop: // B2 Bifrost features (mcploopagent.py)
		var err error
		network, err = a.mcpLoopPlan(rc, files, env)
		if err != nil {
			return nil, fmt.Errorf("plan run %s: mcploop: %w", rc.Run.RunID, err)
		}
		argv = []string{"python3", "-I", mcpLoopFile}
	case ScriptedEmpty, ScriptedMutant, ScriptedWrongAnswer, ScriptedVariant:
		var err error
		argv, err = controlPlan(rc, kind, rc.Agent.Scripted.Index, files)
		if err != nil {
			return nil, fmt.Errorf("plan run %s: %s: %w", rc.Run.RunID, kind, err)
		}
	default:
		return nil, fmt.Errorf("plan run %s: unknown scripted kind %q", rc.Run.RunID, kind)
	}

	plan, err := basePlan(rc, network)
	if err != nil {
		return nil, err
	}
	plan.Argv = argv
	plan.Env = env
	plan.Files = files
	if kind == ScriptedMCPLoop { // B2: the MCP servers (direct or through Bifrost) and the gateway feature headers
		if err := addMCP(plan, rc); err != nil {
			return nil, fmt.Errorf("plan run %s: mcploop: %w", rc.Run.RunID, err)
		}
		addBifrostEnv(plan, rc)
	}
	return plan, nil
}

// toyPlan fills files and env for the toy agent and returns its network. The
// key reaches the script only through env, like a real agent's credential.
func (a *scriptedAdapter) toyPlan(rc *schemas.RunContext, files, env map[string]string) (string, error) {
	if rc.Key == nil || rc.Key.Value == "" {
		return "", fmt.Errorf("no run key")
	}
	if a.cfg.GatewayAgentURL == "" {
		return "", fmt.Errorf("gateway agent url not configured")
	}
	network := a.cfg.EvalNetwork
	if network == "" && rc.Handle != nil {
		network = rc.Handle.Network
	}
	if network == "" {
		return "", fmt.Errorf("no evaluation network")
	}
	instruction, err := readTaskFile(rc.Task, rc.Task.Instruction)
	if err != nil {
		return "", err
	}
	patch, err := readTaskFile(rc.Task, rc.Task.Controls.Reference)
	if err != nil {
		return "", err
	}
	vars := templateVars(rc, a.cfg, instruction)
	files[toyAgentFile] = toyAgentSource
	files[InstructionFile] = instruction
	files[referencePatchFile] = patch
	env["BENCH_GATEWAY_URL"] = vars["gateway.url"]
	env["BENCH_RUN_KEY"] = vars["run.key"]
	env["BENCH_MODEL"] = vars["model"]
	env["BENCH_SESSION"] = vars["run.session"]
	env["BENCH_INSTRUCTION"] = InstructionFile
	env["BENCH_PATCH"] = referencePatchFile
	env["BENCH_WORKSPACE"] = Workspace
	return network, nil
}

// mcpLoopPlan fills files and env for the MCP loop agent (B2) and returns its
// network: the toy's gateway wiring plus a step cap.
func (a *scriptedAdapter) mcpLoopPlan(rc *schemas.RunContext, files, env map[string]string) (string, error) {
	if rc.Key == nil || rc.Key.Value == "" {
		return "", fmt.Errorf("no run key")
	}
	if a.cfg.GatewayAgentURL == "" {
		return "", fmt.Errorf("gateway agent url not configured")
	}
	network := a.cfg.EvalNetwork
	if network == "" && rc.Handle != nil {
		network = rc.Handle.Network
	}
	if network == "" {
		return "", fmt.Errorf("no evaluation network")
	}
	instruction, err := readTaskFile(rc.Task, rc.Task.Instruction)
	if err != nil {
		return "", err
	}
	vars := templateVars(rc, a.cfg, instruction)
	files[mcpLoopFile] = mcpLoopSource
	files[InstructionFile] = instruction
	env["BENCH_GATEWAY_URL"] = vars["gateway.url"]
	env["BENCH_RUN_KEY"] = vars["run.key"]
	env["BENCH_MODEL"] = vars["model"]
	env["BENCH_SESSION"] = vars["run.session"]
	env["BENCH_INSTRUCTION"] = InstructionFile
	env["BENCH_MAX_STEPS"] = "12"
	if rc.Agent.Budgets.Turns > 0 {
		env["BENCH_MAX_STEPS"] = fmt.Sprint(rc.Agent.Budgets.Turns)
	}
	return network, nil
}
