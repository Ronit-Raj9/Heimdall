package traceimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Draft kinds (DraftOptions.Kind): the scenario templates a trace can become.
const (
	KindQA      = scaffold.TemplateQA
	KindToolUse = scaffold.TemplateToolUse
	KindMCP     = scaffold.TemplateMCP
	KindMemory  = scaffold.TemplateMemory
)

// Kinds lists the draft kinds in a stable order.
func Kinds() []string { return []string{KindQA, KindToolUse, KindMCP, KindMemory} }

// Files a draft adds beside task.yaml. Neither is in the workspace, so the
// agent never sees them.
const (
	ObservedToolsFile = "tools.observed.json" // the observed tool calls and results, for a replaying mock
	TranscriptFile    = "from-trace.md"       // the whole recorded session, for the person editing the draft
	createdBy         = "task from-trace"
)

// placeholderAnswer stands where the correct answer must go when the
// recorded answer cannot be trusted.
const placeholderAnswer = "REPLACE WITH THE CORRECT ANSWER"

// Source says where a draft's session came from. Redacted is nil when
// redaction was off.
type Source struct {
	File     string // base name of the trace file
	SHA256   string // of the whole trace file
	Format   string
	Redacted Report
}

// DraftOptions configures WriteDraft.
type DraftOptions struct {
	SuiteDir string   // the suite folder holding suite.yaml
	ID       string   // new task id
	Kind     string   // qa, tool-use, mcp or memory; empty takes the suite's template
	License  string   // SPDX id or "proprietary"; empty keeps the copied task's
	Force    bool     // replace an existing task folder
	Session  *Session // already redacted when redaction is on
	Source   Source
	// Replay swaps the copied tool service of a tool-use or mcp draft for a
	// replay server (replay/ in the task folder) seeded with the observed
	// calls, when the copied service is seeded through files:. False keeps
	// the suite's own service and writes the observed calls as a fixture.
	Replay bool
}

// DraftResult is what WriteDraft wrote.
type DraftResult struct {
	Dir         string
	Template    string
	From        string   // task whose shape was copied
	EditMe      []string // files holding EDIT ME markers, relative to Dir
	Written     []string // files written from the trace, relative to Dir
	SuiteEdited bool
	Replayed    bool     // the draft's tool service replays the observed calls
	Mutants     []string // mutant controls the draft added, relative to Dir
	Warnings    []string
}

// WriteDraft writes a task draft from one session into an existing scenario
// suite. It first lays the task's shape with scaffold.InitScenarioTask (the
// machinery of `task init -kind`), then replaces what the trace knows: the
// instruction (the first user request), the expected answer or the final
// state checks as EDIT ME drafts, the reference control, the observed tool
// calls and a transcript. A judgement the trace cannot make (what the right
// answer or the right final state is) always carries an EDIT ME marker that
// `task validate` refuses. When filling fails, the task folder is removed and
// suite.yaml restored.
func WriteDraft(opts DraftOptions) (*DraftResult, error) {
	if opts.Session == nil {
		return nil, errors.New("no session to draft from")
	}
	if opts.Kind != "" && !slices.Contains(Kinds(), opts.Kind) {
		return nil, fmt.Errorf("-kind %q: want one of %s", opts.Kind, strings.Join(Kinds(), ", "))
	}
	suitePath := filepath.Join(opts.SuiteDir, "suite.yaml")
	suiteYAML, err := os.ReadFile(suitePath)
	if err != nil {
		return nil, fmt.Errorf("read suite: %w", err)
	}
	if tmpl := scaffold.DetectTemplate(suiteYAML); opts.Kind == "" && tmpl != "" && !slices.Contains(Kinds(), tmpl) {
		return nil, fmt.Errorf("%s is a %s suite; task from-trace drafts %s tasks", suitePath, tmpl, strings.Join(Kinds(), ", "))
	}
	base, err := scaffold.InitScenarioTask(scaffold.ScenarioTaskOptions{SuiteDir: opts.SuiteDir, ID: opts.ID, Kind: opts.Kind, License: opts.License, Force: opts.Force})
	if err != nil {
		return nil, err
	}
	res := &DraftResult{Dir: base.Dir, Template: base.Template, From: base.From, SuiteEdited: base.SuiteEdited, Warnings: base.Warnings}
	rollback := func(cause error) (*DraftResult, error) {
		if rmErr := os.RemoveAll(base.Dir); rmErr != nil {
			cause = fmt.Errorf("%w (and remove %s: %v)", cause, base.Dir, rmErr)
		}
		if base.SuiteEdited {
			if wErr := os.WriteFile(suitePath, suiteYAML, 0o644); wErr != nil {
				cause = fmt.Errorf("%w (and restore %s: %v)", cause, suitePath, wErr)
			}
		}
		return nil, cause
	}
	if !slices.Contains(Kinds(), base.Template) {
		return rollback(fmt.Errorf("the suite is a %s suite; task from-trace drafts %s tasks", base.Template, strings.Join(Kinds(), ", ")))
	}
	if err := fill(opts, res); err != nil {
		return rollback(fmt.Errorf("draft from trace: %w", err))
	}
	return res, nil
}

