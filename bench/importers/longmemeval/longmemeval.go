package longmemeval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/importers/memtask"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Name is the import format name.
const Name = "longmemeval"

// Upstream facts, checked against the dataset card and the repository on 15
// Nov 2026.
const (
	sourceURL     = "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned"
	codeURL       = "https://github.com/xiaowu0162/LongMemEval"
	license       = "MIT"
	upstreamJudge = "gpt-4o-2024-08-06"
)

// Variants (-set variant=...) and the files that hold them, newest name
// first. M (about 500 sessions per question) is not offered: one question
// would replay millions of tokens.
const (
	setVariant    = "variant"
	variantOracle = "oracle"
	variantS      = "s"
)

var variantFiles = map[string][]string{
	variantOracle: {"longmemeval_oracle.json"},
	variantS:      {"longmemeval_s_cleaned.json", "longmemeval_s.json"},
}

// The judge prompt and match mode this importer writes; they name the judge
// grader's longmemeval-v1 prompt and the answer_match numeric mode.
const (
	promptVersion = "longmemeval-v1"
	matchNumeric  = "numeric"
)

// Question types as upstream names them (question_type).
const (
	typePreference = "single-session-preference"
	abstainSuffix  = "_abs"
	dateLayout     = "2006/01/02 (Mon) 15:04"
)

func init() {
	importers.Register(&importer{now: time.Now})
}

type importer struct {
	now func() time.Time
}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"longmemeval-cleaned (oracle, s)"},
		Source:     sourceURL + " (longmemeval_oracle.json, longmemeval_s_cleaned.json); prompts and scorer: " + codeURL,
		License:    license,
		Track:      schemas.TrackMemory,
		Grading:    schemas.GradingJudged,
		Needs: []string{
			"a judge model pinned in suite.yaml (grading judged), reached through the gateway on its own key",
			"an agent with memory between sessions (BENCH_MEMORY_DIR or memory.paths)",
		},
		Summary: "LongMemEval long-term chat memory: each question is a multi-session task, history first, question last",
	}
}

// entry is one LongMemEval question as the dataset files hold it.
type entry struct {
	QuestionID         string          `json:"question_id"`
	QuestionType       string          `json:"question_type"`
	Question           string          `json:"question"`
	Answer             json.RawMessage `json:"answer"`
	QuestionDate       string          `json:"question_date"`
	HaystackDates      []string        `json:"haystack_dates"`
	HaystackSessionIDs []string        `json:"haystack_session_ids"`
	HaystackSessions   [][]turn        `json:"haystack_sessions"`
	AnswerSessionIDs   []string        `json:"answer_session_ids"`
}

type turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	mo, rest, err := memtask.ParseOptions(opts.Extra)
	if err != nil {
		return nil, err
	}
	variant := variantOracle
	if v, ok := rest[setVariant]; ok {
		if _, known := variantFiles[v]; !known {
			return nil, fmt.Errorf("-set %s=%s: want %s or %s", setVariant, v, variantOracle, variantS)
		}
		variant = v
		delete(rest, setVariant)
	}
	if err := memtask.RefuseUnknown(rest, setVariant, memtask.SetIngestSessions, memtask.SetGrading, memtask.SetVersion, memtask.SetRevision); err != nil {
		return nil, err
	}
	file, err := findFile(opts.From, variant)
	if err != nil {
		return nil, err
	}
	entries, err := readEntries(ctx, file, opts.IDs, opts.Limit)
	if err != nil {
		return nil, err
	}
	spec := memtask.SuiteSpec{
		ID: opts.SuiteID, Version: mo.Version, Grading: mo.Grading,
		Description: fmt.Sprintf("LongMemEval (%s variant) imported as multi-session memory tasks.", variant),
		Judge:       memtask.PlaceholderJudge(promptVersion),
		Imported: schemas.ImportRecord{Importer: Name, Benchmark: "longmemeval-cleaned@" + variant, Source: sourceURL,
			Revision: mo.Revision, License: license, At: im.now()},
	}
	suiteDir := opts.SuiteDir()
	// Refuse an incompatible suite before any task folder is written or
	// replaced, so a refused import leaves the existing tasks untouched.
	if err := memtask.CheckSuite(suiteDir, spec); err != nil {
		return nil, err
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir}
	seen := map[string]string{}
	tokens := 0
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, hist, skip := buildTask(e, variant, filepath.Base(file), mo)
		if skip == "" && seen[t.ID] != "" {
			skip = fmt.Sprintf("task id %s is also the id of question %s", t.ID, seen[t.ID])
		}
		if skip != "" {
			res.Skipped = append(res.Skipped, importers.Skip{ID: e.QuestionID, Reason: skip})
			continue
		}
		seen[t.ID] = e.QuestionID
		if err := memtask.WriteTask(suiteDir, t, opts.Force); err != nil {
			if errors.Is(err, fs.ErrExist) {
				res.Skipped = append(res.Skipped, importers.Skip{ID: e.QuestionID, Reason: "task folder exists (use -force to replace it)"})
				continue
			}
			return nil, err
		}
		res.Tasks = append(res.Tasks, t.ID)
		tokens += hist
	}
	for _, id := range missingIDs(opts.IDs, entries) {
		res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "not in " + filepath.Base(file)})
	}
	if len(res.Tasks) == 0 {
		return res, nil
	}
	if err := memtask.WriteSuite(suiteDir, spec, res.Tasks); err != nil {
		return nil, err
	}
	if err := importers.Finish(suiteDir); err != nil {
		return nil, err
	}
	res.Notes = notes(opts.SuiteID, variant, mo, len(res.Tasks), tokens)
	return res, nil
}

