package handlers

// Connect a real-time voice agent (docs/voice-realtime.md): the gallery
// forms for an agent in a LiveKit room (LiveKit Agents, Pipecat with its
// LiveKit transport, any bot that joins rooms) and for a phone number
// dialled through LiveKit SIP. Each renders an agent with adapter voice_rtc
// and a voice_rtc block; keys and trunk credentials are names of Settings >
// Secrets entries, never the values.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voicertc"
)

// Gallery ids of the real-time voice forms.
const (
	TemplateLiveKitAgent = "livekit-agent"
	TemplatePhoneNumber  = "phone-number"
)

var rtcTimeoutParam = TemplateParam{Name: "timeout_s", Label: "Call setup timeout (seconds)", Kind: ParamNumber, Default: "30", Group: "advanced",
	Help: "How long Heimdall waits for your agent to join the room or answer the phone."}

var rtcLiveKitServerParams = []TemplateParam{
	{Name: "livekit_url", Label: "Your LiveKit server", Placeholder: "wss://your-project.livekit.cloud", Group: "advanced",
		Help: "Leave empty to use Heimdall's LiveKit server (ask your admin for its URL and keys for your worker). Set it for LiveKit Cloud or your own server."},
	{Name: "api_key_secret", Label: "LiveKit API key", Kind: ParamSecret, Placeholder: "LIVEKIT_API_KEY", Group: "advanced",
		Help: "With your own server: the name of a secret in Settings > Secrets holding its API key."},
	{Name: "api_secret_secret", Label: "LiveKit API secret", Kind: ParamSecret, Placeholder: "LIVEKIT_API_SECRET", Group: "advanced",
		Help: "With your own server: the name of a secret in Settings > Secrets holding its API secret."},
}

var rtcTemplates = []AgentTemplate{
	{ID: TemplateLiveKitAgent, Name: "Connect your LiveKit or Pipecat voice agent", Category: TemplateCategoryBYO, Adapter: "voice_rtc", Providers: abAnyProvider,
		DefaultModel: "hosted", source: "inline", Docs: "docs/voice-realtime.md",
		Description: "A voice agent that joins LiveKit rooms. For each voice task Heimdall opens a room, brings your agent in and calls it over WebRTC with a simulated caller.",
		Params: append([]TemplateParam{
			{Name: "dispatch", Label: "How your agent joins", Kind: ParamSelect, Options: schemas.VoiceDispatches, Default: schemas.VoiceDispatchAgent, Required: true,
				Help: "agent: LiveKit Agents explicit dispatch by agent name. auto: your worker joins every new room. webhook: Heimdall posts the room and a join token to your start URL (Pipecat and custom bots)."},
			{Name: "agent_name", Label: "Agent name", Placeholder: "support-agent",
				Help: "With dispatch agent: the agent_name your LiveKit Agents worker registers (WorkerOptions(agent_name=...))."},
			{Name: "start_url", Label: "Start URL", Placeholder: "https://bots.example.com/start",
				Help: "With dispatch webhook: receives {livekit_url, room, token, session} as a JSON POST and starts your bot in that room."},
			{Name: "start_token_secret", Label: "Start URL token", Kind: ParamSecret, Placeholder: "BOT_START_TOKEN", Group: "auth",
				Help: "Name of a secret in Settings > Secrets sent as Authorization: Bearer on the start URL. Leave empty if it needs none."},
		}, append(rtcLiveKitServerParams, rtcTimeoutParam)...)},
	{ID: TemplatePhoneNumber, Name: "Call your voice agent's phone number", Category: TemplateCategoryBYO, Adapter: "voice_rtc", Providers: abAnyProvider,
		DefaultModel: "hosted", source: "inline", Docs: "docs/voice-realtime.md",
		Description: "Any voice agent with a phone number (Vapi, Retell, a contact centre). Heimdall dials it through LiveKit SIP and your SIP trunk, and a simulated caller talks to it.",
		Params: append([]TemplateParam{
			{Name: "number", Label: "Agent's phone number", Placeholder: "+14155550100", Required: true,
				Help: "E.164. Heimdall only dials numbers your admin put on the allowlist."},
			{Name: "trunk_address", Label: "SIP trunk host", Placeholder: "your-trunk.pstn.twilio.com",
				Help: "Your SIP provider's outbound host (Twilio Elastic SIP, Telnyx, ...). Or set a LiveKit trunk ID below."},
			{Name: "from_number", Label: "Caller ID number", Placeholder: "+14155550199",
				Help: "A number your trunk lets you call from, E.164."},
			{Name: "trunk_user_secret", Label: "Trunk username", Kind: ParamSecret, Placeholder: "SIP_TRUNK_USER", Group: "auth",
				Help: "Name of a secret in Settings > Secrets holding the trunk's username."},
			{Name: "trunk_password_secret", Label: "Trunk password", Kind: ParamSecret, Placeholder: "SIP_TRUNK_PASSWORD", Group: "auth",
				Help: "Name of a secret in Settings > Secrets holding the trunk's password."},
			{Name: "transport", Label: "SIP transport", Kind: ParamSelect, Options: []string{"udp", "tcp", "tls"}, Default: "udp", Group: "advanced"},
			{Name: "trunk_id", Label: "LiveKit trunk ID", Placeholder: "ST_...", Group: "advanced",
				Help: "An outbound trunk already set up on your own LiveKit server (then set that server below and leave the trunk host empty)."},
		}, append(rtcLiveKitServerParams, rtcTimeoutParam)...)},
}

