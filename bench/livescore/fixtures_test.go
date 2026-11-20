package livescore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeJudgeServer answers chat completions with a fixed SCORE line, the
// shape of an OpenAI-compatible endpoint.
func fakeJudgeServer(t *testing.T, score string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer judge-key" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) == 0 || strings.Contains(body.Messages[0].Content, "alice@example.com") {
			http.Error(w, "the transcript was not redacted", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "SCORE: " + score}}}})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestJudgeReportedNotDecided(t *testing.T) {
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	fill(g, start, 30, func(i int) bool { return i%3 == 0 })
	jsrv, calls := fakeJudgeServer(t, "0.75")
	cfg := testConfig(srv.URL)
	cfg.Judge = &JudgeConfig{URL: jsrv.URL + "/v1", KeyEnv: "J", Model: "openai/judge", SampleRate: 1, MaxPerPull: 10}
	now := start.Add(2 * time.Hour)
	s, _, _ := newTestScorer(t, cfg, &now)
	s.Judge = NewChatJudge(cfg.Judge, "judge-key", "", "", nil)
	res, err := s.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Judged != 10 || *calls != 10 {
		t.Errorf("judged %d sessions with %d calls, want 10 (max_per_pull)", res.Judged, *calls)
	}
	all, _ := s.Store.Sessions()
	n := 0
	for _, r := range all {
		if r.Signals.JudgeScore != nil {
			n++
			if r.Signals.JudgeCalibrated || *r.Signals.JudgeScore != 0.75 {
				t.Errorf("session %s judge %v calibrated %v", r.ID, *r.Signals.JudgeScore, r.Signals.JudgeCalibrated)
			}
		}
	}
	if n != 10 {
		t.Errorf("%d sessions carry a judge score, want 10", n)
	}
	st, _ := s.Status(now)
	if st.Judge == nil || st.Judge.Calibrated || !strings.Contains(st.Judge.Note, "never decide") {
		t.Errorf("judge view %+v", st.Judge)
	}
}

// TestWriteFixtures writes the API fixtures the handler contract test and
// the UI read, from the end-to-end scenario: set LIVESCORE_FIXTURES to the
// handlers testdata folder to refresh them.
func TestWriteFixtures(t *testing.T) {
	out := os.Getenv("LIVESCORE_FIXTURES")
	if out == "" {
		t.Skip("set LIVESCORE_FIXTURES=<dir> to write the fixtures")
	}
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	fill(g, start, 80, func(i int) bool { return i%2 == 0 })
	// Loops, injections and gap-grouped sessions too, so every field shows.
	for i := range 6 {
		at := start.Add(time.Duration(i) * 7 * time.Minute)
		g.add(sessionSpec{vk: "vk-b", start: at.Add(30 * time.Second), turns: 5, loop: true, injection: i%2 == 0}.rows(fmt.Sprintf("g%d", i))...)
	}
	jsrv, _ := fakeJudgeServer(t, "0.5")
	cfg := testConfig(srv.URL)
	cfg.Source.URL = "http://127.0.0.1:18080"
	cfg.Judge = &JudgeConfig{URL: jsrv.URL + "/v1", KeyEnv: "J", Model: "openai/judge", SampleRate: 0.2, MaxPerPull: 20}
	cfg.Schedule = "*/15 * * * *"
	now := start.Add(2 * time.Hour)
	s, _, _ := newTestScorer(t, cfg, &now)
	s.Config.Source.URL = srv.URL
	s.Judge = NewChatJudge(cfg.Judge, "judge-key", "", "", nil)
	res, err := s.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Source.URL = "http://127.0.0.1:18080" // the fixture shows a plain gateway URL, not the test server's
	st, err := s.Status(now)
	if err != nil {
		t.Fatal(err)
	}
	cmp, _ := s.Compare()
	list, _ := s.Sessions(SessionFilter{Version: "v2", Limit: 5})
	res.Window = [2]time.Time{start, now}
	write := func(name string, v any) {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, name), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	write("status.json", st)
	write("compare.json", cmp)
	write("sessions.json", list)
	write("pull.json", res)
}

// TestJudgeCapCountsUnusableAnswers is 8-F-01: max_per_pull caps the judge
// requests sent, not the scores kept, so a judge whose answers never parse
// is asked at most max_per_pull times in a pull.
func TestJudgeCapCountsUnusableAnswers(t *testing.T) {
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	fill(g, start, 30, func(i int) bool { return i%3 == 0 })
	jsrv, calls := fakeJudgeServer(t, "not a number")
	cfg := testConfig(srv.URL)
	cfg.Judge = &JudgeConfig{URL: jsrv.URL + "/v1", KeyEnv: "J", Model: "openai/judge", SampleRate: 1, MaxPerPull: 2}
	now := start.Add(2 * time.Hour)
	s, _, _ := newTestScorer(t, cfg, &now)
	s.Judge = NewChatJudge(cfg.Judge, "judge-key", "", "", nil)
	res, err := s.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || res.JudgeAttempts != 2 || res.Judged != 0 {
		t.Fatalf("%d judge calls, attempts %d, judged %d; want 2 calls and attempts (max_per_pull), 0 judged", *calls, res.JudgeAttempts, res.Judged)
	}
}
