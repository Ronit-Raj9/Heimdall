// Package tracebuild turns one recorded failure into a finished scenario
// task draft (round 7, lane T1). A builder model reads the redacted session
// and names where the agent went wrong, with quotes checked against the
// session text; the task shape that can grade that failure is chosen by
// rule; the model then writes the instruction, the checks and a few wrong
// agent variants, and the task folder is written through the existing draft
// writer (traceimport.WriteDraft with a guide, which lays the shape with
// scaffold.InitScenarioTask). When the right behaviour needs a tool result
// the trace never showed, the model's stand-in results are added to the
// replay fixture in the replay server's own format.
//
// The builder never marks a task good: admission decides, and only a person
// approves the instruction. Nothing in a Result sets provenance fields.
package tracebuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/tracedraft"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// Failure kinds.
const (
	KindWrongTool      = "wrong_tool"
	KindWrongArguments = "wrong_arguments"
	KindFalseClaim     = "false_claim"
	KindMissingStep    = "missing_step"
	KindUnsafeAction   = "unsafe_action"
	KindOther          = "other"
)

// FailureKinds lists the kinds a diagnosis may name.
func FailureKinds() []string {
	return []string{KindWrongTool, KindWrongArguments, KindFalseClaim, KindMissingStep, KindUnsafeAction, KindOther}
}

// Purpose names the builder's calls in the model call ledger.
const Purpose = "trace_build"

// Default limits.
const (
	DefaultMaxCalls = 6
	DefaultWall     = 5 * time.Minute
	maxReplyBytes   = 64 << 10
	maxFileBytes    = 256 << 10
)

// ErrLimit is returned when a limit stops the loop before a task is written.
var ErrLimit = errors.New("the builder stopped at its limit")

// Limits bound one build. BudgetUSD is also enforced by the Caller (the
// job's virtual key cap); it is checked here before each call as well.
type Limits struct {
	BudgetUSD float64
	MaxCalls  int
	Wall      time.Duration
}

// Input is one build request.
type Input struct {
	Item    string               // inbox item id, the quotes' run_id
	Session *traceimport.Session // redacted already; Build redacts a copy again
	Source  traceimport.Source
	Suite   string // target suite id; the draft is written in a scratch suite of that id
	TaskID  string // new task id; empty derives one from the item
	WorkDir string // scratch folder Build may write in; empty uses a temp folder
	Model   investigator.Caller
	Limits  Limits
	// Feedback is the failing control of an earlier attempt, for the one
	// retry the website offers ("Try again"). Empty on a first build.
	Feedback string
}

// File is one file of the task folder, relative to the folder.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Failure is the diagnosis.
type Failure struct {
	Kind          string             `json:"kind"`
	Summary       string             `json:"summary"`
	Quotes        []tracedraft.Quote `json:"quotes"`
	DroppedQuotes int                `json:"dropped_quotes"`
}

// Mutant is one wrong agent variant. Control is the control admission runs
// for it (a mutant script, or wrong_answer_<n> of a qa task); empty when no
// control could carry it, which notes explain.
type Mutant struct {
	Name            string `json:"name"`
	WhatItDoesWrong string `json:"what_it_does_wrong"`
	Control         string `json:"control,omitempty"`
}

// Standin is the stand-in for tool results the trace never showed.
type Standin struct {
	Why   string `json:"why"`
	Files []File `json:"files"`
}

// Result is what Build returns and what the job stores.
type Result struct {
	Failure     Failure  `json:"failure"`
	Shape       string   `json:"shape"`
	ShapeReason string   `json:"shape_reason"`
	TaskID      string   `json:"task_id"`
	Files       []File   `json:"files"`
	Mutants     []Mutant `json:"mutants"`
	Standin     *Standin `json:"standin"`
	SpentUSD    float64  `json:"spent_usd"`
	ModelCalls  int      `json:"model_calls"`
	Notes       []string `json:"notes"`
	// Offerable is false when the draft is not finished (an EDIT ME marker
	// left, or the model said it was unsure): it is then not offered for
	// approval.
	Offerable bool `json:"offerable"`
	// Dir is the written task folder on disk (not stored in JSON).
	Dir string `json:"-"`
}

