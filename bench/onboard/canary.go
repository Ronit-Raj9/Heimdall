package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Canary in the draft (lane H07). Publish stamps the benchmark's canary into
// every instruction when a held-out task is published. Stamping after the
// readiness run changed the task files, so no readiness record carried and
// every task landed on the recheck list. StampCanary stamps the same canary
// into the draft before readiness, so publish finds the files already
// stamped and the records still match.

// HasHeldOut reports whether any task of the set is held out.
func (s *DraftSet) HasHeldOut() bool {
	for _, t := range s.Tasks {
		if t.HeldOut {
			return true
		}
	}
	return false
}

// draftCanary is the canary the draft's benchmark will carry: the target
// benchmark's own canary when it has one, else the one already in the
// draft's suite.yaml. Empty when neither has one.
func draftCanary(env *Env, s *DraftSet) (string, error) {
	for _, p := range []string{filepath.Join(env.Benchmarks(), s.Suite, suite.SuiteFile), filepath.Join(s.SuiteDir(), suite.SuiteFile)} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if g := utils.SuiteYAMLCanary(data); g != "" {
			if !utils.ValidCanary(g) {
				return "", fmt.Errorf("%s: canary %q is not a lowercase GUID", p, g)
			}
			return g, nil
		}
	}
	return "", nil
}

// StampCanary stamps the benchmark's canary into every task instruction of
// the draft when any task is held out, writing the canary into the draft's
// suite.yaml (a new one when the target benchmark has none yet). It returns
// the canary and the ids of tasks whose files changed; their readiness
// records no longer match, so they are cleared. With no held-out task it
// does nothing. The caller saves the set.
func (s *DraftSet) StampCanary(ctx context.Context, env *Env) (string, []string, error) {
	if !s.HasHeldOut() {
		return "", nil, nil
	}
	guid, err := draftCanary(env, s)
	if err != nil {
		return "", nil, err
	}
	if guid == "" {
		guid = utils.NewCanary()
	}
	yml := filepath.Join(s.SuiteDir(), suite.SuiteFile)
	data, err := os.ReadFile(yml)
	if err != nil {
		return "", nil, err
	}
	if out, changed := utils.SetSuiteYAMLCanary(data, guid); changed {
		if err := os.WriteFile(yml, out, 0o644); err != nil {
			return "", nil, err
		}
	}
	loaders := suite.Builtin(s.BenchRoot())
	if len(loaders) == 0 {
		return "", nil, fmt.Errorf("draft %s: no suite loader", s.ID)
	}
	st, err := loaders[0].Load(ctx, s.Suite)
	if err != nil {
		return "", nil, fmt.Errorf("load the draft: %w", err)
	}
	files, err := suite.StampCanary(st, guid)
	if err != nil {
		return "", nil, err
	}
	seen := map[string]bool{}
	var changed []string
	for _, f := range files {
		if !f.Changed || seen[f.Task] {
			continue
		}
		seen[f.Task] = true
		changed = append(changed, f.Task)
		if t := s.Task(f.Task); t != nil {
			t.Readiness, t.CatchRate, t.Reason = nil, nil, ""
			dir := filepath.Join(s.SuiteDir(), "tasks", t.ID)
			if m, err := readTaskMeta(dir); err == nil {
				t.Instruction = cut(instructionText(dir, m), 4000)
			}
		}
	}
	return guid, changed, nil
}

// ensureCanaryAs makes the benchmark at target carry a canary, preferring
// want (the draft's) when the benchmark has none, so the tasks stamped in
// the draft need no new stamp.
func ensureCanaryAs(target, want string) (string, error) {
	if want == "" || !utils.ValidCanary(want) {
		g, _, err := suite.EnsureCanary(target)
		return g, err
	}
	path := filepath.Join(target, suite.SuiteFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if g := utils.SuiteYAMLCanary(data); g != "" {
		g, _, err := suite.EnsureCanary(target)
		return g, err
	}
	out, _ := utils.SetSuiteYAMLCanary(data, want)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", err
	}
	return want, nil
}

// draftCanaryOwn is the canary in the draft's own suite.yaml, or empty.
func draftCanaryOwn(s *DraftSet) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.SuiteDir(), suite.SuiteFile))
	if err != nil {
		return "", nil
	}
	g := utils.SuiteYAMLCanary(data)
	if g != "" && !utils.ValidCanary(g) {
		return "", fmt.Errorf("draft suite.yaml: canary %q is not a lowercase GUID", g)
	}
	return g, nil
}
