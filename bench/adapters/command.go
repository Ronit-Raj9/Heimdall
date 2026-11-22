// Package adapters turns agent configs into container run plans and reads
// agents' raw output back into normalized events (DESIGN.md 8.3). The command
// adapter runs any CLI agent from a YAML file, the scripted adapter runs the
// control agents, and the claudecode parser reads Claude Code's stream-json.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// AdapterCommand is the name of the command adapter.
const AdapterCommand = "command"

const (
	commandVersion = "1.0.0"

	// InstructionFile is where every adapter delivers the task instruction.
	InstructionFile = "/tmp/bench/instruction.md"
	// Workspace is the agent's working directory inside the container.
	Workspace = "/workspace"

	agentUser   = "1000"
	defaultPids = 512
)

// CommandConfig is what the command and scripted adapters need from the
// deployment.
type CommandConfig struct {
	GatewayAgentURL string // how agents reach Bifrost from inside the eval network, e.g. http://bifrost:8080
	EvalNetwork     string // the internal evaluation network agents attach to
	BenchRoot       string // folder that agent-config-relative paths (notes.user_file) resolve against

	// GatewayPublicURL is how a remote agent on another machine reaches
	// Bifrost (gateway.public_url); remote_http only. Empty uses GatewayAgentURL.
	GatewayPublicURL string

	// Y11 pull-mode agents (adapters/pull.go): OrgID is the organization
	// whose runs this registry plans ("" in local mode), PullURL the base
	// URL a run's launcher reaches the pull routes at (lib.Config.PullRunURL).
	OrgID   string
	PullURL string
}

type commandAdapter struct {
	cfg CommandConfig
}

// ValidateFault implements the strict opt-in adapter contract. A command is
// eligible only when its image explicitly declares the requested hook.
func (a *commandAdapter) ValidateFault(cfg *schemas.AgentConfig, rule schemas.AdapterFaultRule) error {
	if cfg == nil || cfg.AdapterCaps == nil || cfg.AdapterCaps.FaultProtocolVersion != schemas.AdapterFaultProtocol {
		return fmt.Errorf("command adapter does not declare fault protocol %s", schemas.AdapterFaultProtocol)
	}
	for _, h := range cfg.AdapterCaps.FaultHooks {
		if h.Kind == rule.Kind && h.Name == rule.Name {
			for _, m := range h.Modes {
				if m == rule.Mode {
					return nil
				}
			}
			return fmt.Errorf("hook %s/%s does not support mode %s", rule.Kind, rule.Name, rule.Mode)
		}
	}
	return fmt.Errorf("hook %s/%s is not declared by agent", rule.Kind, rule.Name)
}

// BindFaultRuntime adds scoped runtime settings after the normal plan is
// rendered. Tokens never enter argv or persisted adapter configuration.
func (a *commandAdapter) BindFaultRuntime(rc *schemas.RunContext, plan *schemas.RunPlan) error {
	if rc == nil || rc.FaultRuntime == nil {
		return fmt.Errorf("adapter fault runtime is not bound")
	}
	if plan.Env == nil {
		plan.Env = map[string]string{}
	}
	plan.Env["BENCH_FAULT_PROTOCOL"] = schemas.AdapterFaultProtocol
	plan.Env["BENCH_FAULT_URL"] = rc.FaultRuntime.URL
	plan.Env["BENCH_FAULT_TOKEN"] = rc.FaultRuntime.Token
	plan.Env["BENCH_FAULT_RUN_ID"] = rc.Run.RunID
	if rc.Session != nil {
		plan.Env["BENCH_FAULT_SESSION"] = rc.Session.ID
	} else {
		plan.Env["BENCH_FAULT_SESSION"] = "default"
	}
	if rc.Agent != nil && rc.Agent.AdapterCaps != nil {
		hooks, err := json.Marshal(rc.Agent.AdapterCaps.FaultHooks)
		if err != nil {
			return fmt.Errorf("encode fault hooks: %w", err)
		}
		plan.Env["BENCH_FAULT_HOOKS"] = string(hooks)
	}
	return nil
}

func newCommandAdapter(cfg CommandConfig) *commandAdapter {
	return &commandAdapter{cfg: cfg}
}

// GetName implements schemas.Part.
func (a *commandAdapter) GetName() string { return AdapterCommand }

// GetVersion implements schemas.Part.
func (a *commandAdapter) GetVersion() string { return commandVersion }

