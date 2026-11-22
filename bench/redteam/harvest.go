package redteam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// MetricAttackSucceeded and MetricAttemptsToBreak are the run metrics the
// verifier's reward dimensions become (metrics.RewardDimensionPrefix).
const (
	MetricAttackSucceeded = "reward_attack_succeeded"
	MetricAttemptsToBreak = "reward_attempts_to_break"
	MetricAttempts        = "reward_attempts"
)

// HarvestResult says what Harvest found and wrote.
type HarvestResult struct {
	Evaluation string       `json:"evaluation"`
	Broken     int          `json:"broken"` // successful attacks seen
	Added      []ReviewItem `json:"added"`  // new pending regressions
	Skipped    []string     `json:"skipped,omitempty"`
}

// Harvest turns every successful attack in the sealed runs of ev's
// red-team benchmarks into a pending regression in the regressions
// benchmark folder regDir. suiteRoot gives a benchmark's folder ("" when
// unknown). It is idempotent: a payload already recorded is not added again.
func Harvest(ctx context.Context, src leaderboard.Source, ev *schemas.Evaluation, suiteRoot func(benchmark string) string, regDir string, now time.Time) (*HarvestResult, error) {
	if ev == nil || src == nil {
		return nil, errors.New("harvest: no evaluation or run source")
	}
	out := &HarvestResult{Evaluation: ev.ID, Added: []ReviewItem{}}
	for _, c := range ev.Children {
		if c.ExperimentID == "" || c.Skipped != "" {
			continue
		}
		root := suiteRoot(c.Benchmark)
		if !IsSuite(root) {
			continue
		}
		for _, r := range src.Runs(c.ExperimentID) {
			if err := ctx.Err(); err != nil {
				return out, fmt.Errorf("harvest: %w", err)
			}
			if !r.Sealed || r.Superseded {
				continue
			}
			item, cfg, why, err := harvestRun(src, root, c.Benchmark, r)
			if err != nil {
				return out, err
			}
			if why == "not broken" {
				continue
			}
			out.Broken++
			if why != "" {
				out.Skipped = append(out.Skipped, r.Run.RunID+": "+why)
				continue
			}
			item.Evaluation, item.Created = ev.ID, now.UTC()
			added, err := AddRegression(regDir, cfg, item)
			if err != nil {
				return out, err
			}
			if added {
				item.Task, item.State, item.PayloadSHA256 = cfg.ID, ReviewPending, cfg.ReplaySHA256
				item.Category, item.Strategy, item.Channel = cfg.Category, cfg.Strategy, cfg.Channel
				out.Added = append(out.Added, item)
			}
		}
	}
	return out, nil
}

// harvestRun reads one run; why is "not broken" for a run whose attack did
// not succeed, another reason for a broken run that cannot become a
// regression, "" when cfg is ready.
func harvestRun(src leaderboard.Source, root, benchmark string, r leaderboard.Run) (ReviewItem, AttackConfig, string, error) {
	var item ReviewItem
	raw, err := src.RunFile(r.Run.RunID, "metrics.json")
	if err != nil {
		return item, AttackConfig{}, "not broken", nil
	}
	var metrics map[string]schemas.Value
	if json.Unmarshal(raw, &metrics) != nil {
		return item, AttackConfig{}, "not broken", nil
	}
	if v, ok := metrics[MetricAttackSucceeded].Float(); !ok || v != 1 {
		return item, AttackConfig{}, "not broken", nil
	}
	if _, ok := ParseTaskID(r.Slot.Task); !ok {
		return item, AttackConfig{}, "not a red-team task", nil
	}
	b, err := src.RunFile(r.Run.RunID, RunStateFile)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
		return item, AttackConfig{}, "the run has no attacker state", nil
	}
	if err != nil {
		return item, AttackConfig{}, "", fmt.Errorf("harvest run %s: %w", r.Run.RunID, err)
	}
	state, err := ParseRunState(b)
	if err != nil {
		return item, AttackConfig{}, err.Error(), nil
	}
	orig, err := ReadAttack(filepath.Join(root, "tasks", r.Slot.Task))
	if err != nil {
		return item, AttackConfig{}, err.Error(), nil
	}
	cfg, err := Regression(orig, state)
	if err != nil {
		return item, AttackConfig{}, err.Error(), nil
	}
	item = ReviewItem{Run: r.Run.RunID, SourceSuite: benchmark, SourceTask: r.Slot.Task, AttackerModel: state.Model}
	if v, ok := metrics[MetricAttemptsToBreak].Float(); ok {
		item.AttemptsToBreak = int(v)
	}
	return item, cfg, "", nil
}

// ReadAttack reads a task folder's attack.json.
func ReadAttack(taskDir string) (AttackConfig, error) {
	var cfg AttackConfig
	b, err := os.ReadFile(filepath.Join(taskDir, "attack.json"))
	if err != nil {
		return cfg, fmt.Errorf("read attack.json: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("decode attack.json: %w", err)
	}
	return cfg, nil
}

// HarvestedFile records the evaluations already harvested, beside the
// regressions suite.yaml, so a finished evaluation is read once.
const HarvestedFile = "redteam.harvested.json"

// Harvested reports whether evaluation id was already harvested into regDir.
func Harvested(regDir, id string) bool {
	ids, err := harvestedIDs(regDir)
	return err == nil && slices.Contains(ids, id)
}

// MarkHarvested records evaluation id as harvested into regDir.
func MarkHarvested(regDir, id string) error {
	ids, err := harvestedIDs(regDir)
	if err != nil {
		return err
	}
	if slices.Contains(ids, id) {
		return nil
	}
	b, err := marshal(append(ids, id))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		return fmt.Errorf("create regressions benchmark: %w", err)
	}
	tmp := filepath.Join(regDir, "."+HarvestedFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", HarvestedFile, err)
	}
	return os.Rename(tmp, filepath.Join(regDir, HarvestedFile))
}

func harvestedIDs(regDir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(regDir, HarvestedFile))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", HarvestedFile, err)
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		return nil, fmt.Errorf("decode %s: %w", HarvestedFile, err)
	}
	return ids, nil
}
