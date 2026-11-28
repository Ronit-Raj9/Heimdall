package handlers

import (
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/schemas"
)

// runSkillsOf returns the skills a run installed and whether it read them.
// Installed skills come from the arm's manifest files for the run's
// condition: the planner records "<task>:<skills dir>/<skill>/<file>" for
// every skill file (adapters.AddSkillHashes), so the record holds what was
// copied in, not the agent's current settings. A skill counts as read only
// on evidence in the step timeline: a step whose path or summary names a
// file under that skill's folder (metrics.SkillPaths, the rule of the
// skill_files_read metric), or a skill tool step (Claude Code Skill,
// OpenCode skill) that names the skill. Nil when the arm is not in the
// manifest.
func runSkillsOf(m *schemas.Manifest, slot schemas.Slot, steps []schemas.StepRecord) *schemas.RunSkills {
	if m == nil {
		return nil
	}
	arm, ok := m.Arms[slot.Arm]
	if !ok {
		return nil
	}
	out := &schemas.RunSkills{Installed: []schemas.RunSkill{}}
	dir, err := adapters.SkillsDir(&arm.Config)
	if err != nil {
		return out
	}
	prefix := slot.Task + ":" + dir + "/"
	counts := map[string]int{}
	for k := range arm.Files[slot.Condition] {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		name, file, ok := strings.Cut(rest, "/")
		if !ok || name == "" || file == "" {
			continue
		}
		counts[name]++
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)
	roots := metrics.SkillRootsOf(dir)
	for _, n := range names {
		s := schemas.RunSkill{Name: n, Files: counts[n]}
		s.ReadStep, s.ReadPath, s.Read = firstSkillRead(n, roots, steps)
		out.Installed = append(out.Installed, s)
	}
	return out
}

// firstSkillRead finds the first step that read a file of skill name, or
// loaded it by name with a skill tool.
func firstSkillRead(name string, roots []string, steps []schemas.StepRecord) (int, string, bool) {
	for _, st := range steps {
		if metrics.IsSkillTool(st.ToolName) && skillNamed(st.Summary, name) {
			return st.Seq, "", true
		}
		for _, text := range []string{st.Path, st.Summary} {
			for _, p := range metrics.SkillPaths(text, roots) {
				if strings.HasPrefix(p, name+"/") {
					return st.Seq, p, true
				}
			}
		}
	}
	return 0, "", false
}

// skillNamed reports whether a skill tool step's summary names the skill as
// a whole word.
func skillNamed(summary, name string) bool {
	for _, f := range strings.FieldsFunc(summary, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) {
		if f == name {
			return true
		}
	}
	return false
}
