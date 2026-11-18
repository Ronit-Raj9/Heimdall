package calibration

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// InstrumentName is the verdict instrument this package adds to a judged
// experiment's analysis.
const InstrumentName = "judge_calibration"

// Default thresholds (docs/judge-calibration.md). The bounds are the lower
// ends of exact 95% intervals, so they need enough labels to pass: with
// every label matched, agreement's lower bound passes 0.75 from 13 labels.
const (
	DefaultMinAgreement = 0.75
	DefaultMinTNR       = 0.60
	DefaultMinLabels    = 30
	DefaultMaxAgeDays   = 90
)

// scenarioJudgeKey is the manifest scenario input a judged suite freezes
// (engine/scenarioplan.go).
const scenarioJudgeKey = "suite.judge"

// Thresholds applies the defaults to a policy file's judge_calibration
// section (nil: all defaults). source names where they came from.
func Thresholds(p *schemas.JudgeCalibrationPolicy, source string) schemas.JudgeCalibrationThresholds {
	t := schemas.JudgeCalibrationThresholds{
		MinAgreement: DefaultMinAgreement, MinTNR: DefaultMinTNR, MinLabels: DefaultMinLabels, MaxAgeDays: DefaultMaxAgeDays, Source: "defaults",
	}
	if p == nil {
		return t
	}
	if source != "" {
		t.Source = source
	}
	if p.MinAgreement != nil {
		t.MinAgreement = *p.MinAgreement
	}
	if p.MinTNR != nil {
		t.MinTNR = *p.MinTNR
	}
	if p.MinLabels > 0 {
		t.MinLabels = p.MinLabels
	}
	if p.MaxAgeDays > 0 {
		t.MaxAgeDays = p.MaxAgeDays
	}
	return t
}

// Input is what the instrument reads besides the analysis.
type Input struct {
	Manifest     *schemas.Manifest
	Runs         []schemas.SealedRun // the runs the verdict read, missing slots included
	Records      *RecordSet          // the suite's records; nil when the suite folder is not found
	SetSHA256    string              // the current labels.jsonl hash, "" when there is none
	Policy       *schemas.JudgeCalibrationPolicy
	PolicySource string
	Reviews      map[string][]schemas.Review // run id -> valid reviews, for contested runs
	Now          time.Time
}