// draftShape is what fill reads from the task.yaml task init wrote.
type draftShape struct {
	Instruction string `yaml:"instruction"`
	Sessions    []struct {
		Instruction string `yaml:"instruction"`
		Files       string `yaml:"files"`
	} `yaml:"sessions"`
	Services []struct {
		ID    string            `yaml:"id"`
		Role  string            `yaml:"role"`
		Files map[string]string `yaml:"files"`
	} `yaml:"services"`
	Verifier struct {
		Tests  string `yaml:"tests"`
		Answer string `yaml:"answer"`
	} `yaml:"verifier"`
	Controls struct {
		Reference string `yaml:"reference"`
	} `yaml:"controls"`
}

// fill replaces the copied shape's content with what the session holds.
func fill(opts DraftOptions, res *DraftResult) error {
	meta, err := os.ReadFile(filepath.Join(res.Dir, "task.yaml"))
	if err != nil {
		return err
	}
	var shape draftShape
	if err := yaml.Unmarshal(meta, &shape); err != nil {
		return fmt.Errorf("parse task.yaml: %w", err)
	}
	s := opts.Session
	reqs := s.Requests()
	if len(reqs) == 0 {
		return errors.New("the session has no user request to draft an instruction from")
	}
	w := &draftWriter{dir: res.Dir, res: res, s: s}
	if len(shape.Sessions) > 0 {
		w.sessions(shape, reqs)
	} else {
		w.put(shape.Instruction, instructionDraft(reqs[0], len(reqs), ""), true)
	}
	switch res.Template {
	case KindQA, KindMemory:
		failed := s.Outcome.Status == OutcomeError || strings.TrimSpace(s.FinalAnswer) == ""
		answer := shape.Verifier.Answer
		if answer == "" {
			answer = path.Join(shape.Verifier.Tests, "answer.yaml")
		}
		w.put(answer, answerDraft(reqs[len(reqs)-1], s.FinalAnswer, failed), true)
		if strings.HasSuffix(shape.Controls.Reference, ".txt") {
			ref := ""
			if !failed {
				ref = strings.TrimSpace(s.FinalAnswer) + "\n"
			}
			w.put(shape.Controls.Reference, []byte(ref), false)
		}
	case KindToolUse, KindMCP:
		if err := w.tools(shape, opts, reqs); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "verifier", "requirements.md")); err == nil {
		w.put("verifier/requirements.md", requirementsDraft(opts.ID, s, opts.Source, res.Template), true)
	}
	w.put(TranscriptFile, transcript(s, opts.Source), false)
	replaced := ""
	if res.Replayed {
		replaced = serviceID(shape, res.Template)
	}
	newMeta, err := draftTaskYAML(meta, opts, res.Template, replaced, res.Mutants)
	if err != nil {
		return fmt.Errorf("task.yaml: %w", err)
	}
	w.put("task.yaml", newMeta, false)
	return w.err
}

// draftWriter writes draft files and records them, keeping the first error.
type draftWriter struct {
	dir string
	res *DraftResult
	s   *Session
	err error
}

func (w *draftWriter) put(rel string, data []byte, editMe bool) {
	w.putMode(rel, data, editMe, 0o644)
}

