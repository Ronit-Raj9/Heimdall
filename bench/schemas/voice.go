package schemas

// X8 voice agents (docs/voice-agents.md, docs/voice-protocol.md). A voice
// task is a support task spoken over a call: a voice caller service (role
// user, bench/voice) phones the agent over WebSocket, speaks the simulated
// customer's lines and listens to the agent's audio, and its timeline is the
// bench's witness of the call. Grading stays on the final service state.

// Voice transports (VoiceSpec.Transport).
const (
	// VoiceTransportWebSocket is the heimdall-voice/1 protocol: the agent
	// dials the caller's /call WebSocket, 16 kHz mono pcm_s16le in binary
	// frames, JSON control messages (docs/voice-protocol.md). Agents that
	// speak the OpenAI Realtime event protocol run behind the bench's
	// bridge (bench/voice/realtimebridge.py), which speaks this transport.
	VoiceTransportWebSocket = "websocket"
)

// VoiceSpec marks a task as a voice call (Task.Voice). Every field is
// optional; empty takes the default named on it.
type VoiceSpec struct {
	// Caller is the service id of the voice caller (a role user service
	// running bench/voice/voicecaller.py). Default "customer".
	Caller string `json:"caller,omitempty" yaml:"caller"`
	// Transport is how the agent and the caller exchange audio. Default and
	// only value: websocket. Telephony (SIP, PSTN) is refused.
	Transport string `json:"transport,omitempty" yaml:"transport"`
	// Probes lists the voice probes (BENCH_VOICE_PROBE values: noise,
	// speed, interrupt, silent) this task's script can take. Empty allows
	// every voice probe; a condition with another voice probe is refused at
	// plan time for this task.
	Probes []string `json:"probes,omitempty" yaml:"probes"`
	// BargeInBoundMS is how soon the agent's audio must stop after the
	// caller starts talking over it for barge_in_handled. Default 1000.
	BargeInBoundMS int `json:"barge_in_bound_ms,omitempty" yaml:"barge_in_bound_ms"`
	// DeadAirMS is the silence on the agent's floor counted as dead air
	// (dead_air_s). Default 2000.
	DeadAirMS int `json:"dead_air_ms,omitempty" yaml:"dead_air_ms"`
}