// findFile locates the variant's file: from itself when it is a file, else
// in from or from/data.
func findFile(from, variant string) (string, error) {
	fi, err := os.Stat(from)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", from, err)
	}
	if !fi.IsDir() {
		return from, nil
	}
	for _, dir := range []string{from, filepath.Join(from, "data")} {
		for _, name := range variantFiles[variant] {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("no %s file (%s) in %s or %s/data; download it from %s",
		variant, strings.Join(variantFiles[variant], " or "), from, from, sourceURL)
}

// readEntries streams the JSON array in file and keeps the entries named in
// ids (all when empty), at most limit of them (no limit when 0), in file
// order. The S file is about 280 MB, so it is never read whole.
func readEntries(ctx context.Context, file string, ids []string, limit int) ([]entry, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	defer f.Close()
	want := map[string]bool{}
	for _, id := range ids {
		want[strings.TrimSpace(id)] = true
	}
	dec := json.NewDecoder(f)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, fmt.Errorf("parse %s: want a JSON array of questions", file)
	}
	var out []entry
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var e entry
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("parse %s: question %d: %w", file, len(out)+1, err)
		}
		if len(want) > 0 && !want[e.QuestionID] {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			return out, nil // a deliberate early stop: the rest is never read
		}
	}
	// The whole array was scanned: require its closing bracket and nothing
	// after it, so a truncated download is never imported as complete.
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return nil, fmt.Errorf("parse %s: the JSON array of questions is truncated or malformed", file)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("parse %s: unexpected data after the JSON array of questions", file)
	}
	return out, nil
}

// buildTask converts one question. It returns the task, the approximate
// tokens of its history, and a skip reason when the question cannot be
// imported as asked.
func buildTask(e entry, variant, file string, mo memtask.Options) (memtask.Task, int, string) {
	answer, err := answerText(e.Answer)
	switch {
	case e.QuestionID == "" || e.Question == "":
		return memtask.Task{}, 0, "question_id or question is empty"
	case err != nil:
		return memtask.Task{}, 0, err.Error()
	case len(e.HaystackSessions) == 0:
		return memtask.Task{}, 0, "no history sessions"
	case len(e.HaystackDates) != len(e.HaystackSessions):
		return memtask.Task{}, 0, fmt.Sprintf("%d history dates for %d sessions", len(e.HaystackDates), len(e.HaystackSessions))
	}
	abstain := strings.HasSuffix(e.QuestionID, abstainSuffix)
	spec, reference, skip := answerSpec(e, answer, abstain, mo.Grading)
	if skip != "" {
		return memtask.Task{}, 0, skip
	}
	history := chronological(e)
	groups := memtask.Split(history, mo.IngestSessions)
	tokens := 0
	var ingest []memtask.Session
	at := 1
	for i, g := range groups {
		files := memtask.HistoryFiles(g, at, len(history))
		for _, body := range files {
			tokens += memtask.ApproxTokens(body)
		}
		ingest = append(ingest, memtask.Session{Instruction: ingestInstruction(i+1, len(groups), len(g)), Files: files})
		at += len(g)
	}
	tags := []string{e.QuestionType}
	if abstain {
		tags = append(tags, "abstention")
	}
	agentS := 600
	if variant == variantS {
		agentS = 1800
	}
	return memtask.Task{
		ID: memtask.TaskID(e.QuestionID), Title: fmt.Sprintf("LongMemEval %s question %s", e.QuestionType, e.QuestionID),
		Project: "longmemeval", Tags: tags, Ingest: ingest,
		Question: questionInstruction(e, spec), Answer: spec, Reference: reference, Exploit: memtask.ExploitAnswer,
		Provenance: schemas.TaskProvenance{Source: sourceURL, License: license, ImportedFrom: "longmemeval-cleaned/" + file + "#" + e.QuestionID},
		AgentS:     agentS,
	}, tokens, ""
}