func init() {
	// Beside the other connect forms, before "Wrap my existing app".
	at := len(agentTemplates)
	for i, t := range agentTemplates {
		if t.ID == "wrap-app" {
			at = i
			break
		}
	}
	out := append([]AgentTemplate{}, agentTemplates[:at]...)
	out = append(out, rtcTemplates...)
	agentTemplates = append(out, agentTemplates[at:]...)
}

// connectRTCBlock is the voice_rtc block of a real-time voice form, or nil
// when id is not one.
func connectRTCBlock(id string, params map[string]string) (map[string]any, error) {
	if id != TemplateLiveKitAgent && id != TemplatePhoneNumber {
		return nil, nil
	}
	get := func(name string) (string, error) {
		v := strings.TrimSpace(params[name])
		if len(v) > 2048 || strings.ContainsAny(v, "\n\r") {
			return "", fmt.Errorf("%s must be one line under 2048 characters", name)
		}
		return v, nil
	}
	var errs []string
	str := func(name string) string {
		v, err := get(name)
		if err != nil {
			errs = append(errs, err.Error())
		}
		return v
	}
	secret := func(name, label string) string {
		v, err := connectSecretName(params, name, label)
		if err != nil {
			errs = append(errs, err.Error())
		}
		return v
	}
	server := &schemas.VoiceRTCLiveKit{URL: str("livekit_url"), APIKeySecret: secret("api_key_secret", "LiveKit API key secret"),
		APISecretSecret: secret("api_secret_secret", "LiveKit API secret secret")}
	spec := &schemas.VoiceRTCAgent{}
	if v := strings.TrimSpace(params["timeout_s"]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 5 || n > 300 {
			errs = append(errs, fmt.Sprintf("the call setup timeout must be 5 to 300 seconds, got %q", v))
		}
		spec.TimeoutS = n
	}
	if id == TemplateLiveKitAgent {
		spec.Mode = schemas.VoiceRTCModeLiveKit
		server.Dispatch, server.AgentName, server.StartURL = str("dispatch"), str("agent_name"), str("start_url")
		server.StartTokenSecret = secret("start_token_secret", "start URL token secret")
		spec.LiveKit = server
	} else {
		spec.Mode = schemas.VoiceRTCModeSIP
		spec.SIP = &schemas.VoiceRTCSIP{Number: str("number"), TrunkAddress: str("trunk_address"), FromNumber: str("from_number"),
			TrunkUserSecret: secret("trunk_user_secret", "trunk username secret"), TrunkPasswordSecret: secret("trunk_password_secret", "trunk password secret"),
			Transport: str("transport"), TrunkID: str("trunk_id")}
		if server.URL != "" || server.APIKeySecret != "" || server.APISecretSecret != "" {
			spec.SIP.LiveKit = server
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	if err := voicertc.Check(spec); err != nil {
		return nil, rtcFormError(err)
	}
	return rtcBlockMap(spec), nil
}

// rtcBlockMap is the block as the agent YAML holds it, without empty fields.
func rtcBlockMap(s *schemas.VoiceRTCAgent) map[string]any {
	put := func(m map[string]any, k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	lk := func(l *schemas.VoiceRTCLiveKit) map[string]any {
		m := map[string]any{}
		put(m, "url", l.URL)
		put(m, "api_key_secret", l.APIKeySecret)
		put(m, "api_secret_secret", l.APISecretSecret)
		put(m, "dispatch", l.Dispatch)
		put(m, "agent_name", l.AgentName)
		put(m, "start_url", l.StartURL)
		put(m, "start_token_secret", l.StartTokenSecret)
		return m
	}
	block := map[string]any{"mode": s.Mode}
	if s.TimeoutS > 0 {
		block["timeout_s"] = s.TimeoutS
	}
	if s.LiveKit != nil {
		block["livekit"] = lk(s.LiveKit)
	}
	if s.SIP != nil {
		sip := map[string]any{}
		for k, v := range map[string]string{"number": s.SIP.Number, "trunk_address": s.SIP.TrunkAddress, "from_number": s.SIP.FromNumber,
			"trunk_user_secret": s.SIP.TrunkUserSecret, "trunk_password_secret": s.SIP.TrunkPasswordSecret, "transport": s.SIP.Transport, "trunk_id": s.SIP.TrunkID} {
			put(sip, k, v)
		}
		if s.SIP.LiveKit != nil {
			sip["livekit"] = lk(s.SIP.LiveKit)
		}
		block["sip"] = sip
	}
	return block
}

// rtcFormError words a voice_rtc check for the form's fields.
func rtcFormError(err error) error {
	r := strings.NewReplacer("voice_rtc.sip.livekit.", "", "voice_rtc.livekit.", "", "voice_rtc.sip.", "", "voice_rtc.", "",
		"api_key_secret", "the LiveKit API key", "api_secret_secret", "the LiveKit API secret", "agent_name", "the agent name",
		"start_url", "the start URL", "trunk_address", "the SIP trunk host", "from_number", "the caller ID number")
	return fmt.Errorf("%s", r.Replace(err.Error()))
}