func (w *draftWriter) putMode(rel string, data []byte, editMe bool, mode os.FileMode) {
	if w.err != nil || rel == "" {
		return
	}
	clean := path.Clean(filepath.ToSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		w.err = fmt.Errorf("path %q leaves the task folder", rel)
		return
	}
	p := filepath.Join(w.dir, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.err = err
		return
	}
	if err := os.WriteFile(p, data, mode); err != nil {
		w.err = err
		return
	}
	if err := os.Chmod(p, mode); err != nil {
		w.err = err
		return
	}
	if !slices.Contains(w.res.Written, clean) {
		w.res.Written = append(w.res.Written, clean)
	}
	if editMe && !slices.Contains(w.res.EditMe, clean) {
		w.res.EditMe = append(w.res.EditMe, clean)
	}
}

// sessions writes the instructions of a multi-session (memory) task: the
// first user request to the first session, the last request to the last
// session, the requests between to the sessions between. The first session's
// files folder gets the conversation before the last request.
func (w *draftWriter) sessions(shape draftShape, reqs []string) {
	n := len(shape.Sessions)
	assign := make([]string, n)
	switch {
	case len(reqs) == 1:
		assign[n-1] = reqs[0]
		if n > 1 {
			w.res.Warnings = append(w.res.Warnings, "the session has one user request; it became the last session's instruction, so write the earlier sessions' instructions yourself")
		}
	default:
		assign[n-1] = reqs[len(reqs)-1]
		for i := 0; i < n-1 && i < len(reqs)-1; i++ {
			assign[i] = reqs[i]
		}
	}
	for i, sess := range shape.Sessions {
		if assign[i] != "" {
			w.put(sess.Instruction, instructionDraft(assign[i], 0, ""), true)
		}
	}
	if files := shape.Sessions[0].Files; files != "" && n > 1 && len(reqs) > 1 {
		dir := filepath.Join(w.dir, filepath.FromSlash(path.Clean(files)))
		if err := os.RemoveAll(dir); err != nil && w.err == nil {
			w.err = err
		}
		w.put(path.Join(files, "conversation.md"), conversationFile(w.s), false)
		w.res.Warnings = append(w.res.Warnings, fmt.Sprintf("%s/conversation.md holds the recorded conversation before the last request; the agent reads it in the first session, so cut it to what it must remember", files))
	}
}

// instructionDraft is a draft instruction: the EDIT ME block, then the
// request as the user sent it (after redaction), then extra when set.
func instructionDraft(req string, requests int, extra string) []byte {
	var b strings.Builder
	b.WriteString(scaffold.EditMeMarker + "\n<!--\n")
	b.WriteString("Drafted by `bifrost-bench task from-trace` from a recorded session. Below is\n")
	b.WriteString("a user request of that session as it was sent (after redaction). Rewrite it\n")
	b.WriteString("as the request a user would send: what the agent needs to know and how to\n")
	b.WriteString("tell when it is done. Say nothing about how the result is checked.\n")
	if requests > 1 {
		fmt.Fprintf(&b, "The session had %d user requests; the later ones are in %s beside\ntask.yaml. Fold what matters into this one request.\n", requests, TranscriptFile)
	}
	b.WriteString("Delete this comment and the EDIT ME line when you are done;\n")
	b.WriteString("`bifrost-bench task validate` fails while the EDIT ME line is here.\n-->\n\n")
	b.WriteString(strings.TrimSpace(req) + "\n")
	if extra != "" {
		b.WriteString("\n" + extra + "\n")
	}
	return []byte(b.String())
}

