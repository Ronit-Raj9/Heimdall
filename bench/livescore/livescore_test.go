package livescore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

const testToken = "setup-token-for-tests"

func testConfig(url string) *Config {
	cfg, err := ParseConfig([]byte(fmt.Sprintf(`
source:
  url: %s
  token_env: LIVE_TOKEN
versions:
  - name: v1
    virtual_keys: [vk-a]
  - name: v2
    virtual_keys: [vk-b]
compare:
  baseline: v1
  candidate: v2
  min_pairs: 20
sessions:
  gap: 5m
thresholds:
  - signal: abandoned
    above: 0.25
    min_sessions: 10
alerts:
  - url_env: LIVE_HOOK
inbox:
  max_per_pull: 3
`, url)), "test")
	if err != nil {
		panic(err)
	}
	return cfg
}

type recNotifier struct{ sent []alerts.Payload }

func (n *recNotifier) Send(_ context.Context, cfgs []alerts.Config, p alerts.Payload) error {
	for _, c := range cfgs {
		if c.Wants(p.Event) {
			n.sent = append(n.sent, p)
			return nil
		}
	}
	return nil
}

func newTestScorer(t *testing.T, cfg *Config, now *time.Time) (*Scorer, *traceinbox.FileStore, *recNotifier) {
	t.Helper()
	dir := t.TempDir()
	inbox := traceinbox.NewFileStore(filepath.Join(dir, "inbox"))
	n := &recNotifier{}
	s := &Scorer{Config: cfg, Store: NewStore(filepath.Join(dir, Dir)), Inbox: inbox, Notifier: n,
		Getenv: func(k string) string {
			if k == "LIVE_TOKEN" {
				return testToken
			}
			return ""
		},
		Now: func() time.Time { return *now }}
	return s, inbox, n
}

func TestReadOnlyTransportRefusesWrites(t *testing.T) {
	g, srv := newFakeGateway(t, "")
	c := ReadOnlyClient(nil)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(m, srv.URL+"/api/governance/virtual-keys", strings.NewReader("{}"))
		_, err := c.Do(req)
		if !errors.Is(err, ErrWriteRefused) {
			t.Errorf("%s: err = %v, want ErrWriteRefused", m, err)
		}
	}
	if methods, _ := g.seen(); len(methods) != 0 {
		t.Errorf("the gateway saw %v; a refused write must never leave the process", methods)
	}
}

func TestConfigRefusals(t *testing.T) {
	cases := map[string]string{
		"no selector":       "versions:\n  - name: v1\n",
		"unknown key":       "versions:\n  - name: v1\n    virtual_keys: [a]\n    keys: [b]\n",
		"secret in env":     "source:\n  token_env: sk-live-123456789\nversions:\n  - name: v1\n    virtual_keys: [a]\n",
		"bad compare":       "versions:\n  - name: v1\n    virtual_keys: [a]\ncompare:\n  baseline: v1\n  candidate: v9\n",
		"same arms":         "versions:\n  - name: v1\n    virtual_keys: [a]\ncompare:\n  baseline: v1\n  candidate: v1\n",
		"bad signal":        "versions:\n  - name: v1\n    virtual_keys: [a]\ncompare:\n  baseline: v1\n  candidate: v1\n  signals: [happiness]\n",
		"bad rate":          "versions:\n  - name: v1\n    virtual_keys: [a]\nsampling:\n  rate: 0\n",
		"url with password": "source:\n  url: http://u:p@host\nversions:\n  - name: v1\n    virtual_keys: [a]\n",
		"rate threshold":    "versions:\n  - name: v1\n    virtual_keys: [a]\nthresholds:\n  - signal: abandoned\n    above: 5\n",
		"alert url pasted":  "versions:\n  - name: v1\n    virtual_keys: [a]\nalerts:\n  - url_env: https://hooks.example\n",
	}
	for name, yml := range cases {
		if _, err := ParseConfig([]byte(yml), name); err == nil {
			t.Errorf("%s: accepted, want a refusal", name)
		}
	}
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "none.yaml")); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("missing config: %v, want ErrNotConfigured", err)
	}
}

