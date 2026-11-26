package investigator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// RunInput is one failed run to read.
type RunInput struct {
	RunID   string
	Task    string
	Arm     string
	Outcome string               // the verified outcome, from the run view
	Failed  []string             // failed test ids or failed checks, when the run has them
	Steps   []schemas.StepRecord // see Window
	Final   string               // the agent's final message, redacted
}

// Item is one likely cause in one run, written by a model.
type Item struct {
	ID         string  `json:"id"` // "i" + index
	RunID      string  `json:"run_id"`
	Title      string  `json:"title"`      // at most 80 chars, model-written
	Hypothesis string  `json:"hypothesis"` // at most 400 chars, model-written
	Kind       string  `json:"kind"`       // agent_mistake | environment | task_problem | grader_problem
	Quotes     []Quote `json:"quotes"`     // only verified quotes are kept
	Origin     string  `json:"origin"`     // "model"
}

// Group is items the model says share one cause.
type Group struct {
	Title string   `json:"title"`
	Kind  string   `json:"kind"`
	Items []string `json:"items"` // item ids
	Runs  int      `json:"runs"`  // distinct runs
	Tasks []string `json:"tasks"` // suite/task of those runs
}

// Report is one investigation, stored as investigations/<job id>.json.
type Report struct {
	ID        string      `json:"id"` // the job id
	ExpID     string      `json:"exp_id"`
	Question  string      `json:"question"`
	Runs      []string    `json:"runs"`    // read
	Skipped   []string    `json:"skipped"` // not read: budget or no steps
	Items     []Item      `json:"items"`
	Groups    []Group     `json:"groups"`
	Dropped   int         `json:"dropped_unverified"`
	SpentUSD  float64     `json:"spent_usd"`
	BudgetUSD float64     `json:"budget_usd"`
	Stopped   string      `json:"stopped,omitempty"` // "budget" when the cap ended it
	Model     string      `json:"model"`
	Calls     []LedgerRow `json:"calls"`
	Note      string      `json:"note"`
	CreatedAt time.Time   `json:"created_at"`
}

// DefaultQuestion is asked when the person gives none.
const DefaultQuestion = "Why did these runs fail?"

// ReportNote is the fixed note on every report.
const ReportNote = "These are hypotheses written by a model. Each quote was checked against the run's recorded steps. The run results themselves come from the tasks' tests."

const findingsSystemPrompt = "You read one failed run of an AI agent on a benchmark task. The run already failed by the task's own tests; do not judge whether it failed. Say what most likely went wrong. Every claim must quote the exact words of a step, with its number. Reply as JSON: {\"items\": [{\"title\", \"hypothesis\", \"kind\", \"quotes\": [{\"seq\", \"text\"}]}]}, at most 3 items."

const groupingPrompt = "Group these items by the same cause. Reply {\"groups\": [{\"title\", \"kind\", \"items\": [ids]}]}."

const repairPrompt = "These quotes were not found exactly in the steps you cited. Copy them again exactly from the text, or drop them: "

const itemsSchema = `{"type":"object","required":["items"],"properties":{"items":{"type":"array","maxItems":3,"items":{"type":"object","required":["title","hypothesis","kind","quotes"],"properties":{"title":{"type":"string"},"hypothesis":{"type":"string"},"kind":{"enum":["agent_mistake","environment","task_problem","grader_problem"]},"quotes":{"type":"array","items":{"type":"object","required":["seq","text"],"properties":{"seq":{"type":"integer"},"text":{"type":"string"}}}}}}}}}`

const groupsSchema = `{"type":"object","required":["groups"],"properties":{"groups":{"type":"array","items":{"type":"object","required":["title","items"],"properties":{"title":{"type":"string"},"kind":{"type":"string"},"items":{"type":"array","items":{"type":"string"}}}}}}}`

var itemKinds = map[string]bool{"agent_mistake": true, "environment": true, "task_problem": true, "grader_problem": true}

const (
	windowFirst   = 10
	windowBefore  = 5
	windowLast    = 40
	windowMaxRows = 200
	summaryMax    = 300
)

// Window keeps a prompt bounded: the first 10 steps, every step with
// IsError or a non-zero ExitCode with the 5 steps before it, and the last 40
// steps, deduplicated, in Seq order, at most 200 rows. When more than 200
// rows qualify, the error rows and their predecessors are kept first.
func Window(steps []schemas.StepRecord) []schemas.StepRecord {
	sorted := append([]schemas.StepRecord(nil), steps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })
	keep := map[int]bool{}
	var order []int
	add := func(i int) {
		if i >= 0 && i < len(sorted) && !keep[i] && len(order) < windowMaxRows {
			keep[i] = true
			order = append(order, i)
		}
	}
	for i, s := range sorted {
		if s.IsError || (s.ExitCode != nil && *s.ExitCode != 0) {
			for k := i - windowBefore; k <= i; k++ {
				add(k)
			}
		}
	}
	for i := 0; i < windowFirst; i++ {
		add(i)
	}
	for i := len(sorted) - 1; i >= len(sorted)-windowLast; i-- {
		add(i)
	}
	sort.Ints(order)
	out := make([]schemas.StepRecord, 0, len(order))
	for _, i := range order {
		out = append(out, sorted[i])
	}
	return out
}

