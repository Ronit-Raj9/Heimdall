package adapters

import (
	"context"
	"fmt"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice"
	"github.com/maximhq/bifrost/bench/voiceconnect"
)

// Voice agents hosted on a voice platform (docs/voice-platforms.md).

// AdapterVoicePlatform is the name of the voice_platform adapter.
const AdapterVoicePlatform = "voice_platform"

const (
	voicePlatformVersion = "1.0.0"
	voiceBridgeDir       = "/tmp/bench/voice"
	voiceCallerPort      = "8000"
	envVoicePlatformKey  = "BENCH_VOICE_PLATFORM_KEY"
)

// voicePlatformAdapter runs a voice task against an agent hosted on Vapi,
// ElevenLabs Agents or the team's own call WebSocket. The container it plans
// is the bench's bridge (bench/voice/platformbridge.py): it dials the task's
// voice caller and the platform's call socket and carries audio both ways,
// so the caller's timeline measures the call. Text benchmarks of the same
// agent go through the bench's conversation driver over voiceconnect, not
// through a container.
type voicePlatformAdapter struct {
	cfg CommandConfig
}

func newVoicePlatformAdapter(cfg CommandConfig) *voicePlatformAdapter {
	return &voicePlatformAdapter{cfg: cfg}
}

// GetName implements schemas.Part.
func (a *voicePlatformAdapter) GetName() string { return AdapterVoicePlatform }

// GetVersion implements schemas.Part.
func (a *voicePlatformAdapter) GetVersion() string { return voicePlatformVersion }

// Metadata implements schemas.AdapterMetadata.
func (a *voicePlatformAdapter) Metadata() schemas.AdapterMetadataInfo {
	return schemas.AdapterMetadataInfo{AdapterVersion: voicePlatformVersion}
}

