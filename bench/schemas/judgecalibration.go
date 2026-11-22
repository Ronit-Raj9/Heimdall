package schemas

import "time"

// W8 judge calibration (docs/judge-calibration.md). A judged suite's judge is
// measured against human labels before its verdicts are trusted. The labels
// live in benchmarks/<suite>/calibration/labels.jsonl; `judge calibrate`
// runs the pinned judge on every labelled item and writes a calibration
// record, a read-only, hash-chained child artifact beside the labels. The
// verdict of a judged experiment then carries a judge_calibration
// instrument that names the record it rests on, or says there is none.

// Human labels of a calibration item (CalibrationItem.Label).
const (
	CalibrationCorrect   = "correct"
	CalibrationIncorrect = "incorrect"
)

// Where a calibration label came from (CalibrationItem.Source). Admission
// controls are a weak seed: the suite's own expected and wrong answers,
// labelled by the suite author, not by a person reading an agent's answer.
// They are reported apart and never count toward the label minimum or the
// thresholds.
const (
	CalibrationSourceReview  = "human_review"      // a pass or fail review of a sealed run
	CalibrationSourceLabel   = "label"             // `judge label`, the labelling page or POST .../labels
	CalibrationSourceImport  = "import"            // a CSV or JSONL file
	CalibrationSourceControl = "admission_control" // the suite's expected and wrong answers (weak seed)
)

// Judge verdicts on a calibration item. Undecided is no majority of correct
// or incorrect among the votes (unparsable replies or a tie).
const (
	CalibrationVerdictCorrect   = "correct"
	CalibrationVerdictIncorrect = "incorrect"
	CalibrationVerdictUndecided = "undecided"
)

// Calibration instrument states (JudgeCalibrationVerdict.State).
const (
	CalibrationStateCalibrated   = "calibrated"           // a record for the exact judge and prompt, enough labels, bounds above thresholds
	CalibrationStateFewLabels    = "few_labels"           // a record, fewer human labels than the minimum: a warning with the interval
	CalibrationStateUncalibrated = "uncalibrated"         // no record for the exact judge model and prompt hash: verdict at most INCONCLUSIVE
	CalibrationStateAllowed      = "allowed_uncalibrated" // uncalibrated, and the experiment set allow_uncalibrated_judge: labelled, not capped
	CalibrationStateUnreliable   = "unreliable"           // a bound below its threshold: INVALID "judge unreliable"
	// CalibrationStateControls is a record with too little human evidence
	// whose admission-control items (reference answers, wrong answers, an
	// injection and a refusal) are enough and agree with their labels above
	// the thresholds: verdicts stand, labelled "calibrated on controls".
	// Human labels that pass upgrade it to calibrated ("human-calibrated").
	CalibrationStateControls = "calibrated_on_controls"
)

// CalibrationItem is one labelled answer: a line of labels.jsonl. ID is
// the content address of the task and the answer text (the first 16 hex of
// sha256(task NUL answer)), so the same answer labelled twice is one item.
type CalibrationItem struct {
	ID       string    `json:"id"`
	Task     string    `json:"task"`
	Answer   string    `json:"answer"`
	Label    string    `json:"label"`    // correct | incorrect
	Labeller string    `json:"labeller"` // self-declared name; there is no login
	Note     string    `json:"note,omitempty"`
	Source   string    `json:"source"`        // human_review | label | import | admission_control
	Ref      string    `json:"ref,omitempty"` // review id, run id or file the label came from
	At       time.Time `json:"at"`

	Image string `json:"image,omitempty"` // X1 vision grading: sha256 of the labelled image, calibration/images/<sha256>.png; Answer may then be empty
}

// CalibrationRate is K of N with its exact (Clopper-Pearson) two-sided 95%
// interval. Value, Lo and Hi are nil when N is 0.
type CalibrationRate struct {
	K     int      `json:"k"`
	N     int      `json:"n"`
	Value *float64 `json:"value"`
	Lo    *float64 `json:"lo"`
	Hi    *float64 `json:"hi"`
}

