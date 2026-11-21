package livescore

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// Alert events live scoring sends.
const (
	EventThreshold = alerts.EventLiveThreshold
	EventWorse     = alerts.EventLiveWorse
)

// Notifier delivers alerts; *alerts.Sender implements it.
type Notifier interface {
	Send(ctx context.Context, cfgs []alerts.Config, p alerts.Payload) error
}

// Scorer is live scoring for one project.
type Scorer struct {
	Config *Config
	Store  *Store
	// Inbox receives the worst failing sessions; nil, or inbox.disabled,
	// pushes nothing.
	Inbox traceinbox.Store
	// URL and TokenEnv fill source.url and source.token_env when the config
	// leaves them empty (the bench config's gateway.url and admin_token_env).
	URL      string
	TokenEnv string
	HTTP     *http.Client // wrapped read-only
	Getenv   func(string) string
	Now      func() time.Time
	Notifier Notifier
	// ExtraAlerts are receivers from the bench config, added to the
	// config's own.
	ExtraAlerts []alerts.Config
	// Judge scores a sample of sessions; nil when none is configured.
	Judge Judge
}

func (s *Scorer) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Scorer) getenv(k string) string {
	if s.Getenv != nil {
		return s.Getenv(k)
	}
	return os.Getenv(k)
}

// SourceURL is the gateway read.
func (s *Scorer) SourceURL() string { return firstNonEmpty(s.Config.Source.URL, s.URL) }

// UIURL is the base of links to Bifrost's logs page.
func (s *Scorer) UIURL() string { return firstNonEmpty(s.Config.Source.UIURL, s.SourceURL()) }

func (s *Scorer) tokenEnv() string { return firstNonEmpty(s.Config.Source.TokenEnv, s.TokenEnv) }

// PullResult is what one pull did.
type PullResult struct {
	At        time.Time      `json:"at"`
	Window    [2]time.Time   `json:"window"`
	Rows      map[string]int `json:"rows"`     // list rows read, per version
	Sessions  map[string]int `json:"sessions"` // sessions scored, per version
	Skipped   int            `json:"skipped"`  // closed sessions outside the sample
	Open      int            `json:"open"`     // sessions still running, left for the next pull
	Details   int            `json:"details"`  // detail rows fetched
	Unchecked int            `json:"unchecked,omitempty"`
	Overlap   int            `json:"overlap,omitempty"` // rows that matched more than one version, kept in the first
	Inbox     []string       `json:"inbox"`             // inbox items touched
	Alerts    []string       `json:"alerts"`            // alerts sent (event: subject)
	Decided   []string       `json:"decided"`           // comparison signals that decided in this pull
	Pruned    int            `json:"pruned,omitempty"`
	Truncated bool           `json:"truncated"`
	Warnings  []string       `json:"warnings,omitempty"`
	Judged    int            `json:"judged,omitempty"`
	// JudgeAttempts counts judge requests sent this pull, scored or not;
	// max_per_pull caps it, so unusable answers cannot exceed the limit.
	JudgeAttempts int               `json:"judge_attempts,omitempty"`
	Errors        map[string]string `json:"errors,omitempty"` // per version, when a version's read failed
}