// loop counts calls and enforces the limits.
type loop struct {
	in     Input
	res    *Result
	sess   *traceimport.Session
	src    func(string, int) (string, bool)
	served string
}

// Build runs the steps: diagnose, shape, write, variants, stand-in.
func Build(ctx context.Context, in Input) (*Result, error) {
	if in.Model == nil {
		return nil, errors.New("no builder model: pick a model")
	}
	if in.Limits.BudgetUSD <= 0 {
		return nil, errors.New("no spending cap: set a cap above $0")
	}
	if in.Session == nil {
		return nil, errors.New("the item has no session")
	}
	if in.Limits.MaxCalls <= 0 {
		in.Limits.MaxCalls = DefaultMaxCalls
	}
	if in.Limits.Wall <= 0 {
		in.Limits.Wall = DefaultWall
	}
	if in.Suite == "" || !scaffold.ValidID(in.Suite) {
		return nil, fmt.Errorf("suite %q: want lowercase letters, digits, '.', '_' or '-'", in.Suite)
	}
	if in.TaskID == "" {
		in.TaskID = taskID(in.Item)
	}
	ctx, cancel := context.WithTimeout(ctx, in.Limits.Wall)
	defer cancel()
	sess, err := redactedCopy(in.Session)
	if err != nil {
		return nil, err
	}
	l := &loop{in: in, sess: sess, src: tracedraft.SessionSource(in.Item, sess), res: &Result{TaskID: in.TaskID, Mutants: []Mutant{}, Files: []File{}, Notes: []string{}}}
	if err := l.diagnose(ctx); err != nil {
		return l.res, err
	}
	l.chooseShape()
	w, err := l.write(ctx)
	if err != nil {
		return l.res, err
	}
	if err := l.materialize(w); err != nil {
		return l.res, err
	}
	return l.res, nil
}

// call makes one model call within the limits, then checks the reply's size
// and that it holds no secret.
func (l *loop) call(ctx context.Context, msgs []investigator.Message, schema string) (string, error) {
	if l.res.ModelCalls >= l.in.Limits.MaxCalls {
		return "", fmt.Errorf("%w: %d model calls made", ErrLimit, l.res.ModelCalls)
	}
	if l.in.Model.Spent() >= l.in.Limits.BudgetUSD || l.res.SpentUSD >= l.in.Limits.BudgetUSD {
		return "", fmt.Errorf("%w: $%.4f spent of $%.2f", ErrLimit, l.res.SpentUSD, l.in.Limits.BudgetUSD)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrLimit, err)
	}
	l.res.ModelCalls++
	r, err := l.in.Model.Call(ctx, investigator.Request{Purpose: Purpose, Messages: msgs, Schema: schema, MaxTokens: 3000})
	l.res.SpentUSD += r.CostUSD
	if err != nil {
		if errors.Is(err, investigator.ErrBudget) {
			return "", fmt.Errorf("%w: %v", ErrLimit, err)
		}
		return "", err
	}
	if r.Served != "" && r.Model != "" && r.Served != r.Model {
		//nolint:staticcheck // ST1005: a full sentence shown to the user as is
		return "", fmt.Errorf("The gateway answered with %s, not %s. Nothing was built.", r.Served, r.Model)
	}
	return r.Text, nil
}

// validated is one reply checked: JSON decoded into v, size and secrets.
func validated(text string, v any) error {
	if len(text) > maxReplyBytes {
		return fmt.Errorf("the reply is %d bytes; at most %d", len(text), maxReplyBytes)
	}
	if err := json.Unmarshal([]byte(stripFence(text)), v); err != nil {
		return fmt.Errorf("the reply is not the JSON asked for: %v", err)
	}
	red := traceimport.NewRedactor()
	red.Text(text)
	if red.Report().Total() > 0 {
		return fmt.Errorf("the reply holds what looks like a secret or personal data (%s); leave it out", red.Report())
	}
	return nil
}