// Apply adds the judge_calibration instrument to the analysis of a judged
// experiment and acts on it; an experiment that is not judged is left
// exactly as it is. A judged experiment's verdict:
//
//   - no record for the exact judge model and every prompt template the
//     runs used: "judge not calibrated", a directional decision is capped at
//     INCONCLUSIVE, unless the experiment set allow_uncalibrated_judge (then
//     only labelled);
//   - a record with fewer human labels than the minimum: a warning with the
//     interval, and the same cap when its bounds do not pass yet;
//   - enough labels and the lower bound of agreement or TNR under its
//     threshold: INVALID "judge unreliable";
//   - otherwise calibrated, with drift reminders as warnings.
//
// It also counts contested runs, says when the judge's error rate exceeds
// the effect claimed, and puts the Rogan-Gladen corrected judged rate beside
// each arm's raw one (report only).
func Apply(an *schemas.Analysis, in Input) {
	if an == nil || !judgedExperiment(in.Manifest, in.Runs) {
		return
	}
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	th := Thresholds(in.Policy, in.PolicySource)
	check := schemas.InstrumentCheck{Name: InstrumentName, Threshold: thresholdText(th)}
	model, templates, judged, served := judgedRuns(in.Runs)
	if judged == 0 {
		check.Status = schemas.InstrumentNotApplicable
		check.Detail = "no counted run was graded by the judge"
		an.Verdict.Instruments = append(an.Verdict.Instruments, check)
		return
	}
	jc := &schemas.JudgeCalibrationVerdict{Model: model, Templates: templates, Thresholds: th, JudgedRuns: judged}
	for _, r := range in.Runs {
		if r.Slot.Counted && Contested(&r, in.Reviews[r.Run.RunID]) {
			jc.ContestedRuns++
		}
	}
	allowed := in.Manifest != nil && in.Manifest.Experiment.Verdict.AllowUncalibratedJudge
	rec, newest := in.Records.Covering(model, templates)
	capVerdict := false
	switch {
	case rec == nil:
		jc.State = schemas.CalibrationStateUncalibrated
		jc.Detail = fmt.Sprintf("judge not calibrated: no calibration record for model %s with the prompt the runs used (%s)", model, shortList(templates))
		if newest != nil {
			jc.Detail += fmt.Sprintf("; the newest record for the model, %s, does not cover it", newest.RecordID)
		}
		capVerdict = true
	case rec.HumanLabels == 0:
		fillRecord(jc, rec)
		jc.State = schemas.CalibrationStateUncalibrated
		jc.Detail = fmt.Sprintf("judge not calibrated: record %s has no human labels (%d admission-control seeds only)", rec.RecordID, rec.SeedItems)
		capVerdict = true
	default:
		fillRecord(jc, rec)
		bounds, pass := boundsText(rec, th)
		switch {
		case rec.HumanLabels < th.MinLabels:
			jc.State = schemas.CalibrationStateFewLabels
			jc.Detail = fmt.Sprintf("judge calibrated on %d human labels, fewer than %d: %s", rec.HumanLabels, th.MinLabels, bounds)
			if !pass {
				jc.Detail += "; too few labels to show the judge reliable"
				capVerdict = true
			}
		case !pass:
			jc.State = schemas.CalibrationStateUnreliable
			jc.Detail = fmt.Sprintf("judge unreliable: %s (record %s, %d human labels)", bounds, rec.RecordID, rec.HumanLabels)
		default:
			jc.State = schemas.CalibrationStateCalibrated
			jc.Detail = fmt.Sprintf("judge calibrated on %d human labels: %s (record %s)", rec.HumanLabels, bounds, rec.RecordID)
		}
		jc.Reminders = Reminders(rec, th, in.SetSHA256, served, in.Now)
	}
	if in.Records != nil && len(in.Records.Problems) > 0 {
		jc.Reminders = append(jc.Reminders, fmt.Sprintf("%d calibration record problem(s): %s", len(in.Records.Problems), in.Records.Problems[0]))
	}
	if capVerdict && allowed {
		jc.State = schemas.CalibrationStateAllowed
		jc.Detail += "; allowed by allow_uncalibrated_judge, so the verdict is labelled, not capped"
		capVerdict = false
	}
	if jc.ContestedRuns > 0 {
		jc.Detail += fmt.Sprintf("; %d of %d judged runs contested (in the review queue)", jc.ContestedRuns, judged)
	}
	jc.ErrorOverEffect = errorOverEffect(rec, an.Verdict.Effect)
	jc.BiasAdjusted = biasAdjusted(in.Runs, an.Verdict.Primary.Condition, rec)

	switch jc.State {
	case schemas.CalibrationStateCalibrated:
		check.Status = schemas.InstrumentPass
		if len(jc.Reminders) > 0 {
			check.Status = schemas.InstrumentWarn
		}
	case schemas.CalibrationStateUnreliable:
		check.Status = schemas.InstrumentFail
	case schemas.CalibrationStateUncalibrated:
		check.Status = schemas.InstrumentUnknown
	default:
		check.Status = schemas.InstrumentWarn
	}
	parts := []string{jc.Detail}
	if jc.ErrorOverEffect != "" {
		parts = append(parts, jc.ErrorOverEffect)
	}
	parts = append(parts, jc.Reminders...)
	check.Detail = strings.Join(parts, "; ")
	an.Verdict.Instruments = append(an.Verdict.Instruments, check)

	v := &an.Verdict
	switch {
	case jc.State == schemas.CalibrationStateUnreliable:
		v.InvalidReasons = append(v.InvalidReasons, jc.Detail)
		v.Decision, v.Action = schemas.DecisionInvalid, schemas.ActionFixHarness
	case capVerdict && (v.Decision == schemas.DecisionImproved || v.Decision == schemas.DecisionRegressed):
		jc.CappedFrom = v.Decision
		v.Action = schemas.ActionNoDecision
		if v.Decision == schemas.DecisionRegressed {
			v.Action = schemas.ActionHold
		}
		v.Decision = schemas.DecisionInconclusive
	}
	an.JudgeCalibration = jc
}

// judgedExperiment reports whether the experiment froze a judge or any
// counted run carries a judge record. A verified suite has neither.
func judgedExperiment(m *schemas.Manifest, runs []schemas.SealedRun) bool {
	if m != nil && m.Scenario[scenarioJudgeKey] != "" {
		return true
	}
	for _, r := range runs {
		if r.Slot.Counted && r.Grade != nil && r.Grade.Judge != nil {
			return true
		}
	}
	return false
}