// Pull reads the gateway once: every version's rows since the cursor, the
// sessions that closed, their signals; then the inbox push, the
// comparison, thresholds and alerts, and retention. It holds the store lock
// for the whole pull, so two pulls never interleave.
func (s *Scorer) Pull(ctx context.Context) (*PullResult, error) {
	if s.Config == nil || s.Store == nil {
		return nil, ErrNotConfigured
	}
	base := s.SourceURL()
	if base == "" {
		return nil, errors.New("no gateway URL: set source.url in livescore/config.yaml or gateway.url in the bench config")
	}
	token := ""
	if env := s.tokenEnv(); env != "" {
		if token = s.getenv(env); token == "" {
			return nil, fmt.Errorf("the read credential variable %s is empty; export it (its value is never stored)", env)
		}
	}
	unlock, err := s.Store.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err := s.Store.State()
	if err != nil {
		return nil, err
	}
	// A pull that stopped after appending its sessions but before saving
	// the state left records the cursor does not know about. Skip those
	// session ids and continue the sequence after the highest recorded one,
	// so a retry never appends a session twice or reuses a sequence number.
	prior, err := s.Store.Sessions()
	if err != nil {
		return nil, err
	}
	recorded := make(map[string]bool, len(prior))
	for _, r := range prior {
		recorded[r.ID] = true
		state.Seq = max(state.Seq, r.Seq)
	}
	now := s.now()
	cl := newClient(base, token, s.HTTP)
	cfg := s.Config
	gap := cfg.gap()
	from := now.Add(-cfg.window())
	if !state.Cursor.Since.IsZero() {
		from = state.Cursor.Since.Add(-cfg.overlap())
	}
	res := &PullResult{At: now, Window: [2]time.Time{from, now}, Rows: map[string]int{}, Sessions: map[string]int{},
		Inbox: []string{}, Alerts: []string{}, Decided: []string{}}
	scored := maps.Clone(state.Cursor.Scored)
	if scored == nil {
		scored = map[string]time.Time{}
	}
	// next is where the next pull's window starts (before the overlap):
	// every row before it belongs to a scored or skipped session.
	next := now.Add(-gap)
	owner := map[string]string{} // row id -> version that took it in this pull
	var recs []SessionRecord
	var cands []*candidate
	details := 0
	failed := 0
	for vi := range cfg.Versions {
		v := &cfg.Versions[vi]
		rows, trunc, err := cl.listRows(ctx, v, from, now, func(id string) bool {
			_, done := scored[id]
			return done
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if res.Errors == nil {
				res.Errors = map[string]string{}
			}
			res.Errors[v.Name] = oneLine(err.Error(), 300)
			failed++
			next = minTime(next, from.Add(cfg.overlap())) // read this version's window again next time
			continue
		}
		res.Rows[v.Name] = len(rows)
		if trunc {
			res.Truncated = true
			res.Warnings = append(res.Warnings, fmt.Sprintf("version %s: %d rows, the per-pull cap; pull again for the rest", v.Name, len(rows)))
			if len(rows) > 0 {
				next = minTime(next, rows[len(rows)-1].Timestamp.Add(-gap))
			}
		}
		kept := rows[:0:0]
		for _, r := range rows {
			ok, unchecked := v.matches(&r)
			if !ok {
				continue
			}
			if unchecked {
				res.Unchecked++
			}
			if _, done := scored[r.ID]; done {
				continue
			}
			if prev, taken := owner[r.ID]; taken && prev != v.Name {
				res.Overlap++
				continue
			}
			owner[r.ID] = v.Name
			kept = append(kept, r)
		}
		for _, sess := range group(kept, gap) {
			// A session open for longer than the window (a client that
			// reuses one session id forever) is scored as it stands and
			// marked cut, so it never holds the cursor back for good.
			stale := sess.rows[0].Timestamp.Before(now.Add(-cfg.window()))
			if stale {
				sess.cut = true
			}
			if !stale && !sess.closed(now, gap) {
				res.Open++
				next = minTime(next, sess.rows[0].Timestamp)
				continue
			}
			if recorded[sessionID(v.Name, sess.key, sess.rows[0].ID)] {
				for _, r := range sess.rows {
					scored[r.ID] = r.Timestamp
				}
				continue
			}
			if !sampled(v.Name, sess.key, cfg.sampleRate()) {
				res.Skipped++
				for _, r := range sess.rows {
					scored[r.ID] = r.Timestamp
				}
				continue
			}
			// The list rows carry every number; a detail row is fetched only
			// where it adds something: an error's type and status, and the
			// last request's conversation (its input history and output
			// hold the whole session's transcript).
			need := 0
			for i, r := range sess.rows {
				if needsDetail(r, i == len(sess.rows)-1) {
					need++
				}
			}
			if details+need > MaxDetailsPerPull {
				res.Truncated = true
				next = minTime(next, sess.rows[0].Timestamp)
				continue
			}
			full := make([]Row, 0, len(sess.rows))
			hidden := false
			for i, r := range sess.rows {
				if !needsDetail(r, i == len(sess.rows)-1) {
					if r.ContentHidden {
						hidden = true
					}
					full = append(full, r)
					continue
				}
				d, err := cl.detail(ctx, r.ID)
				details++
				if err != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					res.Warnings = append(res.Warnings, fmt.Sprintf("row %s: %v", r.ID, oneLine(err.Error(), 200)))
					d = &r // the list row still carries the numbers
					hidden = true
				}
				if d.ContentHidden {
					hidden = true
				}
				full = append(full, *d)
			}
			rec, cand := s.score(v.Name, sess, full, hidden, now)
			state.Seq++
			rec.Seq = state.Seq
			if s.Judge != nil && cfg.Judge != nil && res.JudgeAttempts < judgeMax(cfg.Judge) && sampled("judge:"+v.Name, sess.key, cfg.Judge.SampleRate) && cand != nil {
				res.JudgeAttempts++ // debited before the call: a sent request that fails or is unusable still counts against max_per_pull
				if score, calibrated, err := s.Judge.Score(ctx, cand.session); err == nil {
					rec.Signals.JudgeScore, rec.Signals.JudgeCalibrated = &score, calibrated
					res.Judged++
				} else {
					res.Warnings = append(res.Warnings, "judge: "+oneLine(err.Error(), 200))
				}
			}
			recs = append(recs, rec)
			if cand != nil && isFailure(rec.Flags) {
				cand.rec = &recs[len(recs)-1]
				cands = append(cands, cand)
			}
			res.Sessions[v.Name]++
			for _, r := range sess.rows {
				scored[r.ID] = r.Timestamp
			}
		}
	}
	res.Details = details
	if failed == len(cfg.Versions) {
		state.Cursor.LastPull, state.Cursor.LastError = now, "every version failed: "+firstError(res.Errors)
		_ = s.Store.saveState(state)
		_ = s.Store.Log(LogEvent{At: now, Event: "pull", Detail: "error: " + state.Cursor.LastError})
		return res, errors.New(state.Cursor.LastError)
	}
	// Candidates point into recs; recs is final now, so re-point them.
	for _, c := range cands {
		for i := range recs {
			if recs[i].ID == c.rec.ID && recs[i].Seq == c.rec.Seq {
				c.rec = &recs[i]
			}
		}
	}
	if s.Inbox != nil && !cfg.Inbox.Disabled {
		touched, err := pushInbox(s.Inbox, cands, cfg.inboxMax(), cfg.Inbox.Suite, s.UIURL(), now)
		res.Inbox = append(res.Inbox, touched...)
		if err != nil {
			res.Warnings = append(res.Warnings, "inbox: "+oneLine(err.Error(), 200))
		}
	}
	if err := s.Store.appendSessions(recs); err != nil {
		return nil, fmt.Errorf("save scored sessions: %w", err)
	}
	// The cursor: drop scored ids that fall before the next window.
	keepFrom := next.Add(-cfg.overlap() - time.Hour)
	for id, at := range scored {
		if at.Before(keepFrom) {
			delete(scored, id)
		}
	}
	state.Cursor = Cursor{Since: next, Scored: scored, LastPull: now, LastScored: len(recs), Warnings: capList(res.Warnings, 20)}
	if len(res.Errors) > 0 {
		state.Cursor.LastError = "some versions failed: " + firstError(res.Errors)
	}
	all, err := s.Store.Sessions()
	if err != nil {
		return nil, err
	}
	res.Decided = s.updateCompare(state, all, now)
	res.Alerts = s.checkAlerts(ctx, state, all, now, res)
	if n, err := s.Store.prune(now.Add(-cfg.retention())); err == nil {
		res.Pruned = n
	} else {
		res.Warnings = append(res.Warnings, "retention: "+err.Error())
	}
	if err := s.Store.saveState(state); err != nil {
		return nil, err
	}
	total := 0
	for _, n := range res.Sessions {
		total += n
	}
	_ = s.Store.Log(LogEvent{At: now, Event: "pull", Detail: fmt.Sprintf("scored %d sessions, skipped %d by sampling, %d open, %d detail rows, inbox %d items",
		total, res.Skipped, res.Open, details, len(res.Inbox))})
	for _, d := range res.Decided {
		_ = s.Store.Log(LogEvent{At: now, Event: "decision", Detail: d})
	}
	return res, nil
}

