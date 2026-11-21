package schemas

import "strings"

// Score mode used to run its one arm twice: the asked-for arm and a hidden
// copy labelled "<label> (repeat)" whose only difference was a marker env
// var, because experiments needed two arms. One-arm experiments replaced it;
// experiments frozen before then still hold the copy.

// legacyRepeatMarkers are the env vars that set the copy apart.
var legacyRepeatMarkers = []string{"env.HEIMDALL_QUICKRUN_ARM", "env.HEIMDALL_CHECK_ARM"}

// LegacyRepeatArm reports whether arm of m is such a hidden copy: its label
// ends in " (repeat)" and its overrides carry a marker env var. Readers that
// rank setups (the leaderboards) leave its runs out, since the setup it
// copies is already counted under its own arm.
func LegacyRepeatArm(m *Manifest, arm string) bool {
	if m == nil {
		return false
	}
	spec, ok := m.Experiment.Arms[arm]
	if !ok {
		return false
	}
	label := spec.Label
	if a, ok := m.Arms[arm]; ok && a.Label != "" {
		label = a.Label
	}
	if !strings.HasSuffix(label, " (repeat)") {
		return false
	}
	for _, k := range legacyRepeatMarkers {
		if _, ok := spec.Set[k]; ok {
			return true
		}
	}
	return false
}