func TestGrouping(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	mk := func(id string, at time.Time, vk, sid string) Row {
		r := Row{ID: id, Timestamp: at, Status: "success", Latency: ptr(1000.0), VirtualKeyID: ptr(vk)}
		if sid != "" {
			r.SessionID = ptr(sid)
		}
		return r
	}
	rows := []Row{
		mk("a1", t0, "k", "s1"), mk("b1", t0.Add(time.Second), "k", ""), mk("a2", t0.Add(time.Minute), "k", "s1"),
		mk("b2", t0.Add(2*time.Minute), "k", ""),
		mk("b3", t0.Add(30*time.Minute), "k", ""),                                                                 // after the gap: a new gap session
		mk("c1", t0.Add(31*time.Minute), "k", ""), mk("c2", t0.Add(31*time.Minute+200*time.Millisecond), "k", ""), // overlapping by 800 ms
	}
	got := group(rows, 5*time.Minute)
	if len(got) != 3 {
		t.Fatalf("sessions = %d, want 3 (s1, gap at t0, gap at t0+30m)", len(got))
	}
	if got[0].grouping != GroupSessionID || len(got[0].rows) != 2 {
		t.Errorf("first session %+v, want the 2 rows of s1", got[0].key)
	}
	if got[1].grouping != GroupGap || len(got[1].rows) != 2 || got[1].concurrent {
		t.Errorf("second session: grouping %s rows %d concurrent %v", got[1].grouping, len(got[1].rows), got[1].concurrent)
	}
	if !got[2].concurrent || len(got[2].rows) != 3 {
		t.Errorf("third session: rows %d concurrent %v, want 3 overlapping rows marked concurrent", len(got[2].rows), got[2].concurrent)
	}
	now := t0.Add(33 * time.Minute)
	if got[2].closed(now, 5*time.Minute) {
		t.Error("a session active 2 minutes ago is closed with a 5 minute gap")
	}
	if !got[0].closed(now, 5*time.Minute) {
		t.Error("s1, idle for 30 minutes, is not closed")
	}
}

func TestSignals(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	score := func(sp sessionSpec) (Signals, []string) {
		rows := sp.rows("r")
		return measure(rows, transcriptOf("x", rows), false)
	}
	healthy, flags := score(sessionSpec{vk: "k", start: t0, turns: 3})
	if len(flags) != 0 || *healthy.ToolErrorRate != 0 || *healthy.Loop || healthy.Abandoned || *healthy.Refusal {
		t.Errorf("healthy session flagged %v: %+v", flags, healthy)
	}
	if *healthy.ToolCalls != 2 || *healthy.Tokens != 100+20+200+20+300+20 || *healthy.CostUSD != 0.003 {
		t.Errorf("healthy counts: calls %d tokens %d cost %v", *healthy.ToolCalls, *healthy.Tokens, *healthy.CostUSD)
	}
	s, flags := score(sessionSpec{vk: "k", start: t0, turns: 3, toolError: true})
	if *s.ToolErrors != 1 || !slices.Contains(flags, FlagToolError) {
		t.Errorf("tool error: errors %d flags %v", *s.ToolErrors, flags)
	}
	s, flags = score(sessionSpec{vk: "k", start: t0, turns: 5, loop: true})
	if !*s.Loop || *s.RepeatedCalls != 3 || !slices.Contains(flags, FlagLoop) {
		t.Errorf("loop: loop %v repeated %d flags %v", *s.Loop, *s.RepeatedCalls, flags)
	}
	s, flags = score(sessionSpec{vk: "k", start: t0, turns: 3, injection: true})
	if *s.InjectionMarkers != 1 || !slices.Contains(flags, FlagInjection) {
		t.Errorf("injection: markers %d flags %v", *s.InjectionMarkers, flags)
	}
	s, flags = score(sessionSpec{vk: "k", start: t0, turns: 3, abandon: true})
	if !s.Abandoned || s.RequestErrors != 1 || !slices.Contains(flags, FlagAbandoned) || s.CostUSD == nil {
		t.Errorf("abandon: %+v flags %v (an errored row without cost keeps the cost known)", s, flags)
	}
	s, flags = score(sessionSpec{vk: "k", start: t0, turns: 2, budget: true})
	if s.BudgetHits != 1 || !slices.Contains(flags, FlagBudget) {
		t.Errorf("budget: hits %d flags %v", s.BudgetHits, flags)
	}
	s, flags = score(sessionSpec{vk: "k", start: t0, turns: 2, refuse: true})
	if !*s.Refusal || !slices.Contains(flags, FlagRefusal) {
		t.Errorf("refusal: %v flags %v", *s.Refusal, flags)
	}
	// Hidden content: transcript signals are unknown, never zero.
	rows := sessionSpec{vk: "k", start: t0, turns: 3}.rows("h")
	s, _ = measure(rows, nil, true)
	if s.ToolErrorRate != nil || s.Loop != nil || s.InjectionMarkers != nil || s.Refusal != nil || !s.ContentHidden {
		t.Errorf("hidden content gave transcript signals: %+v", s)
	}
}

