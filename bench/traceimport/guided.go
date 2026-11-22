package traceimport

// Guided drafts: the inbox accept form says what should have happened (the
// right answer, or the right tool calls and the wrong ones, or that the
// recorded session was right), and the draft is written finished from that
// instead of carrying EDIT ME markers for a person to fill in by hand.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Limits of a guide.
const (
	MaxGuideAnswers = 20
	MaxGuideCalls   = 50
	maxGuideText    = 20000
)

// ChecksFileName is the declarative checks file a guided tool-use or mcp
// draft writes into its verifier tests folder.
const ChecksFileName = "checks.yaml"

// Guide is "what should have happened" for one recorded session.
type Guide struct {
	// Instruction replaces the first user request as the task's
	// instruction; empty keeps the request as it was sent (after redaction).
	// Multi-session (memory) drafts keep their recorded requests.
	Instruction string `json:"instruction,omitempty"`
	// KeepTurns adds the user's later messages to the instruction, in
	// order, so a multi-turn failure keeps every request.
	KeepTurns bool `json:"keep_turns,omitempty"`
	// UseRecorded says the recorded session was right: its answer, or its
	// tool calls, become the expected ones (a golden from a passing session).
	UseRecorded bool `json:"use_recorded,omitempty"`

	// qa and memory: the expected answer, one fact per entry, and how it is
	// matched (exact, contains_all, regex, numeric, choice).
	Answers []string `json:"answers,omitempty"`
	Match   string   `json:"match,omitempty"`
	// WrongAnswers must fail. RecordedWrong adds the recorded answer.
	WrongAnswers  []string `json:"wrong_answers,omitempty"`
	RecordedWrong bool     `json:"recorded_wrong,omitempty"`

	// tool-use and mcp: the calls a correct agent makes, the ones it must
	// never make, whether Calls must happen in order, and a judged rubric
	// for the final reply.
	Calls       []GuideCall `json:"calls,omitempty"`
	Forbidden   []GuideCall `json:"forbidden,omitempty"`
	Ordered     bool        `json:"ordered,omitempty"`
	ReplyRubric string      `json:"reply_rubric,omitempty"`
}

// GuideCall is one tool call of a guide. Args are matched as a subset: the
// call must carry these arguments with these values.
type GuideCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

var answerMatches = []string{"exact", "contains_all", "regex", "numeric", "choice"}

// normalize trims the guide and refuses what no draft can hold. A nil guide
// is fine.
func (g *Guide) normalize() error {
	if g == nil {
		return nil
	}
	g.Instruction = strings.TrimSpace(g.Instruction)
	g.ReplyRubric = strings.TrimSpace(g.ReplyRubric)
	if len(g.Instruction) > maxGuideText || len(g.ReplyRubric) > maxGuideText {
		return fmt.Errorf("guide: the instruction and the reply rubric are at most %d characters", maxGuideText)
	}
	g.Answers = trimList(g.Answers)
	g.WrongAnswers = trimList(g.WrongAnswers)
	if len(g.Answers) > MaxGuideAnswers || len(g.WrongAnswers) > MaxGuideAnswers {
		return fmt.Errorf("guide: at most %d answers and %d wrong answers", MaxGuideAnswers, MaxGuideAnswers)
	}
	for _, a := range append(slices.Clone(g.Answers), g.WrongAnswers...) {
		if len(a) > maxGuideText {
			return fmt.Errorf("guide: an answer is at most %d characters", maxGuideText)
		}
	}
	if g.Match != "" && !slices.Contains(answerMatches, g.Match) {
		return fmt.Errorf("guide: match %q: want one of %s", g.Match, strings.Join(answerMatches, ", "))
	}
	if len(g.Calls) > MaxGuideCalls || len(g.Forbidden) > MaxGuideCalls {
		return fmt.Errorf("guide: at most %d calls and %d forbidden calls", MaxGuideCalls, MaxGuideCalls)
	}
	for i := range g.Calls {
		if err := g.Calls[i].normalize("calls", i); err != nil {
			return err
		}
	}
	for i := range g.Forbidden {
		if err := g.Forbidden[i].normalize("forbidden", i); err != nil {
			return err
		}
	}
	return nil
}