// CalibrationStats compares the judge with the human labels over one set
// of items. Positive means "correct": a false positive is the judge calling
// an answer correct that a person labelled incorrect. An undecided judge
// verdict counts against agreement, TPR and TNR and is never a false
// positive.
type CalibrationStats struct {
	Items     int             `json:"items"`
	Positives int             `json:"positives"` // labelled correct
	Negatives int             `json:"negatives"` // labelled incorrect
	TP        int             `json:"tp"`
	FP        int             `json:"fp"`
	FN        int             `json:"fn"`
	TN        int             `json:"tn"`
	Undecided int             `json:"undecided"`
	Agreement CalibrationRate `json:"agreement"`
	TPR       CalibrationRate `json:"tpr"`   // judge correct among labelled correct
	TNR       CalibrationRate `json:"tnr"`   // judge incorrect among labelled incorrect
	FPR       CalibrationRate `json:"fpr"`   // judge correct among labelled incorrect
	Kappa     *float64        `json:"kappa"` // Cohen's kappa; nil when chance agreement is 1
}

// CalibrationCategory is the statistics of the items of one answer
// category.
type CalibrationCategory struct {
	Category string           `json:"category"`
	Stats    CalibrationStats `json:"stats"`
}

// CalibrationConsistency is how stable the judge is on the same input:
// the share of items whose votes were unanimous (only with more than one
// vote) and the share whose verdict was the same in two full passes.
type CalibrationConsistency struct {
	Votes           int             `json:"votes"`
	UnanimousItems  CalibrationRate `json:"unanimous_items"`
	RepeatAgreement CalibrationRate `json:"repeat_agreement"`
}

// CalibrationPerturbation is how often a deterministic, meaning-preserving
// change to the answer (extra whitespace, a polite preamble) flips the
// judge's verdict relative to the first pass.
type CalibrationPerturbation struct {
	Name  string          `json:"name"`
	Flips CalibrationRate `json:"flips"`
}

// CalibrationItemResult is the judge's work on one item.
type CalibrationItemResult struct {
	ItemID    string            `json:"item_id"`
	Task      string            `json:"task"`
	Category  string            `json:"category,omitempty"`
	Template  string            `json:"template"`
	Label     string            `json:"label"`
	Source    string            `json:"source"`
	Votes     []string          `json:"votes"`
	Verdict   string            `json:"verdict"` // first pass: correct | incorrect | undecided
	Repeat    string            `json:"repeat"`  // second pass
	Perturbed map[string]string `json:"perturbed,omitempty"`
}

// CalibrationRecord is benchmarks/<suite>/calibration/records/<id>/record.json:
// what one `judge calibrate` measured. Hash is the sha256 of its canonical
// JSON with Hash empty; PrevHash is the hash on the records.log line before
// its own, so records form one chain, checked on every read like reviews.
type CalibrationRecord struct {
	RecordID      string                    `json:"record_id"`
	Suite         string                    `json:"suite"`
	SuiteVersion  string                    `json:"suite_version"`
	Provider      string                    `json:"provider"`
	Model         string                    `json:"model"`
	PromptVersion string                    `json:"prompt_version"`
	PromptSHA256  string                    `json:"prompt_sha256"` // sha256 over the prompt version's template hashes
	Templates     map[string]string         `json:"templates"`     // templates the items were judged with: name -> sha256 of its text, as JudgeRecord.PromptSHA256
	Votes         int                       `json:"votes"`
	Aggregate     string                    `json:"aggregate,omitempty"`     // vision judge: majority | mean, how a grade's votes decide; "" is majority
	PassScore     float64                   `json:"pass_score,omitempty"`    // vision judge with aggregate mean: the score a grade's mean must reach
	ServedModels  []string                  `json:"served_models,omitempty"` // model ids the gateway reported
	SetSHA256     string                    `json:"set_sha256"`              // sha256 of the labels.jsonl snapshot (sets/<sha>.jsonl)
	SetRows       int                       `json:"set_rows"`
	HumanLabels   int                       `json:"human_labels"` // items labelled by people
	SeedItems     int                       `json:"seed_items"`   // admission-control items, reported apart
	Disputed      int                       `json:"disputed"`     // items whose labels disagree, left out
	Labellers     []string                  `json:"labellers,omitempty"`
	Human         CalibrationStats          `json:"human"`
	Seed          *CalibrationStats         `json:"seed,omitempty"`
	Categories    []CalibrationCategory     `json:"categories,omitempty"`
	ByTemplate    []CalibrationCategory     `json:"by_template,omitempty"` // human-item statistics per template sha256 (Category is the hash); a template without an entry has no human evidence
	Consistency   CalibrationConsistency    `json:"consistency"`
	Perturbations []CalibrationPerturbation `json:"perturbations,omitempty"`
	Items         []CalibrationItemResult   `json:"items"`
	Calls         int                       `json:"calls"`
	USD           Value                     `json:"usd"` // cost of the calibration's judge calls
	PriceRef      string                    `json:"price_ref,omitempty"`
	CreatedAt     time.Time                 `json:"created_at"`
	PrevHash      string                    `json:"prev_hash"`
	Hash          string                    `json:"hash"`
}

