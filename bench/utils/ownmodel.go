package utils

import (
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// OwnModelLabel is how an arm that runs on the agent's own model is shown.
const OwnModelLabel = "agent's own model"

// OwnsModel reports whether an agent brings its own model, so an
// evaluation may leave the arm's model blank (lane X10, J2): a remote_http
// endpoint and an A2A agent pick their model themselves; an
// OpenAI-compatible endpoint does when its openai block or its config names
// the model it sends. Agents the bench starts (command, scripted) need the
// model an experiment gives them. It is pure.
func OwnsModel(a *schemas.AgentConfig) bool {
	switch {
	case a == nil:
		return false
	case IsOpenAIAgent(a):
		return a.OpenAI != nil && strings.TrimSpace(a.OpenAI.Model) != "" || strings.TrimSpace(a.Model) != ""
	case IsA2A(a):
		return true
	case a.Adapter == "remote_http" || a.Remote != nil:
		return true
	case a.Adapter == "voice_platform" || a.VoicePlatform != nil: // the platform runs the agent's model
		return true
	}
	return false
}
