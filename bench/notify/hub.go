package notify

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/mail"
	"github.com/maximhq/bifrost/bench/webhooks"
)

// Member is one recipient candidate of an organization.
type Member struct {
	UserID   string
	Email    string
	Name     string
	Disabled bool
}

// Hub sends an organization's notification emails.
type Hub struct {
	Mail mail.Sender
	// PublicURL is the server's address (https://heimdall.example.com);
	// links in emails and chat messages are made absolute with it.
	PublicURL string
	// Members lists an organization's members; nil means this server has
	// no accounts (a local server) and sends no member email.
	Members func(ctx context.Context, orgID string) ([]Member, error)
}

var (
	defaultMu  sync.RWMutex
	defaultHub *Hub
)

// SetDefault installs the server's hub (nil removes it).
func SetDefault(h *Hub) {
	defaultMu.Lock()
	defaultHub = h
	defaultMu.Unlock()
}

// Default is the server's hub, nil when none is set.
func Default() *Hub {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultHub
}

// EmailAvailable reports whether member email can work on this server.
func (h *Hub) EmailAvailable() bool { return h != nil && h.Mail != nil && h.Members != nil }

// Absolute makes a server path absolute with PublicURL.
func (h *Hub) Absolute(link string) string {
	if h == nil || link == "" || !strings.HasPrefix(link, "/") || h.PublicURL == "" {
		return link
	}
	return strings.TrimRight(h.PublicURL, "/") + link
}

// settingsPath is the page where a member changes their email choice.
const settingsPath = "/settings?tab=integrations&section=notifications"

func (h *Hub) footer(string) string {
	return "You get this email because of your email settings in Heimdall. Change what you get at " + h.Absolute(settingsPath)
}

// EventEmail renders one event as an email.
func (h *Hub) EventEmail(orgID string, ev webhooks.Event) (mail.Template, string) {
	s := webhooks.Summary{Title: "Heimdall: " + ev.Type}
	if ev.Summary != nil {
		s = *ev.Summary
	}
	t := mail.Template{Heading: s.Title, Intro: s.Text, Action: "Open in Heimdall", Link: h.Absolute(s.Link), Footer: h.footer(orgID)}
	if len(s.Fields) > 0 {
		sec := mail.Section{}
		for _, f := range s.Fields {
			r := mail.Row{Label: f.Label, Value: f.Value}
			if f.Label == "Change" {
				switch {
				case strings.HasPrefix(f.Value, "-"):
					r.Tone = mail.ToneBad
				case strings.HasPrefix(f.Value, "+"):
					r.Tone = mail.ToneGood
				}
			}
			sec.Rows = append(sec.Rows, r)
		}
		t.Sections = append(t.Sections, sec)
	}
	subject := s.Title
	if len(subject) > 180 {
		subject = subject[:177] + "..."
	}
	return t, subject
}

// recipients are the enabled members whose choice includes want (an event
// type, or "" for the digest).
func (h *Hub) recipients(ctx context.Context, orgID string, st *Store, want string) ([]string, error) {
	ms, err := h.Members(ctx, orgID)
	if err != nil {
		return nil, err
	}
	prefs, err := st.AllPrefs()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range ms {
		if m.Disabled || m.Email == "" || mail.ValidAddress(m.Email) != nil {
			continue
		}
		p, ok := prefs[m.UserID]
		if !ok {
			p = DefaultPrefs()
		}
		if (want == "" && p.Digest) || (want != "" && slices.Contains(p.Events, want)) {
			out = append(out, m.Email)
		}
	}
	return out, nil
}

// EmailEvent sends ev to every member of orgID who chose it, one message
// per recipient (members never see each other's address). It returns how
// many were sent.
func (h *Hub) EmailEvent(ctx context.Context, orgID, root string, ev webhooks.Event) (int, error) {
	if !h.EmailAvailable() || !slices.Contains(EmailEvents, ev.Type) {
		return 0, nil
	}
	to, err := h.recipients(ctx, orgID, Open(root), ev.Type)
	if err != nil || len(to) == 0 {
		return 0, err
	}
	t, subject := h.EventEmail(orgID, ev)
	return h.sendEach(ctx, to, t, subject)
}