// answerSpec builds the hidden answer and the reference answer. Judged:
// the upstream answer (a rubric for preference questions, the explanation
// for abstention ones) with the question type as category. Match: only an
// abstention (the fixed not-known phrases) or an answer that is one plain
// number is matched without a model; every other question is skipped.
func answerSpec(e entry, answer string, abstain bool, grading string) (schemas.AnswerSpec, string, string) {
	spec := schemas.AnswerSpec{Question: e.Question, Category: e.QuestionType, Abstain: abstain}
	reference := answer
	switch {
	case abstain:
		spec.Expected = []string{answer}
		reference = memtask.NotKnownAnswer
	case e.QuestionType == typePreference:
		spec.Rubric = answer
	default:
		spec.Expected = []string{answer}
		spec.WrongOnes = wrongAnswers(e.Question, answer)
	}
	if grading == memtask.GradingJudged {
		return spec, reference, ""
	}
	switch {
	case abstain:
		spec.Expected = nil
	case isPlainNumber(answer):
		spec.Match = matchNumeric
	case e.QuestionType == typePreference:
		return spec, "", "preference question: the answer is a rubric, which only a judge can apply (use grading=judged)"
	default:
		return spec, "", "free-text answer: exact matching is not sound for it (use grading=judged)"
	}
	return spec, reference, ""
}

// wrongAnswers derives answers that must fail: saying it is not known (the
// question is answerable), the other option of an "A or B?" question whose
// answer names one of them, and a different number for a plain-number
// answer.
func wrongAnswers(question, answer string) []string {
	out := []string{memtask.NotKnownAnswer}
	if other := otherOption(question, answer); other != "" {
		out = append(out, other)
	}
	if isPlainNumber(answer) {
		v, _ := strconv.ParseFloat(answer, 64)
		out = append(out, strconv.FormatFloat(v+5, 'f', -1, 64))
	}
	return out
}

// otherOption reads "..., the A or the B?" and returns the option the
// answer does not name, when it names exactly one of the two.
func otherOption(question, answer string) string {
	q := strings.TrimSpace(question)
	if !strings.HasSuffix(q, "?") {
		return ""
	}
	tail := q[:len(q)-1]
	if i := strings.LastIndex(tail, ", "); i >= 0 {
		tail = tail[i+2:]
	}
	a, b, ok := strings.Cut(tail, " or ")
	if !ok || strings.Contains(b, " or ") {
		return ""
	}
	a, b = trimArticle(a), trimArticle(b)
	na, nb, nans := fold(a), fold(b), fold(answer)
	if na == "" || nb == "" || strings.Contains(na, nb) || strings.Contains(nb, na) {
		return ""
	}
	hasA, hasB := strings.Contains(nans, na) || strings.Contains(na, nans), strings.Contains(nans, nb) || strings.Contains(nb, nans)
	switch {
	case hasA && !hasB:
		return b
	case hasB && !hasA:
		return a
	}
	return ""
}

func trimArticle(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{"the ", "The ", "a ", "an "} {
		s = strings.TrimPrefix(s, p)
	}
	return strings.TrimSpace(s)
}

// fold lower-cases s and keeps letters, digits and single spaces.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// answerText reads the answer, which upstream stores as a string or, for
// some counting questions, a number.
func answerText(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return "", errors.New("answer is empty")
		}
		return strings.TrimSpace(s), nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String(), nil
	}
	return "", errors.New("answer is neither text nor a number")
}

