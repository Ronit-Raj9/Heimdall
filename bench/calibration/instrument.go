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
	groups := judgeGroups(in.Runs)
	judged := 0
	for _, g := range groups {
		judged += g.n
	}
	if judged == 0 {
		check.Status = schemas.InstrumentNotApplicable
		check.Detail = "no counted run was graded by the judge"
		an.Verdict.Instruments = append(an.Verdict.Instruments, check)
		return
	}
	jc := &schemas.JudgeCalibrationVerdict{Model: groups[0].id.Model, Templates: allTemplates(groups), Thresholds: th, JudgedRuns: judged}
	for _, r := range in.Runs {
		if r.Slot.Counted && Contested(&r, in.Reviews[r.Run.RunID]) {
			jc.ContestedRuns++
		}
	}
	allowed := in.Manifest != nil && in.Manifest.Experiment.Verdict.AllowUncalibratedJudge
	// Every judge configuration that graded a counted run is assessed on
	// its own record for its own templates (audit 1-C-12, QC2-25-02); the
	// results combine by severity, so a measured reliability failure of any
	// judge stays INVALID whatever the others say (QC2-25-01).
	capVerdict := false
	var majority *schemas.CalibrationRecord
	for i, g := range groups {
		as := assess(g, in.Records, th, len(groups) > 1)
		if i == 0 {
			majority = as.rec
			if as.rec != nil {
				fillRecord(jc, as.rec)
			}
			jc.State, jc.Detail = as.state, as.detail
		} else if as.state != schemas.CalibrationStateCalibrated {
			switch {
			case severity(as.state) > severity(jc.State) && as.state == schemas.CalibrationStateUnreliable:
				jc.Detail = as.detail
			default:
				jc.Detail += "; " + as.detail
			}
			if severity(as.state) > severity(jc.State) {
				jc.State = as.state
			}
		}
		capVerdict = capVerdict || as.caps
		if as.rec != nil { // drift of each judge against its own record (QC2-25-07)
			jc.Reminders = append(jc.Reminders, Reminders(as.rec, th, in.SetSHA256, g.served, in.Now)...)
		}
	}
	if in.Records != nil && len(in.Records.Problems) > 0 {
		jc.Reminders = append(jc.Reminders, fmt.Sprintf("%d calibration record problem(s): %s", len(in.Records.Problems), in.Records.Problems[0]))
	}
	unreliable := jc.State == schemas.CalibrationStateUnreliable
	jc.Caps = capVerdict && !allowed && !unreliable
	if capVerdict && allowed && !unreliable {
		jc.State = schemas.CalibrationStateAllowed
		jc.Detail += "; allowed by allow_uncalibrated_judge, so the verdict is labelled, not capped"
	}
	if allowed || unreliable {
		capVerdict = false
	}
	rec := majority
	if len(groups) > 1 {
		rec = nil // one judge's error rates do not describe a mixture (QC2-25-07)
	}
	if jc.ContestedRuns > 0 {
		jc.Detail += fmt.Sprintf("; %d of %d judged runs contested (in the review queue)", jc.ContestedRuns, judged)
	}
	jc.ErrorOverEffect = errorOverEffect(rec, an.Verdict.Effect)
	jc.BiasAdjusted = biasAdjusted(in.Runs, an.Verdict.Primary.Condition, rec, len(groups) > 1)

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

// judgeGroup is the counted judged runs of one judge configuration.
type judgeGroup struct {
	id        JudgeID
	templates []string // prompt template hashes its runs used, sorted
	served    []string // served model ids its runs recorded, sorted
	n         int
}

// judgeGroups groups the counted judged runs by judge configuration, the
// most common first (ties by name).
func judgeGroups(runs []schemas.SealedRun) []judgeGroup {
	type acc struct {
		tmpl, srv map[string]bool
		n         int
	}
	m := map[JudgeID]*acc{}
	for _, r := range runs {
		if !r.Slot.Counted || r.Grade == nil || r.Grade.Judge == nil || len(r.Grade.Judge.Votes) == 0 {
			continue
		}
		id := GradeJudgeID(r.Grade.Judge)
		a, ok := m[id]
		if !ok {
			a = &acc{tmpl: map[string]bool{}, srv: map[string]bool{}}
			m[id] = a
		}
		a.n++
		if h := r.Grade.Judge.PromptSHA256; h != "" {
			a.tmpl[h] = true
		}
		if sm := r.Grade.Judge.ServedModel; sm != "" {
			a.srv[sm] = true
		}
	}
	out := make([]judgeGroup, 0, len(m))
	for id, a := range m {
		out = append(out, judgeGroup{id: id, templates: sortedSet(a.tmpl), served: sortedSet(a.srv), n: a.n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].id.String() < out[j].id.String()
	})
	return out
}

func allTemplates(groups []judgeGroup) []string {
	set := map[string]bool{}
	for _, g := range groups {
		for _, t := range g.templates {
			set[t] = true
		}
	}
	return sortedSet(set)
}

