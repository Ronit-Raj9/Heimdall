package locomo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
const Name = "locomo"

// Upstream facts, checked against the repository on 15 Nov 2026: one file,
// data/locomo10.json, of ten conversations, under CC BY-NC 4.0 (LICENSE.txt).
const (
	sourceURL = "https://github.com/snap-research/locomo"
	dataFile  = "locomo10.json"
	license   = "CC-BY-NC-4.0"
)

// The judge prompt and match mode this importer writes; they name the judge
// grader's generic qa-v1 prompt (upstream scores LoCoMo with token F1, not a
// judge) and the answer_match numeric mode.
const (
	promptVersion = "qa-v1"
	matchNumeric  = "numeric"
)

// categoryAdversarial is the category of questions about things the
// conversation never says; upstream scores them by whether the prediction
// says "not mentioned" or "no information available".
const categoryAdversarial = 5

// categories names upstream's numeric QA categories. The repository does
// not name them; the names follow its scoring code (task_eval/evaluation.py
// and gpt_utils.py): 1 is scored per comma-separated sub-answer (multi-hop),
// 2 gets the hint to answer with a date (temporal), 3 has ';'-separated
// rationales (open-domain), 5 is adversarial, and 4, the largest, is
// single-hop.
var categories = map[int]string{1: "multi-hop", 2: "temporal", 3: "open-domain", 4: "single-hop", 5: "adversarial"}

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
		Benchmarks: []string{"locomo10"},
		Source:     "git clone --depth 1 " + sourceURL + " (data/locomo10.json)",
		License:    license + " (non-commercial)",
		Track:      schemas.TrackMemory,
		Grading:    schemas.GradingJudged,
		Needs: []string{
			"a judge model pinned in suite.yaml (grading judged), reached through the gateway on its own key",
			"an agent with memory between sessions (BENCH_MEMORY_DIR or memory.paths)",
			"a use that the non-commercial license allows",
		},
		Summary: "LoCoMo very long conversations: each question is a multi-session task, conversation first, question last",
	}
}

// sample is one LoCoMo conversation with its questions.
type sample struct {
	SampleID     string                     `json:"sample_id"`
	Conversation map[string]json.RawMessage `json:"conversation"`
	QA           []qa                       `json:"qa"`
}

type qa struct {
	Question          string          `json:"question"`
	Answer            json.RawMessage `json:"answer"`
	AdversarialAnswer json.RawMessage `json:"adversarial_answer"`
	Evidence          []string        `json:"evidence"`
	Category          int             `json:"category"`
}

type dialogTurn struct {
	Speaker     string `json:"speaker"`
	Text        string `json:"text"`
	BlipCaption string `json:"blip_caption"`
}

// conversation is a sample's speakers and dated sessions in order.
type conversation struct {
	speakerA, speakerB string
	sessions           []memtask.Transcript
}

// Import implements importers.Importer. Task ids are <sample id>-q<NNN>,
// NNN the 1-based position of the question in the sample; -ids takes task
// ids or whole sample ids.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	mo, rest, err := memtask.ParseOptions(opts.Extra)
	if err != nil {
		return nil, err
	}
	if err := memtask.RefuseUnknown(rest, memtask.SetIngestSessions, memtask.SetGrading, memtask.SetVersion, memtask.SetRevision); err != nil {
		return nil, err
	}
	file, err := findFile(opts.From)
	if err != nil {
		return nil, err
	}
	samples, err := readSamples(file)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range opts.IDs {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	suiteDir := importers.SuiteDir(opts.BenchRoot, opts.SuiteID)
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir}
	found := map[string]bool{}
	tokens := 0
	for _, s := range samples {
		conv, convErr := parseConversation(s)
		for i, q := range s.QA {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if opts.Limit > 0 && len(res.Tasks) >= opts.Limit {
				break
			}
			id := memtask.TaskID(fmt.Sprintf("%s-q%03d", s.SampleID, i+1))
			if len(want) > 0 && !want[id] && !want[s.SampleID] {
				continue
			}
			found[id], found[s.SampleID] = true, true
			if convErr != nil {
				res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: convErr.Error()})
				continue
			}
			t, hist, skip := buildTask(id, s.SampleID, q, conv, mo)
			if skip != "" {
				res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: skip})
				continue
			}
			if err := memtask.WriteTask(suiteDir, t, opts.Force); err != nil {
				if errors.Is(err, fs.ErrExist) {
					res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "task folder exists (use -force to replace it)"})
					continue
				}
				return nil, err
			}
			res.Tasks = append(res.Tasks, id)
			tokens += hist
		}
	}
	for _, id := range sortedKeys(want) {
		if !found[id] {
			res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "not in " + dataFile})
		}
	}
	if len(res.Tasks) == 0 {
		return res, nil
	}
	spec := memtask.SuiteSpec{
		ID: opts.SuiteID, Version: mo.Version, Grading: mo.Grading,
		Description: "LoCoMo (locomo10) imported as multi-session memory tasks. Licensed CC BY-NC 4.0: non-commercial use only.",
		Judge:       memtask.PlaceholderJudge(promptVersion),
		Imported: schemas.ImportRecord{Importer: Name, Benchmark: "locomo10", Source: sourceURL, Revision: mo.Revision,
			License: license, At: im.now()},
	}
	if err := memtask.WriteSuite(suiteDir, spec, res.Tasks); err != nil {
		return nil, err
	}
	if err := importers.Finish(suiteDir); err != nil {
		return nil, err
	}
	res.Notes = notes(opts.SuiteID, mo, len(res.Tasks), tokens)
	return res, nil
}

