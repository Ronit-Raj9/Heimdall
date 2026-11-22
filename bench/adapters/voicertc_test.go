package adapters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voicertc"
)

const testRTCSecret = "0123456789abcdef0123456789abcdef"

func voiceRTCFixture(t *testing.T, spec *schemas.VoiceRTCAgent) (*schemas.RunContext, CommandConfig) {
	t.Helper()
	t.Setenv(voicertc.EnvGatewayURL, "wss://voice.heimdall.example/dial")
	t.Setenv(voicertc.EnvGatewaySecret, testRTCSecret)
	t.Setenv(voicertc.EnvSIPAllow, "+1415555")
	rc, cfg := fixture(t, &schemas.AgentConfig{
		ID: "acme-livekit", Adapter: AdapterVoiceRTC, Image: "bench/coding-core-base:1", Model: "hosted",
		Budgets: schemas.Budgets{WallS: 300, CPUs: 1, MemGB: 1}, VoiceRTC: spec,
	})
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
	rc.Task.Voice = &schemas.VoiceSpec{Caller: "caller"}
	return rc, cfg
}

func TestVoiceRTCPlanLiveKitCloud(t *testing.T) {
	rc, cfg := voiceRTCFixture(t, &schemas.VoiceRTCAgent{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{
		URL: "wss://acme.livekit.cloud", APIKeySecret: "LK_KEY", APISecretSecret: "LK_SECRET", AgentName: "support-agent"}})
	a := newVoiceRTCAdapter(cfg)
	now := time.Unix(1_800_000_000, 0)
	a.now = func() time.Time { return now }
	plan, err := a.Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Argv, " ") != "python3 -I /tmp/bench/voicertc/rtcbridge.py" || plan.Network != testNetwork {
		t.Fatalf("argv %q network %q", plan.Argv, plan.Network)
	}
	if len(plan.Egress) != 1 || plan.Egress[0] != "voice.heimdall.example:443" {
		t.Fatalf("egress %v: the gateway is the one route out", plan.Egress)
	}
	want := map[string]string{
		voicertc.EnvBridgeGatewayURL: "wss://voice.heimdall.example/dial", voicertc.EnvBridgeLKKey: "{{secret.LK_KEY}}",
		voicertc.EnvBridgeLKSecret: "{{secret.LK_SECRET}}", "BENCH_VOICE_URL": "ws://caller:8000/call", "BENCH_VOICE_SETUP_S": "30",
		utils.EnvRemoteConnect: utils.RemoteEgressAddr(rc.Run.RunID),
	}
	for k, v := range want {
		if plan.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, plan.Env[k], v)
		}
	}
	run, err := voicertc.VerifyToken(testRTCSecret, plan.Env[voicertc.EnvBridgeGatewayToken], now.Add(5*time.Minute))
	if err != nil || run != rc.Run.RunID {
		t.Fatalf("token run %q err %v", run, err)
	}
	var dial map[string]any
	if err := json.Unmarshal([]byte(plan.Env[voicertc.EnvBridgeDial]), &dial); err != nil {
		t.Fatal(err)
	}
	lk := dial["livekit"].(map[string]any)
	if dial["mode"] != "livekit" || lk["dispatch"] != "agent" || lk["agent_name"] != "support-agent" || lk["url"] != "wss://acme.livekit.cloud" || dial["run"] != rc.Run.RunID {
		t.Fatalf("dial %v", dial)
	}
	for _, f := range []string{"rtcbridge.py", "wsproto.py"} {
		if plan.Files["/tmp/bench/voicertc/"+f] == "" {
			t.Errorf("bridge file %s missing", f)
		}
	}
	if strings.Contains(plan.Env[voicertc.EnvBridgeDial], "secret") {
		t.Fatal("credentials never go in the dial message")
	}
	if got := utils.RunSecretNames(rc); strings.Join(got, ",") != "LK_KEY,LK_SECRET" {
		t.Fatalf("run secret names %v", got)
	}
}

func TestVoiceRTCPlanPhone(t *testing.T) {
	rc, cfg := voiceRTCFixture(t, &schemas.VoiceRTCAgent{Mode: "sip", SIP: &schemas.VoiceRTCSIP{
		Number: "+14155550100", TrunkAddress: "acme.pstn.twilio.com", FromNumber: "+14155550199",
		TrunkUserSecret: "TRUNK_USER", TrunkPasswordSecret: "TRUNK_PASS"}})
	plan, err := newVoiceRTCAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env[voicertc.EnvBridgeTrunkUser] != "{{secret.TRUNK_USER}}" || plan.Env[voicertc.EnvBridgeTrunkPass] != "{{secret.TRUNK_PASS}}" {
		t.Fatalf("trunk env %v", plan.Env)
	}
	if _, ok := plan.Env[voicertc.EnvBridgeLKKey]; ok {
		t.Error("Heimdall's own LiveKit: no org LiveKit key")
	}
	if !strings.Contains(plan.Env[voicertc.EnvBridgeDial], `"number":"+14155550100"`) {
		t.Fatalf("dial %s", plan.Env[voicertc.EnvBridgeDial])
	}
	rc.Agent.VoiceRTC.SIP.Number = "+442071838750"
	if _, err := newVoiceRTCAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("number off the allowlist: %v", err)
	}
}

func TestVoiceRTCRefusals(t *testing.T) {
	spec := func() *schemas.VoiceRTCAgent {
		return &schemas.VoiceRTCAgent{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{Dispatch: "auto"}}
	}
	rc, cfg := voiceRTCFixture(t, spec())
	rc.Task.Voice = nil
	if _, err := newVoiceRTCAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), VoiceRTCTextReason) {
		t.Errorf("text task: %v", err)
	}
	rc, cfg = voiceRTCFixture(t, spec())
	rc.NoPaidTraffic = true
	if _, err := newVoiceRTCAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "scripted model") {
		t.Errorf("no paid traffic: %v", err)
	}
	rc, cfg = voiceRTCFixture(t, spec())
	t.Setenv(voicertc.EnvGatewayURL, "")
	if _, err := newVoiceRTCAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "--profile voice") {
		t.Errorf("no gateway: %v", err)
	}
	rc, cfg = voiceRTCFixture(t, nil)
	if err := newVoiceRTCAdapter(cfg).Validate(context.Background(), rc); err == nil {
		t.Error("no block accepted")
	}
}
