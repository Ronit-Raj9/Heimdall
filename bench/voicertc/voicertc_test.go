package voicertc

import (
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCheck(t *testing.T) {
	ok := []*schemas.VoiceRTCAgent{
		{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{AgentName: "support"}},
		{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{Dispatch: "auto"}},
		{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{StartURL: "https://bots.acme.example/start", StartTokenSecret: "BOT_TOKEN"}},
		{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{URL: "wss://acme.livekit.cloud", APIKeySecret: "K", APISecretSecret: "S", AgentName: "a"}},
		{Mode: "sip", SIP: &schemas.VoiceRTCSIP{Number: "+14155550100", TrunkAddress: "acme.pstn.twilio.com", FromNumber: "+14155550199"}},
		{Mode: "sip", SIP: &schemas.VoiceRTCSIP{Number: "+14155550100", TrunkID: "ST_abc", LiveKit: &schemas.VoiceRTCLiveKit{URL: "wss://acme.livekit.cloud", APIKeySecret: "K", APISecretSecret: "S"}}},
	}
	for i, s := range ok {
		if err := Check(s); err != nil {
			t.Errorf("ok %d: %v", i, err)
		}
	}
	bad := []struct {
		s    *schemas.VoiceRTCAgent
		want string
	}{
		{nil, "needs a voice_rtc block"},
		{&schemas.VoiceRTCAgent{Mode: "webrtc"}, "want one of livekit, sip"},
		{&schemas.VoiceRTCAgent{Mode: "livekit"}, "needs a voice_rtc.livekit block"},
		{&schemas.VoiceRTCAgent{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{}}, "agent_name"},
		{&schemas.VoiceRTCAgent{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{URL: "https://x.example", APIKeySecret: "K", APISecretSecret: "S", AgentName: "a"}}, "want wss://"},
		{&schemas.VoiceRTCAgent{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{URL: "wss://x.example", AgentName: "a"}}, "api_key_secret and api_secret_secret"},
		{&schemas.VoiceRTCAgent{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{APIKeySecret: "K", AgentName: "a"}}, "only for your own server"},
		{&schemas.VoiceRTCAgent{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{Dispatch: "webhook", StartURL: "ftp://x"}}, "start_url"},
		{&schemas.VoiceRTCAgent{Mode: "livekit", LiveKit: &schemas.VoiceRTCLiveKit{AgentName: "a", StartTokenSecret: "T"}}, "only for dispatch webhook"},
		{&schemas.VoiceRTCAgent{Mode: "sip", SIP: &schemas.VoiceRTCSIP{Number: "4155550100", TrunkAddress: "a.example", FromNumber: "+14155550199"}}, "E.164"},
		{&schemas.VoiceRTCAgent{Mode: "sip", SIP: &schemas.VoiceRTCSIP{Number: "+14155550100", TrunkAddress: "a.example"}}, "from_number"},
		{&schemas.VoiceRTCAgent{Mode: "sip", SIP: &schemas.VoiceRTCSIP{Number: "+14155550100", TrunkID: "ST_a"}}, "set sip.livekit.url"},
		{&schemas.VoiceRTCAgent{Mode: "sip", SIP: &schemas.VoiceRTCSIP{Number: "+14155550100", TrunkAddress: "a.example", FromNumber: "+14155550199", TrunkUserSecret: "U"}}, "go together"},
		{&schemas.VoiceRTCAgent{Mode: "sip", SIP: &schemas.VoiceRTCSIP{Number: "+14155550100", TrunkAddress: "a.example", FromNumber: "+14155550199", LiveKit: &schemas.VoiceRTCLiveKit{AgentName: "a"}}}, "a phone call brings the agent in"},
		{&schemas.VoiceRTCAgent{Mode: "livekit", TimeoutS: 900, LiveKit: &schemas.VoiceRTCLiveKit{Dispatch: "auto"}}, "timeout_s"},
	}
	for i, c := range bad {
		err := Check(c.s)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("bad %d: %v, want %q", i, err, c.want)
		}
	}
}

