package livescore

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"time"
)

// Session reconstruction. Bifrost logs requests, not sessions, so rows are
// grouped here:
//
//   - A row with a session_id (the x-bf-session-id header, or a harness
//     session header Bifrost reads: Claude Code, OpenCode, Codex) belongs to
//     that session. This is as reliable as the client: two conversations
//     sent under one id are one session, and an agent that sends no id
//     falls to the next rule.
//   - Otherwise rows of the same virtual key and user follow each other in
//     one session until a gap longer than sessions.gap. This is a heuristic:
//     two conversations running at once on one key are merged (the session
//     is marked concurrent when its requests overlap in time, the visible
//     sign of that), and one conversation with a long pause is split.
//
// Either way a session is scored once it has been idle for the gap, so a
// session still running is never scored half way; a later request with the
// same session id after that starts a new scored segment. Status reports
// the share of each grouping and of concurrent sessions, so the reader can
// see how much of the score rests on the heuristic.

// overlapTolerance is how far a request may start before the previous one
// ended and still count as sequential. Bifrost stamps a row and measures its
// latency at slightly different points, so back-to-back requests of one
// conversation overlap by a fraction of a millisecond on a real v2.5.1
// stack (measured 18 Nov 2026); two conversations at once overlap by whole
// model calls.
const overlapTolerance = 250 * time.Millisecond

// rawSession is a group of rows before scoring.
type rawSession struct {
	key        string // grouping key: sid:<id> or gap:<vk>|<user>|<first row>
	grouping   string
	rows       []Row
	concurrent bool
	processing bool
	cut        bool
}

// lastActivity is when the session's last request ended, as far as the
// rows say.
func (s *rawSession) lastActivity() time.Time {
	last := s.rows[len(s.rows)-1]
	end := last.Timestamp
	if last.Latency != nil && *last.Latency > 0 {
		end = end.Add(time.Duration(*last.Latency * float64(time.Millisecond)))
	}
	return end
}

// closed reports whether the session can be scored at now: idle for gap
// and no row still processing.
func (s *rawSession) closed(now time.Time, gap time.Duration) bool {
	return !s.processing && !s.lastActivity().Add(gap).After(now)
}

// group splits one version's rows (any order) into sessions, oldest first.
func group(rows []Row, gap time.Duration) []*rawSession {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b Row) int { return a.Timestamp.Compare(b.Timestamp) })
	byID := map[string]*rawSession{}
	open := map[string]*rawSession{} // gap sessions by vk|user, the current one
	var out []*rawSession
	for _, r := range sorted {
		var s *rawSession
		if sid := deref(r.SessionID); sid != "" {
			s = byID[sid]
			if s != nil && r.Timestamp.Sub(s.lastActivity()) > gap {
				s = nil // the same id after an idle gap: a new segment
			}
			if s == nil {
				s = &rawSession{key: "sid:" + sid, grouping: GroupSessionID}
				byID[sid] = s
				out = append(out, s)
			}
		} else {
			who := deref(r.VirtualKeyID) + "|" + deref(r.UserID)
			s = open[who]
			if s != nil && r.Timestamp.Sub(s.lastActivity()) > gap {
				s = nil
			}
			if s == nil {
				s = &rawSession{key: "gap:" + who + "|" + r.ID, grouping: GroupGap}
				open[who] = s
				out = append(out, s)
			} else if r.Timestamp.Before(s.lastActivity().Add(-overlapTolerance)) {
				s.concurrent = true
			}
		}
		if len(s.rows) >= MaxRowsPerSession {
			s.cut = true
			continue
		}
		s.rows = append(s.rows, r)
		if r.Status == statusProcessing {
			s.processing = true
		}
	}
	return out
}

// sessionID is the stored id of a session: a hash of the version and the
// grouping key, so a session id the client sent is never stored as is.
func sessionID(version, key, firstRow string) string {
	sum := sha256.Sum256([]byte(version + "\x1f" + key + "\x1f" + firstRow))
	return "ls_" + hex.EncodeToString(sum[:8])
}

// sampled reports whether a session is in the sample: a hash of the
// version and key below rate, so the choice does not depend on anything the
// session did.
func sampled(version, key string, rate float64) bool {
	if rate >= 1 {
		return true
	}
	sum := sha256.Sum256([]byte("sample\x1f" + version + "\x1f" + key))
	u := binary.BigEndian.Uint64(sum[:8])
	return float64(u)/float64(^uint64(0)) < rate
}
