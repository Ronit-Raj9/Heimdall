package compat

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voiceconnect"
)

// Voice platform agents (adapter voice_platform, docs/voice-platforms.md).
// A voice task runs through the bench's bridge to the platform's call
// socket; a text task runs through the bench-driven conversation driver
// over voiceconnect: the pull launcher drives the turns and the server
// answers each one through the platform's chat API (handlers/pullvoice.go).
// That needs an address runs reach the server at (pull.url); a server
// without one (VoicePlatformText false) refuses text tasks with the reason,
// never failed at launch.

// VoicePlatformText is set by a server whose runs can reach it (pull.url,
// cmd/bifrost-bench/app.go), so its conversation driver runs text tasks for
// voice platform agents through voiceconnect.
var VoicePlatformText = false

// VoicePlatformTextPending is the reason a text task is refused meanwhile.
const VoicePlatformTextPending = "it is a text conversation, and this server has no address its runs can reach it at (pull.url), which text benchmarks of a hosted voice agent need; run a voice benchmark, or set pull.url"

// VoicePlatformAnswerOnly is the reason a text task whose answer is not
// the agent's reply is refused: a voice agent has no bench tools and
// changes no files.
const VoicePlatformAnswerOnly = "a voice agent answers in words only, and this task is graded on tool calls or code changes; run a benchmark graded on the agent's replies"

func isVoicePlatform(agent *schemas.AgentConfig) bool {
	return agent.Adapter == "voice_platform" || agent.VoicePlatform != nil
}

func voicePlatformTaskOK(agent *schemas.AgentConfig, t *schemas.Task, kind string) (bool, string) {
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
	if kind != schemas.OutputAnswer {
		return false, VoicePlatformAnswerOnly
	}
	return true, ""
}
