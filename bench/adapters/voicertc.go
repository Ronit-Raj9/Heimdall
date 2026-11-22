package adapters

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice"
	"github.com/maximhq/bifrost/bench/voicertc"
)

// Real-time voice agents over LiveKit or SIP (docs/voice-realtime.md).

// AdapterVoiceRTC is the name of the voice_rtc adapter.
const AdapterVoiceRTC = "voice_rtc"

const (
	voiceRTCVersion = "1.0.0"
	voiceRTCDir     = "/tmp/bench/voicertc"
	// voiceRTCTokenSlackS keeps the gateway token valid a little past the
	// run's wall time, for the gateway's own cleanup.
	voiceRTCTokenSlackS = 120
)

// voiceRTCFiles are what the bridge needs in its container.
var voiceRTCFiles = []string{"rtcbridge.py", "wsproto.py"}

// voiceRTCAdapter runs a voice task against an agent in a LiveKit room or on
// a phone number. The container it plans is the bench's bridge
// (bench/voice/rtcbridge.py): it dials the task's voice caller and the
// operator's real-time gateway, which joins the room as the caller. The
// org's LiveKit and trunk credentials never reach argv or files: each is a
// {{secret.NAME}} reference in its own env variable, expanded by the engine
// and redacted in plan.json. The plan's one egress grant is the gateway.
type voiceRTCAdapter struct {
	cfg CommandConfig
	now func() time.Time
}

func newVoiceRTCAdapter(cfg CommandConfig) *voiceRTCAdapter {
	return &voiceRTCAdapter{cfg: cfg, now: time.Now}
}

// GetName implements schemas.Part.
func (a *voiceRTCAdapter) GetName() string { return AdapterVoiceRTC }

// GetVersion implements schemas.Part.
func (a *voiceRTCAdapter) GetVersion() string { return voiceRTCVersion }

// Metadata implements schemas.AdapterMetadata.
func (a *voiceRTCAdapter) Metadata() schemas.AdapterMetadataInfo {
	return schemas.AdapterMetadataInfo{AdapterVersion: voiceRTCVersion}
}

// Validate implements schemas.AdapterValidator.
func (a *voiceRTCAdapter) Validate(_ context.Context, rc *schemas.RunContext) error {
	if err := checkRunContext(rc); err != nil {
		return err
	}
	if err := voicertc.Check(rc.Agent.VoiceRTC); err != nil {
		return fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	return nil
}

// BuildExecutionPlan implements schemas.AdapterRunner.
func (a *voiceRTCAdapter) BuildExecutionPlan(ctx context.Context, rc *schemas.RunContext) (*schemas.ExecutionPlan, error) {
	return a.Plan(ctx, rc)
}

// Cleanup implements schemas.AdapterCleanup. The gateway deletes the room
// and any temporary trunk when the call ends; the bridge dies with the run.
func (a *voiceRTCAdapter) Cleanup(context.Context, *schemas.RunContext) error { return nil }

// VoiceRTCTextReason refuses a text task: a real-time voice agent is reached
// only by calling it.
const VoiceRTCTextReason = "a real-time voice agent (LiveKit or a phone number) is reached only by a call; this task is not a voice call"

// Plan returns the bridge's plan.
func (a *voiceRTCAdapter) Plan(ctx context.Context, rc *schemas.RunContext) (*schemas.RunPlan, error) {
	if err := a.Validate(ctx, rc); err != nil {
		return nil, err
	}
	spec := rc.Agent.VoiceRTC
	if !voice.IsVoiceTask(rc.Task) {
		return nil, fmt.Errorf("plan run %s: task %s: %s", rc.Run.RunID, rc.Task.ID, VoiceRTCTextReason)
	}
	if rc.NoPaidTraffic {
		return nil, fmt.Errorf("plan run %s: a smoke, scripted_upstream or agent check run cannot hold a real-time voice agent's own model calls to the scripted model", rc.Run.RunID)
	}
	gw, err := voicertc.CheckRun(spec)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	target, err := gw.Target()
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
	plan.Env = a.env(rc, spec, gw, plan.WallS)
	plan.Files = map[string]string{}
	for name, b := range voice.Files(voiceRTCFiles) {
		plan.Files[voiceRTCDir+"/"+name] = string(b)
	}
	plan.Argv = []string{"python3", "-I", voiceRTCDir + "/rtcbridge.py"}
	plan.Egress = []string{target}
	return plan, nil
}

// env is the bridge's environment.
func (a *voiceRTCAdapter) env(rc *schemas.RunContext, spec *schemas.VoiceRTCAgent, gw voicertc.Gateway, wallS int) map[string]string {
	setup := voicertc.SetupS(spec)
	if limit := wallS - remoteTimeoutMargin; limit > 0 && setup > limit {
		setup = max(limit, 1)
	}
	session := rc.Run.RunID
	if rc.Session != nil {
		session = rc.Session.ID
	}
	caller := voice.Effective(rc.Task.Voice).Caller
	maxCall := wallS
	if maxCall <= 0 {
		maxCall = 600
	}
	exp := a.now().Add(time.Duration(wallS+voiceRTCTokenSlackS) * time.Second)
	dial := voicertc.BuildDial(spec, rc.Run.RunID, session, rc.Task.ID, maxCall)
	dial.SetupS = setup
	env := map[string]string{
		voicertc.EnvBridgeGatewayURL:   gw.URL,
		voicertc.EnvBridgeGatewayToken: voicertc.RunToken(gw.Secret, rc.Run.RunID, exp),
		voicertc.EnvBridgeDial:         dial.JSON(),
		"BENCH_VOICE_SETUP_S":          strconv.Itoa(setup),
		"BENCH_VOICE_URL":              "ws://" + caller + ":" + voiceCallerPortRTC + "/call",
		utils.EnvRemoteConnect:         utils.RemoteEgressAddr(rc.Run.RunID),
	}
	for k, v := range voicertc.SecretEnv(spec) {
		env[k] = v
	}
	return env
}

// voiceCallerPortRTC is the caller's port (voicecaller.py serve --port).
const voiceCallerPortRTC = "8000"