// Validate implements schemas.AdapterValidator.
func (a *voicePlatformAdapter) Validate(_ context.Context, rc *schemas.RunContext) error {
	if err := checkRunContext(rc); err != nil {
		return err
	}
	if err := voiceconnect.Check(rc.Agent.VoicePlatform); err != nil {
		return fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	return nil
}

// BuildExecutionPlan implements schemas.AdapterRunner.
func (a *voicePlatformAdapter) BuildExecutionPlan(ctx context.Context, rc *schemas.RunContext) (*schemas.ExecutionPlan, error) {
	return a.Plan(ctx, rc)
}

// Cleanup implements schemas.AdapterCleanup. The bridge's files die with the
// run container.
func (a *voicePlatformAdapter) Cleanup(context.Context, *schemas.RunContext) error { return nil }

// VoicePlatformTextReason refuses a text task the conversation driver
// cannot run: one whose answer is not the agent's reply.
const VoicePlatformTextReason = "a voice platform agent answers in words only, so it can run text benchmarks whose answer is its reply (no tools, no code changes)"

// voiceTextAgent is the agent name on a voice text run's pull token.
const voiceTextAgent = "voice-platform"

// textPlan runs a text task through the pull launcher (pullagent.py): it
// drives the conversation with the simulated customer turn by turn, and
// this server answers each turn through the platform's chat API
// (handlers/pullvoice.go), so the run's only route out is the server. The
// signed run token names the voice_platform block; the platform's key
// reaches the launcher as a {{secret.NAME}} reference and goes with each
// turn in a header.
func (a *voicePlatformAdapter) textPlan(rc *schemas.RunContext) (*schemas.RunPlan, error) {
	spec := rc.Agent.VoicePlatform
	if !voiceconnect.SupportsText(spec.Platform) {
		return nil, fmt.Errorf("plan run %s: task %s is a text conversation, and a voice WebSocket endpoint only takes calls; run a voice benchmark", rc.Run.RunID, rc.Task.ID)
	}
	kind := utils.OutputKind(rc.Suite, rc.Task)
	if kind != schemas.OutputAnswer {
		return nil, fmt.Errorf("plan run %s: task %s (output %s): %s", rc.Run.RunID, rc.Task.ID, kind, VoicePlatformTextReason)
	}
	if rc.NoPaidTraffic {
		return nil, fmt.Errorf("plan run %s: a smoke, scripted_upstream or agent check run cannot hold a hosted voice agent's own model calls to the scripted model", rc.Run.RunID)
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
	// timeout_s bounds one agent turn; the run gets its wall budget.
	pull := &schemas.PullAgent{Tools: schemas.OpenAIToolsOff, TurnTimeoutS: spec.TimeoutS}
	env := pullLauncherEnv(rc, pull, voiceTextAgent, plan.WallS, kind)
	plan.Files = map[string]string{pullLauncherFile: pullLauncherSource, InstructionFile: instruction}
	withConvHelper(plan)
	if err := pullEnvVoice(a.cfg, rc, plan, env, voiceTextAgent, spec); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	// The platform keeps the chat (server history): only the new message
	// is sent each turn.
	conv := &schemas.ConversationSpec{Driver: schemas.ConversationDriverBench, History: schemas.ConversationHistoryServer}
	if _, err := conversationEnv(rc, conv, plan, env); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	if spec.APIKeySecret != "" {
		env[envVoicePlatformKey] = "{{secret." + spec.APIKeySecret + "}}"
	}
	plan.Env = env
	plan.Argv = []string{"python3", "-I", pullLauncherFile}
	return plan, nil
}

// Plan returns the bridge's plan. The API key never reaches argv or files:
// BENCH_VOICE_PLATFORM_KEY holds a {{secret.NAME}} reference the engine
// expands from the org's secrets (redacted in plan.json). The plan asks for
// egress to exactly the platform's host and port (the API host also serves
// its call socket), granted through a per-run forwarder.
func (a *voicePlatformAdapter) Plan(ctx context.Context, rc *schemas.RunContext) (*schemas.RunPlan, error) {
	if err := a.Validate(ctx, rc); err != nil {
		return nil, err
	}
	spec := rc.Agent.VoicePlatform
	if !voice.IsVoiceTask(rc.Task) {
		return a.textPlan(rc)
	}
	if !voiceconnect.SupportsAudio(spec.Platform) {
		return nil, fmt.Errorf("plan run %s: task %s is a voice call and %s agents have no call WebSocket the bench can dial; run its text benchmarks instead", rc.Run.RunID, rc.Task.ID, voiceconnect.PlatformName(spec.Platform))
	}
	if rc.NoPaidTraffic {
		return nil, fmt.Errorf("plan run %s: a smoke, scripted_upstream or agent check run cannot hold a hosted voice agent's own model calls to the scripted model", rc.Run.RunID)
	}
	target, err := voiceconnect.AudioTarget(spec)
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
	plan.Env = voicePlatformEnv(rc, spec, plan.WallS)
	plan.Files = map[string]string{}
	for name, b := range voice.Files(voice.BridgeFiles) {
		plan.Files[voiceBridgeDir+"/"+name] = string(b)
	}
	plan.Argv = []string{"python3", "-I", voiceBridgeDir + "/platformbridge.py"}
	plan.Egress = []string{target}
	return plan, nil
}

// voicePlatformEnv is the bridge's environment.
func voicePlatformEnv(rc *schemas.RunContext, spec *schemas.VoicePlatformAgent, wallS int) map[string]string {
	setup := spec.TimeoutS
	if setup <= 0 {
		setup = 30
	}
	if limit := wallS - remoteTimeoutMargin; limit > 0 && setup > limit {
		setup = max(limit, 1)
	}
	session := rc.Run.RunID
	if rc.Session != nil {
		session = rc.Session.ID
	}
	caller := voice.Effective(rc.Task.Voice).Caller
	url := voiceconnect.APIBase(spec)
	if spec.Platform == schemas.VoicePlatformWebSocket {
		url = spec.URL
	}
	env := map[string]string{
		"BENCH_VOICE_PLATFORM":     spec.Platform,
		"BENCH_VOICE_PLATFORM_URL": url,
		"BENCH_VOICE_AGENT_ID":     spec.AgentID,
		"BENCH_VOICE_SESSION":      session,
		"BENCH_VOICE_SETUP_S":      strconv.Itoa(setup),
		"BENCH_VOICE_URL":          "ws://" + caller + ":" + voiceCallerPort + "/call",
		utils.EnvRemoteConnect:     utils.RemoteEgressAddr(rc.Run.RunID),
	}
	if spec.Platform == schemas.VoicePlatformWebSocket {
		framing := spec.Framing
		if framing == "" {
			framing = schemas.VoiceFramingTwilio
		}
		env["BENCH_VOICE_FRAMING"] = framing
	}
	if spec.APIKeySecret != "" {
		env[envVoicePlatformKey] = "{{secret." + spec.APIKeySecret + "}}"
	}
	return env
}