// score measures one closed session. The rows and the transcript are
// dropped by the caller; only the record (and a redacted candidate for a
// failing session) leaves.
func (s *Scorer) score(version string, sess *rawSession, rows []Row, hidden bool, now time.Time) (SessionRecord, *candidate) {
	first, last := rows[0], rows[len(rows)-1]
	id := sessionID(version, sess.key, first.ID)
	tr := transcriptOf(id, rows)
	sig, flags := measure(rows, tr, hidden || tr == nil)
	if sess.cut {
		flags = append(flags, FlagCut)
	}
	rec := SessionRecord{ID: id, Version: version, Evidence: EvidenceClass, Grouping: sess.grouping, Concurrent: sess.concurrent,
		Started: first.Timestamp.UTC(), Ended: sess.lastActivity().UTC(), ScoredAt: now, Requests: len(rows),
		Model: last.Provider + "/" + last.Model, LastRow: last.ID, Signals: sig, Flags: flags, Severity: severityOf(flags)}
	if !isFailure(flags) && (s.Judge == nil || s.Config.Judge == nil) {
		return rec, nil
	}
	return rec, newCandidate(&rec, tr, rows)
}

// updateCompare starts, continues or reports the comparison. A
// comparison whose settings changed is not continued (status says so).
func (s *Scorer) updateCompare(state *State, all []SessionRecord, now time.Time) []string {
	if s.Config.Compare == nil {
		return nil
	}
	if state.Compare == nil {
		state.Compare = newCompare(s.Config, 0, now, 0)
	}
	if state.Compare.SettingsHash != s.Config.settingsHash() {
		return nil
	}
	return state.Compare.consume(all, now)
}

