package handlers

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
)

func TestBuildEvaluationNoticeFromRealRuns(t *testing.T) {
	_, root, rl, ev := evalReleaseFixture(t, &schemas.EvaluationGate{MinScore: map[string]float64{"terminal": 0.99}})
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Releases: rl, Index: leaderboardIndex()}}
	h := &evaluationsHandler{env: env, now: time.Now}
	h.decideGate(ev)
	n := BuildEvaluationNotice(context.Background(), env, root, ev, []*schemas.Evaluation{ev}, time.Now())
	if n.Agent != "codex" || n.Version != 2 || n.Score == nil || n.Runs == 0 || n.Baseline == nil || n.Baseline.Version != 1 || n.Change == nil {
		t.Fatalf("notice %+v", n)
	}
	if n.Baseline.Source != BaselineFromGate || len(n.Capabilities) == 0 || n.Link != "/evaluations/"+ev.ID {
		t.Fatalf("baseline %+v caps %+v", n.Baseline, n.Capabilities)
	}
	if n.Gate == nil || n.Gate.State != schemas.GateFail || len(n.Blocked) == 0 {
		t.Fatalf("gate %+v blocked %v", n.Gate, n.Blocked)
	}
	evs := n.Events()
	if evs[0] != webhooks.EventEvaluationFinished || evs[len(evs)-1] != webhooks.EventReleaseBlocked {
		t.Fatalf("events %v", evs)
	}
	s := n.Summary(webhooks.EventEvaluationFinished)
	if !strings.HasPrefix(s.Title, "codex v2 scored ") || s.Link != n.Link {
		t.Fatalf("summary %+v", s)
	}
	labels := []string{}
	for _, f := range s.Fields {
		labels = append(labels, f.Label)
	}
	if strings.Join(labels, ",") != "Score,Change,Gate,Status" && strings.Join(labels, ",") != "Score,Change,Gate,Spend,Status" {
		t.Fatalf("fields %v", labels)
	}
	if b := n.Summary(webhooks.EventReleaseBlocked); b.Severity != webhooks.SeverityWarning || !strings.Contains(b.Title, "cannot be released") {
		t.Fatalf("blocked summary %+v", b)
	}
}

func TestScheduledNoticeComparesWithPreviousRun(t *testing.T) {
	_, root, rl, ev := evalReleaseFixture(t, nil)
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Releases: rl, Index: leaderboardIndex()}}
	prev := *ev
	prev.ID, prev.Schedule, prev.CreatedAt = "ev_20261119_000000_prv", "nightly", ev.CreatedAt.Add(-24*time.Hour)
	cur := *ev
	cur.Schedule = "nightly"
	n := BuildEvaluationNotice(context.Background(), env, root, &cur, []*schemas.Evaluation{&prev, &cur}, time.Now())
	if n.Baseline == nil || n.Baseline.Source != BaselineFromPreviousRun || n.Baseline.EvaluationID != prev.ID || n.Change == nil || *n.Change != 0 {
		t.Fatalf("baseline %+v change %v", n.Baseline, n.Change)
	}
	if n.Regressed {
		t.Fatalf("same runs counted as a regression: %v", n.Regressions)
	}
	if f := n.Fields()[1]; f.Label != "Change" || !strings.Contains(f.Value, "vs previous run") {
		t.Fatalf("change field %+v", f)
	}
}

func TestScheduledRegressionRule(t *testing.T) {
	for _, tc := range []struct {
		score, hi, prev, limit float64
		want                   bool
	}{
		{0.60, 0.70, 0.75, 0, true},    // whole interval below the previous score
		{0.60, 0.80, 0.75, 0, false},   // inside the noise
		{0.80, 0.90, 0.75, 0, false},   // better
		{0.60, 0.80, 0.75, 0.10, true}, // a 15 point drop passes a 10 point limit
		{0.70, 0.80, 0.75, 0.10, false},
	} {
		if got := scheduledRegression(tc.score, tc.hi, tc.prev, tc.limit) != ""; got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
	if Points(-0.06) != "-6 points" || Points(0.035) != "+3.5 points" || Points(0) != "0 points" || Points(0.01) != "+1 point" || Pct(0.614) != "61%" {
		t.Fatalf("formatting %s %s %s %s %s", Points(-0.06), Points(0.035), Points(0), Points(0.01), Pct(0.614))
	}
}

func TestEvaluationFinishNotifiesOnce(t *testing.T) {
	evalRefreshChildren = false
	t.Cleanup(func() { evalRefreshChildren = true })
	root := t.TempDir()
	var mu sync.Mutex
	var got []EvaluationNotice
	SetEvaluationNotifier(func(_ OrgEnv, _ *schemas.Evaluation, n EvaluationNotice) {
		mu.Lock()
		got = append(got, n)
		mu.Unlock()
	})
	t.Cleanup(func() { SetEvaluationNotifier(nil) })
	ev := &schemas.Evaluation{ID: "ev_20261122_000000_ntf", Status: schemas.EvaluationRunning, CreatedAt: time.Now(),
		Spec:     schemas.EvaluationSpec{Name: "nightly", Mode: schemas.EvaluationModeScore, Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Version: 3}}},
		Children: []schemas.EvaluationChild{{Benchmark: "b", ExperimentID: "e1", Status: string(schemas.ExperimentDone)}}}
	st := evalstore.Open(root)
	if err := st.Save(ev); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode}
	if n := RefreshEvaluations(env); n != 1 {
		t.Fatalf("refreshed %d", n)
	}
	evalNotifyWait.Wait()
	saved, err := st.Load(ev.ID)
	if err != nil || saved.Status != schemas.EvaluationDone || saved.NotifiedAt == nil {
		t.Fatalf("saved %+v %v", saved, err)
	}
	// Reads after it ended send nothing again.
	h := &evaluationsHandler{env: env, now: time.Now}
	h.refresh(saved)
	RefreshEvaluations(env)
	evalNotifyWait.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Status != schemas.EvaluationDone || got[0].Agent != "codex" || got[0].Version != 3 {
		t.Fatalf("notices %+v", got)
	}
	// No run index here: the notice says the scores are not available.
	if !strings.Contains(got[0].Note, "not available") {
		t.Fatalf("note %q", got[0].Note)
	}
}