type modelItem struct {
	Title      string `json:"title"`
	Hypothesis string `json:"hypothesis"`
	Kind       string `json:"kind"`
	Quotes     []struct {
		Seq  int    `json:"seq"`
		Text string `json:"text"`
	} `json:"quotes"`
}

type modelGroup struct {
	Title string   `json:"title"`
	Kind  string   `json:"kind"`
	Items []string `json:"items"`
}

// Investigate reads each run with one call, keeps only items whose quotes
// pass the quote check (after one repair round), and groups the kept items
// when two or more runs were read. ErrBudget stops the loop; the runs not
// read go to Skipped. Every text is redacted before it is put in a prompt.
func Investigate(ctx context.Context, c Caller, question string, runs []RunInput) (*Report, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		question = DefaultQuestion
	}
	rep := &Report{Question: question, Runs: []string{}, Skipped: []string{}, Items: []Item{}, Groups: []Group{}, Note: ReportNote, CreatedAt: time.Now().UTC()}
	taskOf := map[string]string{}
	stopped := false
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if stopped || (len(run.Steps) == 0 && strings.TrimSpace(run.Final) == "") {
			rep.Skipped = append(rep.Skipped, run.RunID)
			continue
		}
		items, dropped, read, err := investigateRun(ctx, c, question, run, &rep.Model)
		if errors.Is(err, ErrBudget) {
			stopped = true
			rep.Stopped = "budget"
		}
		if !read {
			rep.Skipped = append(rep.Skipped, run.RunID)
			continue
		}
		rep.Runs = append(rep.Runs, run.RunID)
		taskOf[run.RunID] = run.Task
		rep.Dropped += dropped
		for _, it := range items {
			it.ID = fmt.Sprintf("i%d", len(rep.Items))
			rep.Items = append(rep.Items, it)
		}
	}
	rep.Groups = groupItems(ctx, c, rep, taskOf, stopped, &rep.Model)
	rep.SpentUSD, rep.BudgetUSD = c.Spent(), c.Budget()
	if l, ok := c.(interface{ Calls() []LedgerRow }); ok {
		rep.Calls = l.Calls()
	}
	if rep.Calls == nil {
		rep.Calls = []LedgerRow{}
	}
	return rep, nil
}

// investigateRun makes the run's call and its repair round. read is false
// when the first call was not answered.
func investigateRun(ctx context.Context, c Caller, question string, run RunInput, model *string) (items []Item, dropped int, read bool, err error) {
	red := traceimport.NewRedactor()
	source := map[int]string{}
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\nTask: %s\n", red.Text(question), red.Text(run.Task))
	if run.Arm != "" {
		fmt.Fprintf(&b, "Arm: %s\n", red.Text(run.Arm))
	}
	if run.Outcome != "" {
		fmt.Fprintf(&b, "Outcome by the task's tests: %s\n", red.Text(run.Outcome))
	}
	if len(run.Failed) > 0 {
		fmt.Fprintf(&b, "Failed checks: %s\n", red.Text(strings.Join(run.Failed, ", ")))
	}
	b.WriteString("Steps:\n")
	for _, s := range Window(run.Steps) {
		sum := red.Text(s.Summary)
		source[s.Seq] = sum
		cut := sum
		if r := []rune(cut); len(r) > summaryMax {
			cut = string(r[:summaryMax])
		}
		fmt.Fprintf(&b, "#%d %s %s\n", s.Seq, s.Kind, strings.ReplaceAll(cut, "\n", " "))
	}
	if final := red.Text(run.Final); strings.TrimSpace(final) != "" {
		source[0] = final
		fmt.Fprintf(&b, "Final answer (step 0):\n%s\n", final)
	}
	req := Request{Purpose: "findings", Schema: itemsSchema, Messages: []Message{{Role: "system", Content: findingsSystemPrompt}, {Role: "user", Content: b.String()}}}
	reply, err := c.Call(ctx, req)
	if reply.Model != "" {
		*model = reply.Model
	}
	if err != nil {
		return nil, 0, false, err
	}
	parsed := parseItems(reply.Text)
	lookup := func(runID string, seq int) (string, bool) {
		if runID != run.RunID {
			return "", false
		}
		s, ok := source[seq]
		return s, ok
	}
	checked, failed := checkItems(run.RunID, parsed, lookup)
	if len(failed) > 0 {
		var list []string
		for _, f := range failed {
			list = append(list, fmt.Sprintf("quote %q (closest: %q)", f.Quote.Text, f.Closest))
		}
		repair := req
		repair.Messages = append(append([]Message(nil), req.Messages...),
			Message{Role: "assistant", Content: red.Text(reply.Text)},
			Message{Role: "user", Content: repairPrompt + strings.Join(list, "; ")})
		rr, rerr := c.Call(ctx, repair)
		if rerr == nil {
			checked, _ = checkItems(run.RunID, parseItems(rr.Text), lookup)
		} else if errors.Is(rerr, ErrBudget) {
			err = rerr
		}
	}
	for _, it := range checked {
		if len(it.Quotes) == 0 {
			dropped++
			continue
		}
		items = append(items, it)
	}
	return items, dropped, true, err
}

