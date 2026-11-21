package verdict

import (
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Cross-experiment pairing (docs/control-evals.md): the same arm of two
// experiments whose manifests match except for realism and framing, paired
// by task under one condition. It answers "did the agent behave differently
// when it could tell it was tested" for experiments run apart, with the
// same exact sign-flip test the verdict uses.

// CrossMatch refuses two manifests that differ in more than realism and
// framing for arm: the suite, its lock, and the arm's config hash must
// match.
func CrossMatch(a, b *schemas.Manifest, arm string) error {
	if a == nil || b == nil {
		return errors.New("both manifests are needed")
	}
	var errs []error
	if a.SuiteID != b.SuiteID || a.SuiteVersion != b.SuiteVersion {
		errs = append(errs, fmt.Errorf("suites differ: %s@%s against %s@%s", a.SuiteID, a.SuiteVersion, b.SuiteID, b.SuiteVersion))
	}
	if a.SuiteLockHash != b.SuiteLockHash {
		errs = append(errs, errors.New("suite lock hashes differ"))
	}
	aa, okA := a.Arms[arm]
	bb, okB := b.Arms[arm]
	switch {
	case !okA || !okB:
		errs = append(errs, fmt.Errorf("arm %s is not in both experiments", arm))
	case aa.ConfigHash != bb.ConfigHash:
		errs = append(errs, fmt.Errorf("arm %s has different agent configs (%s against %s)", arm, aa.ConfigHash, bb.ConfigHash))
	}
	return errors.Join(errs...)
}

// PairedAcross is the paired effect of arm on metric under condition,
// experiment b minus experiment a, over the tasks both ran. Only counted
// runs enter. The effect names the experiments as its arms.
func PairedAcross(expA, expB string, a, b []schemas.SealedRun, arm, condition, metric string) *schemas.Effect {
	ia, ib := newRunIndex(a), newRunIndex(b)
	return pairedEffect(condition, metric, expA, expB, ia.cells(condition, arm, metric), ib.cells(condition, arm, metric), true)
}
