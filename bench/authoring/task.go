package authoring

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskgates"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// Drafting on a task of a question-and-answer suite: the ops write into the
// task's hidden answer file (rubric, wrong_answers) and its exploit control,
// label every field they wrote in task.yaml (provenance.generated) and set
// review: pending. Nothing a person wrote is removed or replaced: a rubric
// is drafted only when the task has none, and an exploit only when it has
// no exploit control (otherwise the exploit joins the wrong answers).

// ExploitFile is where a drafted exploit control is written, relative to
// the task folder.
const ExploitFile = "controls/exploit.assist.txt"

// DefaultNearMiss is how many near-miss wrong answers are drafted per task.
const DefaultNearMiss = 3

// TaskOptions says which ops to run on a task.
type TaskOptions struct {
	Ops      []Op
	NearMiss int // near-miss answers per task; 0 uses DefaultNearMiss
	// Redraft runs an op again on a task that already has fields it
	// drafted. Off by default, so a resumed job does not add the same kind
	// of wrong answer twice.
	Redraft bool
}

// OpResult is what one op did on one task.
type OpResult struct {
	Op      Op       `json:"op"`
	Status  string   `json:"status"` // drafted | skipped | failed
	Reason  string   `json:"reason,omitempty"`
	Fields  []string `json:"fields,omitempty"`
	CostUSD float64  `json:"cost_usd,omitempty"`
}

// Op statuses (OpResult.Status).
const (
	StatusDrafted = "drafted"
	StatusSkipped = "skipped"
	StatusFailed  = "failed"
)

// TaskDraft is what the assist drafted for one task, before it is written.
type TaskDraft struct {
	Task      string                   `json:"task"`
	Ops       []OpResult               `json:"ops"`
	Rubric    []Criterion              `json:"rubric,omitempty"`
	Wrong     []string                 `json:"wrong_answers,omitempty"`
	Exploit   string                   `json:"exploit,omitempty"`
	CostUSD   float64                  `json:"cost_usd"`
	Generated []schemas.GeneratedField `json:"generated,omitempty"`
	// Stopped is set when the budget ran out during this task.
	Stopped bool `json:"stopped,omitempty"`
}

// Drafted reports whether any op wrote something.
func (d *TaskDraft) Drafted() bool { return len(d.Generated) > 0 }

// taskText is what the ops read from a task.
type taskText struct {
	instruction string
	ans         *schemas.AnswerSpec
	ansRel      string // answer file, relative to the task folder
}

func readTaskText(s *schemas.Suite, t *schemas.Task) (*taskText, error) {
	if utils.OutputKind(s, t) != schemas.OutputAnswer {
		return nil, errors.New("not a question-and-answer task: the assist drafts rubrics and wrong answers for answer tasks only")
	}
	ans, err := graders.LoadAnswerSpec(t)
	if err != nil {
		return nil, fmt.Errorf("read the answer file: %w", err)
	}
	it := taskgates.ItemOf(s, t)
	return &taskText{instruction: clip(strings.TrimSpace(it.Instruction), 12000), ans: ans, ansRel: graders.AnswerSpecPath(t)}, nil
}

// reference is the answer the ops build on.
func (tt *taskText) reference() string {
	if len(tt.ans.Expected) == 0 {
		return ""
	}
	return strings.TrimSpace(tt.ans.Expected[0])
}

