package livescore

import (
	"errors"
	"net/url"
	"slices"
	"sort"
	"time"
)

// Status is GET /livescore and `livescore status`: what is read, how far
// pulls got, every version's signals over time, the comparison, and the
// worst sessions with their inbox items.
type Status struct {
	Evidence   string          `json:"evidence"` // live
	Note       string          `json:"note"`     // why live scores are apart from experiment verdicts
	Configured bool            `json:"configured"`
	Problem    string          `json:"problem,omitempty"` // a config that did not load
	Source     *SourceView     `json:"source,omitempty"`
	Versions   []VersionView   `json:"versions"`
	Signals    []SignalDef     `json:"signals"`
	Grouping   GroupingView    `json:"grouping"`
	Compare    *Comparison     `json:"compare,omitempty"`
	Worst      []SessionRecord `json:"worst"`
	Events     []LogEvent      `json:"events"`
	Privacy    PrivacyView     `json:"privacy"`
	Judge      *JudgeView      `json:"judge,omitempty"`
}

// SourceView is the gateway read, with no credential.
type SourceView struct {
	URL        string     `json:"url"`
	TokenEnv   string     `json:"token_env,omitempty"` // the variable's name; its value is never shown or stored
	ReadOnly   bool       `json:"read_only"`           // always true: only GET requests leave
	LastPull   *time.Time `json:"last_pull,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
	LastScored int        `json:"last_scored"`
	Warnings   []string   `json:"warnings,omitempty"`
	Schedule   string     `json:"schedule,omitempty"`
}

// VersionView is one version: its selector, its counts, its signals over
// the last 24 hours and over time.
type VersionView struct {
	Name     string         `json:"name"`
	Selector string         `json:"selector"` // the selector in words
	Sessions int            `json:"sessions"` // scored sessions kept
	Flagged  int            `json:"flagged"`
	Last24h  []SignalMean   `json:"last_24h"`
	Series   []SeriesBucket `json:"series"`
}

// SignalMean is a signal's mean over some sessions.
type SignalMean struct {
	Signal string  `json:"signal"`
	Mean   float64 `json:"mean"`
	N      int     `json:"n"`
}

// SeriesBucket is one time bucket of a version's signals.
type SeriesBucket struct {
	Start    time.Time    `json:"start"`
	Sessions int          `json:"sessions"`
	Means    []SignalMean `json:"means"`
}

// GroupingView says how sessions were reconstructed, so the reader can
// judge how much rests on the gap heuristic.
type GroupingView struct {
	BySessionID int    `json:"by_session_id"`
	ByGap       int    `json:"by_gap"`
	Concurrent  int    `json:"concurrent"` // gap sessions whose requests overlapped: likely two conversations merged
	GapSeconds  int    `json:"gap_seconds"`
	Reliability string `json:"reliability"`
}

// PrivacyView states what is kept.
type PrivacyView struct {
	SampleRate     float64 `json:"sample_rate"`
	RetentionDays  int     `json:"retention_days"`
	StoresText     bool    `json:"stores_text"`    // always false for livescore's own files
	InboxRedacted  bool    `json:"inbox_redacted"` // always true: pushed transcripts go through the inbox redactor
	InboxPush      bool    `json:"inbox_push"`     // whether failing sessions are pushed
	InboxMaxPerRun int     `json:"inbox_max_per_pull"`
}

// JudgeView is the optional judge.
type JudgeView struct {
	Model      string  `json:"model"`
	SampleRate float64 `json:"sample_rate"`
	Calibrated bool    `json:"calibrated"`
	Note       string  `json:"note"`
}

// StatusNote is why live scores are a separate evidence class.
const StatusNote = "Live evidence, kept apart from experiment verdicts: production traffic has no ground truth (nobody checked whether an answer was right) " +
	"and no control of inputs (each version saw different users asking different things), so a live score says how a version behaved on the traffic it got, " +
	"never that it passes or fails tasks."

// Status builds the status view. now bounds the last-24-hours means.
func (s *Scorer) Status(now time.Time) (*Status, error) {
	out := &Status{Evidence: EvidenceClass, Note: StatusNote, Configured: s.Config != nil, Versions: []VersionView{},
		Signals: SignalDefs(), Worst: []SessionRecord{}, Events: []LogEvent{}}
	if s.Config == nil {
		return out, nil
	}
	cfg := s.Config
	state, err := s.Store.State()
	if err != nil {
		return nil, err
	}
	all, err := s.Store.Sessions()
	if err != nil {
		return nil, err
	}
	src := &SourceView{URL: redactURL(s.SourceURL()), TokenEnv: s.tokenEnv(), ReadOnly: true, LastError: state.Cursor.LastError,
		LastScored: state.Cursor.LastScored, Warnings: state.Cursor.Warnings, Schedule: cfg.Schedule}
	if !state.Cursor.LastPull.IsZero() {
		t := state.Cursor.LastPull
		src.LastPull = &t
	}
	if !state.Cursor.Since.IsZero() {
		t := state.Cursor.Since
		src.Since = &t
	}
	out.Source = src
	for _, v := range cfg.Versions {
		vv := VersionView{Name: v.Name, Selector: v.describe(), Last24h: []SignalMean{}, Series: []SeriesBucket{}}
		for i := range all {
			if all[i].Version == v.Name {
				vv.Sessions++
				if isFailure(all[i].Flags) {
					vv.Flagged++
				}
			}
		}
		for _, d := range signalDefs {
			if m, n := windowMean(all, v.Name, d, now.Add(-24*time.Hour), now); n > 0 {
				vv.Last24h = append(vv.Last24h, SignalMean{Signal: d.Name, Mean: m, N: n})
			}
		}
		vv.Series = series(all, v.Name)
		out.Versions = append(out.Versions, vv)
	}
	g := GroupingView{GapSeconds: int(cfg.gap() / time.Second)}
	for i := range all {
		switch all[i].Grouping {
		case GroupSessionID:
			g.BySessionID++
		case GroupGap:
			g.ByGap++
			if all[i].Concurrent {
				g.Concurrent++
			}
		}
	}
	g.Reliability = groupingReliability(g)
	out.Grouping = g
	if state.Compare != nil {
		out.Compare = state.Compare.View(cfg)
	}
	out.Worst = Worst(all, 20)
	if ev, err := s.Store.Events(30); err == nil && ev != nil {
		out.Events = ev
	}
	out.Privacy = PrivacyView{SampleRate: cfg.sampleRate(), RetentionDays: int(cfg.retention() / (24 * time.Hour)), StoresText: false,
		InboxRedacted: true, InboxPush: !cfg.Inbox.Disabled, InboxMaxPerRun: cfg.inboxMax()}
	if j := cfg.Judge; j != nil {
		jv := &JudgeView{Model: j.Model, SampleRate: j.SampleRate, Note: "no judge client is configured in this process"}
		if cj, ok := s.Judge.(*ChatJudge); ok {
			jv.Calibrated, jv.Note = cj.Calibrated, cj.CalibrationNote
		}
		out.Judge = jv
	}
	return out, nil
}

// groupingReliability says in words how far the sessions can be trusted.
func groupingReliability(g GroupingView) string {
	total := g.BySessionID + g.ByGap
	switch {
	case total == 0:
		return "no sessions scored yet"
	case g.ByGap == 0:
		return "every session came with a session id from the client: grouping is as reliable as the client's ids"
	case g.Concurrent*10 > g.ByGap:
		return "many sessions were grouped by the idle-gap heuristic and their requests overlap, a sign that conversations running at once on one key were merged; send x-bf-session-id from the agent"
	default:
		return "some sessions were grouped by the idle-gap heuristic (same key and user, no gap longer than sessions.gap); conversations running at once on one key would be merged; send x-bf-session-id from the agent for exact sessions"
	}
}

// Worst returns the n flagged sessions with the highest severity, newest
// first among equals.
func Worst(all []SessionRecord, n int) []SessionRecord {
	var out []SessionRecord
	for _, r := range all {
		if isFailure(r.Flags) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		return out[i].Ended.After(out[j].Ended)
	})
	if len(out) > n {
		out = out[:n]
	}
	if out == nil {
		out = []SessionRecord{}
	}
	return out
}

// series buckets a version's sessions by hour when they span three days or
// less, else by day, at most 200 buckets (the newest).
func series(all []SessionRecord, version string) []SeriesBucket {
	var mine []SessionRecord
	for _, r := range all {
		if r.Version == version {
			mine = append(mine, r)
		}
	}
	if len(mine) == 0 {
		return []SeriesBucket{}
	}
	lo, hi := mine[0].Ended, mine[0].Ended
	for _, r := range mine {
		lo, hi = minTime(lo, r.Ended), maxTime(hi, r.Ended)
	}
	step := time.Hour
	if hi.Sub(lo) > 72*time.Hour {
		step = 24 * time.Hour
	}
	buckets := map[time.Time][]SessionRecord{}
	for _, r := range mine {
		k := r.Ended.Truncate(step)
		buckets[k] = append(buckets[k], r)
	}
	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b time.Time) int { return a.Compare(b) })
	if len(keys) > 200 {
		keys = keys[len(keys)-200:]
	}
	out := make([]SeriesBucket, 0, len(keys))
	for _, k := range keys {
		rs := buckets[k]
		b := SeriesBucket{Start: k, Sessions: len(rs), Means: []SignalMean{}}
		for _, d := range signalDefs {
			if m, n := windowMean(rs, version, d, k, k.Add(step)); n > 0 {
				b.Means = append(b.Means, SignalMean{Signal: d.Name, Mean: m, N: n})
			}
		}
		out = append(out, b)
	}
	return out
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// describe renders a selector in words.
func (v *Version) describe() string {
	var parts []string
	add := func(name string, xs []string) {
		if len(xs) > 0 {
			parts = append(parts, name+" "+joinComma(xs))
		}
	}
	add("virtual key", v.VirtualKeys)
	add("team", v.Teams)
	add("customer", v.Customers)
	add("model", v.Models)
	if v.Tag != nil {
		parts = append(parts, "header "+v.Tag.Header+" = "+v.Tag.Value+" (metadata "+v.Tag.MetadataKey()+")")
	}
	return joinWith(parts, " and ")
}

func joinComma(xs []string) string { return joinWith(xs, ", ") }

func joinWith(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}

// redactURL drops any user info and query from a URL for display.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User, u.RawQuery = nil, ""
	return u.String()
}

// SessionFilter narrows GET /livescore/sessions.
type SessionFilter struct {
	Version string
	Flag    string
	Limit   int
}

// SessionList is GET /livescore/sessions.
type SessionList struct {
	Evidence string          `json:"evidence"`
	Total    int             `json:"total"`
	Sessions []SessionRecord `json:"sessions"`
}

// ErrBadFilter marks a filter naming an unknown version or flag.
var ErrBadFilter = errors.New("bad filter")

// Sessions lists scored sessions, newest first.
func (s *Scorer) Sessions(f SessionFilter) (*SessionList, error) {
	if f.Version != "" {
		if _, ok := s.Config.Find(f.Version); !ok {
			return nil, errors.Join(ErrBadFilter, errors.New("version "+f.Version+" is not configured"))
		}
	}
	all, err := s.Store.Sessions()
	if err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	out := &SessionList{Evidence: EvidenceClass, Sessions: []SessionRecord{}}
	for i := len(all) - 1; i >= 0; i-- {
		r := all[i]
		if (f.Version != "" && r.Version != f.Version) || (f.Flag != "" && !slices.Contains(r.Flags, f.Flag)) {
			continue
		}
		out.Total++
		if len(out.Sessions) < f.Limit {
			out.Sessions = append(out.Sessions, r)
		}
	}
	return out, nil
}

// Compare is GET /livescore/compare: the running comparison, or nil when
// none is configured or none has started.
func (s *Scorer) Compare() (*Comparison, error) {
	state, err := s.Store.State()
	if err != nil {
		return nil, err
	}
	if state.Compare == nil {
		return nil, nil
	}
	return state.Compare.View(s.Config), nil
}
