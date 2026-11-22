package schemas

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// Review of generated tasks (docs/authoring-assist.md). A task a model
// helped write (Task.Provenance.Generated is not empty, or Task.Review is
// pending) never counts until a person accepts it: the planner keeps it out
// of new evaluations, so it reaches no gate, release or report card. The
// readiness check still runs on it, because that check is the proof the
// reviewer reads before accepting.
//
// The decision lives in a sidecar beside suite.lock (TaskReviewFileName),
// like the quarantine sidecar, so accepting a task never changes the lock
// hash and the readiness record stays valid.

// Review states (Task.Review, TaskReviewDecision.Decision).
const (
	ReviewPending  = "pending"
	ReviewAccepted = "accepted"
	ReviewRejected = "rejected"
)

// TaskReviewFileName is the review sidecar beside suite.lock. It is hashed
// into nothing.
const TaskReviewFileName = "suite.review.json"

// GeneratedField is one field of a task a model drafted.
type GeneratedField struct {
	// Field names what was written, as a path into the task: "answer.rubric",
	// "answer.wrong_answers[2]", "controls.exploit".
	Field     string  `json:"field" yaml:"field"`
	Op        string  `json:"op" yaml:"op"`                                     // authoring op: rubric, criterion_wrong, near_miss_wrong, exploit
	Model     string  `json:"model,omitempty" yaml:"model,omitempty"`           // provider/model that wrote it
	PromptSHA string  `json:"prompt_sha,omitempty" yaml:"prompt_sha,omitempty"` // sha256 of the op's prompt template
	CostUSD   float64 `json:"cost_usd,omitempty" yaml:"cost_usd,omitempty"`     // priced cost of the call that wrote it
	At        string  `json:"at,omitempty" yaml:"at,omitempty"`                 // RFC 3339
}

// TaskReviewFile is the content of suite.review.json.
type TaskReviewFile struct {
	Suite     string                        `json:"suite"`
	Decisions map[string]TaskReviewDecision `json:"decisions"` // task id -> the person's decision
}

// TaskReviewDecision is a person's decision on a generated task. It holds
// only while the task's generated fields are the ones reviewed: Generated
// is their fingerprint, so drafting a field again makes the task pending
// once more.
type TaskReviewDecision struct {
	Decision    string    `json:"decision"` // accepted | rejected
	By          string    `json:"by,omitempty"`
	ByName      string    `json:"by_name,omitempty"` // how By reads in an answer (a member's display name); never stored
	At          time.Time `json:"at"`
	Note        string    `json:"note,omitempty"`
	TaskVersion string    `json:"task_version,omitempty"`
	Generated   string    `json:"generated"` // GeneratedFingerprint at decision time
}

// IsGenerated reports whether a model drafted any part of t, or the task
// declares a review state.
func (t *Task) IsGenerated() bool {
	if t == nil {
		return false
	}
	return strings.TrimSpace(t.Review) != "" || (t.Provenance != nil && len(t.Provenance.Generated) > 0)
}

// GeneratedFields returns t's generated fields, nil for a hand-written task.
func (t *Task) GeneratedFields() []GeneratedField {
	if t == nil || t.Provenance == nil {
		return nil
	}
	return t.Provenance.Generated
}

// GeneratedFingerprint is a stable hash of the generated fields (field, op,
// model and prompt; never the cost or time), "" when there are none.
func GeneratedFingerprint(fields []GeneratedField) string {
	if len(fields) == 0 {
		return ""
	}
	lines := make([]string, 0, len(fields))
	for _, f := range fields {
		lines = append(lines, f.Field+"\x00"+f.Op+"\x00"+f.Model+"\x00"+f.PromptSHA)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

// TaskReviewState is t's review state given the suite's review sidecar f
// (nil when there is none): accepted for a hand-written task; for a
// generated task the decision on f when it matches the current generated
// fields, else what task.yaml declares, else pending.
func TaskReviewState(t *Task, f *TaskReviewFile) string {
	if t == nil || !t.IsGenerated() {
		return ReviewAccepted
	}
	if f != nil {
		if d, ok := f.Decisions[t.ID]; ok && d.Generated == GeneratedFingerprint(t.GeneratedFields()) {
			switch d.Decision {
			case ReviewAccepted, ReviewRejected:
				return d.Decision
			}
		}
	}
	switch s := strings.TrimSpace(t.Review); s {
	case ReviewAccepted, ReviewRejected:
		return s
	}
	return ReviewPending
}