// ask calls, validates with check, and retries once with the reason.
func (l *loop) ask(ctx context.Context, msgs []investigator.Message, schema string, v any, check func() error) error {
	text, err := l.call(ctx, msgs, schema)
	if err != nil {
		return err
	}
	reason := validated(text, v)
	if reason == nil {
		reason = check()
	}
	if reason == nil {
		return nil
	}
	msgs = append(slices.Clone(msgs),
		investigator.Message{Role: "assistant", Content: text},
		investigator.Message{Role: "user", Content: "Your answer was refused: " + reason.Error() + "\nAnswer again with the whole JSON."})
	text, err = l.call(ctx, msgs, schema)
	if err != nil {
		return err
	}
	if err := validated(text, v); err != nil {
		return fmt.Errorf("the builder model's answer was refused twice: %w", err)
	}
	if err := check(); err != nil {
		return fmt.Errorf("the builder model's answer was refused twice: %w", err)
	}
	return nil
}

// diagnosis is the reply of step 1.
type diagnosis struct {
	Kind    string             `json:"kind"`
	Summary string             `json:"summary"`
	Quotes  []tracedraft.Quote `json:"quotes"`
}

func (l *loop) diagnose(ctx context.Context) error {
	var d diagnosis
	check := func() error {
		d.Summary = strings.TrimSpace(d.Summary)
		if !slices.Contains(FailureKinds(), d.Kind) {
			return fmt.Errorf("kind %q: want one of %s", d.Kind, strings.Join(FailureKinds(), ", "))
		}
		if d.Summary == "" || len(d.Summary) > 400 || strings.Count(d.Summary, "\n") > 0 {
			return errors.New("summary: one sentence of at most 400 characters")
		}
		ok := 0
		var bad []string
		for _, q := range tracedraft.CheckQuotes(d.Quotes, l.src) {
			if q.OK {
				ok++
			} else {
				bad = append(bad, fmt.Sprintf("%q (%s; closest: %q)", q.Quote.Text, q.Reason, q.Closest))
			}
		}
		if ok == 0 {
			return fmt.Errorf("no quote was found in the session; copy exact text: %s", strings.Join(bad, "; "))
		}
		return nil
	}
	if err := l.ask(ctx, diagnosePrompt(l.in.Item, l.sess, l.in.Feedback), diagnoseSchema, &d, check); err != nil {
		return err
	}
	f := Failure{Kind: d.Kind, Summary: d.Summary, Quotes: []tracedraft.Quote{}}
	for _, q := range tracedraft.CheckQuotes(d.Quotes, l.src) {
		if q.OK {
			f.Quotes = append(f.Quotes, q.Quote)
		} else {
			f.DroppedQuotes++
		}
	}
	if f.DroppedQuotes > 0 {
		l.res.Notes = append(l.res.Notes, fmt.Sprintf("%d quote(s) of the diagnosis were not found in the session and were dropped.", f.DroppedQuotes))
	}
	l.res.Failure = f
	return nil
}

