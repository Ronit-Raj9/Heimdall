package tracebulk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/traceimport"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// DraftOptions say which clusters become tasks and where.
type DraftOptions struct {
	Suite      string   // benchmark id under the benchmarks folder
	Clusters   []string // cluster ids; empty takes every cluster
	PerCluster int      // tasks per cluster (default 1, at most 5)
	Kind       string   // qa, tool-use, mcp or memory; empty takes the suite's template
}

// DraftResult is what Draft wrote.
type DraftResult struct {
	Tasks    []TaskDraft `json:"tasks"`
	Warnings []string    `json:"warnings"`
}

// Draft turns clusters of a batch into task drafts with the inbox's accept
// machinery, adds the generated checks.yaml and scenario row, tags each
// draft pending-review, and marks the cluster's other items duplicates of
// the drafted one. The batch is updated in place; the caller saves it.
func Draft(ctx context.Context, ib *traceinbox.Inbox, b *Batch, o DraftOptions, now time.Time) (*DraftResult, error) {
	if o.PerCluster == 0 {
		o.PerCluster = 1
	}
	if o.PerCluster < 1 || o.PerCluster > MaxPerCluster {
		return nil, fmt.Errorf("per_cluster must be 1 to %d", MaxPerCluster)
	}
	if !scaffold.ValidID(o.Suite) {
		return nil, fmt.Errorf("benchmark %q is not a valid id", o.Suite)
	}
	if _, err := os.Stat(filepath.Join(ib.BenchmarksRoot, o.Suite, "suite.yaml")); err != nil {
		return nil, fmt.Errorf("benchmark %s: %w", o.Suite, ErrNotFound)
	}
	ids := o.Clusters
	if len(ids) == 0 {
		for _, c := range b.Clusters {
			ids = append(ids, c.ID)
		}
	}
	var picks []*Cluster
	for _, id := range ids {
		c, ok := b.Cluster(id)
		if !ok {
			return nil, fmt.Errorf("cluster %s: %w", id, ErrNotFound)
		}
		picks = append(picks, c)
	}
	if n := len(picks) * o.PerCluster; n > MaxDraftsPerCall {
		return nil, fmt.Errorf("that is up to %d tasks; draft at most %d at a time (pick fewer clusters)", n, MaxDraftsPerCall)
	}
	items, err := ib.Store.Items()
	if err != nil {
		return nil, err
	}
	state := map[string]string{}
	for i := range items {
		state[items[i].ID] = traceinbox.Visible(&items[i], now)
	}
	res := &DraftResult{Tasks: []TaskDraft{}, Warnings: []string{}}
	for _, c := range picks {
		var drafted []string
		for _, item := range c.Items {
			if len(drafted) >= o.PerCluster {
				break
			}
			if err := ctx.Err(); err != nil {
				return res, err
			}
			if st := state[item]; st != traceinbox.StateNew && st != traceinbox.StateSnoozed {
				continue
			}
			t, err := draftOne(ctx, ib, c, item, o, now)
			if err != nil {
				res.Warnings = append(res.Warnings, "item "+item+": "+oneLine(err.Error(), 240))
				continue
			}
			drafted = append(drafted, item)
			state[item] = traceinbox.StateAccepted
			b.Tasks = append(b.Tasks, *t)
			res.Tasks = append(res.Tasks, *t)
		}
		if len(drafted) == 0 {
			res.Warnings = append(res.Warnings, "cluster "+c.Label+": no open inbox item left to draft")
			continue
		}
		for _, item := range c.Items {
			if st := state[item]; st != traceinbox.StateNew && st != traceinbox.StateSnoozed {
				continue
			}
			if _, err := ib.Reject(item, "same failure mode as "+drafted[0]+" ("+c.Label+"), drafted in batch "+b.ID, drafted[0]); err == nil {
				state[item] = traceinbox.StateDuplicate
			}
		}
	}
	b.Updated = now
	return res, nil
}

