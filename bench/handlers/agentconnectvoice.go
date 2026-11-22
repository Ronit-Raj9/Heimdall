package handlers

// Connect a voice agent hosted on a voice platform (docs/voice-platforms.md):
// the gallery forms for Vapi, Retell, ElevenLabs Agents and a team's own call
// WebSocket. Each renders an agent with adapter voice_platform and a
// voice_platform block; the API key is the name of a Settings > Secrets
// entry, never the key itself.

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voiceconnect"
)

// Gallery ids of the voice platform forms.
const (
	TemplateVapi       = "vapi-assistant"
	TemplateRetell     = "retell-agent"
	TemplateElevenLabs = "elevenlabs-agent"
	TemplateVoiceWS    = "voice-websocket"
)

var voiceTimeoutParam = TemplateParam{Name: "timeout_s", Label: "Timeout (seconds)", Kind: ParamNumber, Default: "60", Group: "advanced",
	Help: "How long Heimdall waits for one reply in a text conversation, or for the call to connect."}

func voiceAPIBaseParam(def string) TemplateParam {
	return TemplateParam{Name: "url", Label: "API base URL", Placeholder: def, Group: "advanced",
		Help: "Leave empty for " + def + ". Set it for a regional host or your own proxy."}
}

var voiceTemplates = []AgentTemplate{
	{ID: TemplateVapi, Name: "Connect your Vapi assistant", Category: TemplateCategoryBYO, Adapter: "voice_platform", Providers: abAnyProvider,
		DefaultModel: "hosted", source: "inline", Docs: "docs/voice-platforms.md",
		Description: "A voice assistant on Vapi. Heimdall calls it over Vapi's WebSocket transport for voice benchmarks and chats with it for text benchmarks.",
		Params: []TemplateParam{
			{Name: "agent_id", Label: "Assistant ID", Placeholder: "8c1e2c4a-...", Required: true,
				Help: "Dashboard > Assistants > your assistant > the ID next to its name."},
			{Name: "api_key_secret", Label: "Private API key", Kind: ParamSecret, Placeholder: "VAPI_PRIVATE_KEY", Required: true, Group: "auth",
				Help: "Name of a secret in Settings > Secrets holding your Vapi private key (not the public key)."},
			voiceAPIBaseParam(voiceconnect.VapiBase),
			voiceTimeoutParam,
		}},
	{ID: TemplateRetell, Name: "Connect your Retell agent", Category: TemplateCategoryBYO, Adapter: "voice_platform", Providers: abAnyProvider,
		DefaultModel: "hosted", source: "inline", Docs: "docs/voice-platforms.md",
		Description: "A Retell chat agent, through Retell's chat API: text benchmarks only. Retell calls run over its own WebRTC and phone stacks, which Heimdall does not dial.",
		Params: []TemplateParam{
			{Name: "agent_id", Label: "Chat agent ID", Placeholder: "agent_...", Required: true,
				Help: "The id of a chat agent (Dashboard > Agents). A voice-only agent needs a chat agent with the same prompt."},
			{Name: "api_key_secret", Label: "API key", Kind: ParamSecret, Placeholder: "RETELL_API_KEY", Required: true, Group: "auth",
				Help: "Name of a secret in Settings > Secrets holding your Retell API key."},
			voiceAPIBaseParam(voiceconnect.RetellBase),
			voiceTimeoutParam,
		}},
	{ID: TemplateElevenLabs, Name: "Connect your ElevenLabs agent", Category: TemplateCategoryBYO, Adapter: "voice_platform", Providers: abAnyProvider,
		DefaultModel: "hosted", source: "inline", Docs: "docs/voice-platforms.md",
		Description: "An ElevenLabs Agents voice agent. Heimdall talks to it over its conversation WebSocket: audio for voice benchmarks, text mode for text benchmarks.",
		Params: []TemplateParam{
			{Name: "agent_id", Label: "Agent ID", Placeholder: "agent_01...", Required: true,
				Help: "Agents > your agent > the ID under its name."},
			{Name: "api_key_secret", Label: "API key", Kind: ParamSecret, Placeholder: "ELEVENLABS_API_KEY", Group: "auth",
				Help: "Name of a secret in Settings > Secrets holding your ElevenLabs API key. Needed for a private agent (Heimdall asks for a signed URL); leave empty for a public one."},
			voiceAPIBaseParam(voiceconnect.ElevenLabsBase),
			voiceTimeoutParam,
		}},
	{ID: TemplateVoiceWS, Name: "Connect your voice WebSocket", Category: TemplateCategoryBYO, Adapter: "voice_platform", Providers: abAnyProvider,
		DefaultModel: "hosted", source: "inline", Docs: "docs/voice-platforms.md",
		Description: "Your own voice server (Pipecat, LiveKit, a custom stack) that answers phone calls over a WebSocket. Heimdall calls it like Twilio does: voice benchmarks only.",
		Params: []TemplateParam{
			{Name: "url", Label: "WebSocket URL", Placeholder: "wss://voice.example.com/twilio", Required: true,
				Help: "The URL Twilio's <Stream> (or your telephony provider) connects to."},
			{Name: "framing", Label: "Audio format", Kind: ParamSelect, Options: schemas.VoiceFramings, Default: schemas.VoiceFramingTwilio,
				Help: "twilio: Twilio Media Streams JSON, mu-law 8 kHz. vapi: raw 16-bit PCM at 16 kHz in binary messages. elevenlabs: ElevenLabs conversation events."},
			{Name: "api_key_secret", Label: "Auth token", Kind: ParamSecret, Placeholder: "VOICE_WS_TOKEN", Group: "auth",
				Help: "Name of a secret in Settings > Secrets, sent as Authorization: Bearer <token> on the upgrade. Leave empty if the endpoint needs no auth."},
			voiceTimeoutParam,
		}},
}

