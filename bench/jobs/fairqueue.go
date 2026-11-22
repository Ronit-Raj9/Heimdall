package jobs

// Fair queueing across organizations (INFRA_I4 item 2). Jobs used to be
// claimed oldest first, so one organization's queue of 50 experiments
// starved every organization that queued after it. The claim now orders
// candidates as Hatchet's multi-tenant queues do
// (https://hatchet.run/blog/multi-tenant-queues): first the organization
// with the fewest running jobs of the candidate's kind (per-org round
// robin), then the job's priority (higher first), then age. The running
// count is read from the jobs table, so it holds across server processes.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Priority bounds. 0 is normal; a release gate can be bumped up to
// MaxPriority, background work down to MinPriority.
const (
	MinPriority = -10
	MaxPriority = 10
)

// ErrNotQueued is returned by SetPriority for a job that already started or
// ended: only a queued job is reordered.
var ErrNotQueued = errors.New("job is not queued")

// fairOrder is the claim's ORDER BY. The inner SELECT of db.ClaimOne reads
// FROM jobs, which the correlated count refers to.
const fairOrder = `(SELECT COUNT(*) FROM jobs AS fq WHERE fq.org_id = jobs.org_id AND fq.kind = jobs.kind AND fq.status = 'running'), ` +
	`priority DESC, created_at, id`

func clampPriority(p int) int {
	return min(max(p, MinPriority), MaxPriority)
}

// SetPriority changes a queued job's priority (clamped to MinPriority..
// MaxPriority) and returns the job. A job that is running or ended is
// ErrNotQueued; an unknown one ErrNotFound.
func (r *Runner) SetPriority(ctx context.Context, orgID, id string, priority int) (*Job, error) {
	res, err := r.db.SQL().ExecContext(ctx, r.q(`UPDATE jobs SET priority = ? WHERE org_id = ? AND id = ? AND status = ?`),
		clampPriority(priority), orgID, id, string(StatusQueued))
	if err != nil {
		return nil, fmt.Errorf("set job priority: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("set job priority: %w", err)
	}
	j, err := r.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return j, fmt.Errorf("%w: %s is %s", ErrNotQueued, id, j.Status)
	}
	r.poke()
	return j, nil
}

// markIdle records that a claim found nothing: the other workers of this
// runner skip their claim until most of a poll interval has passed, so idle
// workers cost about one query per interval however many there are.
// Enqueue, SetPriority and a finished job clear it at once.
func (r *Runner) markIdle() {
	r.idle.Store(time.Now().Add(r.opts.PollEvery * 9 / 10).UnixNano())
}

func (r *Runner) clearIdle() { r.idle.Store(0) }

func (r *Runner) queueIdle() bool {
	until := r.idle.Load()
	return until != 0 && time.Now().UnixNano() < until
}
