package schedule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func watchSchedule(t *testing.T) (string, Schedule) {
	t.Helper()
	root := t.TempDir()
	writeEval(t, root, "ev_20261120_000000_abc", 5)
	s := Schedule{Name: "watch", Cron: "0 6 * * *", Evaluation: "ev_20261120_000000_abc", MaxUSDPerDay: 20,
		NewModels: &NewModelWatch{Providers: []string{"openai"}, Current: "A", USDPerModel: 5}}
	return root, s
}

func TestValidateNewModelsNeedsCurrentArm(t *testing.T) {
	root, s := watchSchedule(t)
	if err := s.validateEvaluation(root); err != nil {
		t.Fatalf("a valid watch was refused: %v", err)
	}
	s.NewModels.Current = "B"
	err := s.validateEvaluation(root)
	if err == nil || !strings.Contains(err.Error(), `no setup named "B"`) {
		t.Fatalf("an unknown current arm: %v", err)
	}
}

func TestValidateNewModelsCapAboveDaily(t *testing.T) {
	root, s := watchSchedule(t)
	s.NewModels.MaxPerDay = 3
	s.NewModels.USDPerModel = 8
	err := s.validateEvaluation(root)
	if err == nil || !strings.Contains(err.Error(), "the watch could start 3 evaluations of 8.00 USD a day, above max_usd_per_day") {
		t.Fatalf("cap above daily: %v", err)
	}
	s.NewModels.MaxPerDay = 4
	s.NewModels.USDPerModel = 1
	if err := s.validateEvaluation(root); err == nil || !strings.Contains(err.Error(), "max_per_day") {
		t.Fatalf("max_per_day 4: %v", err)
	}
	s.NewModels.MaxPerDay = 1
	s.NewModels.USDPerModel = 0
	if err := s.validateEvaluation(root); err == nil || !strings.Contains(err.Error(), "usd_per_model") {
		t.Fatalf("usd_per_model 0: %v", err)
	}
}

func TestValidateNewModelsUnknownProvider(t *testing.T) {
	root, s := watchSchedule(t)
	s.NewModels.Providers = []string{"openai", "nosuch"}
	if err := s.validateEvaluation(root); err == nil || !strings.Contains(err.Error(), `"nosuch"`) {
		t.Fatalf("unknown provider: %v", err)
	}
	s.NewModels.Providers = nil
	if err := s.validateEvaluation(root); err == nil || !strings.Contains(err.Error(), "1 to 5") {
		t.Fatalf("no provider: %v", err)
	}
	s.NewModels.Providers = []string{"openai", "anthropic", "gemini", "azure", "openai", "anthropic"}
	if err := s.validateEvaluation(root); err == nil || !strings.Contains(err.Error(), "1 to 5") {
		t.Fatalf("six providers: %v", err)
	}
}

func TestValidateNewModelsTooFrequentCron(t *testing.T) {
	root, s := watchSchedule(t)
	for _, c := range []string{"*/30 * * * *", "0,30 * * * *", "* 6 * * *"} {
		s.Cron = c
		if err := s.validateEvaluation(root); err == nil || !strings.Contains(err.Error(), "a new model watch checks at most once an hour") {
			t.Fatalf("cron %q: %v", c, err)
		}
	}
	for _, c := range []string{"0 * * * *", "15 */2 * * *", "0 6 * * 1"} {
		s.Cron = c
		if err := s.validateEvaluation(root); err != nil {
			t.Fatalf("cron %q refused: %v", c, err)
		}
	}
}

func TestWatchStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	if st, err := LoadWatchState(root, "watch"); err != nil || st != nil {
		t.Fatalf("absent state = %+v, %v", st, err)
	}
	now := time.Date(2026, 11, 27, 6, 0, 0, 0, time.UTC)
	st := &WatchState{Schedule: "watch", Baseline: map[string][]string{"openai": {"gpt-a"}}, CheckedAt: now,
		Seen: map[string]WatchItem{"openai/gpt-b": {Provider: "openai", Model: "gpt-b", FirstSeen: now, State: WatchStarted, ClaimedAt: now}}}
	if err := SaveWatchState(root, st); err != nil {
		t.Fatal(err)
	}
	p := WatchStatePath(root, "watch")
	if p != filepath.Join(root, ".heimdall", "gateway", "model-watch", "watch.json") {
		t.Fatalf("path %s", p)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode: %v %v", fi, err)
	}
	got, err := LoadWatchState(root, "watch")
	if err != nil || got.Seen["openai/gpt-b"].State != WatchStarted || got.Baseline["openai"][0] != "gpt-a" {
		t.Fatalf("loaded %+v, %v", got, err)
	}
}