// JudgeCalibrationPolicy is the judge_calibration section of a release
// policy file. Unset fields take the documented defaults
// (docs/judge-calibration.md).
type JudgeCalibrationPolicy struct {
	MinAgreement *float64 `json:"min_agreement,omitempty" yaml:"min_agreement"` // lowest lower bound of agreement; default 0.75
	MinTNR       *float64 `json:"min_tnr,omitempty" yaml:"min_tnr"`             // lowest lower bound of the true negative rate; default 0.60
	MinLabels    int      `json:"min_labels,omitempty" yaml:"min_labels"`       // fewer human labels warn; default 30
	MaxAgeDays   int      `json:"max_age_days,omitempty" yaml:"max_age_days"`   // older records get a reminder; default 90
	MinControls  int      `json:"min_controls,omitempty" yaml:"min_controls"`   // fewest admission-control items for "calibrated on controls"; default 20
}

// JudgeCalibrationThresholds are the thresholds in force, defaults applied.
type JudgeCalibrationThresholds struct {
	MinAgreement float64 `json:"min_agreement"`
	MinTNR       float64 `json:"min_tnr"`
	MinLabels    int     `json:"min_labels"`
	MaxAgeDays   int     `json:"max_age_days"`
	MinControls  int     `json:"min_controls"`
	Source       string  `json:"source"` // "defaults" or the policy file
}

// BiasAdjustedRate is the raw judged success rate of one arm beside the
// Rogan-Gladen estimate corrected for the judge's measured TPR and TNR. It
// is report-only and never replaces the verdict. Estimate, Lo and Hi are nil
// when the correction is not defined (TPR + TNR <= 1, or no record).
type BiasAdjustedRate struct {
	Raw      CalibrationRate `json:"raw"`
	Estimate *float64        `json:"estimate"`
	Lo       *float64        `json:"lo"`
	Hi       *float64        `json:"hi"`
	Note     string          `json:"note,omitempty"`
}