// severity orders calibration states: a worse state wins when judges
// combine.
func severity(state string) int {
	switch state {
	case schemas.CalibrationStateUnreliable:
		return 3
	case schemas.CalibrationStateUncalibrated:
		return 2
	case schemas.CalibrationStateFewLabels:
		return 1
	}
	return 0
}

// assessment is one judge configuration's calibration result.
type assessment struct {
	state, detail string
	caps          bool
	rec           *schemas.CalibrationRecord
}

// assess finds the record for one judge configuration and its templates
// and checks it against the thresholds: pooled over its human items and for
// every template the judge used, each on that template's own human items
// (QC2-25-04). named prefixes the detail with the judge when several judged.
func assess(g judgeGroup, recs *RecordSet, th schemas.JudgeCalibrationThresholds, named bool) assessment {
	who := ""
	if named {
		who = "judge " + g.id.String() + ": "
	}
	rec, newest := recs.Covering(g.id, g.templates)
	switch {
	case rec == nil:
		d := fmt.Sprintf("%sjudge not calibrated: no calibration record for model %s with the prompt the runs used (%s)", who, g.id.String(), shortList(g.templates))
		if newest != nil {
			d += fmt.Sprintf("; the newest record for the model, %s, does not cover it (judge configuration or human labels for each template)", newest.RecordID)
		}
		return assessment{state: schemas.CalibrationStateUncalibrated, detail: d, caps: true}
	case rec.HumanLabels == 0:
		return assessment{state: schemas.CalibrationStateUncalibrated, caps: true, rec: rec,
			detail: fmt.Sprintf("%sjudge not calibrated: record %s has no human labels (%d admission-control seeds only)", who, rec.RecordID, rec.SeedItems)}
	}
	as := assessment{rec: rec}
	bounds, pass := boundsText(&rec.Human, th)
	switch {
	case rec.HumanLabels < th.MinLabels:
		as.state = schemas.CalibrationStateFewLabels
		as.detail = fmt.Sprintf("%sjudge calibrated on %d human labels, fewer than %d: %s", who, rec.HumanLabels, th.MinLabels, bounds)
		if !pass {
			as.detail += "; too few labels to show the judge reliable"
			as.caps = true
		}
	case !pass:
		as.state = schemas.CalibrationStateUnreliable
		as.detail = fmt.Sprintf("%sjudge unreliable: %s (record %s, %d human labels)", who, bounds, rec.RecordID, rec.HumanLabels)
		return as
	default:
		as.state = schemas.CalibrationStateCalibrated
		as.detail = fmt.Sprintf("%sjudge calibrated on %d human labels: %s (record %s)", who, rec.HumanLabels, bounds, rec.RecordID)
	}
	if len(rec.Templates) < 2 {
		return as // the pooled statistics are the template's
	}
	for _, t := range g.templates {
		ts := TemplateStats(rec, t)
		if ts == nil {
			continue // Covering already required one
		}
		tb, tpass := boundsText(ts, th)
		switch {
		case ts.Items >= th.MinLabels && !tpass:
			as.state = schemas.CalibrationStateUnreliable
			as.detail = fmt.Sprintf("%sjudge unreliable on %s: %s (record %s, %d human labels for that template)", who, shortList([]string{t}), tb, rec.RecordID, ts.Items)
			as.caps = false
			return as
		case !tpass:
			if as.state == schemas.CalibrationStateCalibrated {
				as.state = schemas.CalibrationStateFewLabels
			}
			as.detail += fmt.Sprintf("; %s has %d human labels of its own, too few to show the judge reliable on it: %s", shortList([]string{t}), ts.Items, tb)
			as.caps = true
		}
	}
	return as
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
func boundsText(h *schemas.CalibrationStats, th schemas.JudgeCalibrationThresholds) (string, bool) {
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
//
// Only the judge's own decisions count: a run whose outcome a human review
// replaced is left out, since its pass is a person's label, not the
// judge's (QC2-25-08). With several judge configurations no single record
// describes the error rates, so nothing is corrected (QC2-25-07).
func biasAdjusted(runs []schemas.SealedRun, condition string, rec *schemas.CalibrationRecord, mixed bool) map[string]schemas.BiasAdjustedRate {
	if condition == "" {
		condition = schemas.ConditionClean
	}
	type kn struct{ k, n int }
	arms := map[string]*kn{}
	for _, r := range runs {
		if !r.Slot.Counted || r.Slot.Condition != condition || r.Grade == nil || !r.Grade.Judged || r.Grade.InvalidEvaluator || r.Review != nil {
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
		if mixed {
			out[a] = schemas.BiasAdjustedRate{Raw: Rate(c.k, c.n), Note: "several judge configurations graded the runs, so one record's error rates cannot correct the judged rate"}
			continue
		}
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
