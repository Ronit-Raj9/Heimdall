package bifrost

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxReasonItems caps how many problems a Reason string lists.
const maxReasonItems = 5

// joinResult is the match between a run's rows and its proxy ledger.
type joinResult struct {
	Rows      map[string][]int      // row id -> indices into Entries it covers, oldest first
	Entries   []schemas.LedgerEntry // the ledger entries a row must cover
	Unmatched []int                 // indices into Entries no row covers
	Problems  []string              // rows that could not be joined, or joined ambiguously
}

// complete reports whether every entry is covered exactly once and every row
// that reached the proxy covers as many entries as it had attempts.
func (j joinResult) complete() bool {
	return len(j.Unmatched) == 0 && len(j.Problems) == 0
}

// reason describes an incomplete join in one line.
func (j joinResult) reason() string {
	var parts []string
	if n := len(j.Unmatched); n > 0 {
		var items []string
		for i, idx := range j.Unmatched {
			if i == maxReasonItems {
				items = append(items, "...")
				break
			}
			e := j.Entries[idx]
			items = append(items, fmt.Sprintf("%s call %d %s %d", e.Kind, e.Call, e.Action, e.Status))
		}
		parts = append(parts, fmt.Sprintf("%d of %d ledger entries have no row (%s)", n, len(j.Entries), strings.Join(items, ", ")))
	}
	if n := len(j.Problems); n > 0 {
		items := j.Problems
		if n > maxReasonItems {
			items = append(append([]string{}, items[:maxReasonItems]...), "...")
		}
		parts = append(parts, strings.Join(items, "; "))
	}
	return strings.Join(parts, "; ")
}

// joinLedger matches rows to the ledger entries of kind message and
// count_tokens for slot (docs/spike.md S5). Bifrost forwards no request id,
// so each row is anchored on its last attempt: the upstream message id in its
// raw response, or the fault nonce and call number in its error. Its earlier
// attempts are the nearest preceding unclaimed entries with the same
// sha256(raw_request), which equals the ledger's req_sha256 byte for byte.
// Rows with no anchor (count_tokens, errors without a nonce) take the oldest
// unclaimed entries with their request hash. Model listings, rejected
// credentials and other slots are not part of the join. It is pure.
func joinLedger(rows []*parsedRow, ledger []schemas.LedgerEntry, slot string) joinResult {
	j := joinResult{Rows: map[string][]int{}}
	for _, e := range ledger {
		if (e.Kind == kindMessage || e.Kind == kindCountTokens) && e.Action != "reject" && (slot == "" || e.Slot == "" || e.Slot == slot) {
			j.Entries = append(j.Entries, e)
		}
	}
	owner := make([]string, len(j.Entries))
	ordered := joinable(rows)
	type anchored struct {
		row *parsedRow
		at  int
	}
	var withAnchor []anchored
	var withoutAnchor []*parsedRow
	for _, r := range ordered {
		at, problem := j.anchor(r)
		switch {
		case problem != "":
			j.Problems = append(j.Problems, problem)
		case at < 0:
			withoutAnchor = append(withoutAnchor, r)
		case owner[at] != "":
			j.Problems = append(j.Problems, fmt.Sprintf("rows %s and %s match the same ledger entry", owner[at], r.Req.ID))
		default:
			owner[at] = r.Req.ID
			withAnchor = append(withAnchor, anchored{row: r, at: at})
		}
	}
	sort.SliceStable(withAnchor, func(a, b int) bool { return withAnchor[a].at < withAnchor[b].at })
	for _, a := range withAnchor {
		j.claimAnchored(a.row, a.at, owner)
	}
	for _, r := range withoutAnchor {
		j.claimByHash(r, owner)
	}
	for i, o := range owner {
		if o == "" {
			j.Unmatched = append(j.Unmatched, i)
		}
	}
	return j
}

// joinable returns the rows that must appear in the ledger, oldest first:
// message and count_tokens rows with at least one upstream attempt.
func joinable(rows []*parsedRow) []*parsedRow {
	var out []*parsedRow
	for _, r := range rows {
		if r != nil && r.Upstream && (r.Req.Kind == kindMessage || r.Req.Kind == kindCountTokens) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Req.Timestamp.Before(out[b].Req.Timestamp) })
	return out
}

// anchor returns the entry for the row's last attempt, -1 when the row has
// nothing to anchor on, or a problem when its anchor is ambiguous.
func (j *joinResult) anchor(r *parsedRow) (int, string) {
	var hits []int
	switch {
	case r.Req.ResponseID != "":
		for i, e := range j.Entries {
			if e.ResponseID == r.Req.ResponseID {
				hits = append(hits, i)
			}
		}
	case r.Nonce != "":
		for i, e := range j.Entries {
			if e.Nonce == r.Nonce && (r.NonceCall == 0 || e.Call == r.NonceCall) {
				hits = append(hits, i)
			}
		}
	default:
		return -1, ""
	}
	switch len(hits) {
	case 0:
		return -1, ""
	case 1:
		e := j.Entries[hits[0]]
		if r.ReqSHA256 != "" && e.ReqSHA256 != "" && e.ReqSHA256 != r.ReqSHA256 {
			return -1, fmt.Sprintf("row %s matches ledger call %d but its request hash differs", r.Req.ID, e.Call)
		}
		return hits[0], ""
	default:
		return -1, fmt.Sprintf("row %s matches %d ledger entries", r.Req.ID, len(hits))
	}
}

// claimAnchored gives the row its anchor plus one earlier entry with the same
// request hash per extra attempt.
func (j *joinResult) claimAnchored(r *parsedRow, at int, owner []string) {
	claimed := []int{at}
	hash := j.Entries[at].ReqSHA256
	for i := at - 1; i >= 0 && len(claimed) < attempts(r); i-- {
		if owner[i] == "" && j.Entries[i].ReqSHA256 == hash && j.Entries[i].Kind == j.Entries[at].Kind {
			owner[i] = r.Req.ID
			claimed = append(claimed, i)
		}
	}
	sort.Ints(claimed)
	j.Rows[r.Req.ID] = claimed
	if len(claimed) < attempts(r) {
		j.Problems = append(j.Problems, fmt.Sprintf("row %s has %d attempts but %d ledger entries", r.Req.ID, attempts(r), len(claimed)))
	}
}

// claimByHash gives an unanchored row the oldest unclaimed entries of its kind
// with its request hash, one per attempt.
func (j *joinResult) claimByHash(r *parsedRow, owner []string) {
	if r.ReqSHA256 == "" {
		j.Problems = append(j.Problems, fmt.Sprintf("row %s has no response id, nonce or stored request to join on", r.Req.ID))
		return
	}
	var claimed []int
	for i, e := range j.Entries {
		if len(claimed) == attempts(r) {
			break
		}
		if owner[i] == "" && e.ReqSHA256 == r.ReqSHA256 && e.Kind == r.Req.Kind {
			owner[i] = r.Req.ID
			claimed = append(claimed, i)
		}
	}
	j.Rows[r.Req.ID] = claimed
	switch {
	case len(claimed) == 0:
		j.Problems = append(j.Problems, fmt.Sprintf("row %s (%s) has no ledger entry", r.Req.ID, r.Req.Kind))
	case len(claimed) < attempts(r):
		j.Problems = append(j.Problems, fmt.Sprintf("row %s has %d attempts but %d ledger entries", r.Req.ID, attempts(r), len(claimed)))
	}
}

// attempts is the number of physical upstream tries of a row, at least one.
func attempts(r *parsedRow) int {
	if n := len(r.Req.Attempts); n > 0 {
		return n
	}
	return 1
}
