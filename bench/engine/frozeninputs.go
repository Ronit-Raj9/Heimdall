package engine

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// verifyFrozenInputs re-hashes, before a resumed experiment runs another
// slot here, every file the manifest froze for its arms and their team
// members (ArmManifest.Files, MemberManifest.Files), and refuses the run on
// any difference. Those files can live outside the suite (an arm's or a
// member's notes.user_file, project skills), so neither the manifest hash nor
// the suite lock binds them; without this check later slots would run
// changed bytes under the same frozen manifest (QC2-09-03). The conditions
// and tasks are taken from each arm's own slots, as the planner laid them.
func (s *Scheduler) verifyFrozenInputs(m *schemas.Manifest, suite *schemas.Suite) error {
	p := NewPlanner(s.reg, s.benchRoot, s.logger)
	arms := map[string]*schemas.ArmManifest{}
	for id := range m.Arms {
		a := m.Arms[id]
		arms[id] = &a
	}
	if m.Compatibility != nil {
		arms[CompatArm] = m.Compatibility
	}
	var problems []string
	for _, id := range sortedKeys(arms) {
		arm := arms[id]
		conds := frozenConds(m, suite, id)
		if len(conds) == 0 {
			continue
		}
		if arm.Files != nil {
			files, err := p.layFiles(&arm.Config, suite, conds)
			if err != nil {
				problems = append(problems, fmt.Sprintf("arm %s: %v", id, err))
			} else if d := fileMapDiff(files, arm.Files); d != "" {
				problems = append(problems, fmt.Sprintf("arm %s: %s", id, d))
			}
		}
		for _, member := range arm.Config.Team {
			frozen, ok := arm.Members[member.ID]
			if member.Config == nil || !ok || frozen.Files == nil {
				continue
			}
			files, err := p.layFiles(member.Config, suite, conds)
			if err != nil {
				problems = append(problems, fmt.Sprintf("arm %s, team member %s: %v", id, member.ID, err))
			} else if d := fileMapDiff(files, frozen.Files); d != "" {
				problems = append(problems, fmt.Sprintf("arm %s, team member %s: %s", id, member.ID, d))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("frozen input files changed since the freeze: %s", strings.Join(problems, "; "))
	}
	return nil
}

// frozenConds rebuilds the conditions an arm runs from its slots: each
// condition with the tasks the arm's slots name, in slot order.
func frozenConds(m *schemas.Manifest, suite *schemas.Suite, armID string) []condPlan {
	var out []condPlan
	at := map[string]int{}
	seen := map[string]bool{}
	for _, sl := range m.Slots {
		if sl.Arm != armID {
			continue
		}
		i, ok := at[sl.Condition]
		if !ok {
			cp := condPlan{id: sl.Condition}
			if sl.Condition != schemas.ConditionClean {
				cp.probe = suite.ProbeByID(sl.Condition)
			}
			out = append(out, cp)
			i = len(out) - 1
			at[sl.Condition] = i
		}
		if k := sl.Condition + "\x00" + sl.Task; !seen[k] {
			seen[k] = true
			out[i].tasks = append(out[i].tasks, sl.Task)
		}
	}
	return out
}

// fileMapDiff names the condition files that differ between the files laid
// now and the frozen ones, for the conditions laid now; "" when they match.
func fileMapDiff(now, frozen map[string]map[string]string) string {
	var diffs []string
	for _, cond := range sortedKeys(now) {
		want := frozen[cond]
		got := now[cond]
		if maps.Equal(got, want) {
			continue
		}
		var paths []string
		for p, h := range got {
			if want[p] != h {
				paths = append(paths, p)
			}
		}
		for p := range want {
			if _, ok := got[p]; !ok {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		diffs = append(diffs, fmt.Sprintf("condition %s: %s", cond, strings.Join(paths, ", ")))
	}
	return strings.Join(diffs, "; ")
}