func init() {
	// After "Connect your OpenAI-compatible endpoint", before "Wrap my existing app".
	at := len(agentTemplates)
	for i, t := range agentTemplates {
		if t.ID == "wrap-app" {
			at = i
			break
		}
	}
	out := append([]AgentTemplate{}, agentTemplates[:at]...)
	out = append(out, voiceTemplates...)
	agentTemplates = append(out, agentTemplates[at:]...)
}

// connectVoiceBlock is the voice_platform block of a voice form, or nil when
// id is not one.
func connectVoiceBlock(id string, params map[string]string) (map[string]any, error) {
	platform := map[string]string{TemplateVapi: schemas.VoicePlatformVapi, TemplateRetell: schemas.VoicePlatformRetell,
		TemplateElevenLabs: schemas.VoicePlatformElevenLabs, TemplateVoiceWS: schemas.VoicePlatformWebSocket}[id]
	if platform == "" {
		return nil, nil
	}
	get := func(name string) (string, error) {
		v := strings.TrimSpace(params[name])
		if len(v) > 2048 || strings.ContainsAny(v, "\n\r") {
			return "", fmt.Errorf("%s must be one line under 2048 characters", name)
		}
		return v, nil
	}
	spec := &schemas.VoicePlatformAgent{Platform: platform}
	var err error
	if spec.AgentID, err = get("agent_id"); err != nil {
		return nil, err
	}
	if spec.URL, err = get("url"); err != nil {
		return nil, err
	}
	if spec.APIKeySecret, err = connectSecretName(params, "api_key_secret", "API key secret"); err != nil {
		return nil, err
	}
	if platform == schemas.VoicePlatformWebSocket {
		if spec.Framing, err = get("framing"); err != nil {
			return nil, err
		}
		if spec.Framing == "" {
			spec.Framing = schemas.VoiceFramingTwilio
		}
	}
	timeout := 0
	if strings.TrimSpace(params["timeout_s"]) != "" {
		if timeout, err = connectTimeout(params); err != nil {
			return nil, err
		}
	}
	spec.TimeoutS = timeout
	if err := voiceconnect.Check(spec); err != nil {
		return nil, voiceFormError(err)
	}
	block := map[string]any{"platform": platform}
	for k, v := range map[string]string{"agent_id": spec.AgentID, "api_key_secret": spec.APIKeySecret, "url": spec.URL, "framing": spec.Framing} {
		if v != "" {
			block[k] = v
		}
	}
	if timeout > 0 {
		block["timeout_s"] = timeout
	}
	return block, nil
}

// voiceFormError words a voice_platform check for the form's fields.
func voiceFormError(err error) error {
	r := strings.NewReplacer("voice_platform.api_key_secret", "the API key", "voice_platform.agent_id", "the agent ID",
		"voice_platform.url", "the URL", "voice_platform.framing", "the audio format", "voice_platform.timeout_s", "the timeout")
	return fmt.Errorf("%s", r.Replace(err.Error()))
}