// chooseShape picks the task shape by rule from the diagnosis. The mcp and
// tool-use verifiers read only the tool calls (calls.json), so a false claim
// in the reply is graded by a qa task, whose verifier reads the final answer.
func (l *loop) chooseShape() {
	r := l.res
	hasTools := len(l.sess.ToolCalls) > 0
	switch r.Failure.Kind {
	case KindFalseClaim:
		r.Shape, r.ShapeReason = traceimport.KindQA, "The fault is in the final reply, and only the qa verifier reads the reply; the tool shapes grade tool calls only."
	case KindWrongTool, KindWrongArguments, KindMissingStep, KindUnsafeAction:
		if hasTools {
			r.Shape, r.ShapeReason = traceimport.KindMCP, "The fault is in the tool calls, so the task grades the calls the agent makes against replayed tool answers."
		} else {
			r.Shape, r.ShapeReason = traceimport.KindToolUse, "The fault is in the tool calls, so the task grades the calls the agent makes."
		}
	default:
		if strings.TrimSpace(l.sess.FinalAnswer) != "" || !hasTools {
			r.Shape, r.ShapeReason = traceimport.KindQA, "No tool fault was named; the task grades the final answer."
		} else {
			r.Shape, r.ShapeReason = traceimport.KindMCP, "No answer was recorded; the task grades the tool calls."
		}
	}
}

// writing is the reply of step 3 to 5.
type writing struct {
	Instruction string         `json:"instruction"`
	Answers     []string       `json:"answers"`
	Match       string         `json:"match"`
	Calls       []call         `json:"calls"`
	Forbidden   []call         `json:"forbidden"`
	Ordered     bool           `json:"ordered"`
	Variants    []variant      `json:"variants"`
	Standin     []standinEntry `json:"standin"`
	StandinWhy  string         `json:"standin_why"`
	Unsure      string         `json:"unsure"`
}

type call struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

type variant struct {
	Name   string `json:"name"`
	What   string `json:"what_it_does_wrong"`
	Answer string `json:"answer,omitempty"` // qa: the wrong final answer
	Calls  []call `json:"calls,omitempty"`  // tool shapes: the wrong call list
}

type standinEntry struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Result    string         `json:"result"`
	IsError   bool           `json:"is_error"`
}

func (l *loop) write(ctx context.Context) (*writing, error) {
	var w writing
	shape := l.res.Shape
	known := map[string]bool{}
	for _, c := range l.sess.ToolCalls {
		known[c.Name] = true
	}
	check := func() error {
		w.Instruction = strings.TrimSpace(w.Instruction)
		if w.Instruction == "" || len(w.Instruction) > 4000 {
			return errors.New("instruction: the task text, 1 to 4000 characters")
		}
		if strings.Contains(w.Instruction, scaffold.EditMeMarker) {
			return errors.New("instruction: no EDIT ME markers")
		}
		if len(w.Variants) < 2 || len(w.Variants) > 4 {
			return fmt.Errorf("variants: two to four, got %d", len(w.Variants))
		}
		for i, v := range w.Variants {
			if strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.What) == "" {
				return fmt.Errorf("variants[%d]: name and what_it_does_wrong", i)
			}
		}
		switch shape {
		case traceimport.KindQA, traceimport.KindMemory:
			if len(w.Answers) == 0 {
				return errors.New("answers: the right answer, at least one entry")
			}
			for _, a := range w.Answers {
				if strings.Contains(w.Instruction, strings.TrimSpace(a)) && len(strings.TrimSpace(a)) > 3 {
					return errors.New("instruction: it must not hold the expected answer")
				}
			}
			for i, v := range w.Variants {
				if strings.TrimSpace(v.Answer) == "" {
					return fmt.Errorf("variants[%d]: a qa variant gives its wrong answer", i)
				}
			}
		default:
			if len(w.Calls) == 0 && len(w.Forbidden) == 0 {
				return errors.New("calls: the calls a correct agent makes, or forbidden calls")
			}
			stood := map[string]bool{}
			for _, s := range w.Standin {
				stood[s.Tool] = true
			}
			for _, c := range append(slices.Clone(w.Calls), w.Forbidden...) {
				if c.Tool == "" {
					return errors.New("calls: every call names its tool")
				}
				if !known[c.Tool] && !stood[c.Tool] {
					return fmt.Errorf("calls: tool %q never appears in the session; give its result in standin or leave it out", c.Tool)
				}
			}
		}
		return nil
	}
	if err := l.ask(ctx, writePrompt(l.in.Item, l.sess, l.res.Failure, shape, l.in.Feedback), writeSchema, &w, check); err != nil {
		return nil, err
	}
	return &w, nil
}