// DraftTask runs opts.Ops on t with a. An op that fails is reported and the
// others still run; when the budget runs out the draft stops and says so.
// It writes nothing: Edits turns the draft into file edits.
func DraftTask(ctx context.Context, a *Assistant, s *schemas.Suite, t *schemas.Task, opts TaskOptions) (*TaskDraft, error) {
	tt, err := readTaskText(s, t)
	if err != nil {
		return nil, err
	}
	d := &TaskDraft{Task: t.ID}
	ref := tt.reference()
	judged := utils.Grading(s) == schemas.GradingJudged
	want := map[Op]bool{}
	for _, op := range opts.Ops {
		want[op] = true
	}
	nearMiss := opts.NearMiss
	if nearMiss <= 0 {
		nearMiss = DefaultNearMiss
	}
	if nearMiss > maxNearMiss {
		nearMiss = maxNearMiss
	}
	criteria := ParseRubric(tt.ans.Rubric)
	skip := func(op Op, why string) { d.Ops = append(d.Ops, OpResult{Op: op, Status: StatusSkipped, Reason: why}) }
	record := func(op Op, call *Call, err error, fields ...string) bool {
		r := OpResult{Op: op}
		if call != nil {
			r.CostUSD = call.CostUSD
			d.CostUSD += call.CostUSD
		}
		if err != nil {
			r.Status, r.Reason = StatusFailed, err.Error()
			if errors.Is(err, ErrBudget) {
				d.Stopped = true
			}
			d.Ops = append(d.Ops, r)
			return false
		}
		r.Status, r.Fields = StatusDrafted, fields
		for i, f := range fields {
			g := call.Field(f)
			if i > 0 {
				g.CostUSD = 0 // one call is counted once, on the first field it wrote (fix wave 3, N278)
			}
			d.Generated = append(d.Generated, g)
		}
		d.Ops = append(d.Ops, r)
		return true
	}
	wrongBase := len(tt.ans.WrongOnes)
	wrongField := func() string {
		return "answer.wrong_answers[" + strconv.Itoa(wrongBase+len(d.Wrong)-1) + "]"
	}
	for _, op := range AllOps {
		if !want[op] {
			continue
		}
		if d.Stopped {
			skip(op, "the assist budget ran out")
			continue
		}
		if !opts.Redraft && hasOp(t, op) {
			skip(op, "already drafted on this task")
			continue
		}
		switch op {
		case OpRubric:
			switch {
			case !judged:
				skip(op, "the benchmark grades by exact answer; a rubric is read only by the judge")
				continue
			case strings.TrimSpace(tt.ans.Rubric) != "":
				skip(op, "the task already has a rubric")
				continue
			case ref == "":
				skip(op, "the task has no reference answer to base a rubric on")
				continue
			}
			var out RubricOutput
			call, err := a.Run(ctx, op, RubricInput{Instruction: tt.instruction, Question: tt.ans.Question, Reference: ref}, &out)
			if record(op, call, err, "answer.rubric") {
				d.Rubric, criteria = out.Criteria, out.Criteria
			}
		case OpCriterionWrong:
			switch {
			case !judged:
				skip(op, "the benchmark grades by exact answer; criterion replies need a judged rubric")
				continue
			case len(criteria) == 0:
				skip(op, "the task has no rubric criteria (numbered or bulleted lines) to break")
				continue
			}
			out := (&CriterionWrongOutput{}).ExpectCriteria(len(criteria), ref)
			call, err := a.Run(ctx, op, CriterionWrongInput{Instruction: tt.instruction, Question: tt.ans.Question, Reference: ref, Criteria: criteria}, out)
			if err == nil {
				var fields []string
				for _, r := range out.Replies {
					d.Wrong = append(d.Wrong, r.Reply)
					fields = append(fields, wrongField())
				}
				record(op, call, nil, fields...)
			} else {
				record(op, call, err)
			}
		case OpNearMissWrong:
			if ref == "" {
				skip(op, "the task has no reference answer to stay close to")
				continue
			}
			out := (&NearMissOutput{}).Expect(nearMiss, ref)
			call, err := a.Run(ctx, op, NearMissInput{Instruction: tt.instruction, Question: tt.ans.Question, Reference: ref, Count: nearMiss}, out)
			if err == nil {
				var fields []string
				for _, an := range out.Answers {
					d.Wrong = append(d.Wrong, an.Answer)
					fields = append(fields, wrongField())
				}
				record(op, call, nil, fields...)
			} else {
				record(op, call, err)
			}
		case OpExploit:
			checks := tt.ans.Rubric
			if checks == "" {
				checks = "The answer is matched against the expected answer (" + tt.ans.Match + ")."
			}
			if len(d.Rubric) > 0 {
				checks = RenderRubric(d.Rubric)
			}
			out := (&ExploitOutput{}).Expect(ref)
			call, err := a.Run(ctx, op, ExploitInput{Instruction: tt.instruction, Question: tt.ans.Question, Checks: checks, Reference: ref}, out)
			if err != nil {
				record(op, call, err)
				continue
			}
			if t.Controls.Exploit == "" {
				d.Exploit = out.Reply
				record(op, call, nil, "controls.exploit")
			} else {
				d.Wrong = append(d.Wrong, out.Reply)
				record(op, call, nil, wrongField())
			}
		}
	}
	return d, nil
}