// judgedRuns returns the judge model of the counted judged runs (the most
// common one), every prompt template hash they used (sorted), how many
// there are, and the served model ids they recorded.
func judgedRuns(runs []schemas.SealedRun) (model string, templates []string, n int, served []string) {
	models := map[string]int{}
	tmpl := map[string]bool{}
	srv := map[string]bool{}
	for _, r := range runs {
		if !r.Slot.Counted || r.Grade == nil || r.Grade.Judge == nil || len(r.Grade.Judge.Votes) == 0 {
			continue
		}
		n++
		models[r.Grade.Judge.Model]++
		if r.Grade.Judge.PromptSHA256 != "" {
			tmpl[r.Grade.Judge.PromptSHA256] = true
		}
		if r.Grade.Judge.ServedModel != "" {
			srv[r.Grade.Judge.ServedModel] = true
		}
	}
	best := -1
	for _, m := range sortedSet(boolKeys(models)) {
		if models[m] > best {
			model, best = m, models[m]
		}
	}
	return model, sortedSet(tmpl), n, sortedSet(srv)
}

func boolKeys(m map[string]int) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func fillRecord(jc *schemas.JudgeCalibrationVerdict, rec *schemas.CalibrationRecord) {
	at := rec.CreatedAt
	jc.RecordID, jc.RecordHash, jc.RecordAt = rec.RecordID, rec.Hash, &at
	jc.HumanLabels = rec.HumanLabels
	a, t, f := rec.Human.Agreement, rec.Human.TNR, rec.Human.FPR
	jc.Agreement, jc.TNR, jc.FPR, jc.Kappa = &a, &t, &f, rec.Human.Kappa
}

// boundsText describes agreement, TNR and the false-positive bound against
// the thresholds and reports whether both bounds pass. An unmeasured TNR
// (no human label is incorrect) does not pass: the judge's false positives
// were never tested.
func boundsText(rec *schemas.CalibrationRecord, th schemas.JudgeCalibrationThresholds) (string, bool) {
	h := rec.Human
	pass := true
	var parts []string
	if h.Agreement.Lo == nil || *h.Agreement.Lo < th.MinAgreement {
		pass = false
	}
	parts = append(parts, fmt.Sprintf("agreement %s (lower bound must be at least %.2f)", rateText(h.Agreement), th.MinAgreement))
	if h.TNR.Lo == nil {
		pass = false
		parts = append(parts, "TNR not measured: no human label is incorrect")
	} else {
		if *h.TNR.Lo < th.MinTNR {
			pass = false
		}
		parts = append(parts, fmt.Sprintf("TNR %s (lower bound must be at least %.2f)", rateText(h.TNR), th.MinTNR))
	}
	if h.FPR.Hi != nil {
		parts = append(parts, fmt.Sprintf("false-positive rate at most %.1f%%", 100**h.FPR.Hi))
	}
	if h.Kappa != nil {
		parts = append(parts, fmt.Sprintf("kappa %.2f", *h.Kappa))
	}
	return strings.Join(parts, ", "), pass
}

// rateText is "0.900 [0.735, 0.979] (27/30)".
func rateText(r schemas.CalibrationRate) string {
	if r.Value == nil {
		return fmt.Sprintf("not measured (%d/%d)", r.K, r.N)
	}
	return fmt.Sprintf("%.3f [%.3f, %.3f] (%d/%d)", *r.Value, *r.Lo, *r.Hi, r.K, r.N)
}

func thresholdText(t schemas.JudgeCalibrationThresholds) string {
	return fmt.Sprintf("a record for the exact judge and prompt; agreement lower bound >= %.2f, TNR lower bound >= %.2f; %d+ human labels (%s)",
		t.MinAgreement, t.MinTNR, t.MinLabels, t.Source)
}

func shortList(hashes []string) string {
	out := make([]string, len(hashes))
	for i, h := range hashes {
		if len(h) > 12 {
			h = h[:12]
		}
		out[i] = "prompt " + h
	}
	if len(out) == 0 {
		return "no prompt hash recorded"
	}
	return strings.Join(out, ", ")
}

// Reminders are the drift warnings of a record: older than MaxAgeDays, the
// gateway serving the judge alias as a model id the calibration never saw,
// or labels added or changed since it was made.
func Reminders(rec *schemas.CalibrationRecord, th schemas.JudgeCalibrationThresholds, setSHA string, served []string, now time.Time) []string {
	var out []string
	if age := Age(rec, now); age > th.MaxAgeDays {
		out = append(out, fmt.Sprintf("calibration %s is %d days old (more than %d): rerun judge calibrate", rec.RecordID, age, th.MaxAgeDays))
	}
	seen := map[string]bool{}
	for _, m := range rec.ServedModels {
		seen[m] = true
	}
	var fresh []string
	for _, m := range served {
		if !seen[m] {
			fresh = append(fresh, m)
		}
	}
	if len(fresh) > 0 && len(rec.ServedModels) > 0 {
		out = append(out, fmt.Sprintf("the gateway served judge alias %s as %s, but calibration %s saw %s: the model behind the alias may have changed; rerun judge calibrate",
			rec.Model, strings.Join(fresh, ", "), rec.RecordID, strings.Join(rec.ServedModels, ", ")))
	}
	if setSHA != "" && rec.SetSHA256 != "" && setSHA != rec.SetSHA256 {
		out = append(out, fmt.Sprintf("labels changed since calibration %s: rerun judge calibrate to measure the judge on them", rec.RecordID))
	}
	return out
}

