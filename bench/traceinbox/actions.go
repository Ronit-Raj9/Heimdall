package traceinbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// RegressionTag is the tag an accepted task gets, so a suite tier can select
// the tasks that came from production incidents.
const RegressionTag = "regression"

// List returns the items in first-seen order with their visible state,
// filtered by state and source when set, each enriched with its task's
// admission and release status.
func (ib *Inbox) List(ctx context.Context, state, source string) ([]Item, error) {
	if state != "" && !validState(state) {
		return nil, fmt.Errorf("state %q: want one of %s", state, strings.Join(States(), ", "))
	}
	items, err := ib.Store.Items()
	if err != nil {
		return nil, err
	}
	now := ib.now()
	out := make([]Item, 0, len(items))
	for _, it := range items {
		it.State = Visible(&it, now)
		if (state != "" && it.State != state) || (source != "" && it.Source != source) {
			continue
		}
		ib.enrich(ctx, &it)
		out = append(out, it)
	}
	return out, nil
}

// Counts returns the number of items in each visible state.
func (ib *Inbox) Counts() (map[string]int, error) {
	items, err := ib.Store.Items()
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, s := range States() {
		out[s] = 0
	}
	now := ib.now()
	for _, it := range items {
		out[Visible(&it, now)]++
	}
	return out, nil
}

// Show returns one item and its preview.
func (ib *Inbox) Show(ctx context.Context, id string) (*Item, []byte, error) {
	it, err := ib.find(id)
	if err != nil {
		return nil, nil, err
	}
	it.State = Visible(it, ib.now())
	ib.enrich(ctx, it)
	preview, err := ib.Store.Preview(id)
	if err != nil {
		return nil, nil, err
	}
	return it, preview, nil
}

func (ib *Inbox) find(id string) (*Item, error) {
	if !ValidItemID(id) {
		return nil, fmt.Errorf("item %q: %w", id, ErrNotFound)
	}
	items, err := ib.Store.Items()
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, fmt.Errorf("item %s: %w", id, ErrNotFound)
}

// enrich fills the task's admission and the release status of an accepted
// item from the suite lock and the results.
func (ib *Inbox) enrich(ctx context.Context, it *Item) {
	if it.Task == nil {
		return
	}
	it.Task.Admitted = TaskAdmitted(ib.BenchmarksRoot, it.Task.Suite, it.Task.ID)
	if ib.Results == nil {
		return
	}
	rs, err := ib.Results.Latest(ctx, it.Task.Suite, it.Task.ID)
	if err == nil && rs != nil {
		it.Release = rs
	}
}

// TaskAdmitted reads the suite lock: the task is admitted when its lock
// entry says so.
func TaskAdmitted(root, suiteID, taskID string) bool {
	if root == "" {
		return false
	}
	data, err := readCapped(filepath.Join(root, suiteID, "suite.lock"), 64<<20)
	if err != nil {
		return false
	}
	var lock schemas.SuiteLock
	if json.Unmarshal(data, &lock) != nil {
		return false
	}
	tl, ok := lock.Tasks[taskID]
	return ok && tl.Status == schemas.TaskAdmitted
}

// AcceptOptions configure Accept.
type AcceptOptions struct {
	Suite  string // suite id under the benchmarks folder
	Kind   string // qa, tool-use, mcp or memory; empty takes the suite's template
	TaskID string // empty: the item id
	Force  bool   // replace an existing task folder
}

// AcceptResult is what Accept wrote.
type AcceptResult struct {
	Item  Item                     `json:"item"`
	Draft *traceimport.DraftResult `json:"-"`
	Task  string                   `json:"task"` // suite/id
}