func draftOne(ctx context.Context, ib *traceinbox.Inbox, c *Cluster, item string, o DraftOptions, now time.Time) (*TaskDraft, error) {
	acc, err := ib.Accept(ctx, item, traceinbox.AcceptOptions{Suite: o.Suite, Kind: o.Kind})
	if err != nil {
		return nil, err
	}
	sess, err := ib.Store.Session(item)
	if err != nil {
		return nil, err
	}
	dir := acc.Draft.Dir
	taskID := filepath.Base(dir)
	rubric := SuggestedRubric(sess, c)
	checks, ids, err := ChecksYAML(taskID, sess, c, rubric, "")
	if err != nil {
		return nil, err
	}
	scen, err := ScenarioJSON(taskID, sess, c, rubric, "")
	if err != nil {
		return nil, err
	}
	if err := writeFile(dir, ChecksFile, checks); err != nil {
		return nil, err
	}
	if err := writeFile(dir, ScenarioFile, scen); err != nil {
		return nil, err
	}
	if err := editTags(filepath.Join(dir, "task.yaml"), []string{TagMaxim, TagPendingReview}, nil); err != nil {
		return nil, err
	}
	t := &TaskDraft{Cluster: c.ID, Item: item, Suite: o.Suite, ID: taskID, Kind: acc.Draft.Template, State: StatePending,
		Generated: ids, Request: oneLine(sess.FirstRequest(), 600), Recorded: oneLine(sess.FinalAnswer, 600), Suggested: rubric,
		Readiness: Readiness{Status: ReadyNotRun}, Drafted: now}
	t.EditMe, _ = EditMeFiles(dir)
	return t, nil
}

// Review is a person's "what should have happened".
type Review struct {
	// Answer is the right answer (question and answer tasks: required).
	Answer string `json:"answer,omitempty"`
	// Rubric replaces the generated rubric; empty keeps it.
	Rubric string `json:"rubric,omitempty"`
	// Instruction replaces the drafted request; empty keeps the recorded one.
	Instruction string `json:"instruction,omitempty"`
}

// Caps of a review.
const (
	MaxAnswerBytes      = 8 << 10
	MaxRubricBytes      = 4000
	MaxInstructionBytes = 16 << 10
)

// taskShape is what Accept reads from a draft's task.yaml.
type taskShape struct {
	Instruction string `yaml:"instruction"`
	Verifier    struct {
		Tests  string `yaml:"tests"`
		Answer string `yaml:"answer"`
	} `yaml:"verifier"`
	Controls struct {
		Reference string `yaml:"reference"`
	} `yaml:"controls"`
}

// Accept records a person's review of a pending draft: it writes the
// instruction, the expected answer (question and answer tasks) and the
// reviewed checks, drops the pending-review tag, and sets the state to
// accepted, or needs_files when a file still holds an EDIT ME line (a
// tool-use draft's state checks). It changes t in place.
func Accept(benchRoot string, ib *traceinbox.Inbox, b *Batch, t *TaskDraft, r Review, who string, now time.Time) error {
	if t.State != StatePending && t.State != StateNeedFiles {
		return fmt.Errorf("%w: task %s is %s", ErrConflict, t.Key(), t.State)
	}
	r.Answer, r.Rubric, r.Instruction = strings.TrimSpace(r.Answer), strings.TrimSpace(r.Rubric), strings.TrimSpace(r.Instruction)
	switch {
	case len(r.Answer) > MaxAnswerBytes:
		return fmt.Errorf("answer is over %d bytes", MaxAnswerBytes)
	case len(r.Rubric) > MaxRubricBytes:
		return fmt.Errorf("rubric is over %d bytes", MaxRubricBytes)
	case len(r.Instruction) > MaxInstructionBytes:
		return fmt.Errorf("instruction is over %d bytes", MaxInstructionBytes)
	}
	qa := t.Kind == traceimport.KindQA || t.Kind == traceimport.KindMemory
	if qa && r.Answer == "" {
		return errors.New("say what the right answer is: a question and answer task is graded against it")
	}
	dir := TaskDir(benchRoot, t)
	meta, err := os.ReadFile(filepath.Join(dir, "task.yaml"))
	if err != nil {
		return fmt.Errorf("task %s: %w", t.Key(), err)
	}
	var shape taskShape
	if err := yaml.Unmarshal(meta, &shape); err != nil {
		return fmt.Errorf("task %s: task.yaml: %w", t.Key(), err)
	}
	sess, err := ib.Store.Session(t.Item)
	if err != nil {
		return err
	}
	c, _ := b.Cluster(t.Cluster)
	who = firstNonEmpty(who, "a reviewer")
	rubric := firstNonEmpty(r.Rubric, t.Suggested)
	if shape.Instruction != "" && !strings.Contains(shape.Instruction, "..") {
		instr := r.Instruction
		if instr == "" {
			cur, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(shape.Instruction)))
			if err != nil {
				return err
			}
			instr = stripDraftHeader(string(cur))
		}
		if strings.TrimSpace(instr) == "" {
			return errors.New("the instruction is empty; write the request the agent gets")
		}
		if err := writeFile(dir, shape.Instruction, []byte(strings.TrimSpace(instr)+"\n")); err != nil {
			return err
		}
	}
	if qa {
		ansRel := shape.Verifier.Answer
		if ansRel == "" {
			ansRel = path.Join(firstNonEmpty(shape.Verifier.Tests, "verifier"), "answer.yaml")
		}
		if err := writeFile(dir, ansRel, answerYAML(sess.FirstRequest(), r.Answer, sess.FinalAnswer, who)); err != nil {
			return err
		}
		if strings.HasSuffix(shape.Controls.Reference, ".txt") {
			if err := writeFile(dir, shape.Controls.Reference, []byte(r.Answer+"\n")); err != nil {
				return err
			}
		}
	}
	checks, ids, err := ChecksYAML(t.ID, sess, c, rubric, who)
	if err != nil {
		return err
	}
	if err := writeFile(dir, ChecksFile, checks); err != nil {
		return err
	}
	scen, err := ScenarioJSON(t.ID, sess, c, rubric, who)
	if err != nil {
		return err
	}
	if err := writeFile(dir, ScenarioFile, scen); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "verifier", "requirements.md")); err == nil {
		if err := writeFile(dir, "verifier/requirements.md", requirementsMD(t, c, r, rubric, who, now)); err != nil {
			return err
		}
	}
	if err := editTags(filepath.Join(dir, "task.yaml"), nil, []string{TagPendingReview}); err != nil {
		return err
	}
	t.Generated = ids
	t.Suggested = rubric
	t.EditMe, err = EditMeFiles(dir)
	if err != nil {
		return err
	}
	at := now
	t.Reviewed, t.ReviewedBy = &at, who
	t.State = StateAccepted
	if len(t.EditMe) > 0 {
		t.State = StateNeedFiles
	}
	b.Updated = now
	return nil
}