// Metadata implements schemas.AdapterMetadata.
func (a *commandAdapter) Metadata() schemas.AdapterMetadataInfo {
	return schemas.AdapterMetadataInfo{AdapterVersion: commandVersion}
}

// Validate implements schemas.AdapterValidator. Plan remains the legacy
// entry point and performs the same checks for old callers.
func (a *commandAdapter) Validate(ctx context.Context, rc *schemas.RunContext) error {
	if err := checkRunContext(rc); err != nil {
		return err
	}
	if rc.Key == nil || rc.Key.Value == "" {
		return fmt.Errorf("plan run %s: no run key", rc.Run.RunID)
	}
	if a.cfg.GatewayAgentURL == "" {
		return fmt.Errorf("plan run %s: gateway agent url not configured", rc.Run.RunID)
	}
	return nil
}

// BuildExecutionPlan implements schemas.AdapterRunner.
func (a *commandAdapter) BuildExecutionPlan(ctx context.Context, rc *schemas.RunContext) (*schemas.ExecutionPlan, error) {
	return a.Plan(ctx, rc)
}

// Cleanup implements schemas.AdapterCleanup. The command adapter has no
// adapter-owned files outside the environment's run folder.
func (a *commandAdapter) Cleanup(context.Context, *schemas.RunContext) error { return nil }

// Plan renders the agent's argv, env and files and resolves its budgets. A
// scenario run also gets its skills (addSkills) and its MCP servers (mcpVars,
// addMCP); a run with neither gets exactly the plan it always did.
//
// The run key is a secret: it may appear only in the rendered env and files,
// which the Environment hands to the container and redacts on disk. A config
// that would put it in argv (visible in `ps` and in the written plan) is
// rejected. The instruction and the user notes file are delivered as literal
// file content and are never rendered, so braces in them are harmless.
func (a *commandAdapter) Plan(ctx context.Context, rc *schemas.RunContext) (*schemas.RunPlan, error) {
	if err := checkRunContext(rc); err != nil {
		return nil, err
	}
	if rc.Key == nil || rc.Key.Value == "" {
		return nil, fmt.Errorf("plan run %s: no run key", rc.Run.RunID)
	}
	if a.cfg.GatewayAgentURL == "" {
		return nil, fmt.Errorf("plan run %s: gateway agent url not configured", rc.Run.RunID)
	}
	instruction, err := readTaskFile(rc.Task, rc.Task.Instruction)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	vars := templateVars(rc, a.cfg, instruction)
	if err := mcpVars(vars, rc); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	if err := externalVars(vars, rc); err != nil { // External APIs area: {{external.<id>.url}}, {{external.apis_json}}
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	bifrostVars(vars, rc) // B2 Bifrost features: {{gateway.headers_json}} (bifrostfeatures.go)

	argv, err := renderList(rc.Agent.Argv, vars)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: argv: %w", rc.Run.RunID, err)
	}
	for _, arg := range argv {
		if strings.Contains(arg, rc.Key.Value) {
			return nil, fmt.Errorf("plan run %s: argv would expose the run key; pass it through env", rc.Run.RunID)
		}
	}
	env, err := renderMap(rc.Agent.Env, vars)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: env: %w", rc.Run.RunID, err)
	}
	files, err := renderMap(rc.Agent.Files, vars)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: files: %w", rc.Run.RunID, err)
	}
	if err := addFile(files, InstructionFile, instruction); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	if err := a.addUserNotes(files, rc.Agent.Notes); err != nil {
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
	plan.Argv = argv
	plan.Env = env
	plan.Files = files
	if err := addSkills(plan, a.cfg.BenchRoot, rc); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	if err := addMCP(plan, rc); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	addBifrostEnv(plan, rc) // B2 Bifrost features: BENCH_GATEWAY_HEADERS (bifrostfeatures.go)
	return plan, nil
}

// addUserNotes installs notes.user_file at notes.user_path. The path must be
// outside the workspace (DESIGN.md 8.6): user-level instructions are an arm
// variable, and a workspace path would land in the exported diff.
func (a *commandAdapter) addUserNotes(files map[string]string, notes schemas.AgentNotes) error {
	if notes.UserFile == "" {
		return nil
	}
	if !path.IsAbs(notes.UserPath) {
		return fmt.Errorf("notes.user_path %q must be an absolute container path", notes.UserPath)
	}
	if clean := path.Clean(notes.UserPath); clean == Workspace || strings.HasPrefix(clean, Workspace+"/") {
		return fmt.Errorf("notes.user_path %q is inside the workspace", notes.UserPath)
	}
	src := notes.UserFile
	if !filepath.IsAbs(src) {
		src = filepath.Join(a.cfg.BenchRoot, src)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read notes.user_file: %w", err)
	}
	return addFile(files, notes.UserPath, string(b))
}

