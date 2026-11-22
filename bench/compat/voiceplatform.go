package compat

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voiceconnect"
)

// Voice platform agents (adapter voice_platform, docs/voice-platforms.md).
// A voice task runs through the bench's bridge to the platform's call
// socket; a text task runs through the bench-driven conversation driver
// over voiceconnect. Until a server wires that driver (VoicePlatformText),
// text tasks are refused with the reason, never failed at launch.

// VoicePlatformText is set by a server whose conversation driver runs text
// tasks for voice platform agents through voiceconnect.
var VoicePlatformText = false

// VoicePlatformTextPending is the reason a text task is refused meanwhile.
const VoicePlatformTextPending = "it is a text conversation, and text benchmarks for a hosted voice agent are not available on this server yet; run a voice benchmark"

func isVoicePlatform(agent *schemas.AgentConfig) bool {
	return agent.Adapter == "voice_platform" || agent.VoicePlatform != nil
}

func voicePlatformTaskOK(agent *schemas.AgentConfig, t *schemas.Task) (bool, string) {
	p := ""
	if agent.VoicePlatform != nil {
		p = agent.VoicePlatform.Platform
	}
	name := voiceconnect.PlatformName(p)
	if t.Voice != nil {
		if !voiceconnect.SupportsAudio(p) {
			return false, "it is a voice call, and a " + name + " agent can only be reached through its chat API; run a text benchmark"
		}
		return true, ""
	}
	if !voiceconnect.SupportsText(p) {
		return false, "it is a text conversation, and a voice WebSocket endpoint only takes calls"
	}
	if !VoicePlatformText {
		return false, VoicePlatformTextPending
	}
	return true, ""
}
