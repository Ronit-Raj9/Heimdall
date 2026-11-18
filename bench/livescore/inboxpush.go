package livescore

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// InboxSourceKind is the source_kind of inbox items live scoring pushed:
// their provenance.
const InboxSourceKind = "live"

// InboxSourceName is the inbox source name of a version's items.
func InboxSourceName(version string) string { return "live-" + version }

// candidate is a failing session waiting for the inbox: its record and its
// redacted transcript. The transcript lives only until the push.
type candidate struct {
	rec     *SessionRecord
	session *traceimport.Session
	redact  traceimport.Report
	reasons []string
}

// newCandidate redacts a failing session's transcript and computes its
// failure signature. The raw transcript is not kept.
func newCandidate(rec *SessionRecord, tr *traceimport.Session, rows []Row) *candidate {
	if tr == nil {
		return nil
	}
	r := traceimport.NewRedactor()
	r.Session(tr)
	tr.ID = rec.ID
	kinds := make([]string, 0, len(rec.Flags))
	for _, f := range rec.Flags {
		if slices.Contains(failureFlags, f) {
			kinds = append(kinds, "live_"+f)
		}
	}
	sig, _ := traceinbox.Signature(tr, kinds)
	rec.Signature = sig
	reasons := []string{fmt.Sprintf("live score, version %s: %s", rec.Version, strings.Join(rec.Flags, ", "))}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Status == statusError {
			msg := oneLine(r.Text(rows[i].ErrorDetails.message()), 160)
			reason := fmt.Sprintf("request to %s/%s failed", rows[i].Provider, rows[i].Model)
			if st := rows[i].ErrorDetails.status(); st != 0 {
				reason += fmt.Sprintf(" (status %d)", st)
			}
			if msg != "" {
				reason += ": " + msg
			}
			reasons = append(reasons, reason)
			break
		}
	}
	return &candidate{rec: rec, session: tr, redact: r.Report(), reasons: reasons}
}

