package suite

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// W8 judge calibration (docs/judge-calibration.md).

// lintJudgeAuditor names what is wrong with a judge's auditor, "" when
// there is none or it is fine: it must come from another provider than the
// judge's (a second opinion from the same vendor is not independent), and
// its votes must be odd, as the judge's.
func lintJudgeAuditor(j *schemas.JudgeSpec) string {
	if j == nil || j.Auditor == nil {
		return ""
	}
	a := j.Auditor
	switch {
	case a.Provider == j.Provider:
		return fmt.Sprintf("judge.auditor.provider: %s is the judge's own provider; the auditor must come from another vendor", a.Provider)
	case a.Votes > 0 && a.Votes%2 == 0:
		return fmt.Sprintf("judge.auditor.votes: %d is even; the majority needs an odd number of votes", a.Votes)
	}
	return ""
}
