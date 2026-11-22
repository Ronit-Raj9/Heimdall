package adapters

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice"
)

func voicePlatformFixture(t *testing.T, spec *schemas.VoicePlatformAgent) (*schemas.RunContext, CommandConfig) {
	t.Helper()
	rc, cfg := fixture(t, &schemas.AgentConfig{
		ID: "acme-phone", Adapter: AdapterVoicePlatform, Image: "bench/coding-core-base:1", Model: "hosted",
		Budgets: schemas.Budgets{WallS: 300, CPUs: 1, MemGB: 1}, VoicePlatform: spec,
	})
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
	rc.Task.Voice = &schemas.VoiceSpec{Caller: "caller"}
	return rc, cfg
}

func TestVoicePlatformPlanVapi(t *testing.T) {
	rc, cfg := voicePlatformFixture(t, &schemas.VoicePlatformAgent{Platform: "vapi", AgentID: "asst_1", APIKeySecret: "VAPI_KEY"})
	plan, err := newVoicePlatformAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Argv, " ") != "python3 -I /tmp/bench/voice/platformbridge.py" || plan.Network != testNetwork {
		t.Fatalf("argv %q network %q", plan.Argv, plan.Network)
	}
	if len(plan.Egress) != 1 || plan.Egress[0] != "api.vapi.ai:443" {
		t.Fatalf("egress %v", plan.Egress)
	}
	want := map[string]string{
		"BENCH_VOICE_PLATFORM": "vapi", "BENCH_VOICE_PLATFORM_URL": "https://api.vapi.ai", "BENCH_VOICE_AGENT_ID": "asst_1",
		envVoicePlatformKey: "{{secret.VAPI_KEY}}", "BENCH_VOICE_URL": "ws://caller:8000/call", "BENCH_VOICE_SETUP_S": "30",
		utils.EnvRemoteConnect: "bench-eg-r_p20fj3s8wa:9300",
	}
	for k, v := range want {
		if plan.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, plan.Env[k], v)
		}
	}
	if _, ok := plan.Env["BENCH_VOICE_FRAMING"]; ok {
		t.Error("framing is only for a websocket endpoint")
	}
	for _, n := range voice.BridgeFiles {
		if plan.Files["/tmp/bench/voice/"+n] != string(voice.File(n)) {
			t.Errorf("bridge file %s missing", n)
		}
	}
	for _, v := range plan.Argv {
		if strings.Contains(v, "secret") {
			t.Fatal("the key must never be in argv")
		}
	}
	if got := utils.RunSecretNames(rc); len(got) != 1 || got[0] != "VAPI_KEY" {
		t.Fatalf("run secret names %v", got)
	}
}

func TestVoicePlatformPlanWebSocket(t *testing.T) {
	rc, cfg := voicePlatformFixture(t, &schemas.VoicePlatformAgent{Platform: "websocket", URL: "wss://voice.acme.example:8443/twilio"})
	plan, err := newVoicePlatformAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Egress[0] != "voice.acme.example:8443" || plan.Env["BENCH_VOICE_FRAMING"] != "twilio" ||
		plan.Env["BENCH_VOICE_PLATFORM_URL"] != "wss://voice.acme.example:8443/twilio" {
		t.Fatalf("egress %v env %v", plan.Egress, plan.Env)
	}
	if _, ok := plan.Env[envVoicePlatformKey]; ok {
		t.Error("no key configured, none sent")
	}
}

func TestVoicePlatformRefusals(t *testing.T) {
	a := func(cfg CommandConfig) *voicePlatformAdapter { return newVoicePlatformAdapter(cfg) }
	rc, cfg := voicePlatformFixture(t, &schemas.VoicePlatformAgent{Platform: "retell", AgentID: "agent_1", APIKeySecret: "K"})
	if _, err := a(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "Retell agents have no call WebSocket") {
		t.Errorf("retell audio: %v", err)
	}
	rc, cfg = voicePlatformFixture(t, &schemas.VoicePlatformAgent{Platform: "elevenlabs", AgentID: "agent_1"})
	rc.Task.Voice = nil
	if _, err := a(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), VoicePlatformTextReason) {
		t.Errorf("text task: %v", err)
	}
	rc, cfg = voicePlatformFixture(t, &schemas.VoicePlatformAgent{Platform: "elevenlabs", AgentID: "agent_1"})
	rc.NoPaidTraffic = true
	if _, err := a(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "scripted model") {
		t.Errorf("no paid traffic: %v", err)
	}
	rc, cfg = voicePlatformFixture(t, &schemas.VoicePlatformAgent{Platform: "vapi", AgentID: "a"})
	if err := a(cfg).Validate(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "api_key_secret") {
		t.Errorf("missing key: %v", err)
	}
	rc, cfg = voicePlatformFixture(t, nil)
	if err := a(cfg).Validate(context.Background(), rc); err == nil {
		t.Error("no block accepted")
	}
}

// A text task of a voice platform agent runs through the pull launcher,
// with the agent's voice_platform block on the signed run token (the
// server answers each turn, handlers/pullvoice.go).
func TestVoicePlatformTextPlan(t *testing.T) {
	spec := &schemas.VoicePlatformAgent{Platform: "vapi", AgentID: "asst_1", APIKeySecret: "VAPI_KEY", TimeoutS: 45}
	agent := &schemas.AgentConfig{ID: "acme-phone", Adapter: AdapterVoicePlatform, Image: "img", Model: "hosted",
		Budgets: schemas.Budgets{WallS: 300}, VoicePlatform: spec}
	rc, cfg := conversationRC(t, agent)
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
	if _, err := newVoicePlatformAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "pull.url") {
		t.Fatalf("no pull URL: %v", err)
	}
	cfg.PullURL, cfg.OrgID = "https://heimdall.example.com/api/bench/pull/run", "org_a"
	plan, err := newVoicePlatformAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Argv, " ") != "python3 -I "+pullLauncherFile || len(plan.Egress) != 1 || plan.Egress[0] != "heimdall.example.com:443" {
		t.Fatalf("argv %v egress %v", plan.Argv, plan.Egress)
	}
	if plan.Env[envVoicePlatformKey] != "{{secret.VAPI_KEY}}" || plan.Env["BENCH_CONV_HISTORY"] != schemas.ConversationHistoryServer ||
		plan.Env["BENCH_CONV_USERS"] == "" || plan.Env[utils.EnvMCPServers] != "" || plan.Env["BENCH_PULL_TURN_S"] != "45" {
		t.Fatalf("env %v", plan.Env)
	}
	c, err := pullagent.DefaultSigner().VerifyRun(plan.Env["BENCH_PULL_TOKEN"], time.Now())
	if err != nil || c.Org != "org_a" || c.Voice == nil || c.Voice.AgentID != "asst_1" || c.Voice.Platform != "vapi" {
		t.Fatalf("token claims %+v %v", c, err)
	}
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
	if _, err := newVoicePlatformAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), VoicePlatformTextReason) {
		t.Fatalf("state task: %v", err)
	}
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
	agent.VoicePlatform = &schemas.VoicePlatformAgent{Platform: "websocket", URL: "wss://voice.acme.example/twilio"}
	if _, err := newVoicePlatformAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "only takes calls") {
		t.Fatalf("websocket text: %v", err)
	}
}