func TestSIPAllowed(t *testing.T) {
	cases := []struct {
		n, allow string
		want     bool
	}{
		{"+14155550100", "", false},
		{"+14155550100", "+1415", true},
		{"+14155550100", " +44, +1415555 ", true},
		{"+14155550100", "+1416", false},
		{"+14155550100", "1415", false},
		{"14155550100", "+1", false},
	}
	for _, c := range cases {
		if got := SIPAllowed(c.n, c.allow); got != c.want {
			t.Errorf("SIPAllowed(%q, %q) = %v", c.n, c.allow, got)
		}
	}
}

func TestRunToken(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tok := RunToken("s3cret-s3cret-s3cret", "r_abc", now.Add(time.Minute))
	if run, err := VerifyToken("s3cret-s3cret-s3cret", tok, now); err != nil || run != "r_abc" {
		t.Fatalf("verify: %q %v", run, err)
	}
	if _, err := VerifyToken("s3cret-s3cret-s3cret", tok, now.Add(2*time.Minute)); err == nil {
		t.Error("expired token accepted")
	}
	if _, err := VerifyToken("another-secret-xx", tok, now); err == nil {
		t.Error("token of another secret accepted")
	}
	forged := strings.Replace(tok, "r_abc", "r_xyz", 1)
	if _, err := VerifyToken("s3cret-s3cret-s3cret", forged, now); err == nil {
		t.Error("token for another run accepted")
	}
	// The fixed vector rtc_test.py checks the gateway against.
	if got := RunToken("0123456789abcdef0123456789abcdef", "run1", time.Unix(1800000000, 0)); got != "hrtc1.run1.1800000000.f10876dd23f72811f4343e84b23109f912a37120838caa6503b86f01155a690d" {
		t.Fatal(got)
	}
}

func TestGatewayFromEnv(t *testing.T) {
	env := map[string]string{}
	lookupEnv = func(k string) string { return env[k] }
	t.Cleanup(func() { lookupEnv = func(k string) string { return "" } })
	if _, err := GatewayFromEnv(); err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("unset: %v", err)
	}
	env[EnvGatewayURL] = "https://x"
	env[EnvGatewaySecret] = "0123456789abcdef"
	if _, err := GatewayFromEnv(); err == nil || !strings.Contains(err.Error(), "wss://") {
		t.Fatalf("bad url: %v", err)
	}
	env[EnvGatewayURL] = "ws://voice-rtc:8790/dial"
	env[EnvGatewaySecret] = "short"
	if _, err := GatewayFromEnv(); err == nil || !strings.Contains(err.Error(), "16 characters") {
		t.Fatalf("short secret: %v", err)
	}
	env[EnvGatewaySecret] = "0123456789abcdef"
	g, err := GatewayFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if tg, _ := g.Target(); tg != "voice-rtc:8790" {
		t.Fatalf("target %s", tg)
	}
	sip := &schemas.VoiceRTCAgent{Mode: "sip", SIP: &schemas.VoiceRTCSIP{Number: "+14155550100"}}
	if _, err := CheckRun(sip); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("no allowlist: %v", err)
	}
	env[EnvSIPAllow] = "+1415"
	if _, err := CheckRun(sip); err != nil {
		t.Fatal(err)
	}
}

func TestBuildDialAndSecrets(t *testing.T) {
	s := &schemas.VoiceRTCAgent{Mode: "livekit", TimeoutS: 45, LiveKit: &schemas.VoiceRTCLiveKit{
		URL: "wss://acme.livekit.cloud", APIKeySecret: "K", APISecretSecret: "S", StartURL: "https://bots.example/start", StartTokenSecret: "T", RoomPrefix: "acme"}}
	d := BuildDial(s, "r1", "s1", "t1", 300)
	if d.SetupS != 45 || d.LiveKit.Dispatch != "webhook" || d.LiveKit.RoomPrefix != "acme" || d.SIP != nil {
		t.Fatalf("dial %+v", d)
	}
	if got := strings.Join(SecretNames(s), ","); got != "K,S,T" {
		t.Fatalf("secret names %s", got)
	}
	if strings.Contains(d.JSON(), "secret") {
		t.Fatal("dial carries secrets")
	}
}
