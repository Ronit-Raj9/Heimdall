package notify

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/mail"
	"github.com/maximhq/bifrost/bench/webhooks"
)

func f(v float64) *float64 { return &v }

func members(ms ...Member) func(context.Context, string) ([]Member, error) {
	return func(context.Context, string) ([]Member, error) { return ms, nil }
}

func TestPrefsDefaultsAndValidation(t *testing.T) {
	st := Open(t.TempDir())
	p, err := st.Prefs("u1")
	if err != nil || !p.Digest || len(p.Events) != 0 {
		t.Fatalf("default %+v %v", p, err)
	}
	if _, err := st.SetPrefs("u1", Prefs{Events: []string{"nope"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown event: %v", err)
	}
	if _, err := st.SetPrefs("", Prefs{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no user: %v", err)
	}
	p, err = st.SetPrefs("u1", Prefs{Events: []string{webhooks.EventEvaluationRegressed, webhooks.EventEvaluationRegressed}, Digest: false})
	if err != nil || len(p.Events) != 1 || p.Digest {
		t.Fatalf("saved %+v %v", p, err)
	}
	fi, err := os.Stat(st.path("prefs.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("prefs mode %v %v", fi, err)
	}
}

func TestEmailEventGoesToMembersWhoChoseIt(t *testing.T) {
	root := t.TempDir()
	st := Open(root)
	_, _ = st.SetPrefs("u1", Prefs{Events: []string{webhooks.EventEvaluationRegressed}})
	_, _ = st.SetPrefs("u3", Prefs{Events: []string{webhooks.EventEvaluationRegressed}})
	log := mail.NewLog(nil)
	h := &Hub{Mail: log, PublicURL: "https://h.example.com", Members: members(
		Member{UserID: "u1", Email: "ana@example.com"},
		Member{UserID: "u2", Email: "bo@example.com"},                  // never chose
		Member{UserID: "u3", Email: "cy@example.com", Disabled: true}, // disabled
	)}
	ev := webhooks.NewEvent(webhooks.EventEvaluationRegressed, "org1", nil)
	ev.Summary = &webhooks.Summary{Title: "codex v7 regressed: 61% (-6 points)", Text: "the gate allows 5 points", Link: "/evaluations/ev_1",
		Fields: []webhooks.Field{{Label: "Change", Value: "-6 points vs v6 (67%)"}}}
	n, err := h.EmailEvent(context.Background(), "org1", root, ev)
	if err != nil || n != 1 {
		t.Fatalf("sent %d %v", n, err)
	}
	r := log.Recent()
	if len(r) != 1 || r[0].To[0] != "ana@example.com" || r[0].Subject != "codex v7 regressed: 61% (-6 points)" {
		t.Fatalf("recent %+v", r)
	}
	if !strings.Contains(r[0].Text, "https://h.example.com/evaluations/ev_1") || !strings.Contains(r[0].Text, "https://h.example.com/settings?tab=integrations&section=notifications") {
		t.Fatalf("links not absolute:\n%s", r[0].Text)
	}
	// An event members cannot choose is never emailed.
	if n, _ := h.EmailEvent(context.Background(), "org1", root, webhooks.NewEvent(webhooks.EventJobFailed, "org1", nil)); n != 0 {
		t.Fatalf("job.failed emailed %d", n)
	}
	// No accounts: nothing.
	if n, _ := (&Hub{Mail: log}).EmailEvent(context.Background(), "org1", root, ev); n != 0 {
		t.Fatal("sent without members")
	}
}

func TestDigestDue(t *testing.T) {
	mon8 := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC) // Monday
	if DigestDue(mon8, "") {
		t.Fatal("due before 09:00 Monday")
	}
	if !DigestDue(mon8.Add(time.Hour), "") || !DigestDue(mon8.Add(3*24*time.Hour), "2026-W47") {
		t.Fatal("not due after Monday 09:00")
	}
	if DigestDue(mon8.Add(2*time.Hour), ISOWeek(mon8)) {
		t.Fatal("due twice in one week")
	}
	if ISOWeek(mon8) != "2026-W48" {
		t.Fatalf("week %s", ISOWeek(mon8))
	}
}

func TestWeeklyDigest(t *testing.T) {
	root := t.TempDir()
	st := Open(root)
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for _, r := range []Record{
		{At: now.Add(-6 * 24 * time.Hour), EvaluationID: "ev_1", Agent: "codex", Version: 6, Status: "done", Score: f(0.67), CostUSD: f(1.5)},
		{At: now.Add(-2 * 24 * time.Hour), EvaluationID: "ev_2", Agent: "codex", Version: 7, Status: "done", Score: f(0.61), Change: f(-0.06), Regressed: true, CostUSD: f(2.25)},
		{At: now.Add(-1 * 24 * time.Hour), EvaluationID: "ev_3", Agent: "support-bot", Version: 2, Status: "done", Score: f(0.8)},
		{At: now.Add(-20 * 24 * time.Hour), EvaluationID: "ev_old", Agent: "old", Status: "done"},
	} {
		if err := st.AppendHistory(r); err != nil {
			t.Fatal(err)
		}
	}
	log := mail.NewLog(nil)
	h := &Hub{Mail: log, PublicURL: "https://h.example.com", Members: members(Member{UserID: "u1", Email: "ana@example.com"}, Member{UserID: "u2", Email: "bo@example.com"})}
	_, _ = st.SetPrefs("u2", Prefs{Digest: false})
	n, err := h.SendDigest(context.Background(), "org1", root, now)
	if err != nil || n != 1 {
		t.Fatalf("digest sent %d %v", n, err)
	}
	r := log.Recent()[0]
	if r.To[0] != "ana@example.com" || r.Subject != "Heimdall weekly: 3 evaluations, 1 regression" {
		t.Fatalf("digest %+v", r)
	}
	for _, want := range []string{"codex v7", "61% (-6 points this week), 1 regression", "support-bot v2", "80%", "$3.75 (2 of 3 evaluations priced)"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("digest lacks %q:\n%s", want, r.Text)
		}
	}
	if strings.Contains(r.Text, "old") {
		t.Errorf("digest includes a record older than a week:\n%s", r.Text)
	}
	// Once per week.
	if n, _ := h.SendDigest(context.Background(), "org1", root, now.Add(time.Hour)); n != 0 {
		t.Fatal("digest sent twice")
	}
	// A week with nothing finished sends nothing but is marked.
	empty := t.TempDir()
	if n, err := h.SendDigest(context.Background(), "org1", empty, now); n != 0 || err != nil {
		t.Fatalf("empty week %d %v", n, err)
	}
	if w, _ := Open(empty).DigestWeek(); w != ISOWeek(now) {
		t.Fatalf("empty week not marked: %q", w)
	}
}
