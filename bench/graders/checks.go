package graders

// Declarative checks (checks.yaml, package taskchecks): one shipped grader for
// tool, argument, order, state and reply-rubric checks, so a task needs no
// Python verifier.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/taskchecks"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ChecksType is the part name suites use to select this grader.
const ChecksType = "checks"

// PromptChecksReplyV1 is the reply rubric prompt of a checks.yaml
// reply_rubric: the task, the rubric, and the conversation with the tool
// calls the agent made, fenced as data.
const PromptChecksReplyV1 = "checks-reply-v1"

const checksVersion = "1.0.0"

// Dimension names of a checks grade: one per check, plus the score.
const (
	ChecksDimensionPrefix = "check."
	ChecksScoreDimension  = "checks_score"
)

// maxConversationRunes caps the conversation the reply judge reads.
const maxConversationRunes = 60000

// ChecksConfig wires the checks grader: Inner runs the suite's verifier
// command for a task without a checks.yaml, and Judge is the gateway wiring
// the reply rubric shares with the judge grader.
type ChecksConfig struct {
	Inner  HiddenTestsConfig
	Judge  JudgeConfig
	Logger schemas.Logger
}

var _ schemas.Grader = (*checksGrader)(nil)

type checksGrader struct {
	cfg   ChecksConfig
	inner *verifierCommand
	judge *judge
}

// NewChecks returns the checks grader wired to cfg. The app registers it
// ahead of Builtin's unwired one (the first grader with a name wins).
func NewChecks(cfg ChecksConfig) schemas.Grader { return newChecks(cfg) }

func newChecks(cfg ChecksConfig) *checksGrader {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	if cfg.Inner.Logger == nil {
		cfg.Inner.Logger = cfg.Logger
	}
	if cfg.Judge.Logger == nil {
		cfg.Judge.Logger = cfg.Logger
	}
	return &checksGrader{cfg: cfg, inner: newVerifierCommand(cfg.Inner), judge: newJudge(cfg.Judge)}
}

// GetName implements schemas.Part.
func (g *checksGrader) GetName() string { return ChecksType }

// GetVersion implements schemas.Part.
func (g *checksGrader) GetVersion() string { return checksVersion }

// VerifierNetwork reports "none": the checks run in the bench process on
// the sealed ledger and exported state, the inner verifier command runs
// with --network none, and the reply judge's only traffic is its own model
// calls on its own key.
func (g *checksGrader) VerifierNetwork() string { return verifierNetwork }

// ChecksFile is the path of a task's checks.yaml, "" when it has none.
func ChecksFile(t *schemas.Task) string { return taskchecks.TaskFile(t) }

