package schemas

// Voice agents hosted on a voice platform (adapter voice_platform,
// docs/voice-platforms.md). The bench never runs the agent: it talks to the
// platform, as text (each user turn of a support conversation sent through
// the platform's chat API, bench/voiceconnect) or as audio (the bench's voice
// caller bridged to the platform's call WebSocket, bench/voice/
// platformbridge.py, so the caller's timeline measures the call exactly as
// it does for an in-container voice agent).

// Voice platforms (VoicePlatformAgent.Platform).
const (
	VoicePlatformVapi       = "vapi"       // Vapi: POST /chat (text), POST /call with the vapi.websocket transport (audio)
	VoicePlatformRetell     = "retell"     // Retell: POST /create-chat, /create-chat-completion (text only)
	VoicePlatformElevenLabs = "elevenlabs" // ElevenLabs Agents: the conversation WebSocket, text_only or audio
	VoicePlatformWebSocket  = "websocket"  // the team's own wss:// endpoint in one of the framings below (audio only)
)

// Audio framings of a websocket platform (VoicePlatformAgent.Framing).
const (
	VoiceFramingTwilio     = "twilio"     // Twilio Media Streams JSON, mu-law 8 kHz (default)
	VoiceFramingVapi       = "vapi"       // binary pcm_s16le 16 kHz frames, JSON control (Vapi's websocket transport)
	VoiceFramingElevenLabs = "elevenlabs" // ElevenLabs conversation events, base64 pcm 16 kHz
)

// VoicePlatforms lists the platforms in documentation order.
var VoicePlatforms = []string{VoicePlatformVapi, VoicePlatformRetell, VoicePlatformElevenLabs, VoicePlatformWebSocket}

// VoiceFramings lists the framings of a websocket platform.
var VoiceFramings = []string{VoiceFramingTwilio, VoiceFramingVapi, VoiceFramingElevenLabs}

// VoicePlatformAgent is the voice_platform block of an agent config.
type VoicePlatformAgent struct {
	// Platform is vapi, retell, elevenlabs or websocket.
	Platform string `json:"platform" yaml:"platform"`
	// AgentID is the platform's id of the agent: the Vapi assistant id, the
	// Retell chat agent id, the ElevenLabs agent id. Not used by websocket.
	AgentID string `json:"agent_id,omitempty" yaml:"agent_id"`
	// APIKeySecret names the org secret (Settings > Secrets) holding the
	// platform's API key: a Vapi private key, a Retell API key, an
	// ElevenLabs API key (needed for a private agent), or the bearer token
	// a websocket endpoint checks. Empty sends no key.
	APIKeySecret string `json:"api_key_secret,omitempty" yaml:"api_key_secret"`
	// URL overrides the platform's API base (a regional ElevenLabs host,
	// a proxy). For websocket it is the endpoint itself, wss://... and it
	// is required.
	URL string `json:"url,omitempty" yaml:"url"`
	// Framing is the audio framing of a websocket endpoint: twilio
	// (default), vapi or elevenlabs. The other platforms use their own.
	Framing string `json:"framing,omitempty" yaml:"framing"`
	// TimeoutS bounds one agent turn (text) or the call setup (audio).
	// Default 60.
	TimeoutS int `json:"timeout_s,omitempty" yaml:"timeout_s"`
}