// errorOverEffect says when the judge's error rate (one minus the lower
// bound of agreement) is larger than the effect the verdict reports: a
// judge that misreads that many answers could produce the effect on its
// own if its errors fell unevenly between the arms.
func errorOverEffect(rec *schemas.CalibrationRecord, eff *schemas.Effect) string {
	if rec == nil || rec.Human.Agreement.Lo == nil || eff == nil {
		return ""
	}
	errRate := 1 - *rec.Human.Agreement.Lo
	effect := math.Abs(eff.DBar)
	if effect < 1e-9 || errRate <= effect { // no effect, nothing claimed
		return ""
	}
	return fmt.Sprintf("the judge's error rate (up to %.1f%%, one minus the lower bound of agreement) exceeds the effect claimed (%+.1f pp): misjudged answers alone could produce it",
		100*errRate, 100*eff.DBar)
}

// biasAdjusted is, per arm on the condition, the raw judged success rate
// (passed over runs the judge decided) and its Rogan-Gladen correction from
// the record's TPR and TNR.
func biasAdjusted(runs []schemas.SealedRun, condition string, rec *schemas.CalibrationRecord) map[string]schemas.BiasAdjustedRate {
	if condition == "" {
		condition = schemas.ConditionClean
	}
	type kn struct{ k, n int }
	arms := map[string]*kn{}
	for _, r := range runs {
		if !r.Slot.Counted || r.Slot.Condition != condition || r.Grade == nil || !r.Grade.Judged || r.Grade.InvalidEvaluator {
			continue
		}
		a, ok := arms[r.Slot.Arm]
		if !ok {
			a = &kn{}
			arms[r.Slot.Arm] = a
		}
		a.n++
		if r.Grade.Passed {
			a.k++
		}
	}
	if len(arms) == 0 {
		return nil
	}
	names := make([]string, 0, len(arms))
	for a := range arms {
		names = append(names, a)
	}
	sort.Strings(names)
	out := map[string]schemas.BiasAdjustedRate{}
	for _, a := range names {
		c := arms[a]
		if rec == nil || rec.HumanLabels == 0 {
			out[a] = schemas.BiasAdjustedRate{Raw: Rate(c.k, c.n), Note: "no calibration record with human labels, so the judged rate cannot be corrected"}
			continue
		}
		h := rec.Human
		out[a] = RoganGladen(c.k, c.n, h.TP, h.Positives, h.TN, h.Negatives)
	}
	return out
}

// Contested reports whether a judged run needs a person because the
// judge's verdict is in dispute: its votes split between correct and
// incorrect, the auditor disagreed, or the newest decision review written
// against the run's seal says the opposite of the judge's majority.
// Contested never changes an outcome; it only routes the run to the review
// queue.
func Contested(sr *schemas.SealedRun, reviews []schemas.Review) bool {
	if sr == nil || sr.Grade == nil || sr.Grade.Judge == nil {
		return false
	}
	j := sr.Grade.Judge
	var c, i bool
	for _, v := range j.Votes {
		c = c || v == schemas.CalibrationVerdictCorrect
		i = i || v == schemas.CalibrationVerdictIncorrect
	}
	if c && i {
		return true
	}
	if a := j.Audit; a != nil && a.Error == "" && !a.Agrees {
		return true
	}
	majority := Majority(j.Votes)
	for k := len(reviews) - 1; k >= 0; k-- {
		r := reviews[k]
		if r.Decision != schemas.ReviewPass && r.Decision != schemas.ReviewFail {
			if r.Decision == schemas.ReviewInvalid && r.ParentSeal == sr.SealHash {
				return false
			}
			continue
		}
		if r.ParentSeal != sr.SealHash {
			continue
		}
		return (r.Decision == schemas.ReviewPass && majority == schemas.CalibrationVerdictIncorrect) ||
			(r.Decision == schemas.ReviewFail && majority == schemas.CalibrationVerdictCorrect)
	}
	return false
}