// answerDraft is the hidden expected answer. Every judgement line carries its
// own EDIT ME comment, so deleting only the top line still fails validation.
func answerDraft(question, answer string, failed bool) []byte {
	answer = strings.TrimSpace(answer)
	short := answer != "" && !strings.Contains(answer, "\n") && len(answer) <= 120
	var b strings.Builder
	b.WriteString("# EDIT ME: confirm the expected answer and how it is matched, then delete every EDIT ME line. `bifrost-bench task validate` fails while one is here.\n")
	b.WriteString("# Hidden expected answer, drafted by `bifrost-bench task from-trace` from a\n")
	b.WriteString("# recorded session and read by the answer_match grader (no model). The trace\n")
	b.WriteString("# shows what the agent answered, not what was right.\n")
	fmt.Fprintf(&b, "question: %s\n", quote(oneLine(question, 500)))
	match := "contains_all"
	if short || failed {
		match = "exact"
	}
	fmt.Fprintf(&b, "match: %s\nexpected:\n", match)
	switch {
	case failed:
		b.WriteString("  # EDIT ME: the recorded session failed or gave no answer; write the correct answer here.\n")
		fmt.Fprintf(&b, "  - %s\n", quote(placeholderAnswer))
	case short:
		b.WriteString("  # EDIT ME: this is the answer the agent gave in the trace; keep it only if it is right.\n")
		fmt.Fprintf(&b, "  - %s\n", quote(answer))
	default:
		b.WriteString("  # EDIT ME: this is the agent's whole answer from the trace; cut it down to the facts a correct answer must contain, one per entry.\n")
		fmt.Fprintf(&b, "  - %s\n", quote(answer))
	}
	b.WriteString("normalize: [lower, collapse_space, trim]\n")
	b.WriteString("# Plausible wrong answers that must fail; admission checks each one.\n")
	if failed && answer != "" {
		fmt.Fprintf(&b, "wrong_answers: [%s]\n", quote(answer))
	} else {
		b.WriteString("wrong_answers: []\n")
	}
	return []byte(b.String())
}

// checksDraft is a state checks file with no checks yet. A replaying draft
// reads the replay server's calls.json and lists one example check per
// observed tool; otherwise the copied file's STATE line is kept.
func checksDraft(old []byte, s *Session, replay bool) []byte {
	state := `STATE = "inventory/db.json"`
	if m := stateLine.Find(old); m != nil {
		state = string(m)
	}
	var b strings.Builder
	b.WriteString("# EDIT ME: write the checks this task's final state must pass, then delete this line. `bifrost-bench task validate` fails while it is here.\n")
	b.WriteString("# Drafted by `bifrost-bench task from-trace`. A trace shows what an agent did,\n")
	b.WriteString("# not what was right: decide the final state a correct agent leaves and write\n")
	b.WriteString("# it as checks (verify.py beside this file lists every test a check can use).\n")
	b.WriteString("# The traced session's calls and results are in " + ObservedToolsFile + " beside\n")
	b.WriteString("# task.yaml, the whole session in " + TranscriptFile + ".\n")
	if !replay {
		b.WriteString(state + "\n\nCHECKS = [\n    # {\"name\": \"...\", \"path\": \"...\", \"equals\": ...},\n]\n")
		return []byte(b.String())
	}
	b.WriteString("# The replay server records every call the agent makes in calls.json:\n")
	b.WriteString("# {\"calls\": [{\"seq\", \"tool\", \"arguments\", \"replayed\", \"exact\", \"is_error\"}]}.\n")
	fmt.Fprintf(&b, "STATE = %s\n\nCHECKS = [\n", quote(ReplayServiceID+"/calls.json"))
	var seen []string
	for _, c := range s.ToolCalls {
		if slices.Contains(seen, c.Name) || len(seen) == 10 {
			continue
		}
		seen = append(seen, c.Name)
		fmt.Fprintf(&b, "    # {\"name\": %s, \"path\": \"calls\", \"where\": {\"tool\": %s}, \"length\": 1},\n", quote("calls_"+checkName(c.Name)), quote(c.Name))
	}
	b.WriteString("]\n")
	return []byte(b.String())
}

// checkName makes a tool name usable in a check name.
func checkName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

var stateLine = regexp.MustCompile(`(?m)^STATE\s*=.*$`)