func isPlainNumber(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil && !strings.ContainsAny(s, "eE+")
}

// chronological returns the history sessions sorted by date (upstream sorts
// S and M but not oracle). A date that does not parse keeps file order
// among its neighbours.
func chronological(e entry) []memtask.Transcript {
	type dated struct {
		at time.Time
		t  memtask.Transcript
	}
	ds := make([]dated, len(e.HaystackSessions))
	for i, sess := range e.HaystackSessions {
		at, _ := time.Parse(dateLayout, e.HaystackDates[i])
		tr := memtask.Transcript{Date: e.HaystackDates[i]}
		for _, t := range sess {
			tr.Turns = append(tr.Turns, memtask.Turn{Speaker: speaker(t.Role), Text: t.Content})
		}
		ds[i] = dated{at: at, t: tr}
	}
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].at.IsZero() || ds[j].at.IsZero() {
			return false
		}
		return ds[i].at.Before(ds[j].at)
	})
	out := make([]memtask.Transcript, len(ds))
	for i, d := range ds {
		out[i] = d.t
	}
	return out
}

func speaker(role string) string {
	switch role {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	}
	return role
}

func ingestInstruction(part, parts, n int) string {
	return fmt.Sprintf(`# Conversation history, part %d of %d

The history folder in your workspace holds %s of past conversations between a user and an AI assistant, each with its date. This is part %d of %d of the history; the other parts come in other sessions.

Read every transcript and keep what you will need to answer the user's questions about these conversations later: facts about the user, events and when they happened, what changed over time, and what the assistant said. This workspace is wiped after this session, so only what you save in your memory folder (the folder named by BENCH_MEMORY_DIR) carries over. No answer is needed now.
`, part, parts, memtask.Count(n, "transcript"), part, parts)
}

func questionInstruction(e entry, spec schemas.AnswerSpec) string {
	form := "Answer in one or two sentences."
	if spec.Match == matchNumeric {
		form = "Reply with the answer only: the number."
	}
	return fmt.Sprintf(`# The user's question

Today is %s.

In earlier sessions you read transcripts of past conversations between the user and an AI assistant and kept what mattered in your memory folder (the folder named by BENCH_MEMORY_DIR). The transcripts are no longer in your workspace. Answer the user's question from what you remember.

The user asks:

> %s

Write your answer to answer.txt in the workspace (the path in BENCH_ANSWER_PATH). %s If what you remember does not contain the answer, say that you do not know.
`, e.QuestionDate, strings.Join(strings.Fields(e.Question), " "), form)
}

func missingIDs(ids []string, got []entry) []string {
	have := map[string]bool{}
	for _, e := range got {
		have[e.QuestionID] = true
	}
	var out []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !have[id] {
			out = append(out, id)
		}
	}
	return out
}

func notes(suiteID, variant string, mo memtask.Options, tasks, tokens int) []string {
	var out []string
	if mo.Grading == memtask.GradingJudged {
		out = append(out, fmt.Sprintf("Pin the judge in benchmarks/%s/suite.yaml: judge.provider and judge.model are %s. Upstream LongMemEval judges with %s at temperature 0; prompt_version %s is its per-type prompt, adapted. Set judge.votes to 3 for a majority.",
			suiteID, memtask.Placeholder, upstreamJudge, promptVersion))
	} else {
		out = append(out, "grading=match: only abstention questions (fixed not-known phrases) and plain-number answers were imported; the rest were skipped with the reason.")
	}
	avg := tokens / max(tasks, 1)
	cost := fmt.Sprintf("Token cost: each question replays its whole history through the agent across %d ingest session(s): about %d tokens per question on average (characters / 4), %d for these %d task(s), per arm, condition and trial, before the agent's own work.",
		mo.IngestSessions, avg, tokens, tasks)
	if variant == variantS {
		cost += " The s variant is about 115k tokens of history per question (upstream's figure); start with -limit."
	}
	out = append(out, cost,
		fmt.Sprintf("Run `bifrost-bench suite lint %s`, then `suite lock` and `suite admit`: admission checks that the reference answer passes and every wrong answer and the exploit fail, which calibrates the grader on each task.", suiteID),
	)
	if mo.Grading == memtask.GradingJudged {
		out = append(out, memtask.JudgeKeyNote)
	}
	return out
}