// fill writes n sessions per version into the fake gateway, version v2
// with every second session abandoned on a 503.
func fill(g *fakeGateway, start time.Time, n int, faulty func(i int) bool) {
	for i := range n {
		at := start.Add(time.Duration(i) * 20 * time.Second)
		a := sessionSpec{vk: "vk-a", version: "v1", sid: fmt.Sprintf("a-%d", i), start: at, turns: 3, secret: "card 4111 1111 1111 1111 alice@example.com"}
		b := sessionSpec{vk: "vk-b", version: "v2", sid: fmt.Sprintf("b-%d", i), start: at.Add(5 * time.Second), turns: 3, abandon: faulty(i), toolError: faulty(i),
			secret: "card 4111 1111 1111 1111 alice@example.com"}
		g.add(a.rows(fmt.Sprintf("a%d", i))...)
		g.add(b.rows(fmt.Sprintf("b%d", i))...)
	}
}

func TestPullEndToEnd(t *testing.T) {
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	fill(g, start, 80, func(i int) bool { return i%2 == 0 })
	now := start.Add(2 * time.Hour)
	s, inbox, notes := newTestScorer(t, testConfig(srv.URL), &now)
	res, err := s.Pull(context.Background())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if res.Sessions["v1"] != 80 || res.Sessions["v2"] != 80 {
		t.Fatalf("scored %v, want 80 each", res.Sessions)
	}
	methods, paths := g.seen()
	for i, m := range methods {
		if m != http.MethodGet {
			t.Errorf("request %d was %s %s; live scoring must only read", i, m, paths[i])
		}
		if !strings.HasPrefix(paths[i], "/api/logs") {
			t.Errorf("request %d read %s, outside the log API", i, paths[i])
		}
	}
	cmp, _ := s.Compare()
	if cmp == nil || cmp.Result != ResultWorse {
		t.Fatalf("comparison = %+v, want worse", cmp)
	}
	worse := map[string]bool{}
	for _, sr := range cmp.Signals {
		if sr.Decision == ResultWorse {
			worse[sr.Name] = true
		}
		// Failed sessions end early, so they are cheaper: cost and tokens
		// may rightly decide better. Nothing else may.
		if sr.Decision == ResultBetter && sr.Name != "cost_usd" && sr.Name != "tokens" {
			t.Errorf("signal %s decided better on a candidate that only got worse", sr.Name)
		}
	}
	if !worse["abandoned"] || !worse["tool_error_rate"] {
		t.Errorf("worse signals %v, want abandoned and tool_error_rate", worse)
	}
	// Alerts: the threshold on abandoned for v2 and the comparison.
	events := map[string]int{}
	for _, p := range notes.sent {
		events[p.Event]++
	}
	if events[EventThreshold] != 1 || events[EventWorse] != 1 {
		t.Errorf("alerts %v, want one threshold and one worse alert", events)
	}
	// Inbox: items with provenance live, linked from the sessions.
	items, err := inbox.Items()
	if err != nil || len(items) == 0 || len(items) > 3 {
		t.Fatalf("inbox items %d (err %v), want 1 to 3", len(items), err)
	}
	for _, it := range items {
		if it.SourceKind != InboxSourceKind || it.Source != "live-v2" {
			t.Errorf("item %s source %s/%s, want live-v2/live", it.ID, it.Source, it.SourceKind)
		}
		if it.Occurrences < 1 || len(it.Traces) == 0 {
			t.Errorf("item %s occurrences %d traces %d", it.ID, it.Occurrences, len(it.Traces))
		}
	}
	all, _ := s.Store.Sessions()
	linked := 0
	for _, r := range all {
		if r.InboxItem != "" {
			linked++
		}
		if r.Evidence != EvidenceClass {
			t.Errorf("session %s evidence %q", r.ID, r.Evidence)
		}
	}
	if linked != 40 {
		t.Errorf("%d sessions linked to inbox items, want the 40 failing ones", linked)
	}
	// Privacy: no prompt, output or secret in livescore's own files; the
	// inbox transcript is redacted.
	assertNoText(t, s.Store.Dir(), []string{"Please check order", "Order 12 has shipped", "alice@example.com", "4111 1111", "upstream unavailable", "a-1", "b-1"})
	assertNoText(t, inbox.Dir(), []string{"alice@example.com", "4111 1111 1111 1111"})
	// A second pull reads the overlap again and scores nothing twice.
	now = now.Add(10 * time.Minute)
	res2, err := s.Pull(context.Background())
	if err != nil {
		t.Fatalf("second pull: %v", err)
	}
	if res2.Sessions["v1"]+res2.Sessions["v2"] != 0 {
		t.Errorf("second pull scored %v again", res2.Sessions)
	}
	if len(notes.sent) != 2 {
		t.Errorf("open alerts were sent again: %d alerts", len(notes.sent))
	}
	st, err := s.Status(now)
	if err != nil {
		t.Fatal(err)
	}
	if st.Grouping.BySessionID != 160 || len(st.Worst) == 0 || st.Worst[0].InboxItem == "" || st.Evidence != "live" {
		t.Errorf("status grouping %+v worst %d", st.Grouping, len(st.Worst))
	}
}

