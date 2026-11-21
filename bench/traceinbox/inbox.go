// Package traceinbox is the failure inbox: it pulls flagged production
// sessions from the tools a team already runs (Langfuse, LangSmith, the
// Bifrost gateway's logs, a folder an OpenTelemetry collector writes, or a
// webhook any tool can push to), normalizes each through traceimport's
// session model, redacts it, and collapses repeats of one failure into one
// inbox item with a count. A person then accepts an item, which drafts a
// task with `task from-trace`'s machinery (EDIT ME markers, the observed
// calls as a mutant, a replay tool server), or rejects or snoozes it.
//
// The inbox never decides what was right: an accepted item is a draft that
// still needs a person to confirm the expected outcome and pass `task
// validate` and `task admit`. It never stores a raw payload: every session is
// redacted before it touches disk, and only the redacted session, a preview
// and counts of what was scrubbed are kept.
//
// Storage is plain files under inbox/ in the project (items.jsonl, one folder
// per item, cursors.json, log.jsonl) behind the small Store interface, so a
// hosted build can index the same items later. docs/failure-inbox.md is the
// user guide.
package traceinbox

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Item states.
const (
	StateNew       = "new"       // waiting for a person
	StateAccepted  = "accepted"  // became a task draft
	StateRejected  = "rejected"  // not a task, with a reason
	StateDuplicate = "duplicate" // the same failure as another item
	StateSnoozed   = "snoozed"   // hidden until SnoozedUntil, then new again
)

// States lists the item states in display order.
func States() []string {
	return []string{StateNew, StateAccepted, StateRejected, StateDuplicate, StateSnoozed}
}

// Source kinds (Source.Kind).
const (
	KindLangfuse  = "langfuse"
	KindLangSmith = "langsmith"
	KindBifrost   = "bifrost"
	KindOTLPDir   = "otlp_dir"
	KindWebhook   = "webhook"
	KindMaxim     = "maxim" // X23: the hackathon sponsor's log repositories (maxim.go)
)

// Release statuses (ReleaseStatus.Status).
const (
	ReleasePasses = "passes"
	ReleaseFails  = "fails"
	ReleaseNotRun = "not_run"
)

// Caps. An item never grows without bound and the inbox refuses to grow past
// MaxItems; a pull stops at the per-pull caps and says so.
const (
	MaxItems          = 5000         // open items (not accepted or rejected); the intake cap
	maxStoredItems    = 4 * MaxItems // items kept in items.jsonl; the oldest resolved ones go first
	MaxTraceRefs      = 20           // trace references kept per item, newest last
	MaxFlagged        = 8            // reasons kept per item
	MaxPreviewBytes   = 16 << 10     // preview.md
	MaxSessionBytes   = 8 << 20      // session.json of one item
	MaxTitleBytes     = 160          // the one-line title
	DefaultMaxPerPull = 200          // sessions fetched per source per pull
	MaxRejectReason   = 500          // bytes of a reject reason
	MaxSnoozeDays     = 90           // longest snooze
	maxLogLineBytes   = 4 << 10      // one log.jsonl line
	maxItemsFileBytes = 256 << 20    // items.jsonl read cap
)

// ErrConflict marks an action the item's state does not allow.
var ErrConflict = errors.New("conflict")

// ErrNotFound marks an unknown item id.
var ErrNotFound = errors.New("not found")

// The inbox's wire types live in schemas (schemas/inbox.go), so the API
// description and the UI contract name them; these aliases keep this
// package's code short.
type (
	Item           = schemas.InboxItem
	SignatureParts = schemas.InboxSignatureParts
	TraceRef       = schemas.InboxTraceRef
	TaskLink       = schemas.InboxTaskLink
	ReleaseStatus  = schemas.InboxReleaseStatus
)

// TaskKey is a task link's suite/id.
func TaskKey(t *TaskLink) string { return t.Suite + "/" + t.ID }

// Visible returns the state an item shows now: a snooze that has run out
// reads as new.
func Visible(it *Item, now time.Time) string {
	if it.State == StateSnoozed && it.SnoozedUntil != nil && !now.Before(*it.SnoozedUntil) {
		return StateNew
	}
	return it.State
}

// addTrace records one occurrence, keeping the newest MaxTraceRefs. It
// returns false when the trace is already recorded.
func addTrace(it *Item, ref TraceRef) bool {
	for _, r := range it.Traces {
		if r.ID == ref.ID && r.Source == ref.Source {
			return false
		}
	}
	it.Traces = append(it.Traces, ref)
	if len(it.Traces) > MaxTraceRefs {
		it.Traces = slices.Clone(it.Traces[len(it.Traces)-MaxTraceRefs:])
	}
	return true
}

// addFlagged merges reasons, keeping at most MaxFlagged.
func addFlagged(it *Item, reasons []string) {
	for _, r := range reasons {
		if r == "" || slices.Contains(it.Flagged, r) {
			continue
		}
		if len(it.Flagged) >= MaxFlagged {
			return
		}
		it.Flagged = append(it.Flagged, r)
	}
}

// validState reports whether s is a known state.
func validState(s string) bool { return slices.Contains(States(), s) }

// stateError is the conflict error for an action the state does not allow.
func stateError(it *Item, action string) error {
	return fmt.Errorf("%w: item %s is %s; only new or snoozed items can be %s", ErrConflict, it.ID, it.State, action)
}

// OpenItems counts the items still waiting for a decision: accepted and
// rejected items keep their record (recurrence history) but no longer count
// toward MaxItems, so resolving old items frees intake (QC2-26-08).
func OpenItems(items []Item) int {
	n := 0
	for i := range items {
		if items[i].State != StateAccepted && items[i].State != StateRejected {
			n++
		}
	}
	return n
}

// pruneResolved drops the least recently updated accepted or rejected items
// until at most limit items remain; open items are never dropped.
func pruneResolved(items []Item, limit int) []Item {
	excess := len(items) - limit
	if excess <= 0 {
		return items
	}
	var resolved []int
	for i := range items {
		if items[i].State == StateAccepted || items[i].State == StateRejected {
			resolved = append(resolved, i)
		}
	}
	sort.SliceStable(resolved, func(a, b int) bool { return items[resolved[a]].Updated.Before(items[resolved[b]].Updated) })
	drop := map[int]bool{}
	for _, i := range resolved[:min(excess, len(resolved))] {
		drop[i] = true
	}
	out := items[:0:0]
	for i := range items {
		if !drop[i] {
			out = append(out, items[i])
		}
	}
	return out
}