// findFile locates locomo10.json: from itself when it is a file, else in
// from or from/data.
func findFile(from string) (string, error) {
	fi, err := os.Stat(from)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", from, err)
	}
	if !fi.IsDir() {
		return from, nil
	}
	for _, p := range []string{filepath.Join(from, dataFile), filepath.Join(from, "data", dataFile)} {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("no %s in %s or %s/data; clone %s", dataFile, from, from, sourceURL)
}

func readSamples(file string) ([]sample, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	var out []sample
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	return out, nil
}

// parseConversation reads the speakers and the sessions session_<n> in
// ascending n, which upstream states is chronological, each with its
// session_<n>_date_time. An empty session is dropped; a session without a
// date is an error, since temporal questions depend on the dates.
func parseConversation(s sample) (conversation, error) {
	var c conversation
	if err := decodeField(s.Conversation, "speaker_a", &c.speakerA); err != nil {
		return c, err
	}
	if err := decodeField(s.Conversation, "speaker_b", &c.speakerB); err != nil {
		return c, err
	}
	var nums []int
	for k := range s.Conversation {
		rest, ok := strings.CutPrefix(k, "session_")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(rest); err == nil {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	for _, n := range nums {
		key := fmt.Sprintf("session_%d", n)
		var turns []dialogTurn
		if err := decodeField(s.Conversation, key, &turns); err != nil {
			return c, err
		}
		if len(turns) == 0 {
			continue
		}
		var date string
		if err := decodeField(s.Conversation, key+"_date_time", &date); err != nil {
			return c, err
		}
		tr := memtask.Transcript{Date: date}
		for _, t := range turns {
			text := t.Text
			if t.BlipCaption != "" {
				text = strings.TrimSpace(text + " [shares a photo: " + t.BlipCaption + "]")
			}
			tr.Turns = append(tr.Turns, memtask.Turn{Speaker: t.Speaker, Text: text})
		}
		c.sessions = append(c.sessions, tr)
	}
	if len(c.sessions) == 0 {
		return c, fmt.Errorf("sample %s has no sessions", s.SampleID)
	}
	return c, nil
}

func decodeField(m map[string]json.RawMessage, key string, out any) error {
	raw, ok := m[key]
	if !ok {
		return fmt.Errorf("conversation has no %s", key)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("conversation %s: %w", key, err)
	}
	return nil
}

// buildTask converts one question of a conversation.
func buildTask(id, sampleID string, q qa, c conversation, mo memtask.Options) (memtask.Task, int, string) {
	category, ok := categories[q.Category]
	if !ok {
		return memtask.Task{}, 0, fmt.Sprintf("unknown category %d", q.Category)
	}
	if strings.TrimSpace(q.Question) == "" {
		return memtask.Task{}, 0, "question is empty"
	}
	answer, hasAnswer := text(q.Answer)
	adversarial, hasAdversarial := text(q.AdversarialAnswer)
	abstain := q.Category == categoryAdversarial
	switch {
	case abstain && hasAnswer:
		return memtask.Task{}, 0, "adversarial question with both answer and adversarial_answer: upstream does not say which is expected"
	case abstain && !hasAdversarial:
		return memtask.Task{}, 0, "adversarial question without adversarial_answer"
	case !abstain && !hasAnswer:
		return memtask.Task{}, 0, "question has no answer"
	}
	spec := schemas.AnswerSpec{Question: q.Question, Category: category, Abstain: abstain}
	reference := answer
	if abstain {
		spec.Expected = []string{fmt.Sprintf("The conversations between %s and %s never state this.", c.speakerA, c.speakerB)}
		spec.WrongOnes = []string{adversarial}
		reference = "That is not mentioned in the conversations."
	} else {
		spec.Expected = []string{answer}
		spec.WrongOnes = []string{memtask.NotKnownAnswer}
		if v, err := strconv.ParseFloat(answer, 64); err == nil && isPlainNumber(answer) {
			spec.WrongOnes = append(spec.WrongOnes, strconv.FormatFloat(v+5, 'f', -1, 64))
		}
	}
	if mo.Grading == memtask.GradingMatch {
		switch {
		case abstain:
			spec.Expected = nil
		case isPlainNumber(answer):
			spec.Match = matchNumeric
		default:
			return memtask.Task{}, 0, "free-text answer: exact matching is not sound for it (use grading=judged)"
		}
	}
	groups := memtask.Split(c.sessions, mo.IngestSessions)
	tokens := 0
	var ingest []memtask.Session
	at := 1
	for i, g := range groups {
		files := memtask.HistoryFiles(g, at, len(c.sessions))
		for _, body := range files {
			tokens += memtask.ApproxTokens(body)
		}
		ingest = append(ingest, memtask.Session{Instruction: ingestInstruction(c, i+1, len(groups), len(g)), Files: files})
		at += len(g)
	}
	return memtask.Task{
		ID: id, Title: fmt.Sprintf("LoCoMo %s question %s", category, id), Project: "locomo",
		Tags: []string{category, sampleID}, Ingest: ingest,
		Question: questionInstruction(c, q, spec), Answer: spec, Reference: reference, Exploit: memtask.ExploitAnswer,
		Provenance: schemas.TaskProvenance{Source: sourceURL, License: license, ImportedFrom: "locomo10/" + id},
		AgentS:     900,
	}, tokens, ""
}

// text reads an answer stored as text or a number; false when absent,
// null or empty.
func text(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		s = strings.TrimSpace(s)
		return s, s != ""
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String(), true
	}
	return "", false
}

func isPlainNumber(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil && !strings.ContainsAny(s, "eE+")
}

func ingestInstruction(c conversation, part, parts, n int) string {
	return fmt.Sprintf(`# Conversation history, part %d of %d

The history folder in your workspace holds %s of conversations between %s and %s, in order, each with its date. This is part %d of %d of their history; the other parts come in other sessions.

Read every transcript and keep what you will need to answer questions about these two people later: facts about them, events and when they happened, and how things changed over time. This workspace is wiped after this session, so only what you save in your memory folder (the folder named by BENCH_MEMORY_DIR) carries over. No answer is needed now.
`, part, parts, memtask.Count(n, "transcript"), c.speakerA, c.speakerB, part, parts)
}

func questionInstruction(c conversation, q qa, spec schemas.AnswerSpec) string {
	form := "Answer in one or two sentences."
	switch {
	case spec.Match == matchNumeric:
		form = "Reply with the answer only: the number or year."
	case q.Category == 2:
		form = "Use the dates of the conversations to answer; give an approximate date when no exact one is stated."
	}
	return fmt.Sprintf(`# A question about %s and %s

In earlier sessions you read transcripts of conversations between %s and %s and kept what mattered in your memory folder (the folder named by BENCH_MEMORY_DIR). The transcripts are no longer in your workspace. Answer the question from what you remember.

> %s

Write your answer to answer.txt in the workspace (the path in BENCH_ANSWER_PATH). %s If what you remember does not contain the answer, say that it is not mentioned.
`, c.speakerA, c.speakerB, c.speakerA, c.speakerB, strings.Join(strings.Fields(q.Question), " "), form)
}

func notes(suiteID string, mo memtask.Options, tasks, tokens int) []string {
	var out []string
	out = append(out, "License: LoCoMo is CC BY-NC 4.0 (non-commercial use only, with attribution to Maharana et al., 2024). The imported tasks carry its text; use them only where non-commercial use is allowed. The suite folder's .gitignore keeps them out of git: never commit them.")
	if mo.Grading == memtask.GradingJudged {
		out = append(out, fmt.Sprintf("Pin the judge in benchmarks/%s/suite.yaml: judge.provider and judge.model are %s. Upstream LoCoMo scores answers with token F1, not a judge, so judged results are not comparable with its published numbers; prompt_version %s is the bench's generic prompt. Set judge.votes to 3 for a majority.",
			suiteID, memtask.Placeholder, promptVersion))
	} else {
		out = append(out, "grading=match: only adversarial questions (fixed not-known phrases, as upstream's own check) and plain-number answers were imported; the rest were skipped with the reason.")
	}
	avg := tokens / max(tasks, 1)
	out = append(out,
		fmt.Sprintf("Token cost: each question replays its whole conversation (19 to 32 sessions) through the agent across %d ingest session(s): about %d tokens per question on average (characters / 4), %d for these %d task(s), per arm, condition and trial, before the agent's own work. A conversation has 105 to 260 questions, so importing all of them replays each conversation that many times; start with -limit or -ids.",
			mo.IngestSessions, avg, tokens, tasks),
		fmt.Sprintf("Run `bifrost-bench suite lint %s`, then `suite lock` and `suite admit`: admission checks that the reference answer passes and every wrong answer and the exploit fail, which calibrates the grader on each task.", suiteID),
	)
	if mo.Grading == memtask.GradingJudged {
		out = append(out, memtask.JudgeKeyNote)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