func assertNoText(t *testing.T, dir string, needles []string) {
	t.Helper()
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(p)
		for _, n := range needles {
			if bytes.Contains(data, []byte(n)) {
				t.Errorf("%s holds %q", p, n)
			}
		}
		return nil
	})
}

func TestAAComparisonDoesNotDecide(t *testing.T) {
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	// Both versions fail the same way at the same rate: an A/A test.
	for i := range 120 {
		at := start.Add(time.Duration(i) * 20 * time.Second)
		bad := i%5 == 0
		g.add(sessionSpec{vk: "vk-a", sid: fmt.Sprintf("a-%d", i), start: at, turns: 3, abandon: bad}.rows(fmt.Sprintf("a%d", i))...)
		g.add(sessionSpec{vk: "vk-b", sid: fmt.Sprintf("b-%d", i), start: at.Add(time.Second), turns: 3, abandon: (i+2)%5 == 0}.rows(fmt.Sprintf("b%d", i))...)
	}
	now := start.Add(3 * time.Hour)
	s, _, _ := newTestScorer(t, testConfig(srv.URL), &now)
	if _, err := s.Pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	cmp, _ := s.Compare()
	if cmp.Result != ResultUnknown {
		t.Errorf("A/A comparison decided %s: %s", cmp.Result, cmp.summaryLine())
	}
}

