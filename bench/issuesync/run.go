package issuesync

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench/emit"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
)

// Sentences a person reads.
const (
	PrivateGoneReason = "The GitHub connection is gone, so this private repository cannot be read. Connect GitHub again in Settings."
	PublicNoGitHub    = "Finding issues needs a working GitHub connection, even for public repositories: reconnect GitHub in Settings > GitHub."
	BusyText          = "A sync of this repository is already running."
	NothingNewReason  = "No new merged fixes since the last sync."
)

// ErrBusy is a second sync of a repository while one runs.
var ErrBusy = errors.New(BusyText)

// Deps is what one sync needs; tests pass fakes.
type Deps struct {
	Store     Store
	Manager   *pipeline.Manager
	Tasks     func(benchmark string) (map[string]bool, error) // task ids already in the benchmark
	Public    func(ctx context.Context, repo string) bool     // work.RepoPublic with the real git
	HasGitHub func(ctx context.Context) bool                  // auth.FindRepoToken found a token
	// FirstPrivate, when set, runs once when a sync first finds its
	// repository private (the members-only data policy and the tag).
	FirstPrivate func(r Repo) error
	Now          func() time.Time
}

var running sync.Map // sync id -> struct{}

// Run does one weekly sync of one connected repository.
func Run(ctx context.Context, d Deps, id string) (RunRef, error) {
	if _, busy := running.LoadOrStore(id, struct{}{}); busy {
		return RunRef{}, ErrBusy
	}
	defer running.Delete(id)
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	ref := RunRef{At: now().UTC()}
	r, err := d.Store.Get(id)
	if err != nil {
		return ref, err
	}
	// (1) A paused sync is refused with its reason.
	if r.Paused {
		ref.Status, ref.Reason = RunRefused, r.PausedReason
		return ref, record(d.Store, id, ref, nil)
	}
	// (2) Private check, and no scan at all without a GitHub connection.
	public := d.Public != nil && d.Public(ctx, r.Repo)
	hasGitHub := d.HasGitHub != nil && d.HasGitHub(ctx)
	if !hasGitHub {
		ref.Status = RunRefused
		if public {
			ref.Reason = PublicNoGitHub
			return ref, record(d.Store, id, ref, nil)
		}
		ref.Reason = PrivateGoneReason
		return ref, record(d.Store, id, ref, func(x *Repo) { x.Paused, x.PausedReason = true, PrivateGoneReason })
	}
	if !public && !r.Private {
		if d.FirstPrivate != nil {
			if err := d.FirstPrivate(*r); err != nil {
				ref.Status, ref.Reason = RunFailed, "Could not limit the benchmark to members: "+err.Error()
				return ref, record(d.Store, id, ref, nil)
			}
		}
		if err := record(d.Store, id, RunRef{}, func(x *Repo) { x.Private = true }); err != nil {
			return ref, err
		}
	}
	// (3) Scan the fixes merged since the cursor, one day of overlap.
	opts := pipeline.ScanOptions{Limit: r.MaxIssues * 3}
	if !r.MergedAfter.IsZero() {
		opts.Since = r.MergedAfter.AddDate(0, 0, -1).Format("2006-01-02")
	}
	st, err := d.Manager.Scan(ctx, r.Repo, opts)
	if err != nil {
		ref.Status, ref.Reason = RunFailed, err.Error()
		return ref, record(d.Store, id, ref, nil)
	}
	ref.RunID = st.ID
	// (4) Drop what was seen or is already a task; keep at most MaxIssues.
	var have map[string]bool
	if d.Tasks != nil {
		if have, err = d.Tasks(r.Benchmark); err != nil {
			ref.Status, ref.Reason = RunFailed, err.Error()
			return ref, record(d.Store, id, ref, nil)
		}
	}
	newest := r.MergedAfter
	var keep []int
	for _, it := range st.Items {
		c := it.Candidate
		if c.PRMerged.After(newest) {
			newest = c.PRMerged
		}
		if slices.Contains(r.SeenPRs, c.PR) || have[emit.InstanceID(c)] {
			ref.Skipped++
			continue
		}
		if len(keep) < r.MaxIssues {
			keep = append(keep, c.PR)
		}
	}
	if _, err := d.Manager.Keep(st.ID, keep); err != nil {
		ref.Status, ref.Reason = RunFailed, err.Error()
		return ref, record(d.Store, id, ref, nil)
	}
	advance := func(x *Repo) {
		x.MergedAfter = newest
		x.SeenPRs = append(x.SeenPRs, keep...)
		if n := len(x.SeenPRs); n > maxSeen {
			x.SeenPRs = x.SeenPRs[n-maxSeen:]
		}
	}
	if len(keep) == 0 {
		ref.Status, ref.Reason = RunNothingNew, NothingNewReason
		return ref, record(d.Store, id, ref, advance)
	}
	// (5) Spend caps, then (6) start.
	if _, err := d.Manager.Limit(st.ID, r.WeeklyUSD, r.PerIssueUSD); err != nil {
		ref.Status, ref.Reason = RunFailed, err.Error()
		return ref, record(d.Store, id, ref, nil)
	}
	if _, err := d.Manager.StartWith(st.ID, true, r.Model); err != nil {
		ref.Status, ref.Reason = RunFailed, err.Error()
		return ref, record(d.Store, id, ref, nil)
	}
	// (7) Record the cursor, the seen fixes and the run.
	ref.Status, ref.Started = RunStarted, len(keep)
	return ref, record(d.Store, id, ref, advance)
}

// record appends ref (when it has a status) and applies change to repo id.
func record(s Store, id string, ref RunRef, change func(*Repo)) error {
	return s.Update(func(list []Repo) ([]Repo, error) {
		for i := range list {
			if list[i].ID != id {
				continue
			}
			if change != nil {
				change(&list[i])
			}
			if ref.Status != "" {
				list[i].Runs = append(list[i].Runs, ref)
				if n := len(list[i].Runs); n > maxRuns {
					list[i].Runs = list[i].Runs[n-maxRuns:]
				}
			}
			return list, nil
		}
		return nil, ErrNotFound
	})
}
