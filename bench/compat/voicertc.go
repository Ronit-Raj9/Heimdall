package compat

import "github.com/maximhq/bifrost/bench/schemas"

// Real-time voice agents (adapter voice_rtc, docs/voice-realtime.md): an
// agent in a LiveKit room or on a phone number is reached only by a call,
// so only voice tasks are compatible; a text task is refused with the
// reason, never failed at launch.

// VoiceRTCTextReason is why a text task is refused.
const VoiceRTCTextReason = "it is a text conversation, and a real-time voice agent (LiveKit or a phone number) is reached only by a call; run a voice benchmark"

func isVoiceRTC(agent *schemas.AgentConfig) bool {
	return agent.Adapter == "voice_rtc" || agent.VoiceRTC != nil
}

func voiceRTCTaskOK(t *schemas.Task) (bool, string) {
	if t.Voice == nil {
		return false, VoiceRTCTextReason
	}
	return true, ""
}