func TestTagSelectorAndClientCheck(t *testing.T) {
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	for i := range 6 {
		v := "v1"
		if i%2 == 1 {
			v = "v2"
		}
		g.add(sessionSpec{vk: "shared", version: v, sid: fmt.Sprintf("s-%d", i), start: start.Add(time.Duration(i) * time.Minute), turns: 2}.rows(fmt.Sprintf("t%d", i))...)
	}
	// The gateway ignores the metadata filter, as an older Bifrost would:
	// the client-side check must still keep the versions apart.
	g.ignore["metadata_agent-version"] = true
	cfg, err := ParseConfig([]byte(fmt.Sprintf(`
source: {url: %s, token_env: LIVE_TOKEN}
versions:
  - name: v1
    tag: {header: x-bf-lh-agent-version, value: v1}
  - name: v2
    tag: {header: x-bf-lh-agent-version, value: v2}
`, srv.URL)), "tag")
	if err != nil {
		t.Fatal(err)
	}
	now := start.Add(time.Hour)
	s, _, _ := newTestScorer(t, cfg, &now)
	res, err := s.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Sessions["v1"] != 3 || res.Sessions["v2"] != 3 || res.Overlap != 0 {
		t.Errorf("sessions %v overlap %d, want 3 and 3", res.Sessions, res.Overlap)
	}
	_, paths := g.seen()
	_ = paths
}

func TestOpenSessionWaitsAndSamplingIsStable(t *testing.T) {
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	g.add(sessionSpec{vk: "vk-a", sid: "live-one", start: start, turns: 2}.rows("o")...)
	now := start.Add(time.Minute) // still inside the 5 minute gap
	s, _, _ := newTestScorer(t, testConfig(srv.URL), &now)
	res, err := s.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Open != 1 || res.Sessions["v1"] != 0 {
		t.Fatalf("open %d scored %v: a running session must wait", res.Open, res.Sessions)
	}
	now = start.Add(20 * time.Minute)
	if res, err = s.Pull(context.Background()); err != nil || res.Sessions["v1"] != 1 {
		t.Fatalf("after the gap: %v %v", res, err)
	}
	// Sampling picks by key only.
	n := 0
	for i := range 10000 {
		if sampled("v1", fmt.Sprintf("sid:%d", i), 0.1) {
			n++
		}
		if sampled("v1", fmt.Sprintf("sid:%d", i), 0.1) != sampled("v1", fmt.Sprintf("sid:%d", i), 0.1) {
			t.Fatal("sampling is not deterministic")
		}
	}
	if n < 900 || n > 1100 {
		t.Errorf("rate 0.1 sampled %d of 10000", n)
	}
}

func TestCredentialRequiredAndRefused(t *testing.T) {
	_, srv := newFakeGateway(t, testToken)
	now := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	s, _, _ := newTestScorer(t, testConfig(srv.URL), &now)
	s.Getenv = func(string) string { return "" }
	if _, err := s.Pull(context.Background()); err == nil || !strings.Contains(err.Error(), "LIVE_TOKEN") {
		t.Errorf("empty credential: %v", err)
	}
	s.Getenv = func(string) string { return "wrong" }
	_, err := s.Pull(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refused the read credential") {
		t.Errorf("wrong credential: %v", err)
	}
	if strings.Contains(fmt.Sprint(err), "wrong") {
		t.Error("the error message carries the credential")
	}
}

func TestRestartDoesNotRetestOldData(t *testing.T) {
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	fill(g, start, 60, func(i int) bool { return i%2 == 0 })
	now := start.Add(2 * time.Hour)
	s, _, _ := newTestScorer(t, testConfig(srv.URL), &now)
	if _, err := s.Pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := s.RestartCompare()
	if err != nil {
		t.Fatal(err)
	}
	if c.Result != ResultUnknown || c.SessionsA != 0 || c.Restarts != 1 {
		t.Errorf("after restart: %+v", c)
	}
	now = now.Add(time.Hour)
	if _, err := s.Pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Compare()
	if c.SessionsA != 0 || c.SessionsB != 0 {
		t.Errorf("the restarted comparison consumed %d and %d old sessions", c.SessionsA, c.SessionsB)
	}
}

