package suite

import (
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Requires is the set of agent capabilities benchmark s needs (agent-first
// contract, section 1). A requires: list in suite.yaml wins. Otherwise it
// comes from the track: coding (the default), terminal and skills need
// shell; memory needs sessions; tool_use, mcp, support, safety and research
// need tools (research adds browser when the environment is a browser);
// computer_use needs desktop; browser needs browser; voice needs voice;
// ml_engineering needs shell and gpu. qa and artifact suites need nothing
// beyond a model call. The environment adds browser or desktop when its name
// says so. A task's GPU request is a machine need the GPU scheduler checks,
// not an agent capability. Nil s needs nothing.
func Requires(s *schemas.Suite) schemas.CapabilitySet {
	if s == nil {
		return schemas.CapabilitySet{}
	}
	if len(s.Requires) > 0 {
		return s.Requires.Normalize()
	}
	env := strings.ToLower(s.Environment)
	var set schemas.CapabilitySet
	switch strings.ToLower(strings.TrimSpace(s.Track)) {
	case "", schemas.TrackCoding, schemas.TrackTerminal, schemas.TrackSkills:
		set = append(set, schemas.CapShell)
	case schemas.TrackMemory:
		set = append(set, schemas.CapSessions)
	case schemas.TrackToolUse, schemas.TrackMCP, schemas.TrackSupport, schemas.TrackSafety:
		set = append(set, schemas.CapTools)
	case schemas.TrackResearch, "web":
		set = append(set, schemas.CapTools)
		if strings.Contains(env, "browser") {
			set = append(set, schemas.CapBrowser)
		}
	case schemas.TrackComputerUse, "desktop":
		set = append(set, schemas.CapDesktop)
	case schemas.TrackBrowser:
		set = append(set, schemas.CapBrowser)
	case schemas.TrackVoice:
		set = append(set, schemas.CapVoice)
	case schemas.TrackMLEngineering, "gpu":
		set = append(set, schemas.CapShell, schemas.CapGPU)
	}
	switch {
	case strings.Contains(env, "desktop"):
		set = append(set, schemas.CapDesktop)
	case strings.Contains(env, "browser") && s.Track != schemas.TrackResearch:
		set = append(set, schemas.CapBrowser)
	}
	return set.Normalize()
}