// materialize writes the task folder through the draft writer in a scratch
// suite of the chosen shape and fills the Result from it.
func (l *loop) materialize(w *writing) error {
	r := l.res
	work := l.in.WorkDir
	if work == "" {
		d, err := os.MkdirTemp("", "tracebuild-")
		if err != nil {
			return err
		}
		work = d
	}
	root := filepath.Join(work, "benchmarks")
	suiteDir := filepath.Join(root, l.in.Suite)
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: l.in.Suite, Template: r.Shape})
	if err != nil {
		return err
	}
	if err := scaffold.WriteFiles(suiteDir, files, false); err != nil {
		return err
	}
	g := &traceimport.Guide{Instruction: w.Instruction}
	sess := l.sess
	switch r.Shape {
	case traceimport.KindQA, traceimport.KindMemory:
		g.Answers, g.Match = w.Answers, w.Match
		if g.Match == "" {
			g.Match = "contains_all"
		}
		if !slices.Contains([]string{"exact", "contains_all", "regex", "numeric", "choice"}, g.Match) {
			r.Notes = append(r.Notes, "The model's match "+g.Match+" is unknown; contains_all is used.")
			g.Match = "contains_all"
		}
		g.RecordedWrong = strings.TrimSpace(sess.FinalAnswer) != ""
		for _, v := range w.Variants {
			g.WrongAnswers = append(g.WrongAnswers, v.Answer)
		}
	default:
		if err := checksGrader(suiteDir); err != nil {
			return err
		}
		g.Calls, g.Forbidden, g.Ordered = guideCalls(w.Calls), guideCalls(w.Forbidden), w.Ordered
		for _, v := range w.Variants {
			for _, c := range v.Calls {
				if !hasCall(g.Calls, c) && !hasCall(g.Forbidden, c) && c.Args != nil {
					g.Forbidden = append(g.Forbidden, traceimport.GuideCall{Tool: c.Tool, Args: c.Args})
				}
			}
		}
		if len(w.Standin) > 0 {
			sess = withStandin(sess, w.Standin)
		}
	}
	res, err := traceimport.WriteDraft(traceimport.DraftOptions{SuiteDir: suiteDir, ID: l.in.TaskID, Kind: r.Shape, Session: sess, Source: l.in.Source, Replay: true, Guide: g})
	if err != nil {
		return fmt.Errorf("write the task: %w", err)
	}
	r.Dir = res.Dir
	r.Notes = append(r.Notes, res.Warnings...)
	if r.Files, err = readTree(res.Dir); err != nil {
		return err
	}
	r.Mutants = mutantsOf(r.Shape, w.Variants, g, res)
	if len(w.Standin) > 0 && r.Shape != traceimport.KindQA {
		why := strings.TrimSpace(w.StandinWhy)
		if why == "" {
			why = "The right behaviour calls a tool, or gets a result, the trace never showed."
		}
		st := &Standin{Why: why, Files: []File{}}
		for _, f := range r.Files {
			if f.Path == traceimport.ObservedToolsFile || strings.HasPrefix(f.Path, "replay/") {
				st.Files = append(st.Files, f)
			}
		}
		r.Standin = st
	}
	r.Offerable = traceimport.GuidedDone(res) && len(res.EditMe) == 0
	for _, f := range r.Files {
		if f.Path != "task.yaml" && strings.Contains(f.Content, scaffold.EditMeMarker) { // task.yaml only carries a comment naming the marker
			r.Offerable = false
			r.Notes = append(r.Notes, f.Path+" still holds an EDIT ME marker.")
		}
	}
	if u := strings.TrimSpace(w.Unsure); u != "" {
		r.Offerable = false
		r.Notes = append(r.Notes, "The builder model is unsure: "+oneLine(u, 400))
	}
	return nil
}