func TestRetentionPrunes(t *testing.T) {
	st := NewStore(t.TempDir())
	now := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	recs := []SessionRecord{{Seq: 1, ID: "ls_old", Ended: now.Add(-40 * 24 * time.Hour)}, {Seq: 2, ID: "ls_new", Ended: now}}
	if err := st.appendSessions(recs); err != nil {
		t.Fatal(err)
	}
	n, err := st.prune(now.Add(-30 * 24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	all, _ := st.Sessions()
	if len(all) != 1 || all[0].ID != "ls_new" {
		t.Errorf("kept %+v", all)
	}
}

func TestSessionsToleratesHalfWrittenLastLine(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.appendSessions([]SessionRecord{{Seq: 1, ID: "ls_a"}}); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(st.Dir(), SessionsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString(`{"seq":2,"id":"ls_b","sig`)
	f.Close()
	all, err := st.Sessions()
	if err != nil || len(all) != 1 {
		t.Fatalf("half-written last line: %d sessions, %v", len(all), err)
	}
	f, _ = os.OpenFile(filepath.Join(st.Dir(), SessionsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("\n" + `{"seq":3,"id":"ls_c"}` + "\n")
	f.Close()
	if _, err := st.Sessions(); err == nil {
		t.Error("a damaged line in the middle was accepted")
	}
}

func TestStaleOpenSessionIsScoredCut(t *testing.T) {
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	// One session id used all day, a request every two minutes: never idle.
	for i := range 100 {
		g.add(sessionSpec{vk: "vk-a", sid: "forever", start: start.Add(time.Duration(i) * 2 * time.Minute), turns: 1}.rows(fmt.Sprintf("f%d", i))...)
	}
	cfg := testConfig(srv.URL)
	cfg.Source.Window = traceinbox.Duration(time.Hour)
	now := start.Add(80 * time.Minute)
	s, _, _ := newTestScorer(t, cfg, &now)
	res, err := s.Pull(context.Background())
	if err != nil || res.Open != 1 {
		t.Fatalf("first pull: open %d, %v", res.Open, err)
	}
	now = start.Add(150 * time.Minute) // the session has now been open longer than the window
	if res, err = s.Pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Store.Sessions()
	if res.Sessions["v1"] != 1 || len(all) != 1 || !slices.Contains(all[0].Flags, FlagCut) {
		t.Fatalf("scored %v, flags %v: a session open past the window is scored and cut", res.Sessions, all)
	}
}

// TestRestartCountsUnsavedSessions covers QC2-26-10: a session appended by
// a pull that stopped before saving its state was scored before the
// restart, so the restarted comparison never consumes it.
func TestRestartCountsUnsavedSessions(t *testing.T) {
	now := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	cfg := testConfig("http://127.0.0.1:1")
	s, _, _ := newTestScorer(t, cfg, &now)
	if err := s.Store.saveState(&State{Seq: 9}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.appendSessions([]SessionRecord{{Seq: 10, ID: "ls_unsaved", Version: "v1", Ended: now}}); err != nil {
		t.Fatal(err)
	}
	c, err := s.RestartCompare()
	if err != nil {
		t.Fatal(err)
	}
	st, _ := s.Store.State()
	if st.Compare.LastSeq != 10 || c.SessionsA != 0 {
		t.Fatalf("restart cutoff %d, want 10 (the durable session)", st.Compare.LastSeq)
	}
}

// TestAppendRepairsTornTail covers QC2-26-03: an append after a pull died
// mid-record starts on a line boundary, so the new sessions read back.
func TestAppendRepairsTornTail(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.appendSessions([]SessionRecord{{Seq: 1, ID: "ls_a"}}); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(st.Dir(), SessionsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString(`{"seq":2,"id":"ls_b","sig`)
	f.Close()
	for _, recs := range [][]SessionRecord{{{Seq: 2, ID: "ls_c"}}, {{Seq: 3, ID: "ls_d"}, {Seq: 4, ID: "ls_e"}}} {
		if err := st.appendSessions(recs); err != nil {
			t.Fatal(err)
		}
	}
	all, err := st.Sessions()
	if err != nil || len(all) != 4 || all[1].ID != "ls_c" || all[3].ID != "ls_e" {
		t.Fatalf("sessions %+v, %v", all, err)
	}
}
