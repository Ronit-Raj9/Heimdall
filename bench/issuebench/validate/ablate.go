package validate

import (
	"context"
	"errors"
	"slices"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// MaxVerifiedAblations caps the hunk ablations run per issue.
const MaxVerifiedAblations = 4

// VerifyAblations runs each hunk ablation of c's fix (RankedAblations, at
// most limit) once at the gold state, the ablation in place of the fix, and
// keeps the ones that are really wrong: a FAIL_TO_PASS test does not pass,
// or a PASS_TO_PASS test fails. One that passes leaves out a
// behaviour-neutral hunk (a refactor) and is dropped, as is one that does
// not apply or times out. An error is returned only when the context ends.
func VerifyAblations(ctx context.Context, r Runner, env *issuebench.EnvSpec, c issuebench.Candidate, v *issuebench.Validation, lang issuebench.Language, limit int) (issuebench.AblationReport, error) {
	var rep issuebench.AblationReport
	if r == nil || lang == nil || v == nil || len(v.FailToPass) == 0 {
		return rep, nil
	}
	isTest := func(p string) bool { return slices.Contains(c.TestFiles, p) }
	tests := testFiles(c)
	if len(tests) == 0 {
		return rep, nil
	}
	for _, patch := range RankedAblations(c.FixPatch, isTest, limit) {
		rep.Tried++
		ac := c
		ac.FixPatch = patch
		raw, err := r.Run(ctx, env, &ac, Run{State: StateGold, Tests: tests, Command: lang.TestCommand(tests)})
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		if err != nil && (errors.Is(err, ErrPatchApply) || errors.Is(err, ErrTimeout)) {
			rep.Dropped++
			continue
		}
		res, perr := lang.ParseResults(raw)
		if err != nil || perr != nil || !ablationWrong(v, res) {
			rep.Dropped++
			continue
		}
		rep.Kept++
		rep.Patches = append(rep.Patches, normalisePatch(patch))
	}
	return rep, nil
}

func ablationWrong(v *issuebench.Validation, res map[string]issuebench.TestStatus) bool {
	for _, id := range v.FailToPass {
		if res[id] != issuebench.TestPass {
			return true
		}
	}
	for _, id := range v.PassToPass {
		if s, ok := res[id]; ok && (s == issuebench.TestFail || s == issuebench.TestError) {
			return true
		}
	}
	return false
}