func parseItems(text string) []modelItem {
	var out struct {
		Items []modelItem `json:"items"`
	}
	if json.Unmarshal([]byte(StripFence(text)), &out) != nil {
		return nil
	}
	if len(out.Items) > 3 {
		out.Items = out.Items[:3]
	}
	return out.Items
}

// checkItems keeps the passing quotes of each item; failed lists the
// quotes that did not pass.
func checkItems(runID string, ms []modelItem, source func(string, int) (string, bool)) ([]Item, []QuoteResult) {
	var items []Item
	var failed []QuoteResult
	for _, m := range ms {
		qs := make([]Quote, 0, len(m.Quotes))
		for _, q := range m.Quotes {
			qs = append(qs, Quote{RunID: runID, Seq: q.Seq, Text: q.Text})
		}
		it := Item{RunID: runID, Title: cutRunes(m.Title, 80), Hypothesis: cutRunes(m.Hypothesis, 400), Kind: m.Kind, Quotes: []Quote{}, Origin: "model"}
		if !itemKinds[it.Kind] {
			it.Kind = "agent_mistake"
		}
		for _, r := range CheckQuotes(qs, source) {
			if r.OK {
				it.Quotes = append(it.Quotes, r.Quote)
			} else {
				failed = append(failed, r)
			}
		}
		items = append(items, it)
	}
	return items, failed
}

// groupItems makes the grouping call over titles and hypotheses only (no
// step text). Ids the model invents are ignored; an item in no group
// becomes its own group.
func groupItems(ctx context.Context, c Caller, rep *Report, taskOf map[string]string, stopped bool, model *string) []Group {
	byID := map[string]Item{}
	for _, it := range rep.Items {
		byID[it.ID] = it
	}
	var groups []Group
	used := map[string]bool{}
	if len(rep.Runs) >= 2 && len(rep.Items) > 0 && !stopped {
		red := traceimport.NewRedactor()
		var b strings.Builder
		for _, it := range rep.Items {
			fmt.Fprintf(&b, "%s [%s] %s: %s\n", it.ID, it.Kind, red.Text(it.Title), red.Text(it.Hypothesis))
		}
		reply, err := c.Call(ctx, Request{Purpose: "findings", Schema: groupsSchema, Messages: []Message{{Role: "system", Content: groupingPrompt}, {Role: "user", Content: b.String()}}})
		if reply.Model != "" {
			*model = reply.Model
		}
		if errors.Is(err, ErrBudget) {
			rep.Stopped = "budget"
		}
		if err == nil {
			var out struct {
				Groups []modelGroup `json:"groups"`
			}
			if json.Unmarshal([]byte(StripFence(reply.Text)), &out) == nil {
				for _, mg := range out.Groups {
					var ids []string
					for _, id := range mg.Items {
						if _, ok := byID[id]; ok && !used[id] {
							used[id] = true
							ids = append(ids, id)
						}
					}
					if len(ids) == 0 {
						continue
					}
					kind := mg.Kind
					if !itemKinds[kind] {
						kind = byID[ids[0]].Kind
					}
					groups = append(groups, makeGroup(cutRunes(mg.Title, 80), kind, ids, byID, taskOf))
				}
			}
		}
	}
	for _, it := range rep.Items {
		if !used[it.ID] {
			groups = append(groups, makeGroup(it.Title, it.Kind, []string{it.ID}, byID, taskOf))
		}
	}
	if groups == nil {
		groups = []Group{}
	}
	return groups
}

func makeGroup(title, kind string, ids []string, byID map[string]Item, taskOf map[string]string) Group {
	runs := map[string]bool{}
	tasks := map[string]bool{}
	for _, id := range ids {
		r := byID[id].RunID
		runs[r] = true
		if t := taskOf[r]; t != "" {
			tasks[t] = true
		}
	}
	ts := make([]string, 0, len(tasks))
	for t := range tasks {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	return Group{Title: title, Kind: kind, Items: ids, Runs: len(runs), Tasks: ts}
}

func cutRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