func (c *GuideCall) normalize(field string, i int) error {
	c.Tool = strings.TrimSpace(c.Tool)
	if c.Tool == "" || len(c.Tool) > 128 || strings.ContainsAny(c.Tool, "\n\r") {
		return fmt.Errorf("guide: %s[%d]: a tool name of at most 128 characters", field, i)
	}
	return nil
}

func trimList(l []string) []string {
	var out []string
	for _, s := range l {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// check refuses a guide that leaves the draft of this kind unfinished.
func (g *Guide) check(kind string, s *Session) error {
	if g == nil {
		return nil
	}
	switch kind {
	case KindQA, KindMemory:
		if len(g.Calls) > 0 || len(g.Forbidden) > 0 || g.ReplyRubric != "" {
			return fmt.Errorf("guide: a %s task is graded on its answer; give answers, not tool calls or a reply rubric", kind)
		}
		if g.UseRecorded {
			if s.Outcome.Status == OutcomeError || strings.TrimSpace(s.FinalAnswer) == "" {
				return errors.New("guide: the recorded session failed or gave no answer, so it cannot be the expected answer; write the right answer instead")
			}
			if len(g.Answers) == 0 {
				g.Answers = []string{strings.TrimSpace(s.FinalAnswer)}
			}
		}
		if len(g.Answers) == 0 {
			return errors.New("guide: write the right answer (answers)")
		}
	case KindToolUse, KindMCP:
		if len(g.Answers) > 0 || len(g.WrongAnswers) > 0 || g.RecordedWrong {
			return fmt.Errorf("guide: a %s task is graded on its tool calls; give calls, not answers", kind)
		}
		if g.UseRecorded && len(g.Calls) == 0 {
			for _, c := range s.ToolCalls {
				if c.IsError {
					continue
				}
				gc := GuideCall{Tool: c.Name}
				var args map[string]any
				if json.Unmarshal(c.Arguments, &args) == nil && len(args) > 0 {
					gc.Args = args
				}
				g.Calls = append(g.Calls, gc)
			}
			if len(g.Calls) > MaxGuideCalls {
				g.Calls = g.Calls[:MaxGuideCalls]
			}
			g.Ordered = g.Ordered || len(g.Calls) > 1
		}
		if len(g.Calls) == 0 && len(g.Forbidden) == 0 && g.ReplyRubric == "" {
			return errors.New("guide: tick the calls a correct agent makes, mark the wrong ones, or write a reply rubric; the task needs at least one check")
		}
	}
	return nil
}

// instruction writes an instruction file: the EDIT ME draft, or with a
// guide the finished instruction.
func (w *draftWriter) instruction(rel, req string, requests int, extra string) {
	if w.guide == nil {
		w.put(rel, instructionDraft(req, requests, extra), true)
		return
	}
	var b strings.Builder
	if w.guide.Instruction != "" && requests > 0 {
		b.WriteString(w.guide.Instruction + "\n")
	} else {
		b.WriteString(strings.TrimSpace(req) + "\n")
	}
	if reqs := w.s.Requests(); w.guide.KeepTurns && requests > 1 && len(reqs) > 1 {
		b.WriteString("\nLater in the conversation you were also asked, in this order:\n\n")
		for i, r := range reqs[1:] {
			fmt.Fprintf(&b, "%d. %s\n", i+1, strings.ReplaceAll(strings.TrimSpace(r), "\n", "\n   "))
		}
	}
	if extra != "" {
		b.WriteString("\n" + extra + "\n")
	}
	w.unmark(rel)
	w.put(rel, []byte(b.String()), false)
}

// unmark drops rel from the files still holding EDIT ME markers.
func (w *draftWriter) unmark(rel string) {
	clean := path.Clean(filepath.ToSlash(rel))
	w.res.EditMe = slices.DeleteFunc(w.res.EditMe, func(s string) bool { return s == clean })
}

// guidedAnswer writes the hidden expected answer and the reference answer
// of a qa or memory draft from the guide.
func (w *draftWriter) guidedAnswer(shape draftShape, rel, question string) {
	g := w.guide
	match := g.Match
	if match == "" {
		match = "contains_all"
		if len(g.Answers) == 1 && !strings.Contains(g.Answers[0], "\n") && len(g.Answers[0]) <= 120 {
			match = "exact"
		}
	}
	wrong := slices.Clone(g.WrongAnswers)
	if rec := strings.TrimSpace(w.s.FinalAnswer); g.RecordedWrong && rec != "" && !slices.Contains(wrong, rec) && !slices.Contains(g.Answers, rec) {
		wrong = append(wrong, rec)
	}
	if wrong == nil {
		wrong = []string{}
	}
	doc := struct {
		Question     string   `yaml:"question"`
		Match        string   `yaml:"match"`
		Expected     []string `yaml:"expected"`
		Normalize    []string `yaml:"normalize,flow"`
		WrongAnswers []string `yaml:"wrong_answers"`
	}{oneLine(question, 500), match, g.Answers, []string{"lower", "collapse_space", "trim"}, wrong}
	b, err := yaml.Marshal(doc)
	if err != nil {
		w.err = err
		return
	}
	head := "# Hidden expected answer, written by the inbox accept form (\"what should have\n" +
		"# happened\") from a recorded session, and read by the answer_match grader.\n" +
		"# Plausible wrong answers must fail; admission checks each one.\n"
	w.unmark(rel)
	w.put(rel, append([]byte(head), b...), false)
	if strings.HasSuffix(shape.Controls.Reference, ".txt") {
		ref := g.Answers[0]
		if match == "contains_all" {
			ref = strings.Join(g.Answers, "\n")
		}
		w.put(shape.Controls.Reference, []byte(ref+"\n"), false)
	}
	w.checks = []string{fmt.Sprintf("the answer matches (%s) %s", match, strings.Join(quoteAll(g.Answers), ", "))}
	if len(wrong) > 0 {
		w.checks = append(w.checks, fmt.Sprintf("%d plausible wrong answers fail", len(wrong)))
	}
}

func quoteAll(l []string) []string {
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = quote(oneLine(s, 120))
	}
	return out
}

// guidedTools writes a tool-use or mcp draft's checks.yaml, reference and
// mutant controls from the guide. The checks grader reads checks.yaml, so
// checks.py is left as an empty list for the suite's verifier command.
func (w *draftWriter) guidedTools(shape draftShape) {
	if w.err != nil {
		return
	}
	g, res := w.guide, w.res
	toActions := func(l []GuideCall) []scaffold.ScenarioAction {
		out := make([]scaffold.ScenarioAction, 0, len(l))
		for _, c := range l {
			out = append(out, scaffold.ScenarioAction{Tool: c.Tool, Args: c.Args})
		}
		return out
	}
	checks, _, err := scaffold.ScenarioChecksYAML("Checks of this task, written by the inbox accept form (\"what should have happened\")\n"+
		"from a recorded session. The checks grader reads this file; edit it freely.",
		toActions(g.Calls), toActions(g.Forbidden), g.Ordered, g.ReplyRubric, "")
	if err != nil {
		w.err = err
		return
	}
	tests := shape.Verifier.Tests
	if tests == "" {
		tests = "verifier"
	}
	w.put(path.Join(tests, ChecksFileName), checks, false)
	py := path.Join(tests, "checks.py")
	w.unmark(py)
	w.put(py, []byte("# This task is graded by "+ChecksFileName+" beside this file (grader checks);\n"+
		"# the suite's verifier command only runs for tasks without one.\nCHECKS = []\n"), false)
	for _, c := range g.Calls {
		w.checks = append(w.checks, "the agent calls "+c.Tool+argsNote(c.Args))
	}
	for _, c := range g.Forbidden {
		w.checks = append(w.checks, "the agent never calls "+c.Tool+argsNote(c.Args))
	}
	if g.Ordered && len(g.Calls) > 1 {
		w.checks = append(w.checks, "the calls happen in the order listed")
	}
	if g.ReplyRubric != "" {
		w.checks = append(w.checks, "the final reply meets the rubric: "+oneLine(g.ReplyRubric, 200))
	}
	if !strings.HasSuffix(shape.Controls.Reference, ".sh") {
		return
	}
	svc := serviceID(shape, res.Template)
	if res.Replayed {
		svc = ReplayServiceID
	}
	if res.Template == KindToolUse && !res.Replayed {
		res.Warnings = append(res.Warnings, "the tool server is the suite's own, so map each call of "+shape.Controls.Reference+" to its HTTP route before the readiness check")
		return
	}
	calls := func(l []GuideCall) *Session {
		cp := *w.s
		cp.ToolCalls = nil
		for i, c := range l {
			args, _ := json.Marshal(c.Args)
			if c.Args == nil {
				args = []byte("{}")
			}
			cp.ToolCalls = append(cp.ToolCalls, ToolCall{Seq: i + 1, Name: c.Tool, Arguments: args, Result: "(expected call)"})
		}
		return &cp
	}
	header := "# Reference control written by the inbox accept form: the calls a correct\n" +
		"# agent makes, as the person accepting the session listed them. Admission runs\n" +
		"# it in the agent's place and requires every check to pass.\n"
	w.unmark(shape.Controls.Reference)
	w.putMode(shape.Controls.Reference, callScript(header, calls(g.Calls), res.Template, svc, res.Replayed, false), false, 0o755)
	var mutants []string
	if len(g.Calls) > 0 {
		key := g.Calls[len(g.Calls)-1]
		rel := "controls/mutant-skips-" + guideSlug(key.Tool) + ".sh"
		h := "# Mutant control written by the inbox accept form: the expected calls without\n" +
			"# the last one (" + oneLine(key.Tool, 80) + "). Admission requires it to fail.\n"
		w.putMode(rel, callScript(h, calls(g.Calls[:len(g.Calls)-1]), res.Template, svc, res.Replayed, true), false, 0o755)
		mutants = append(mutants, rel)
	}
	if len(g.Forbidden) > 0 && g.Forbidden[0].Args != nil {
		bad := g.Forbidden[0]
		rel := "controls/mutant-also-" + guideSlug(bad.Tool) + ".sh"
		h := "# Mutant control written by the inbox accept form: the expected calls and also\n" +
			"# a forbidden one (" + oneLine(bad.Tool, 80) + "). Admission requires it to fail.\n"
		w.putMode(rel, callScript(h, calls(append(slices.Clone(g.Calls), bad)), res.Template, svc, res.Replayed, true), false, 0o755)
		mutants = append(mutants, rel)
	}
	for _, m := range mutants {
		if !slices.Contains(res.Mutants, m) {
			res.Mutants = append(res.Mutants, m)
		}
	}
	if len(res.Mutants) == 0 {
		res.Warnings = append(res.Warnings, "no mutant control proves the checks: list a forbidden call with its arguments, or an expected call")
	}
}

func argsNote(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	b, _ := json.Marshal(args)
	return " with " + oneLine(string(b), 160)
}

func guideSlug(s string) string {
	out := strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return '-'
	}, s), "-")
	if len(out) > 40 {
		out = out[:40]
	}
	if out == "" {
		out = "call"
	}
	return out
}