// mutantsOf names each variant with the control that carries it.
func mutantsOf(shape string, vs []variant, g *traceimport.Guide, res *traceimport.DraftResult) []Mutant {
	out := []Mutant{}
	switch shape {
	case traceimport.KindQA, traceimport.KindMemory:
		for i, v := range vs {
			out = append(out, Mutant{Name: v.Name, WhatItDoesWrong: v.What, Control: fmt.Sprintf("wrong_answer_%d", i+1)})
		}
		return out
	}
	for _, v := range vs {
		m := Mutant{Name: v.Name, WhatItDoesWrong: v.What}
		for _, c := range v.Calls {
			if hasCall(g.Forbidden, c) && len(g.Forbidden) > 0 && sameCall(g.Forbidden[0], c) {
				for _, rel := range res.Mutants {
					if strings.HasPrefix(rel, "controls/mutant-also-") {
						m.Control = rel
					}
				}
			}
		}
		if m.Control == "" && len(v.Calls) < len(g.Calls) {
			for _, rel := range res.Mutants {
				if strings.HasPrefix(rel, "controls/mutant-skips-") {
					m.Control = rel
				}
			}
		}
		if m.Control == "" && slices.Contains(res.Mutants, traceimport.ObservedMutant) {
			m.Control = traceimport.ObservedMutant
		}
		out = append(out, m)
	}
	return out
}

func guideCalls(l []call) []traceimport.GuideCall {
	out := make([]traceimport.GuideCall, 0, len(l))
	for _, c := range l {
		out = append(out, traceimport.GuideCall{Tool: c.Tool, Args: c.Args})
	}
	return out
}

func sameCall(g traceimport.GuideCall, c call) bool {
	a, _ := json.Marshal(g.Args)
	b, _ := json.Marshal(c.Args)
	return g.Tool == c.Tool && string(a) == string(b)
}

func hasCall(l []traceimport.GuideCall, c call) bool {
	return slices.ContainsFunc(l, func(g traceimport.GuideCall) bool { return sameCall(g, c) })
}

// withStandin adds the stand-in results as extra observed calls, so the
// replay server (which matches a call by name and arguments) answers them.
func withStandin(s *traceimport.Session, st []standinEntry) *traceimport.Session {
	cp := *s
	cp.ToolCalls = slices.Clone(s.ToolCalls)
	n := len(cp.ToolCalls)
	for i, e := range st {
		args, err := json.Marshal(e.Arguments)
		if err != nil || e.Arguments == nil {
			args = []byte("{}")
		}
		cp.ToolCalls = append(cp.ToolCalls, traceimport.ToolCall{Seq: n + i + 1, ID: "standin_" + fmt.Sprint(i+1), Name: e.Tool, Arguments: args, Result: e.Result, IsError: e.IsError})
	}
	return &cp
}

func checksGrader(suiteDir string) error {
	p := filepath.Join(suiteDir, "suite.yaml")
	raw, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	next, err := scaffold.SetChecksGrader(raw, nil)
	if err != nil {
		return err
	}
	return os.WriteFile(p, next, 0o644)
}

// readTree reads every file of the task folder.
func readTree(dir string) ([]File, error) {
	out := []File{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if len(b) > maxFileBytes {
			b = b[:maxFileBytes]
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, File{Path: filepath.ToSlash(rel), Content: string(b)})
		return nil
	})
	return out, err
}

func taskID(item string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(item) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 24 {
		s = s[:24]
	}
	return "from-" + strings.Trim(s, "-") + "-built"
}

func redactedCopy(s *traceimport.Session) (*traceimport.Session, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var out traceimport.Session
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	traceimport.NewRedactor().Session(&out)
	return &out, nil
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "```"); ok {
		rest = strings.TrimPrefix(rest, "json")
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "```"))
	}
	return s
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}