// sendEach sends one message per recipient, retrying each twice.
func (h *Hub) sendEach(ctx context.Context, to []string, t mail.Template, subject string) (int, error) {
	sent := 0
	var errs []error
	for _, addr := range to {
		m := t.Message([]string{addr}, subject)
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			if err = h.Mail.Send(ctx, m); err == nil || errors.Is(err, mail.ErrInvalid) {
				break
			}
			select {
			case <-ctx.Done():
				return sent, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", addr, err))
			continue
		}
		sent++
	}
	return sent, errors.Join(errs...)
}

// SendTest sends a test email to one address.
func (h *Hub) SendTest(ctx context.Context, orgID, to string) error {
	if h == nil || h.Mail == nil {
		return errors.New("this server has no email sender")
	}
	t := mail.Template{Heading: "Email from Heimdall works",
		Intro:  "This is the test you asked for. Evaluation results, regressions and the weekly digest you choose arrive at this address.",
		Action: "Open your email settings", Link: h.Absolute(settingsPath), Footer: h.footer(orgID)}
	return h.Mail.Send(ctx, t.Message([]string{to}, "Heimdall test email"))
}

// ---- weekly digest ----

// ISOWeek names t's ISO week, e.g. "2026-W47".
func ISOWeek(t time.Time) string {
	y, w := t.UTC().ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// DigestHour is the UTC hour on Monday after which the week's digest goes out.
const DigestHour = 9

// DigestDue reports whether this week's digest should go out at now,
// given the week the last one went out for.
func DigestDue(now time.Time, lastWeek string) bool {
	now = now.UTC()
	if ISOWeek(now) == lastWeek {
		return false
	}
	// Monday 09:00 UTC of now's week.
	off := (int(now.Weekday()) + 6) % 7 // days since Monday
	monday := time.Date(now.Year(), now.Month(), now.Day()-off, DigestHour, 0, 0, 0, time.UTC)
	return !now.Before(monday)
}

// agentLine is one agent's week in the digest.
type agentLine struct {
	name        string
	first, last *Record
	runs        int
	regressions int
}

// BuildDigest renders the digest of recs (the week's finished
// evaluations). ok is false when nothing ran: no digest is sent then.
func (h *Hub) BuildDigest(orgID string, recs []Record, from, to time.Time) (t mail.Template, subject string, ok bool) {
	if len(recs) == 0 {
		return t, "", false
	}
	agents := map[string]*agentLine{}
	var order []string
	done, regressed, blocked := 0, 0, 0
	spend, priced := 0.0, 0
	for i := range recs {
		r := &recs[i]
		if r.Status == "done" {
			done++
		}
		if r.Regressed {
			regressed++
		}
		if r.Blocked {
			blocked++
		}
		if r.CostUSD != nil {
			spend += *r.CostUSD
			priced++
		}
		key := r.Agent
		if key == "" {
			key = firstNonEmpty(r.Name, r.EvaluationID)
		}
		a, seen := agents[key]
		if !seen {
			a = &agentLine{name: key}
			agents[key] = a
			order = append(order, key)
		}
		a.runs++
		if r.Regressed {
			a.regressions++
		}
		if r.Score != nil {
			if a.first == nil {
				a.first = r
			}
			a.last = r
		}
	}
	t = mail.Template{
		Heading: "Your week in Heimdall",
		Intro: fmt.Sprintf("%s to %s: %d %s finished, %d %s.", from.UTC().Format("Jan 2"), to.UTC().Format("Jan 2"),
			len(recs), plural(len(recs), "evaluation", "evaluations"), regressed, plural(regressed, "regression", "regressions")),
		Action: "Open evaluations", Link: h.Absolute("/evaluations"), Footer: h.footer(orgID) + " (weekly digest)",
	}
	sec := mail.Section{Title: "Agents"}
	for _, k := range order {
		a := agents[k]
		label := a.name
		val := fmt.Sprintf("%d %s, no score", a.runs, plural(a.runs, "evaluation", "evaluations"))
		r := mail.Row{Label: label}
		if a.last != nil {
			if a.last.Version > 0 {
				r.Label = fmt.Sprintf("%s v%d", a.name, a.last.Version)
			}
			val = pct(*a.last.Score)
			if a.first != a.last && a.first != nil {
				d := *a.last.Score - *a.first.Score
				val += " (" + points(d) + " this week)"
				r.Tone = tone(d)
			} else if a.last.Change != nil {
				val += " (" + points(*a.last.Change) + " vs baseline)"
				r.Tone = tone(*a.last.Change)
			}
		}
		if a.regressions > 0 {
			val += fmt.Sprintf(", %d %s", a.regressions, plural(a.regressions, "regression", "regressions"))
			r.Tone = mail.ToneBad
		}
		r.Value = val
		sec.Rows = append(sec.Rows, r)
	}
	week := mail.Section{Title: "This week", Rows: []mail.Row{
		{Label: "Evaluations finished", Value: fmt.Sprintf("%d of %d", done, len(recs))},
		{Label: "Regressions", Value: fmt.Sprint(regressed), Tone: map[bool]string{true: mail.ToneBad}[regressed > 0]},
		{Label: "Releases blocked", Value: fmt.Sprint(blocked)},
	}}
	switch {
	case priced == len(recs):
		week.Rows = append(week.Rows, mail.Row{Label: "Provider spend", Value: fmt.Sprintf("$%.2f", spend)})
	case priced > 0:
		week.Rows = append(week.Rows, mail.Row{Label: "Provider spend", Value: fmt.Sprintf("$%.2f (%d of %d evaluations priced)", spend, priced, len(recs))})
	default:
		week.Rows = append(week.Rows, mail.Row{Label: "Provider spend", Value: "not known (unpriced models)"})
	}
	t.Sections = []mail.Section{sec, week}
	subject = fmt.Sprintf("Heimdall weekly: %d %s, %d %s", len(recs), plural(len(recs), "evaluation", "evaluations"), regressed, plural(regressed, "regression", "regressions"))
	return t, subject, true
}

// SendDigest sends the week's digest of orgID when it is due and records
// it. It returns how many emails went out.
func (h *Hub) SendDigest(ctx context.Context, orgID, root string, now time.Time) (int, error) {
	if !h.EmailAvailable() {
		return 0, nil
	}
	st := Open(root)
	last, err := st.DigestWeek()
	if err != nil {
		return 0, err
	}
	if !DigestDue(now, last) {
		return 0, nil
	}
	week := ISOWeek(now)
	from := now.Add(-7 * 24 * time.Hour)
	recs, err := st.History(from)
	if err != nil {
		return 0, err
	}
	t, subject, ok := h.BuildDigest(orgID, recs, from, now)
	if !ok {
		return 0, st.SetDigestWeek(week, 0, now)
	}
	to, err := h.recipients(ctx, orgID, st, "")
	if err != nil {
		return 0, err
	}
	sent, serr := h.sendEach(ctx, to, t, subject)
	// The week is marked even when some sends failed, so a broken address
	// does not resend the digest to everyone else every hour.
	if err := st.SetDigestWeek(week, sent, now); err != nil {
		return sent, err
	}
	return sent, serr
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", math.Round(v*100)) }

func points(d float64) string {
	p := math.Round(d*1000) / 10
	if p == 0 {
		return "0 points"
	}
	s := strings.TrimSuffix(fmt.Sprintf("%+.1f", p), ".0")
	if math.Abs(p) == 1 {
		return s + " point"
	}
	return s + " points"
}

func tone(d float64) string {
	switch {
	case d < -0.0005:
		return mail.ToneBad
	case d > 0.0005:
		return mail.ToneGood
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