// Edits turns d into file edits relative to the suite root: the answer
// file (rubric set, wrong answers appended), the exploit control file, and
// task.yaml (exploit control, provenance.generated, review: pending).
// A draft that wrote nothing has no edit.
func Edits(s *schemas.Suite, t *schemas.Task, d *TaskDraft) ([]taskgates.FileEdit, error) {
	if !d.Drafted() {
		return nil, nil
	}
	taskRel, err := relTo(s.Root, t.Dir)
	if err != nil {
		return nil, err
	}
	var edits []taskgates.FileEdit
	ansRel := graders.AnswerSpecPath(t)
	if len(d.Rubric) > 0 || len(d.Wrong) > 0 {
		abs, err := utils.ResolveInside(t.Dir, ansRel)
		if err != nil {
			return nil, err
		}
		old, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", ansRel, err)
		}
		out, err := editMapping(old, func(m *yaml.Node) error {
			if len(d.Rubric) > 0 {
				setNode(m, "rubric", scalar(RenderRubric(d.Rubric)))
			}
			if len(d.Wrong) > 0 {
				seq := sequence(m, "wrong_answers")
				for _, w := range d.Wrong {
					seq.Content = append(seq.Content, scalar(w))
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ansRel, err)
		}
		edits = append(edits, taskgates.FileEdit{Rel: path.Join(taskRel, filepath.ToSlash(ansRel)), Old: old, Data: out})
	}
	if d.Exploit != "" {
		rel := path.Join(taskRel, ExploitFile)
		var old []byte
		if b, err := os.ReadFile(filepath.Join(t.Dir, filepath.FromSlash(ExploitFile))); err == nil {
			old = b
		}
		edits = append(edits, taskgates.FileEdit{Rel: rel, Old: old, Data: []byte(d.Exploit + "\n")})
	}
	old, err := os.ReadFile(filepath.Join(t.Dir, "task.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read task.yaml: %w", err)
	}
	out, err := editMapping(old, func(m *yaml.Node) error {
		if d.Exploit != "" {
			setNode(mapping(m, "controls"), "exploit", scalar(ExploitFile))
		}
		prov := mapping(m, "provenance")
		gen := sequence(prov, "generated")
		// a field drafted again replaces its old label
		redrafted := map[string]bool{}
		for _, g := range d.Generated {
			redrafted[g.Field] = true
		}
		kept := gen.Content[:0]
		for _, n := range gen.Content {
			if f := mapValue(n, "field"); f != nil && redrafted[f.Value] {
				continue
			}
			kept = append(kept, n)
		}
		gen.Content = kept
		for _, g := range d.Generated {
			n, err := encodeNode(g)
			if err != nil {
				return err
			}
			gen.Content = append(gen.Content, n)
		}
		setNode(m, "review", scalar(schemas.ReviewPending))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("task.yaml: %w", err)
	}
	edits = append(edits, taskgates.FileEdit{Rel: path.Join(taskRel, "task.yaml"), Old: old, Data: out})
	return edits, nil
}

func relTo(root, abs string) (string, error) {
	r, err := filepath.Rel(root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the suite folder", abs)
	}
	return filepath.ToSlash(r), nil
}

// hasOp reports whether op already drafted a field of t.
func hasOp(t *schemas.Task, op Op) bool {
	for _, g := range t.GeneratedFields() {
		if g.Op == string(op) {
			return true
		}
	}
	return false
}