// guidedRequirements is the requirements map of a guided draft: each check
// and where it came from.
func (w *draftWriter) guidedRequirements(id string, src Source) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: what a correct result holds\n\n", id)
	b.WriteString("Written by the inbox accept form from a recorded session: the person accepting\n")
	if w.guide.UseRecorded {
		b.WriteString("it confirmed the recorded session was right, so it is the expected behaviour.\n\n")
	} else {
		b.WriteString("it said what should have happened. Each check below follows from the instruction.\n\n")
	}
	for _, c := range w.checks {
		b.WriteString("- " + c + "\n")
	}
	fmt.Fprintf(&b, "\n- Recorded session: %s trace %s, file %s\n", src.Format, w.s.ID, src.File)
	fmt.Fprintf(&b, "- Outcome as the trace records it: %s\n", outcomeLine(w.s))
	return []byte(b.String())
}

// GuidedDone reports whether a guided draft is finished: no file holds an
// EDIT ME marker any more.
func GuidedDone(res *DraftResult) bool {
	if res == nil {
		return false
	}
	for _, rel := range res.EditMe {
		b, err := os.ReadFile(filepath.Join(res.Dir, filepath.FromSlash(rel)))
		if err == nil && strings.Contains(string(b), scaffold.EditMeMarker) {
			return false
		}
	}
	return true
}