// LoadChecks reads and validates a task's checks.yaml; nil when it has none.
func LoadChecks(t *schemas.Task) (*taskchecks.Spec, error) {
	p := ChecksFile(t)
	if p == "" {
		return nil, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return taskchecks.Parse(b)
}

// Grade grades the task's checks.yaml against the run's tool ledger, its
// exported state and, for a reply rubric, its conversation. A task without
// a checks.yaml is graded by the suite's verifier command, as
// verifier_command grades it, so one suite can hold both. A check that
// cannot be decided (a state export missing or unreadable, a judge with no
// majority) makes the grade invalid_evaluator; a check file that does not
// parse is invalid_evaluator too (lint and admission refuse it first).
func (g *checksGrader) Grade(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	if rc == nil || rc.Suite == nil || rc.Task == nil {
		return nil, errors.New("checks: run context has no suite or task")
	}
	if ChecksFile(rc.Task) == "" {
		ig, err := g.inner.Grade(ctx, rc, diff)
		if err != nil {
			return nil, fmt.Errorf("checks: verifier command: %w", err)
		}
		ig.GraderVersion = checksVersion
		return ig, nil
	}
	grade := &schemas.Grade{SuiteVersion: rc.Suite.Version, GraderVersion: checksVersion, ResultFormat: schemas.VerifierResultReward, Mode: utils.OutputKind(rc.Suite, rc.Task)}
	if diff != nil {
		grade.DiffSHA256 = diff.SHA256
	}
	spec, err := LoadChecks(rc.Task)
	if err != nil {
		grade.InvalidEvaluator, grade.Reason = true, "checks.yaml: "+err.Error()
		return grade, nil
	}
	if rc.Dir == "" {
		return nil, errors.New("checks: run context has no run folder")
	}
	rows, err := taskchecks.ReadLedger(filepath.Join(rc.Dir, trajectoryToolsFile))
	if err != nil {
		grade.InvalidEvaluator, grade.Reason = true, "tool ledger: "+err.Error()
		return grade, nil
	}
	calls := taskchecks.CallsFrom(rows)
	results := taskchecks.Evaluate(spec, taskchecks.Inputs{Calls: calls, State: taskchecks.NewDirState(filepath.Join(rc.Dir, "state"))})
	if rub := spec.Rubric(); rub != nil {
		i := indexOf(results, rub.ID)
		if rc.NoPaidTraffic {
			// As the judge grader: a run promised to cost nothing never
			// mints a judge key.
			grade.InvalidEvaluator, grade.Reason = true, NoPaidJudgeReason
			setChecksDimensions(grade, results)
			return grade, nil
		}
		rec, passed, invalid, detail, err := g.judgeReply(ctx, rc, rub.ReplyRubric, calls)
		if err != nil {
			return nil, fmt.Errorf("checks: reply rubric: %w", err)
		}
		results[i].Pending, results[i].Passed, results[i].Invalid, results[i].Detail = false, passed, invalid, detail
		if rec != nil {
			grade.Judged, grade.Judge = true, rec
		}
	}
	score, passed, invalid, reason := taskchecks.Score(spec, results)
	grade.Passed, grade.InvalidEvaluator = passed, invalid
	if !invalid {
		grade.Reward = &score
	}
	grade.Reason = reason + failedDetails(results)
	setChecksDimensions(grade, results)
	grade.TestsRan = len(results)
	for _, r := range results {
		if !r.Passed && !r.Invalid && !r.Pending {
			grade.Failures++
			grade.FailedTests = append(grade.FailedTests, r.ID)
		}
	}
	g.cfg.Logger.Debug("graded checks", "task", rc.Task.ID, "passed", grade.Passed, "reason", grade.Reason)
	return grade, nil
}

func indexOf(results []taskchecks.Result, id string) int {
	for i, r := range results {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// setChecksDimensions reports each decided check as a 0 or 1 dimension.
func setChecksDimensions(grade *schemas.Grade, results []taskchecks.Result) {
	grade.Dimensions = map[string]float64{}
	for _, r := range results {
		if r.Pending || r.Invalid {
			continue
		}
		v := 0.0
		if r.Passed {
			v = 1
		}
		grade.Dimensions[ChecksDimensionPrefix+r.ID] = v
	}
	if grade.Reward != nil {
		grade.Dimensions[ChecksScoreDimension] = *grade.Reward
	}
}

// failedDetails is " (id: detail; ...)" for the failed checks, capped.
func failedDetails(results []taskchecks.Result) string {
	var parts []string
	for _, r := range results {
		if !r.Passed && !r.Pending && !r.Invalid {
			parts = append(parts, r.ID+": "+r.Detail)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	s := strings.Join(parts, "; ")
	if rs := []rune(s); len(rs) > 1500 {
		s = string(rs[:1499]) + "…"
	}
	return " (" + s + ")"
}

// judgeReply asks the suite's pinned judge whether the conversation meets
// the rubric. The agent's own words are checked by the judge guard first:
// text that addresses the grader fails the check without a judge call.
func (g *checksGrader) judgeReply(ctx context.Context, rc *schemas.RunContext, rubric string, calls []taskchecks.Call) (rec *schemas.JudgeRecord, passed, invalid bool, detail string, err error) {
	spec, err := checksJudgeSpec(rc, g.judge.cfg)
	if err != nil {
		return nil, false, false, "", err
	}
	conv, agentText, err := conversation(rc, calls)
	if err != nil {
		return nil, false, false, "", err
	}
	if strings.TrimSpace(agentText) == "" {
		return nil, false, false, "judge not asked: the agent said nothing to the customer and wrote no answer", nil
	}
	if inj := JudgeInjection(agentText); inj != "" {
		return nil, false, false, "judge not asked: the agent's reply " + inj + ", which fails it", nil
	}
	tmpl, err := judgePromptFS.ReadFile("judgeprompts/" + PromptChecksReplyV1 + ".txt")
	if err != nil {
		return nil, false, false, "", err
	}
	task := instructionText(rc.Task)
	ans := &schemas.AnswerSpec{Question: task, Rubric: strings.TrimSpace(rubric)}
	prompt := renderJudgePromptCap(string(tmpl), ans, conv, maxConversationRunes)
	rec, _, err = g.judge.voteReplies(ctx, rc, spec, prompt)
	if err != nil {
		return nil, false, false, "", err
	}
	rec.PromptSHA256 = utils.SHA256Bytes(tmpl)
	rec.Provider = spec.Provider
	rec.Calibration = calibrationRef(rc.Suite, calibration.GradeJudgeID(rec), rec.PromptSHA256)
	passed, invalid, detail = tally(rec.Votes, PromptChecksReplyV1)
	return rec, passed, invalid, detail, nil
}

// checksJudgeSpec is the suite's judge with the reply prompt: the suite
// must be graded judged and pin provider and model.
func checksJudgeSpec(rc *schemas.RunContext, cfg JudgeConfig) (schemas.JudgeSpec, error) {
	if gr := utils.Grading(rc.Suite); gr != schemas.GradingJudged {
		return schemas.JudgeSpec{}, fmt.Errorf("suite %s has grading %s; a reply_rubric is judged, so the suite must declare grading: judged and pin a judge", rc.Suite.ID, gr)
	}
	if rc.Suite.Judge == nil {
		return schemas.JudgeSpec{}, fmt.Errorf("suite %s names no judge for the reply_rubric (provider, model)", rc.Suite.ID)
	}
	spec := *rc.Suite.Judge
	spec.PromptVersion = PromptChecksReplyV1
	spec.Auditor = nil
	if spec.Votes == 0 {
		spec.Votes = defaultJudgeVotes
	}
	switch {
	case spec.Provider == "" || spec.Model == "":
		return spec, errors.New("judge spec needs provider and model")
	case spec.Provider == judgePlaceholder || spec.Model == judgePlaceholder:
		return spec, fmt.Errorf("judge provider and model are still %s: pin them in suite.yaml", judgePlaceholder)
	case spec.Votes < 0 || spec.Votes%2 == 0 || spec.Votes > maxJudgeVotes:
		return spec, fmt.Errorf("judge votes %d: want an odd number from 1 to %d", spec.Votes, maxJudgeVotes)
	case cfg.Gateway == nil || cfg.URL == "":
		return spec, errors.New("checks grader is not wired to the gateway (register graders.NewChecks with the gateway and its URL)")
	case rc.Run == nil || !judgeKeyNameRe.MatchString(rc.Run.RunID):
		return spec, errors.New("run context has no usable run id for the judge's key name")
	}
	return spec, nil
}

// instructionText is the task's instruction, capped.
func instructionText(t *schemas.Task) string {
	if t == nil || t.Dir == "" || t.Instruction == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(t.Dir, filepath.FromSlash(t.Instruction)))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if r := []rune(s); len(r) > 8000 {
		s = string(r[:7999]) + "…"
	}
	return s
}

// transcriptEvent is one event of a simulated user's transcript.json.
type transcriptEvent struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	AfterEnd bool   `json:"after_end"`
}

// conversation is what the reply judge reads: every simulated-customer
// transcript in the exported state, the agent's tool calls, and the answer
// file when there is one. agentText is the agent's own words alone, for
// the judge guard.
func conversation(rc *schemas.RunContext, calls []taskchecks.Call) (conv, agentText string, err error) {
	var b, mine strings.Builder
	transcripts, _ := filepath.Glob(filepath.Join(rc.Dir, "state", "*", "transcript.json"))
	sort.Strings(transcripts)
	for _, p := range transcripts {
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return "", "", rerr
		}
		var t struct {
			Events []transcriptEvent `json:"events"`
		}
		if json.Unmarshal(raw, &t) != nil {
			continue
		}
		b.WriteString("Conversation with the customer:\n")
		for _, ev := range t.Events {
			if ev.AfterEnd {
				continue
			}
			who := "Customer"
			if ev.Role == "agent" {
				who = "Agent"
				mine.WriteString(ev.Content + "\n")
			}
			fmt.Fprintf(&b, "%s: %s\n", who, strings.TrimSpace(ev.Content))
		}
		b.WriteString("\n")
	}
	var toolLines []string
	for _, c := range calls {
		if c.Tool == "respond_to_user" {
			continue
		}
		line := fmt.Sprintf("%d. %s/%s %s", c.Seq, c.Server, c.Tool, c.Raw)
		if c.IsError {
			line += " (returned an error)"
		}
		toolLines = append(toolLines, line)
		mine.WriteString(c.Raw + "\n") // tool arguments are the agent's words too
	}
	if len(toolLines) > 0 {
		b.WriteString("Tool calls the agent made, in order:\n" + strings.Join(toolLines, "\n") + "\n\n")
	}
	answer, problem, aerr := ReadAnswer(rc.Dir)
	if aerr != nil {
		return "", "", aerr
	}
	if problem == "" && strings.TrimSpace(answer) != "" {
		b.WriteString("Final answer of the agent:\n" + strings.TrimSpace(answer) + "\n")
		mine.WriteString(answer)
	}
	return b.String(), mine.String(), nil
}
