package tracebulk

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/maxim"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// SourceName is the inbox source name of bulk Maxim imports.
const SourceName = "maxim-bulk"

// maxWarnings caps the warnings one batch keeps.
const maxWarnings = 20

// Maxim is a connected Maxim log repository.
type Maxim struct {
	Client   maxim.Client
	APIKey   string
	Settings maxim.Settings
}

// PullOptions configure PullMaxim.
type PullOptions struct {
	Filter    Filter
	Now       time.Time // zero: time.Now
	CreatedBy string
}

type candidate struct {
	id      string
	at      time.Time
	reasons []string
	kinds   []string
}

type candidates struct {
	byID  map[string]*candidate
	order []string
	limit int
	full  bool
}

// add records a flag on a trace; it returns false once the limit is reached.
func (c *candidates) add(id string, at time.Time, reason, kind string) bool {
	if id == "" {
		return true
	}
	if cd, ok := c.byID[id]; ok {
		if !slices.Contains(cd.kinds, kind) {
			cd.kinds = append(cd.kinds, kind)
		}
		if reason != "" && !slices.Contains(cd.reasons, reason) {
			cd.reasons = append(cd.reasons, reason)
		}
		return true
	}
	if len(c.order) >= c.limit {
		c.full = true
		return false
	}
	c.byID[id] = &candidate{id: id, at: at, reasons: []string{reason}, kinds: []string{kind}}
	c.order = append(c.order, id)
	return true
}