// checkRunContext rejects a context missing what every adapter reads.
func checkRunContext(rc *schemas.RunContext) error {
	switch {
	case rc == nil:
		return fmt.Errorf("plan: nil run context")
	case rc.Run == nil:
		return fmt.Errorf("plan: run context has no run")
	case rc.Agent == nil:
		return fmt.Errorf("plan run %s: no agent config", rc.Run.RunID)
	case rc.Task == nil:
		return fmt.Errorf("plan run %s: no task", rc.Run.RunID)
	case rc.Agent.Image == "":
		return fmt.Errorf("plan run %s: agent %s has no image", rc.Run.RunID, rc.Agent.ID)
	}
	return nil
}

// templateVars is the fixed variable set of DESIGN.md 16.5. run.key is
// present only when the run has a key, so a scripted config without one
// fails loudly if it references it.
func templateVars(rc *schemas.RunContext, cfg CommandConfig, instruction string) map[string]string {
	vars := map[string]string{
		"instruction":      instruction,
		"instruction_file": InstructionFile,
		"workspace":        Workspace,
		"model":            rc.Agent.Model,
		"gateway.url":      strings.TrimRight(cfg.GatewayAgentURL, "/"),
		"gateway.provider": rc.Agent.Gateway.Provider, // the bare-model agent's model prefix (G4-21)
		"run.id":           rc.Run.RunID,
		"run.session":      rc.Run.RunID,
		"exp.id":           expID(rc),
		"task.id":          rc.Task.ID,
	}
	if rc.Key != nil && rc.Key.Value != "" {
		vars["run.key"] = rc.Key.Value
	}
	return vars
}

func expID(rc *schemas.RunContext) string {
	if rc.Run.ExpID != "" {
		return rc.Run.ExpID
	}
	if rc.Experiment != nil {
		return rc.Experiment.ID
	}
	return ""
}

// basePlan fills everything but argv, env and files. The wall time is the
// stricter of the agent budget and the task timeout, so arms that differ only
// in config never get different clocks from the task.
func basePlan(rc *schemas.RunContext, network string) (*schemas.RunPlan, error) {
	wall := stricter(rc.Agent.Budgets.WallS, rc.Task.Timeouts.AgentS)
	if wall <= 0 {
		return nil, fmt.Errorf("plan run %s: no wall time budget in agent %s or task %s", rc.Run.RunID, rc.Agent.ID, rc.Task.ID)
	}
	pids := rc.Agent.Budgets.PidsLimit
	if pids <= 0 {
		pids = defaultPids
	}
	return &schemas.RunPlan{
		Image:   rc.Agent.Image,
		Workdir: Workspace,
		WallS:   wall,
		CPUs:    rc.Agent.Budgets.CPUs,
		MemGB:   rc.Agent.Budgets.MemGB,
		Pids:    pids,
		Network: network,
		User:    agentUser,
	}, nil
}

// stricter returns the smaller positive limit; zero means unset.
func stricter(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// readTaskFile reads a file named relative to the task folder.
func readTaskFile(task *schemas.Task, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("task %s: empty file name", task.ID)
	}
	p := rel
	if !filepath.IsAbs(p) {
		p = filepath.Join(task.Dir, rel)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("task %s: read %s: %w", task.ID, rel, err)
	}
	return string(b), nil
}

func renderList(in []string, vars map[string]string) ([]string, error) {
	out := make([]string, 0, len(in))
	for i, s := range in {
		r, err := utils.Render(s, vars)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// renderMap renders values only. Keys (env names, container paths) are literal
// so a secret can never become a file name.
func renderMap(in map[string]string, vars map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for k, v := range in {
		r, err := utils.Render(v, vars)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = r
	}
	return out, nil
}

// addFile adds a file the adapter itself delivers. A config file at the same
// path is an error, not an override, so a delivery is never silently lost.
func addFile(files map[string]string, p, content string) error {
	if _, ok := files[p]; ok {
		return fmt.Errorf("agent files already define %s", p)
	}
	files[p] = content
	return nil
}