// Accept turns an item into a task draft with task from-trace's machinery
// and records the link. The draft carries EDIT ME markers, the observed
// calls as a mutant when the session failed, the replay tool server, tag
// regression and provenance.incident; it still needs a person to confirm
// the expected outcome and pass `task validate` and `task admit`.
func (ib *Inbox) Accept(ctx context.Context, id string, opts AcceptOptions) (*AcceptResult, error) {
	it, err := ib.find(id)
	if err != nil {
		return nil, err
	}
	if v := Visible(it, ib.now()); v != StateNew && v != StateSnoozed {
		return nil, stateError(it, "accepted")
	}
	if !scaffold.ValidID(opts.Suite) {
		return nil, fmt.Errorf("suite %q is not a valid id", opts.Suite)
	}
	taskID := opts.TaskID
	if taskID == "" {
		taskID = it.ID
	}
	if !scaffold.ValidID(taskID) {
		return nil, fmt.Errorf("task id %q is not valid", taskID)
	}
	if opts.Kind != "" && !slices.Contains(traceimport.Kinds(), opts.Kind) {
		return nil, fmt.Errorf("kind %q: want one of %s", opts.Kind, strings.Join(traceimport.Kinds(), ", "))
	}
	sess, err := ib.Store.Session(id)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(sess)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	suiteDir := filepath.Join(ib.BenchmarksRoot, opts.Suite)
	suitePath := filepath.Join(suiteDir, "suite.yaml")
	suiteBefore, err := os.ReadFile(suitePath)
	if err != nil {
		return nil, fmt.Errorf("suite %s: %w", opts.Suite, err)
	}
	src := traceimport.Source{File: "inbox item " + it.ID, SHA256: hex.EncodeToString(sum[:]), Format: sess.Format, Redacted: traceimport.Report(it.Redacted)}
	if src.Redacted == nil {
		src.Redacted = traceimport.Report{}
	}
	res, err := traceimport.WriteDraft(traceimport.DraftOptions{
		SuiteDir: suiteDir, ID: taskID, Kind: opts.Kind, Force: opts.Force, Session: sess, Source: src, Replay: true,
	})
	if err != nil {
		return nil, err
	}
	incident := schemas.TaskIncident{ID: it.ID, Source: it.Source, TraceID: it.TraceID, Link: it.Link,
		FirstSeen: it.FirstSeen.UTC().Format(time.RFC3339), Signature: it.Signature}
	// rollback removes the draft and restores the suite, so a failed accept
	// leaves no registered draft behind (a retry then needs no -force).
	rollback := func() error {
		rmErr := os.RemoveAll(res.Dir)
		if res.SuiteEdited {
			rmErr = errors.Join(rmErr, os.WriteFile(suitePath, suiteBefore, 0o644))
		}
		return rmErr
	}
	if err := markIncident(filepath.Join(res.Dir, "task.yaml"), incident); err != nil {
		return nil, errors.Join(fmt.Errorf("mark the draft as an incident: %w", err), rollback())
	}
	now := ib.now()
	rel := res.Dir
	if ib.ProjectRoot != "" {
		if r, err := filepath.Rel(ib.ProjectRoot, res.Dir); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}
	var accepted Item
	err = ib.Store.Update(func(items []Item) ([]Item, error) {
		for i := range items {
			if items[i].ID != id {
				continue
			}
			if v := Visible(&items[i], now); v != StateNew && v != StateSnoozed {
				return nil, stateError(&items[i], "accepted")
			}
			items[i].State = StateAccepted
			items[i].SnoozedUntil = nil
			items[i].Task = &TaskLink{Suite: opts.Suite, ID: taskID, Dir: filepath.ToSlash(rel), AcceptedAt: now}
			items[i].Updated = now
			accepted = items[i]
			return items, nil
		}
		return nil, fmt.Errorf("item %s: %w", id, ErrNotFound)
	})
	if err != nil {
		// The item did not change state (a failed write, or a concurrent
		// reject): the draft must not outlive the failed accept (QC2-26-09).
		return nil, errors.Join(err, rollback())
	}
	_ = ib.Store.Log(LogEvent{At: now, Event: "accept", Item: id, Detail: opts.Suite + "/" + taskID})
	return &AcceptResult{Item: accepted, Draft: res, Task: opts.Suite + "/" + taskID}, nil
}

// markIncident adds tag regression and provenance.incident to a draft's
// task.yaml, keeping every other field and comment.
func markIncident(path string, inc schemas.TaskIncident) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("task.yaml is not a mapping")
	}
	m := doc.Content[0]
	tags := mapValue(m, "tags")
	if tags == nil || tags.Kind != yaml.SequenceNode {
		tags = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		setNode(m, "tags", tags)
	}
	has := false
	for _, t := range tags.Content {
		if t.Value == RegressionTag {
			has = true
		}
	}
	if !has {
		tags.Content = append(tags.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: RegressionTag})
	}
	prov := mapValue(m, "provenance")
	if prov == nil || prov.Kind != yaml.MappingNode {
		prov = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setNode(m, "provenance", prov)
	}
	var incNode yaml.Node
	if err := incNode.Encode(inc); err != nil {
		return err
	}
	incNode.HeadComment = "The production incident this task was drafted from (failure inbox, docs/failure-inbox.md)."
	setNode(prov, "incident", &incNode)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
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
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

// Reject marks an item rejected with a reason, or duplicate of another item
// when duplicateOf is set.
func (ib *Inbox) Reject(id, reason, duplicateOf string) (*Item, error) {
	reason = oneLine(reason, MaxRejectReason)
	if reason == "" {
		return nil, errors.New("a reject needs a reason")
	}
	if duplicateOf != "" {
		if duplicateOf == id {
			return nil, errors.New("an item cannot duplicate itself")
		}
		if _, err := ib.find(duplicateOf); err != nil {
			return nil, err
		}
	}
	state, event := StateRejected, "reject"
	if duplicateOf != "" {
		state, event = StateDuplicate, "duplicate"
	}
	return ib.transition(id, event, "rejected", reason, func(it *Item) {
		it.State, it.Reason, it.DuplicateOf, it.SnoozedUntil = state, reason, duplicateOf, nil
	})
}

// Snooze hides an item for days (1 to MaxSnoozeDays); it reads as new again
// afterwards.
func (ib *Inbox) Snooze(id string, days int) (*Item, error) {
	if days < 1 || days > MaxSnoozeDays {
		return nil, fmt.Errorf("snooze %d days: want 1 to %d", days, MaxSnoozeDays)
	}
	until := ib.now().Add(time.Duration(days) * 24 * time.Hour)
	return ib.transition(id, "snooze", "snoozed", until.Format(time.RFC3339), func(it *Item) {
		it.State, it.SnoozedUntil = StateSnoozed, &until
	})
}

// transition changes a new or snoozed item.
func (ib *Inbox) transition(id, event, verb, detail string, change func(*Item)) (*Item, error) {
	if !ValidItemID(id) {
		return nil, fmt.Errorf("item %q: %w", id, ErrNotFound)
	}
	now := ib.now()
	var out Item
	err := ib.Store.Update(func(items []Item) ([]Item, error) {
		for i := range items {
			if items[i].ID != id {
				continue
			}
			if v := Visible(&items[i], now); v != StateNew && v != StateSnoozed {
				return nil, stateError(&items[i], verb)
			}
			change(&items[i])
			items[i].Updated = now
			out = items[i]
			return items, nil
		}
		return nil, fmt.Errorf("item %s: %w", id, ErrNotFound)
	})
	if err != nil {
		return nil, err
	}
	_ = ib.Store.Log(LogEvent{At: now, Event: event, Item: id, Detail: detail})
	return &out, nil
}