// PullMaxim reads the failing traces of a Maxim log repository, records
// each in the inbox and returns a new, unsaved batch with its clusters.
func PullMaxim(ctx context.Context, ib *traceinbox.Inbox, m Maxim, opts PullOptions) (*Batch, error) {
	f := opts.Filter
	if err := f.Normalize(); err != nil {
		return nil, err
	}
	if m.APIKey == "" {
		return nil, fmt.Errorf("maxim is connected without an api key; save the key again")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	b := &Batch{ID: NewID(now), Source: traceinbox.KindMaxim, Workspace: m.Settings.WorkspaceID, Repo: m.Settings.RepoID,
		Filter: f, Created: now, Updated: now, CreatedBy: opts.CreatedBy, Clusters: []Cluster{}, Tasks: []TaskDraft{}}
	q := maxim.LogSearch{WorkspaceID: m.Settings.WorkspaceID, RepoID: m.Settings.RepoID,
		From: now.Add(-time.Duration(f.Days) * 24 * time.Hour).Format(time.RFC3339), To: now.Format(time.RFC3339)}
	cs := &candidates{byID: map[string]*candidate{}, limit: f.Limit}
	counted := map[string]bool{}
	mark := func(id, label string, what *int) {
		if !counted[label+"\x1f"+id] {
			counted[label+"\x1f"+id] = true
			*what++
		}
	}
	search := func(q maxim.LogSearch, take func(maxim.LogEntry) bool) error {
		for page := 0; page*maxim.SearchPageSize < MaxScan; page++ {
			q.Page = page
			p, err := m.Client.SearchLogs(ctx, m.APIKey, q)
			if err != nil {
				return err
			}
			b.Counts.Scanned += len(p.Logs)
			for _, l := range p.Logs {
				if !take(l) {
					return nil
				}
			}
			if len(p.Logs) < maxim.SearchPageSize || (page+1)*maxim.SearchPageSize >= p.Total {
				return nil
			}
		}
		b.Truncated = true
		return nil
	}
	at := func(l maxim.LogEntry) time.Time {
		t, err := time.Parse(time.RFC3339Nano, l.StartTimestamp)
		if err != nil {
			return now
		}
		return t.UTC()
	}
	if f.Errors {
		q := q
		q.Errors = true
		if err := search(q, func(l maxim.LogEntry) bool {
			ok := cs.add(l.ID, at(l), "trace errored in Maxim", traceinbox.FlagError)
			if ok {
				mark(l.ID, "errored", &b.Counts.Errored)
			}
			return ok
		}); err != nil {
			return nil, err
		}
	}
	for _, name := range f.Evaluators {
		q := q
		q.Evaluator = name
		if err := search(q, func(l maxim.LogEntry) bool {
			reason := "Maxim evaluator " + name + " failed"
			for _, e := range l.EvaluationData {
				if e.EvaluatorName == name && strings.TrimSpace(e.Reasoning) != "" {
					reason += ": " + oneLine(e.Reasoning, 160)
				}
			}
			ok := cs.add(l.ID, at(l), reason, traceinbox.FlagEvaluator+":"+name)
			if ok {
				mark(l.ID, "evaluator", &b.Counts.EvaluatorFailed)
			}
			return ok
		}); err != nil {
			return nil, err
		}
	}
	if f.ScoreBelow != nil {
		limit := *f.ScoreBelow
		if err := search(q, func(l maxim.LogEntry) bool {
			for _, e := range l.EvaluationData {
				if len(f.ScoreEvaluators) > 0 && !slices.Contains(f.ScoreEvaluators, e.EvaluatorName) {
					continue
				}
				v, ok := e.ScoreValue()
				if !ok || v >= limit {
					continue
				}
				reason := fmt.Sprintf("Maxim evaluator %s scored %s, below %s", e.EvaluatorName, num(v), num(limit))
				if strings.TrimSpace(e.Reasoning) != "" {
					reason += ": " + oneLine(e.Reasoning, 160)
				}
				if !cs.add(l.ID, at(l), reason, traceinbox.FlagScore+":"+e.EvaluatorName) {
					return false
				}
				mark(l.ID, "score", &b.Counts.LowScore)
			}
			return true
		}); err != nil {
			return nil, err
		}
	}
	b.Truncated = b.Truncated || cs.full
	b.Counts.Failed = len(cs.order)
	src := &traceinbox.Source{Name: SourceName, Kind: traceinbox.KindMaxim, Workspace: m.Settings.WorkspaceID, Project: m.Settings.RepoID,
		Link: linkTemplate(m.Settings)}
	touched := map[string]bool{}
	for _, id := range cs.order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cd := cs.byID[id]
		raw, err := m.Client.Trace(ctx, m.APIKey, id)
		if err == nil {
			var res traceinbox.TakeResult
			sess, cerr := traceinbox.MaximSession(id, raw)
			if cerr == nil {
				b.Counts.Fetched++
				res, cerr = ib.Take(src, traceinbox.Flagged{Session: sess, TraceID: id, At: cd.at, Reasons: cd.reasons, Kinds: cd.kinds,
					Extra: map[string]string{"workspace": m.Settings.WorkspaceID}})
			}
			if cerr == nil {
				touched[res.ItemID] = true
				switch {
				case res.Created:
					b.Counts.NewItems++
				case res.Counted:
					b.Counts.Repeats++
				}
				continue
			}
			err = cerr
		}
		if err == maxim.ErrUnauthorized {
			return nil, err
		}
		if len(b.Warnings) < maxWarnings {
			b.Warnings = append(b.Warnings, "trace "+id+": "+oneLine(err.Error(), 200))
		}
	}
	items, err := ib.Store.Items()
	if err != nil {
		return nil, err
	}
	var mine []traceinbox.Item
	for _, it := range items {
		if touched[it.ID] {
			mine = append(mine, it)
		}
	}
	b.Clusters = Clusters(mine)
	return b, nil
}

// linkTemplate turns the connection's trace link into an inbox source link
// template ({workspace}, {project}, {trace_id}).
func linkTemplate(s maxim.Settings) string {
	tmpl := s.LinkTemplate
	if tmpl == "" {
		tmpl = maxim.DefaultLinkTemplate
	}
	return strings.NewReplacer("{workspace_id}", "{workspace}", "{repo_id}", "{project}").Replace(tmpl)
}

func num(v float64) string { return strconv.FormatFloat(v, 'g', 4, 64) }
