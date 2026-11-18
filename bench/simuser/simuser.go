// Package simuser is the bench's user simulator for customer support tasks
// (docs/customer-support.md): simuser.py, a task service (role user) that
// plays the customer the agent talks to over MCP through respond_to_user,
// and the Go side that pins it. A suite's simulator settings
// (Suite.Simulator, optionally overridden for a whole experiment by
// Experiment.Simulator) choose the mode: scripted (a deterministic script
// per task, the default) or llm (a pinned model plays the customer from a
// persona under a versioned prompt, through the gateway on the service's own
// virtual key). The effective settings and the prompt's sha256 are hashed
// into the manifest, like a judge's.
package simuser

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Shipped prompt versions (SimulatorSpec.PromptVersion).
const (
	// PromptUserV1 is the customer prompt for a persona file: name, goal,
	// known and unknown facts, behaviour, when satisfied; reveal only when
	// asked, never invent, the agent's message fenced as speech, the stop
	// token only when satisfied. Written for Heimdall; its disclosure and
	// stop rules follow tau2-bench's text user guidelines (below).
	PromptUserV1 = "user-v1"
	// PromptTau2UserV1 is tau2-bench's text user simulator prompt, for
	// imported tau2 tasks (`import tau2 -set user=llm`): the _TEXT_PLAIN
	// guidelines of src/tau2/user/guidelines.py and the <scenario> block of
	// SYSTEM_PROMPT in src/tau2/user/user_simulator.py,
	// github.com/sierra-research/tau2-bench at commit
	// c06b07dd7c4d4ec61ba6ff9bcdf0b15dd7ef36d4 (MIT, Copyright (c) 2025
	// Sierra Research). Adapted: the persona and language blocks are
	// dropped, the scenario's fields are laid out as labelled sections, and
	// one rule is added for the <agent_message> fence.
	PromptTau2UserV1 = "tau2-user-v1"
)

// Defaults and names shared with simuser.py.
const (
	DefaultMaxTurns = 12
	MaxMaxTurns     = 100
	Placeholder     = "pin-me" // what an importer writes until the user pins a provider and model
	StopToken       = "###STOP###"
	// TranscriptExport is the export id and file name of the conversation.
	TranscriptExport = "transcript.json"
	// UserFile is where a task's user file (script and persona) is copied
	// in the simulator's container.
	UserFile = "/sim/user.json"
)

// Environment variables the environment passes to a role user service.
const (
	EnvMode         = "BENCH_SIM_MODE"
	EnvPrompt       = "BENCH_SIM_PROMPT_VERSION"
	EnvPromptSHA256 = "BENCH_SIM_PROMPT_SHA256"
	EnvMaxTurns     = "BENCH_SIM_MAX_TURNS"
	EnvSeed         = "BENCH_SIM_SEED"
	EnvHostile      = "BENCH_SIM_HOSTILE" // set by a service-layer probe, never by the bench
)

//go:embed simuser.py prompts/*.txt
var assets embed.FS

var versionRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

// Script returns simuser.py.
func Script() []byte {
	b, err := assets.ReadFile("simuser.py")
	if err != nil {
		panic("simuser: embedded simuser.py missing: " + err.Error())
	}
	return b
}

// Prompt returns the prompt template of version.
func Prompt(version string) ([]byte, error) {
	if !versionRe.MatchString(version) {
		return nil, fmt.Errorf("simulator prompt_version %q is not a valid name", version)
	}
	b, err := assets.ReadFile("prompts/" + version + ".txt")
	if err != nil {
		return nil, fmt.Errorf("simulator prompt_version %q is not shipped (want %s or %s)", version, PromptUserV1, PromptTau2UserV1)
	}
	return b, nil
}