// Reject removes a pending draft: its folder and its id in suite.yaml.
func Reject(benchRoot string, b *Batch, t *TaskDraft, reason, who string, now time.Time) error {
	if t.State == StateRejected {
		return fmt.Errorf("%w: task %s is already rejected", ErrConflict, t.Key())
	}
	if t.State == StateAccepted && t.Readiness.Status == ReadyPassed {
		return fmt.Errorf("%w: task %s passed its readiness check; remove it from the benchmark instead", ErrConflict, t.Key())
	}
	suiteDir := filepath.Join(benchRoot, t.Suite)
	if err := os.RemoveAll(TaskDir(benchRoot, t)); err != nil {
		return err
	}
	if err := removeTaskID(filepath.Join(suiteDir, "suite.yaml"), t.ID); err != nil {
		return err
	}
	at := now
	t.State, t.Reviewed, t.ReviewedBy = StateRejected, &at, firstNonEmpty(who, "a reviewer")
	t.Note = oneLine(reason, 300)
	t.EditMe = nil
	b.Updated = now
	return nil
}

// TaskDir is a drafted task's folder (benchmarks/<suite>/tasks/<id>).
func TaskDir(benchRoot string, t *TaskDraft) string {
	return filepath.Join(benchRoot, t.Suite, "tasks", t.ID)
}

// EditMeFiles lists the task files that still hold an EDIT ME line, as
// task validate reads them. The transcript beside task.yaml is skipped: it
// is a copy of the session, not a file to finish.
func EditMeFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == traceimport.TranscriptFile {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, scaffold.EditMeMarker) || strings.HasPrefix(l, "# EDIT ME") {
				out = append(out, rel)
				break
			}
		}
		return nil
	})
	return out, err
}

// stripDraftHeader removes the EDIT ME marker line and the comment block
// the draft put above the recorded request.
func stripDraftHeader(s string) string {
	s = strings.TrimLeft(s, "\n")
	if rest, ok := strings.CutPrefix(s, scaffold.EditMeMarker); ok {
		s = strings.TrimLeft(rest, " \n")
	}
	if strings.HasPrefix(s, "<!--") {
		if i := strings.Index(s, "-->"); i >= 0 {
			s = s[i+3:]
		}
	}
	return strings.TrimSpace(s)
}

// answerYAML is the expected answer a person gave, matched exactly when it
// is one short line and as facts (one per line, all present) otherwise. The
// recorded production answer is a wrong answer readiness must fail.
func answerYAML(question, answer, recorded, who string) []byte {
	var facts []string
	for _, l := range strings.Split(answer, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			facts = append(facts, l)
		}
	}
	match := "contains_all"
	if len(facts) == 1 && len(facts[0]) <= 120 {
		match = "exact"
	}
	doc := map[string]any{
		"question":  oneLine(question, 500),
		"match":     match,
		"expected":  facts,
		"normalize": []string{"lower", "collapse_space", "trim"},
	}
	wrong := []string{}
	if rec := strings.TrimSpace(recorded); rec != "" && !strings.EqualFold(strings.Join(strings.Fields(rec), " "), strings.Join(strings.Fields(answer), " ")) {
		wrong = append(wrong, oneLine(rec, 2000))
	}
	doc["wrong_answers"] = wrong
	var buf bytes.Buffer
	buf.WriteString("# Hidden expected answer of a task drafted from a Maxim production failure,\n")
	buf.WriteString("# written by " + oneLine(who, 80) + " in the review step. The recorded production\n")
	buf.WriteString("# answer is kept as a wrong answer the readiness check must see fail.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(orderedAnswer(doc))
	_ = enc.Close()
	return buf.Bytes()
}

