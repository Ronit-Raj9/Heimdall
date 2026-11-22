package traceinbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// Flag kinds a source reports; the signature uses them when nothing in the
// session errored, so a thumbs-down and an error on the same request stay
// apart.
const (
	FlagScore     = "score"
	FlagFeedback  = "feedback"
	FlagLevel     = "level_error"
	FlagTag       = "tag"
	FlagError     = "error"
	FlagRetries   = "retries"
	FlagFallback  = "fallback"
	FlagBudget    = "budget"
	FlagPushed    = "pushed"
	FlagEvaluator = "evaluator" // maxim: an online evaluator failed the trace
	FlagGolden    = "golden"    // a passing session sampled as a golden (regression) candidate
)

// Flagged is one session a source flagged, before redaction.
type Flagged struct {
	Session *traceimport.Session
	TraceID string
	At      time.Time         // when it was flagged
	Reasons []string          // why, in words
	Kinds   []string          // flag kinds
	Extra   map[string]string // more link template values, e.g. row_id
	// Golden marks a passing session sampled as a golden candidate: the
	// source's golden_per_day caps how many become items each day.
	Golden bool
}

// Batch is what one fetch of a source returned.
type Batch struct {
	Sessions  []Flagged
	Cursor    Cursor // the source's cursor after this fetch
	Warnings  []string
	Truncated bool // the per-pull cap stopped the fetch early
}

// holdAt keeps the cursor from moving past t, so the next pull's window
// (which starts at the cursor minus the overlap) covers a trace this fetch
// could not take.
func (b *Batch) holdAt(t time.Time) {
	if !t.IsZero() && t.Before(b.Cursor.Since) {
		b.Cursor.Since = t
	}
}

// redactedID is the cursor key of a trace id.
func redactedID(id string) string { return traceimport.NewRedactor().ID(id) }

// Fetcher reads flagged sessions from one kind of source since the cursor.
type Fetcher interface {
	Fetch(ctx context.Context, src *Source, cur Cursor, now time.Time) (*Batch, error)
}

// SuiteRef is a scenario suite a draft can go into.
type SuiteRef struct {
	ID       string `json:"id"`
	Template string `json:"template"`
}

// TaskResults says how a task did in the latest experiment that ran it.
// Nil Results leave ReleaseStatus unset.
type TaskResults interface {
	Latest(ctx context.Context, suite, task string) (*ReleaseStatus, error)
}

// Inbox is the failure inbox of one project.
type Inbox struct {
	Store          Store
	Config         *Config
	BenchmarksRoot string // where accepted drafts go and suites are listed
	ProjectRoot    string // for relative task paths in messages
	Getenv         func(string) string
	HTTP           *http.Client
	Sleep          func(context.Context, time.Duration) error // retry waits; tests pass a no-op
	Now            func() time.Time
	Results        TaskResults
	// Fetchers overrides the fetcher of a kind (tests).
	Fetchers map[string]Fetcher
}

// New returns an inbox over the folder dir with the given configuration.
func New(dir string, cfg *Config, benchmarksRoot string) *Inbox {
	if cfg == nil {
		cfg = &Config{}
	}
	return &Inbox{Store: NewFileStore(dir), Config: cfg, BenchmarksRoot: benchmarksRoot}
}

func (ib *Inbox) now() time.Time {
	if ib.Now != nil {
		return ib.Now().UTC()
	}
	return time.Now().UTC()
}

func (ib *Inbox) getenv(k string) string {
	if ib.Getenv != nil {
		return ib.Getenv(k)
	}
	return os.Getenv(k)
}

// fetcher returns the fetcher of a source's kind.
func (ib *Inbox) fetcher(kind string) (Fetcher, error) {
	if f, ok := ib.Fetchers[kind]; ok {
		return f, nil
	}
	c := newAPIClient(ib.HTTP, ib.Sleep)
	switch kind {
	case KindLangfuse:
		return &langfuseFetcher{c: c, getenv: ib.getenv}, nil
	case KindLangSmith:
		return &langsmithFetcher{c: c, getenv: ib.getenv}, nil
	case KindBifrost:
		return &bifrostFetcher{c: c, getenv: ib.getenv}, nil
	case KindOTLPDir:
		return &otlpDirFetcher{}, nil
	case KindMaxim:
		return &maximFetcher{c: c, getenv: ib.getenv}, nil
	}
	return nil, fmt.Errorf("source kind %s cannot be pulled", kind)
}