// PromptSHA256 is the sha256 of the prompt template of version.
func PromptSHA256(version string) (string, error) {
	b, err := Prompt(version)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Files are the simulator's files as a suite carries them next to its
// service Dockerfile: simuser.py and prompts/<version>.txt.
func Files() map[string][]byte {
	out := map[string][]byte{"simuser.py": Script()}
	entries, err := assets.ReadDir("prompts")
	if err != nil {
		panic("simuser: embedded prompts missing: " + err.Error())
	}
	for _, e := range entries {
		b, err := assets.ReadFile("prompts/" + e.Name())
		if err != nil {
			panic(err)
		}
		out["prompts/"+e.Name()] = b
	}
	return out
}

// Effective is the simulator settings a run uses: the suite's, with the
// experiment's non-empty fields on top, and defaults filled (mode scripted,
// prompt user-v1, 12 turns). Nil when neither sets any.
func Effective(suite *schemas.Suite, exp *schemas.Experiment) *schemas.SimulatorSpec {
	var base, over *schemas.SimulatorSpec
	if suite != nil {
		base = suite.Simulator
	}
	if exp != nil {
		over = exp.Simulator
	}
	if base == nil && over == nil {
		return nil
	}
	out := schemas.SimulatorSpec{}
	if base != nil {
		out = *base
	}
	if over != nil {
		if over.Mode != "" {
			out.Mode = over.Mode
		}
		if over.Provider != "" {
			out.Provider = over.Provider
		}
		if over.Model != "" {
			out.Model = over.Model
		}
		if over.PromptVersion != "" {
			out.PromptVersion = over.PromptVersion
		}
		if over.MaxTurns != 0 {
			out.MaxTurns = over.MaxTurns
		}
		if over.Seed != 0 {
			out.Seed = over.Seed
		}
	}
	if out.Mode == "" {
		out.Mode = schemas.SimulatorScripted
	}
	if out.PromptVersion == "" {
		out.PromptVersion = PromptUserV1
	}
	if out.MaxTurns == 0 {
		out.MaxTurns = DefaultMaxTurns
	}
	return &out
}

// IsLLM reports whether spec has a model play the user.
func IsLLM(spec *schemas.SimulatorSpec) bool {
	return spec != nil && spec.Mode == schemas.SimulatorLLM
}

// Check validates effective settings: a known mode, a turn cap in range, a
// shipped prompt, and in llm mode a pinned provider and model.
func Check(spec *schemas.SimulatorSpec) error {
	if spec == nil {
		return nil
	}
	switch {
	case spec.Mode != schemas.SimulatorScripted && spec.Mode != schemas.SimulatorLLM:
		return fmt.Errorf("simulator mode %q: want %s or %s", spec.Mode, schemas.SimulatorScripted, schemas.SimulatorLLM)
	case spec.MaxTurns < 1 || spec.MaxTurns > MaxMaxTurns:
		return fmt.Errorf("simulator max_turns %d: want 1 to %d", spec.MaxTurns, MaxMaxTurns)
	}
	if _, err := Prompt(spec.PromptVersion); err != nil {
		return err
	}
	if spec.Mode == schemas.SimulatorLLM {
		switch {
		case spec.Provider == "" || spec.Model == "":
			return errors.New("simulator mode llm needs provider and model (the model that plays the user)")
		case spec.Provider == Placeholder || spec.Model == Placeholder:
			return fmt.Errorf("simulator provider and model are still %s: pin them in suite.yaml or the experiment's simulator", Placeholder)
		}
	}
	return nil
}

// Label is how reports name the user of runs under spec: "scripted user",
// or "simulated user (model: provider/model)" for llm mode.
func Label(spec *schemas.SimulatorSpec) string {
	if !IsLLM(spec) {
		return "scripted user"
	}
	return "simulated user (model: " + spec.Provider + "/" + spec.Model + ")"
}

// Hash is the sha256 of the effective settings and the prompt's sha256,
// recorded in the manifest's scenario hashes under "simulator".
func Hash(spec *schemas.SimulatorSpec) (string, error) {
	if spec == nil {
		return "", nil
	}
	psha, err := PromptSHA256(spec.PromptVersion)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Spec         schemas.SimulatorSpec `json:"spec"`
		PromptSHA256 string                `json:"prompt_sha256"`
	}{*spec, psha})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ServiceEnv is what a role user service gets from the effective settings:
// mode, prompt version and hash, turn cap, seed. Gateway access is added by
// the environment (environments/servicegateway.go), never here.
func ServiceEnv(spec *schemas.SimulatorSpec) map[string]string {
	if spec == nil {
		return nil
	}
	env := map[string]string{
		EnvMode:     spec.Mode,
		EnvPrompt:   spec.PromptVersion,
		EnvMaxTurns: fmt.Sprint(spec.MaxTurns),
		EnvSeed:     fmt.Sprint(spec.Seed),
	}
	if sha, err := PromptSHA256(spec.PromptVersion); err == nil {
		env[EnvPromptSHA256] = sha
	}
	return env
}

// SimulatorBacked reports whether a service plays the user: a role user
// service, or a service whose gateway names no provider and no model
// (gateway: {}), such as an imported tau2 domain server that holds its own
// user. Such a service gets the simulator settings in its environment, and
// gateway access only in llm mode.
func SimulatorBacked(spec schemas.ServiceSpec) bool {
	return spec.Role == schemas.ServiceRoleUser || (spec.Gateway != nil && spec.Gateway.Provider == "" && spec.Gateway.Model == "")
}

// GatewayFor is the gateway access a service gets under the effective
// simulator settings. A simulator-backed service gets one only in llm mode,
// with an empty provider or model filled from the simulator; any other
// service gets its own Gateway, or none. So no key is minted for a script.
func GatewayFor(spec schemas.ServiceSpec, sim *schemas.SimulatorSpec) *schemas.ServiceGateway {
	backed := SimulatorBacked(spec)
	switch {
	case backed && !IsLLM(sim):
		return nil
	case !backed && spec.Gateway == nil:
		return nil
	}
	var g schemas.ServiceGateway
	if spec.Gateway != nil {
		g = *spec.Gateway
	}
	if backed {
		if g.Provider == "" {
			g.Provider = sim.Provider
		}
		if g.Model == "" {
			g.Model = sim.Model
		}
	}
	return &g
}

// SimulatedServices are the run's simulator-backed services, suite first.
func SimulatedServices(suite *schemas.Suite, task *schemas.Task) []schemas.ServiceSpec {
	var out []schemas.ServiceSpec
	if suite != nil {
		for _, s := range suite.Services {
			if SimulatorBacked(s) {
				out = append(out, s)
			}
		}
	}
	if task != nil {
		for _, s := range task.Services {
			if SimulatorBacked(s) {
				out = append(out, s)
			}
		}
	}
	return out
}

// UserConfig is the part of a task's user file the bench checks.
type UserConfig struct {
	Script *struct {
		Opening string `json:"opening"`
	} `json:"script"`
	Persona *struct {
		Goal string `json:"goal"`
	} `json:"persona"`
}

// CheckUserFile refuses a user file that cannot drive mode: scripted needs
// script.opening, llm needs persona.goal. Every task of a support suite
// carries both, so a suite switches modes without editing tasks.
func CheckUserFile(path, mode string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("user file: %w", err)
	}
	var c UserConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("user file %s: %w", path, err)
	}
	switch mode {
	case schemas.SimulatorLLM:
		if c.Persona == nil || c.Persona.Goal == "" {
			return fmt.Errorf("user file %s has no persona.goal: llm mode cannot play this user", path)
		}
	default:
		if c.Script == nil || c.Script.Opening == "" {
			return fmt.Errorf("user file %s has no script.opening: scripted mode cannot play this user", path)
		}
	}
	return nil
}