// JudgeCalibrationVerdict is the judge_calibration instrument's full
// result on an analysis of a judged experiment (Analysis.JudgeCalibration).
type JudgeCalibrationVerdict struct {
	State           string                      `json:"state"`
	Detail          string                      `json:"detail"`
	Model           string                      `json:"model"`
	Templates       []string                    `json:"templates"` // prompt hashes the judged runs used
	RecordID        string                      `json:"record_id,omitempty"`
	RecordHash      string                      `json:"record_hash,omitempty"`
	RecordAt        *time.Time                  `json:"record_at,omitempty"`
	HumanLabels     int                         `json:"human_labels"`
	ControlItems    int                         `json:"control_items,omitempty"` // admission-control items of the record
	Agreement       *CalibrationRate            `json:"agreement,omitempty"`
	TNR             *CalibrationRate            `json:"tnr,omitempty"`
	FPR             *CalibrationRate            `json:"fpr,omitempty"`
	Kappa           *float64                    `json:"kappa,omitempty"`
	Thresholds      JudgeCalibrationThresholds  `json:"thresholds"`
	Reminders       []string                    `json:"reminders,omitempty"`
	JudgedRuns      int                         `json:"judged_runs"`
	ContestedRuns   int                         `json:"contested_runs"`
	ErrorOverEffect string                      `json:"error_over_effect,omitempty"` // set when the judge's error rate exceeds the effect claimed
	CappedFrom      Decision                    `json:"capped_from,omitempty"`       // the decision before an uncalibrated judge capped it
	Caps            bool                        `json:"caps,omitempty"`              // the calibration state caps any directional decision at INCONCLUSIVE (whatever this stage decided)
	BiasAdjusted    map[string]BiasAdjustedRate `json:"bias_adjusted,omitempty"`     // arm -> raw and corrected judged rate
}

// JudgeAudit is the optional second judge's opinion on one grade
// (JudgeRecord.Audit). It never decides the grade: a disagreement marks the
// run contested for a person to look at.
type JudgeAudit struct {
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	Votes       []string `json:"votes"`
	Verdict     string   `json:"verdict"` // correct | incorrect | undecided
	Agrees      bool     `json:"agrees"`
	ServedModel string   `json:"served_model,omitempty"`
	USD         Value    `json:"usd"`
	Error       string   `json:"error,omitempty"` // the audit could not run; the grade stands
}

// JudgeAuditorSpec names the auditor of a judged suite (JudgeSpec.Auditor):
// a judge from another provider than the pinned one.
type JudgeAuditorSpec struct {
	Provider string `json:"provider" yaml:"provider"`
	Model    string `json:"model" yaml:"model"`
	Votes    int    `json:"votes,omitempty" yaml:"votes"`
}

// CalibrationStatus is `judge status` and GET /suites/{id}/calibration: the
// suite's judge, label counts, the newest record for the pinned judge and
// any drift reminders.
type CalibrationStatus struct {
	Suite         string                     `json:"suite"`
	Judged        bool                       `json:"judged"`
	Provider      string                     `json:"provider,omitempty"`
	Model         string                     `json:"model,omitempty"`
	PromptVersion string                     `json:"prompt_version,omitempty"`
	PromptSHA256  string                     `json:"prompt_sha256,omitempty"`
	State         string                     `json:"state"` // calibrated | calibrated_on_controls | few_labels | uncalibrated | unreliable | not_judged
	Detail        string                     `json:"detail"`
	SetSHA256     string                     `json:"set_sha256,omitempty"`
	Labels        int                        `json:"labels"`       // rows in labels.jsonl
	HumanLabels   int                        `json:"human_labels"` // distinct items labelled by people
	SeedItems     int                        `json:"seed_items"`
	Disputed      int                        `json:"disputed"`
	Latest        *CalibrationRecord         `json:"latest,omitempty"` // newest valid record for the pinned judge, items left out
	Records       int                        `json:"records"`
	Thresholds    JudgeCalibrationThresholds `json:"thresholds"`
	Reminders     []string                   `json:"reminders"`
	Problems      []string                   `json:"problems"` // records ignored because the chain does not verify
}

// CalibrationLabelRequest is POST /suites/{id}/calibration/labels and the
// input of `judge label`.
type CalibrationLabelRequest struct {
	Task     string `json:"task"`
	Answer   string `json:"answer"`
	Label    string `json:"label"`
	Labeller string `json:"labeller"`
	Note     string `json:"note,omitempty"`
	Ref      string `json:"ref,omitempty"`
}

// CalibrationLabels is GET /suites/{id}/calibration/labels.
type CalibrationLabels struct {
	Suite     string            `json:"suite"`
	SetSHA256 string            `json:"set_sha256"`
	Items     []CalibrationItem `json:"items"`
}