// PullOptions select what a pull reads.
type PullOptions struct {
	Sources []string  // names; empty pulls every source but the webhook
	Since   time.Time // when set, the window starts here instead of at the cursor
}

// PullResult is one source's pull.
type PullResult struct {
	Source    string   `json:"source"`
	Kind      string   `json:"kind"`
	Fetched   int      `json:"fetched"`   // flagged sessions the source returned
	Skipped   int      `json:"skipped"`   // already seen in an earlier pull
	New       int      `json:"new"`       // new inbox items
	Repeats   int      `json:"repeats"`   // occurrences added to existing items
	Recurred  []string `json:"recurred"`  // accepted items whose failure came back
	Truncated bool     `json:"truncated"` // the per-pull cap was hit; pull again
	Warnings  []string `json:"warnings,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// Pull fetches every selected source once. A failing source is reported in
// its result and does not stop the others; the error is non-nil when every
// selected source failed.
func (ib *Inbox) Pull(ctx context.Context, opts PullOptions) ([]PullResult, error) {
	var srcs []*Source
	for i := range ib.Config.Sources {
		s := &ib.Config.Sources[i]
		if len(opts.Sources) > 0 && !slices.Contains(opts.Sources, s.Name) {
			continue
		}
		if s.Kind == KindWebhook {
			if len(opts.Sources) > 0 {
				return nil, fmt.Errorf("source %s is the webhook; it is pushed to, not pulled", s.Name)
			}
			continue
		}
		srcs = append(srcs, s)
	}
	for _, name := range opts.Sources {
		if _, ok := ib.Config.Find(name); !ok {
			return nil, fmt.Errorf("no source %q in %s", name, SourcesFile)
		}
	}
	if len(srcs) == 0 {
		return nil, fmt.Errorf("no source to pull; add one to %s/%s (docs/failure-inbox.md)", Dir, SourcesFile)
	}
	cursors, err := ib.Store.Cursors()
	if err != nil {
		return nil, err
	}
	var out []PullResult
	failed := 0
	for _, s := range srcs {
		res := ib.pullOne(ctx, s, cursors[s.Name], opts.Since)
		if res.Error != "" {
			failed++
		}
		out = append(out, res)
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
	}
	if failed == len(srcs) {
		return out, errors.New("every source failed: " + out[0].Error)
	}
	return out, nil
}

// pullOne fetches one source and takes in what it flagged.
func (ib *Inbox) pullOne(ctx context.Context, s *Source, cur Cursor, since time.Time) PullResult {
	res := PullResult{Source: s.Name, Kind: s.Kind, Recurred: []string{}}
	now := ib.now()
	if !since.IsZero() {
		cur.Since = since.UTC()
	}
	if cur.Seen == nil {
		cur.Seen = map[string]time.Time{}
	}
	f, err := ib.fetcher(s.Kind)
	if err == nil {
		var b *Batch
		if b, err = f.Fetch(ctx, s, cloneCursor(cur), now); err == nil {
			res.Fetched, res.Truncated, res.Warnings = len(b.Sessions), b.Truncated, b.Warnings
			next := b.Cursor
			if next.Seen == nil {
				next.Seen = map[string]time.Time{}
			}
			// A session that could not be taken in keeps the source
			// position where it was, so the next pull fetches it again;
			// the ones taken in are remembered as seen either way.
			takeFailed := false
			for _, fl := range b.Sessions {
				key := ib.redactID(fl.TraceID)
				if _, seen := cur.Seen[key]; seen && key != "" {
					res.Skipped++
					continue
				}
				out, ierr := ib.take(s, fl)
				if ierr != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf("trace %s: %v", ib.redactID(key), ierr))
					takeFailed = true
					continue
				}
				next.Seen[key] = firstTime(fl.At, now)
				cur.Seen[key] = next.Seen[key]
				switch {
				case out.Created:
					res.New++
				case out.Counted:
					res.Repeats++
				default:
					res.Skipped++ // the item already records this trace
				}
				if out.Recurred {
					res.Recurred = append(res.Recurred, out.ItemID)
				}
			}
			if !takeFailed {
				next.prune(s.overlap() + time.Hour)
				cur = next
			}
		}
	}
	cur.LastPull, cur.Pulled, cur.LastError = now, res.New+res.Repeats, ""
	if err != nil {
		res.Error = err.Error()
		cur.LastError = oneLine(err.Error(), 300)
	}
	if serr := ib.Store.SaveCursor(s.Name, cur); serr != nil && res.Error == "" {
		res.Error = serr.Error()
	}
	detail := fmt.Sprintf("fetched %d, new %d, repeats %d, skipped %d", res.Fetched, res.New, res.Repeats, res.Skipped)
	if res.Error != "" {
		detail = "error: " + oneLine(res.Error, 300)
	}
	_ = ib.Store.Log(LogEvent{At: now, Event: "pull", Source: s.Name, Detail: detail})
	return res
}

// takeResult is what taking in one flagged session did.
type takeResult struct {
	ItemID   string
	Created  bool // a new item
	Counted  bool // an occurrence added to an existing item
	Recurred bool // the item was accepted earlier: its failure came back
	Skipped  bool // a golden over the source's daily sample, not kept
}

// take redacts one flagged session, computes its signature and records it:
// a new item, or one more occurrence of the item with that signature. The
// session is redacted before anything is written; the raw one is dropped.
func (ib *Inbox) take(s *Source, fl Flagged) (takeResult, error) {
	if fl.Session == nil {
		return takeResult{}, errors.New("no session")
	}
	sess := fl.Session
	r := traceimport.NewRedactor()
	r.Session(sess)
	traceID := r.ID(firstNonEmpty(fl.TraceID, sess.ID))
	reasons := make([]string, 0, len(fl.Reasons))
	for _, reason := range fl.Reasons {
		reasons = append(reasons, oneLine(r.Text(reason), 240))
	}
	extra := map[string]string{}
	for k, v := range fl.Extra {
		extra[k] = r.ID(v)
	}
	if raw, err := json.Marshal(sess); err != nil || len(raw) > MaxSessionBytes {
		return takeResult{}, fmt.Errorf("the redacted session is over the %d byte cap; it is not stored", MaxSessionBytes)
	}
	sig, parts := Signature(sess, fl.Kinds)
	id := ItemID(sig)
	now := ib.now()
	at := fl.At
	if at.IsZero() {
		at = firstTime(sess.Started, now)
	}
	at = at.UTC()
	link := s.LinkFor(traceID, extra)
	if link == "" {
		link = extra[linkOverride]
	}
	ref := TraceRef{ID: traceID, Source: s.Name, At: at, Link: link}
	kind := SuggestKind(sess)
	var out takeResult
	out.ItemID = id
	err := ib.Store.Update(func(items []Item) ([]Item, error) {
		for i := range items {
			it := &items[i]
			if it.ID != id {
				continue
			}
			if !addTrace(it, ref) {
				return items, nil
			}
			out.Counted = true
			it.Occurrences++
			if at.After(it.LastSeen) {
				it.LastSeen = at
			}
			if at.Before(it.FirstSeen) {
				it.FirstSeen = at
			}
			addFlagged(it, reasons)
			if it.Redacted == nil {
				it.Redacted = map[string]int{}
			}
			for k, v := range r.Report() {
				it.Redacted[k] += v
			}
			if it.State == StateAccepted {
				out.Recurred = true
				it.Recurrences++
				t := at
				it.LastRecurrence = &t
			}
			it.Updated = now
			return items, nil
		}
		if fl.Golden {
			limit := s.GoldenLimit()
			day := now.Format(time.DateOnly)
			n := 0
			for _, it := range items {
				if it.Golden && it.Source == s.Name && it.FirstSeen.UTC().Format(time.DateOnly) == day {
					n++
				}
			}
			if n >= limit {
				out.Skipped = true
				return items, nil
			}
		}
		if n := OpenItems(items); n >= MaxItems {
			return nil, fmt.Errorf("the inbox holds %d open items, the cap; reject or accept old items first", n)
		}
		// The session and preview are written before the item that lists
		// them, so a failed write leaves no item that cannot become a task.
		if err := ib.Store.SaveSession(id, sess, Preview(sess, reasons, s.Name)); err != nil {
			return nil, fmt.Errorf("item %s: %w", id, err)
		}
		out.Created = true
		it := Item{
			ID: id, State: StateNew, Source: s.Name, SourceKind: s.Kind, TraceID: traceID, Link: link,
			Title:     oneLine(firstNonEmpty(sess.FirstRequest(), sess.Name, "(no user request in the trace)"), MaxTitleBytes),
			Signature: sig, SignatureParts: parts, Occurrences: 1, FirstSeen: at, LastSeen: at, Traces: []TraceRef{ref},
			SuggestedKind: kind, SuggestedSuite: ib.suggestSuite(s, kind), Tools: toolNames(sess), Model: sess.Model,
			Outcome: sess.Outcome.Status, Redacted: map[string]int(r.Report()), Updated: now, Flagged: []string{},
			Golden: fl.Golden,
		}
		if len(it.Redacted) == 0 {
			it.Redacted = nil
		}
		addFlagged(&it, reasons)
		return append(items, it), nil
	})
	if err != nil {
		return takeResult{}, err
	}
	if out.Recurred {
		_ = ib.Store.Log(LogEvent{At: now, Event: "recur", Item: id, Source: s.Name, Detail: "trace " + traceID})
	}
	return out, nil
}

// redactID scrubs an id for a message.
func (ib *Inbox) redactID(id string) string { return traceimport.NewRedactor().ID(id) }

// memoryHint marks a last request that leans on earlier conversation.
var memoryHint = regexp.MustCompile(`(?i)\b(earlier|before|remember|previous(ly)?|last time|you said|i told you|we discussed|as i mentioned)\b`)

// SuggestKind proposes a draft kind from what the session did: tool calls
// make it tool-use, or mcp when a tool name looks like an MCP tool
// (mcp__server__tool, server__tool, mcp_ or mcp. prefixes); several requests
// whose last leans on the earlier conversation make it memory; else qa.
func SuggestKind(s *traceimport.Session) string {
	if len(s.ToolCalls) > 0 {
		for _, c := range s.ToolCalls {
			n := strings.ToLower(c.Name)
			if strings.HasPrefix(n, "mcp") || strings.Contains(n, "__") {
				return traceimport.KindMCP
			}
		}
		return traceimport.KindToolUse
	}
	if reqs := s.Requests(); len(reqs) >= 2 && memoryHint.MatchString(reqs[len(reqs)-1]) {
		return traceimport.KindMemory
	}
	return traceimport.KindQA
}

// suggestSuite is the source's suite, else the first suite whose template
// is the kind.
func (ib *Inbox) suggestSuite(s *Source, kind string) string {
	if s.Suite != "" {
		return s.Suite
	}
	suites, err := ListSuites(ib.BenchmarksRoot)
	if err != nil {
		return ""
	}
	for _, su := range suites {
		if su.Template == kind {
			return su.ID
		}
	}
	return ""
}

func toolNames(s *traceimport.Session) []string {
	var out []string
	for _, c := range s.ToolCalls {
		if !slices.Contains(out, c.Name) && len(out) < 20 {
			out = append(out, c.Name)
		}
	}
	return out
}

func cloneCursor(c Cursor) Cursor {
	c.Seen = maps.Clone(c.Seen)
	c.Files = maps.Clone(c.Files)
	return c
}

// windowStart is where a fetch starts: the cursor minus the overlap, or the
// source's window before now on the first pull.
func windowStart(s *Source, cur Cursor, now time.Time) time.Time {
	if cur.Since.IsZero() {
		return now.Add(-s.window())
	}
	return cur.Since.Add(-s.overlap())
}

func firstTime(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// oneLine collapses s to one line of at most n bytes, cut on a rune start.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}