// RestartCompare starts a new comparison from now on: only sessions scored
// after the restart are consumed, so the old data is never tested twice.
// The restart count is kept and shown.
func (s *Scorer) RestartCompare() (*Comparison, error) {
	if s.Config.Compare == nil {
		return nil, errors.New("no compare section in livescore/config.yaml")
	}
	unlock, err := s.Store.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err := s.Store.State()
	if err != nil {
		return nil, err
	}
	// Reconcile as Pull does: a pull that appended sessions and stopped
	// before saving the state left durable records past state.Seq, and they
	// were scored before this restart.
	prior, err := s.Store.Sessions()
	if err != nil {
		return nil, err
	}
	for _, r := range prior {
		state.Seq = max(state.Seq, r.Seq)
	}
	restarts := 0
	if state.Compare != nil {
		restarts = state.Compare.Restarts + 1
	}
	now := s.now()
	state.Compare = newCompare(s.Config, state.Seq, now, restarts)
	if err := s.Store.saveState(state); err != nil {
		return nil, err
	}
	_ = s.Store.Log(LogEvent{At: now, Event: "restart", Detail: fmt.Sprintf("comparison %s vs %s restarted (restart %d)", state.Compare.Candidate, state.Compare.Baseline, restarts)})
	return state.Compare.View(s.Config), nil
}

// needsDetail reports whether a row's detail is fetched: an error row (the
// list row may lack its error type and status) and the session's last row
// (the transcript).
func needsDetail(r Row, last bool) bool {
	return last || r.Status == statusError
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func firstError(m map[string]string) string {
	keys := slices.Sorted(maps.Keys(m))
	if len(keys) == 0 {
		return ""
	}
	return keys[0] + ": " + m[keys[0]]
}

func capList(xs []string, n int) []string {
	if len(xs) > n {
		return append(slices.Clone(xs[:n]), fmt.Sprintf("and %d more", len(xs)-n))
	}
	return xs
}

func judgeMax(j *JudgeConfig) int {
	if j.MaxPerPull > 0 {
		return j.MaxPerPull
	}
	return 20
}