// orderedAnswer keeps answer.yaml's keys in reading order.
func orderedAnswer(m map[string]any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, k := range []string{"question", "match", "expected", "normalize", "wrong_answers"} {
		var v yaml.Node
		_ = v.Encode(m[k])
		if k == "normalize" || (k == "wrong_answers" && len(m[k].([]string)) == 0) {
			v.Style = yaml.FlowStyle
		}
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, &v)
	}
	return n
}

func requirementsMD(t *TaskDraft, c *Cluster, r Review, rubric, who string, now time.Time) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: what a correct result holds, and why\n\n", t.ID)
	if c != nil {
		fmt.Fprintf(&b, "Drafted from a Maxim production failure: %s (%d occurrences in batch cluster %s).\n\n", c.Label, c.Occurrences, c.ID)
	}
	if r.Answer != "" {
		b.WriteString("Right answer, as the reviewer wrote it:\n\n")
		for _, l := range strings.Split(r.Answer, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				b.WriteString("- " + l + "\n")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("What should have happened: " + oneLine(rubric, 3000) + "\n\n")
	fmt.Fprintf(&b, "Reviewed by %s on %s.\n", oneLine(who, 80), now.UTC().Format("2006-01-02"))
	return []byte(b.String())
}

func writeFile(dir, rel string, data []byte) error {
	if rel == "" || strings.Contains(rel, "..") || path.IsAbs(rel) {
		return fmt.Errorf("refusing to write %q", rel)
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// editTags adds and removes task.yaml tags, keeping every other field.
func editTags(p string, add, remove []string) error {
	return editYAML(p, func(m *yaml.Node) error {
		var tags *yaml.Node
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "tags" {
				tags = m.Content[i+1]
			}
		}
		if tags == nil || tags.Kind != yaml.SequenceNode {
			tags = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
			setNode(m, "tags", tags)
		}
		var kept []*yaml.Node
		for _, t := range tags.Content {
			if !slices.Contains(remove, t.Value) {
				kept = append(kept, t)
			}
		}
		for _, a := range add {
			if !slices.ContainsFunc(kept, func(n *yaml.Node) bool { return n.Value == a }) {
				kept = append(kept, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: a})
			}
		}
		tags.Content = kept
		return nil
	})
}

// removeTaskID drops id from suite.yaml's tasks list.
func removeTaskID(p, id string) error {
	return editYAML(p, func(m *yaml.Node) error {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value != "tasks" || m.Content[i+1].Kind != yaml.SequenceNode {
				continue
			}
			seq := m.Content[i+1]
			seq.Content = slices.DeleteFunc(seq.Content, func(n *yaml.Node) bool { return n.Value == id })
		}
		return nil
	})
}

func editYAML(p string, fn func(*yaml.Node) error) error {
	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s is not a mapping", filepath.Base(p))
	}
	if err := fn(doc.Content[0]); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(p, buf.Bytes(), 0o644)
}

func setNode(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

// SetReadiness records a readiness job queued for a task.
func SetReadiness(t *TaskDraft, jobID string, now time.Time) {
	at := now
	t.Readiness = Readiness{Status: ReadyQueued, JobID: jobID, At: &at}
}

// RefreshReadiness reads each accepted task's admission from its suite
// lock, and the outcome of its readiness job through jobDone (finished,
// whatever the result); a finished job with no admission is not ready.
func RefreshReadiness(benchRoot string, b *Batch, jobDone func(id string) bool) {
	for i := range b.Tasks {
		t := &b.Tasks[i]
		if t.State != StateAccepted {
			continue
		}
		switch {
		case traceinbox.TaskAdmitted(benchRoot, t.Suite, t.ID):
			t.Readiness.Status = ReadyPassed
		case t.Readiness.Status == ReadyQueued && t.Readiness.JobID != "" && jobDone != nil && jobDone(t.Readiness.JobID):
			t.Readiness.Status = ReadyNotReady
		}
	}
}
