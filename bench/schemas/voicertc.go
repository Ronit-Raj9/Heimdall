package schemas

// Real-time voice agents (adapter voice_rtc, docs/voice-realtime.md): an
// agent reached over WebRTC in a LiveKit room or over a phone line through
// LiveKit SIP. The bench never runs the agent. Its bridge
// (bench/voice/rtcbridge.py) dials the task's voice caller and Heimdall's
// real-time voice gateway (bench/voice/rtcgateway.py, compose profile
// voice), which joins the room as the caller and brings the agent in, so
// the caller's timeline measures the call exactly as it does for an
// in-container voice agent.

// Real-time call modes (VoiceRTCAgent.Mode).
const (
	VoiceRTCModeLiveKit = "livekit" // a LiveKit room: LiveKit Agents, Pipecat (LiveKit transport) or any bot that joins rooms
	VoiceRTCModeSIP     = "sip"     // a phone number dialled through LiveKit SIP (any voice agent with a number: Vapi, Retell, a contact centre)
)

// How the agent is brought into the room (VoiceRTCLiveKit.Dispatch).
const (
	VoiceDispatchAgent   = "agent"   // LiveKit explicit agent dispatch by agent_name (LiveKit Agents workers)
	VoiceDispatchAuto    = "auto"    // the agent's worker joins every new room by itself
	VoiceDispatchWebhook = "webhook" // POST the room and a join token to the agent's start URL (Pipecat and custom bots)
)

// VoiceRTCModes and VoiceDispatches list the values in documentation order.
var (
	VoiceRTCModes   = []string{VoiceRTCModeLiveKit, VoiceRTCModeSIP}
	VoiceDispatches = []string{VoiceDispatchAgent, VoiceDispatchAuto, VoiceDispatchWebhook}
)

// VoiceRTCAgent is the voice_rtc block of an agent config.
type VoiceRTCAgent struct {
	// Mode is livekit or sip.
	Mode    string           `json:"mode" yaml:"mode"`
	LiveKit *VoiceRTCLiveKit `json:"livekit,omitempty" yaml:"livekit"`
	SIP     *VoiceRTCSIP     `json:"sip,omitempty" yaml:"sip"`
	// TimeoutS bounds the call setup (the room, the dispatch, the phone
	// answering). Default 30.
	TimeoutS int `json:"timeout_s,omitempty" yaml:"timeout_s"`
}

// VoiceRTCLiveKit is where the room is and how the agent joins it.
type VoiceRTCLiveKit struct {
	// URL is the LiveKit server, wss://... (LiveKit Cloud or the team's own
	// server). Empty uses Heimdall's LiveKit server (compose service
	// livekit), whose URL and keys the operator gives the team for its
	// worker.
	URL string `json:"url,omitempty" yaml:"url"`
	// APIKeySecret and APISecretSecret name the org secrets (Settings >
	// Secrets) holding the LiveKit API key and secret of URL. Required with
	// URL, unused without it.
	APIKeySecret    string `json:"api_key_secret,omitempty" yaml:"api_key_secret"`
	APISecretSecret string `json:"api_secret_secret,omitempty" yaml:"api_secret_secret"`
	// Dispatch is agent (default when AgentName is set), auto or webhook.
	Dispatch string `json:"dispatch,omitempty" yaml:"dispatch"`
	// AgentName is the LiveKit Agents worker's agent_name (dispatch agent).
	AgentName string `json:"agent_name,omitempty" yaml:"agent_name"`
	// StartURL receives {"livekit_url", "room", "token", "session"} as a
	// JSON POST (dispatch webhook); StartTokenSecret names the secret sent
	// as its bearer token.
	StartURL         string `json:"start_url,omitempty" yaml:"start_url"`
	StartTokenSecret string `json:"start_token_secret,omitempty" yaml:"start_token_secret"`
	// RoomPrefix starts every room name. Default heimdall.
	RoomPrefix string `json:"room_prefix,omitempty" yaml:"room_prefix"`
}

// VoiceRTCSIP is the phone number to call and the trunk that carries it.
type VoiceRTCSIP struct {
	// Number is the agent's phone number, E.164 (+14155550100). The
	// gateway dials only numbers on the operator's allowlist.
	Number string `json:"number" yaml:"number"`
	// TrunkID is an outbound trunk already set up on the LiveKit server
	// (LiveKit Cloud). Without it the gateway creates a temporary outbound
	// trunk from TrunkAddress and the credentials, and deletes it after
	// the call.
	TrunkID string `json:"trunk_id,omitempty" yaml:"trunk_id"`
	// TrunkAddress is the SIP provider's host (example.pstn.twilio.com).
	TrunkAddress string `json:"trunk_address,omitempty" yaml:"trunk_address"`
	// FromNumber is the caller id the trunk presents, E.164.
	FromNumber string `json:"from_number,omitempty" yaml:"from_number"`
	// TrunkUserSecret and TrunkPasswordSecret name the org secrets holding
	// the trunk's digest credentials.
	TrunkUserSecret     string `json:"trunk_user_secret,omitempty" yaml:"trunk_user_secret"`
	TrunkPasswordSecret string `json:"trunk_password_secret,omitempty" yaml:"trunk_password_secret"`
	// Transport is udp (default), tcp or tls.
	Transport string `json:"transport,omitempty" yaml:"transport"`
	// LiveKit is the LiveKit server with SIP that places the call. Empty
	// uses Heimdall's (compose services livekit and livekit-sip).
	LiveKit *VoiceRTCLiveKit `json:"livekit,omitempty" yaml:"livekit"`
}