// pushInbox sends the worst failure signatures of this pull to the inbox:
// candidates are grouped by signature, groups ranked by how many sessions
// they hold and then by the worst severity, and the top limit groups become
// items (or new occurrences of existing ones). It returns the item ids it
// touched and sets InboxItem on every pushed record.
func pushInbox(store traceinbox.Store, cands []*candidate, limit int, suite, uiURL string, now time.Time) ([]string, error) {
	if store == nil || len(cands) == 0 || limit <= 0 {
		return nil, nil
	}
	type grp struct {
		sig   string
		cands []*candidate
		worst int
	}
	groups := map[string]*grp{}
	var order []string
	for _, c := range cands {
		g := groups[c.rec.Signature]
		if g == nil {
			g = &grp{sig: c.rec.Signature}
			groups[c.rec.Signature] = g
			order = append(order, c.rec.Signature)
		}
		g.cands = append(g.cands, c)
		g.worst = max(g.worst, c.rec.Severity)
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := groups[order[i]], groups[order[j]]
		if len(a.cands) != len(b.cands) {
			return len(a.cands) > len(b.cands)
		}
		return a.worst > b.worst
	})
	if len(order) > limit {
		order = order[:limit]
	}
	var touched []string
	for _, sig := range order {
		g := groups[sig]
		id := traceinbox.ItemID(sig)
		// The worst session of the group is the item's transcript.
		slices.SortStableFunc(g.cands, func(a, b *candidate) int { return b.rec.Severity - a.rec.Severity })
		head := g.cands[0]
		created := false
		err := store.Update(func(items []traceinbox.Item) ([]traceinbox.Item, error) {
			idx := -1
			for i := range items {
				if items[i].ID == id {
					idx = i
					break
				}
			}
			if idx < 0 {
				if len(items) >= traceinbox.MaxItems {
					return nil, fmt.Errorf("the inbox holds %d items, the cap", len(items))
				}
				created = true
				src := InboxSourceName(head.rec.Version)
				it := traceinbox.Item{ID: id, State: traceinbox.StateNew, Source: src, SourceKind: InboxSourceKind,
					TraceID: head.rec.ID, Link: rowLink(uiURL, head.rec.LastRow),
					Title:     oneLine(firstNonEmpty(head.session.FirstRequest(), "(no user request in the transcript)"), traceinbox.MaxTitleBytes),
					Signature: sig, FirstSeen: head.rec.Ended, LastSeen: head.rec.Ended, Traces: []traceinbox.TraceRef{},
					SuggestedKind: traceinbox.SuggestKind(head.session), SuggestedSuite: suite, Model: head.session.Model,
					Outcome: head.session.Outcome.Status, Updated: now, Flagged: []string{}}
				_, it.SignatureParts = signatureParts(head)
				for _, t := range head.session.ToolCalls {
					if !slices.Contains(it.Tools, t.Name) && len(it.Tools) < 20 {
						it.Tools = append(it.Tools, t.Name)
					}
				}
				items = append(items, it)
				idx = len(items) - 1
			}
			it := &items[idx]
			for _, c := range g.cands {
				ref := traceinbox.TraceRef{ID: c.rec.ID, Source: InboxSourceName(c.rec.Version), At: c.rec.Ended, Link: rowLink(uiURL, c.rec.LastRow)}
				dup := false
				for _, t := range it.Traces {
					if t.ID == ref.ID {
						dup = true
						break
					}
				}
				if dup {
					continue
				}
				it.Traces = append(it.Traces, ref)
				if len(it.Traces) > traceinbox.MaxTraceRefs {
					it.Traces = slices.Clone(it.Traces[len(it.Traces)-traceinbox.MaxTraceRefs:])
				}
				if !(created && c == head) {
					it.Occurrences++
				} else {
					it.Occurrences = max(it.Occurrences, 1)
				}
				if c.rec.Ended.After(it.LastSeen) {
					it.LastSeen = c.rec.Ended
				}
				if c.rec.Ended.Before(it.FirstSeen) {
					it.FirstSeen = c.rec.Ended
				}
				for _, rs := range c.reasons {
					if len(it.Flagged) < traceinbox.MaxFlagged && !slices.Contains(it.Flagged, rs) {
						it.Flagged = append(it.Flagged, rs)
					}
				}
				if it.Redacted == nil {
					it.Redacted = map[string]int{}
				}
				for k, v := range c.redact {
					it.Redacted[k] += v
				}
				if it.State == traceinbox.StateAccepted && !(created && c == head) {
					it.Recurrences++
					t := c.rec.Ended
					it.LastRecurrence = &t
				}
			}
			if len(it.Redacted) == 0 {
				it.Redacted = nil
			}
			it.Updated = now
			return items, nil
		})
		if err != nil {
			return touched, err
		}
		if created {
			if err := store.SaveSession(id, head.session, traceinbox.Preview(head.session, head.reasons, InboxSourceName(head.rec.Version))); err != nil {
				return touched, fmt.Errorf("inbox item %s: %w", id, err)
			}
		}
		for _, c := range g.cands {
			c.rec.InboxItem = id
		}
		touched = append(touched, id)
	}
	return touched, nil
}

// signatureParts recomputes the signature parts with the candidate's flag
// kinds, as the signature itself was computed.
func signatureParts(c *candidate) (string, traceinbox.SignatureParts) {
	kinds := make([]string, 0, len(c.rec.Flags))
	for _, f := range c.rec.Flags {
		if slices.Contains(failureFlags, f) {
			kinds = append(kinds, "live_"+f)
		}
	}
	return traceinbox.Signature(c.session, kinds)
}

// rowLink is the Bifrost logs page with the row selected, as the inbox's
// Bifrost source links it.
func rowLink(uiURL, rowID string) string {
	if uiURL == "" || rowID == "" {
		return ""
	}
	return strings.TrimRight(uiURL, "/") + "/workspace/logs?selected_log=" + url.QueryEscape(rowID)
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