// requirementsDraft is the requirements map, to be written by a person.
func requirementsDraft(id string, s *Session, src Source, tmpl string) []byte {
	what := "the expected answer holds"
	if tmpl == KindToolUse || tmpl == KindMCP {
		what = "the final state checks check"
	}
	var b strings.Builder
	b.WriteString(scaffold.EditMeMarker + " map every check of this task to the instruction sentence it checks, then delete this line.\n\n")
	fmt.Fprintf(&b, "# %s: what %s, and why\n\n", id, what)
	b.WriteString("Replace this paragraph with what a correct result holds, and why, in terms\n")
	b.WriteString("of the instruction. A trace shows what happened, not what was right.\n\n")
	fmt.Fprintf(&b, "- Recorded session: %s trace %s, file %s\n", src.Format, s.ID, src.File)
	fmt.Fprintf(&b, "- Outcome as the trace records it: %s\n", outcomeLine(s))
	return []byte(b.String())
}

// observedTools is the tools.observed.json fixture.
func observedTools(s *Session, src Source) ([]byte, error) {
	calls := s.ToolCalls
	if calls == nil {
		calls = []ToolCall{}
	}
	doc := struct {
		CreatedBy string     `json:"created_by"`
		Format    string     `json:"format"`
		TraceID   string     `json:"trace_id"`
		Source    string     `json:"source"`
		Note      string     `json:"note"`
		Calls     []ToolCall `json:"calls"`
	}{createdBy, src.Format, s.ID, src.File + " sha256:" + src.SHA256,
		"Tool calls of the recorded session, in order, with their results as recorded. They show what happened, not what was right. A mock tool service can replay them: match a call by name and arguments, answer with its result or error.",
		calls}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", ObservedToolsFile, err)
	}
	return append(out, '\n'), nil
}

// serviceID is the first service of the copied shape with the kind's role.
func serviceID(shape draftShape, tmpl string) string {
	role := "tool"
	if tmpl == KindMCP {
		role = "mcp"
	}
	for _, sv := range shape.Services {
		if sv.Role == role {
			return sv.ID
		}
	}
	if len(shape.Services) > 0 {
		return shape.Services[0].ID
	}
	return "inventory"
}

// quote renders s as a double-quoted YAML scalar (a JSON string is one).
func quote(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(out)
}

func outcomeLine(s *Session) string {
	if len(s.Outcome.Hints) == 0 {
		return s.Outcome.Status
	}
	return s.Outcome.Status + " (" + strings.Join(s.Outcome.Hints, "; ") + ")"
}

// draftTaskYAML sets the title, the head comment and from-trace provenance,
// keeping every other field and comment task init wrote.
func draftTaskYAML(src []byte, opts DraftOptions, tmpl, replaced string, mutants []string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping")
	}
	m := doc.Content[0]
	m.HeadComment = fmt.Sprintf("Drafted by `bifrost-bench task from-trace` (%s) from a recorded session:\n"+
		"%s trace %s, file %s.\n"+
		"A trace shows what happened, not what was right. Edit every file that holds\n"+
		"an EDIT ME marker, review the rest, then run `bifrost-bench task validate`\n"+
		"and `bifrost-bench task admit`. The draft is never admitted on its own.",
		tmpl, opts.Source.Format, opts.Session.ID, opts.Source.File)
	doc.HeadComment = ""
	if len(m.Content) > 0 {
		m.Content[0].HeadComment = ""
	}
	title := "Task " + opts.ID
	if req := opts.Session.FirstRequest(); req != "" {
		title += ": " + oneLine(req, 60)
	}
	setScalar(m, "title", title)
	prov := mapValue(m, "provenance")
	license := opts.License
	if l := mapValue(prov, "license"); license == "" && l != nil {
		license = l.Value
	}
	if license == "" {
		license = "proprietary"
	}
	np := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setScalar(np, "source", fmt.Sprintf("trace %s (%s) sha256:%s, trace id %s", opts.Source.File, opts.Source.Format, opts.Source.SHA256, opts.Session.ID))
	setScalar(np, "license", license)
	setScalar(np, "created_by", createdBy)
	setNode(m, "provenance", np)
	if replaced != "" {
		if err := replaceService(m, replaced, tmpl); err != nil {
			return nil, err
		}
	}
	if controls := mapValue(m, "controls"); len(mutants) > 0 && controls != nil && controls.Kind == yaml.MappingNode {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, mu := range mutants {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: mu})
		}
		setNode(controls, "mutants", seq)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setNode(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			val.LineComment = m.Content[i+1].LineComment
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

func setScalar(m *yaml.Node, key, value string) {
	setNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}
