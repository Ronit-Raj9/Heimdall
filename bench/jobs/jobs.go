// Package jobs runs background work inside the server process: jobs are rows
// of the jobs table, claimed by a Runner's workers with a lease that a
// heartbeat renews. A job's handler gets a context that is cancelled when a
// cancel is requested (polled from the row) and when the runner shuts down,
// a logger whose lines land in job_logs (bounded per job), and a progress
// callback whose latest value is stored as JSON. A running job whose lease
// expired (its process died) is queued again with its attempt counted, up to
// a maximum, and its handler sees Attempt > 1: a resume.
//
// Every query is scoped to one organization (docs/platform-design.md, Jobs).
package jobs

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// MigrationSet is the name the jobs tables are registered under.
const MigrationSet = "jobs"

func init() {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic("jobs: migrations: " + err.Error())
	}
	db.Register(MigrationSet, sub)
}

// Status is a job's lifecycle state.
type Status string

// Job states. Succeeded, failed and cancelled are terminal.
const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Terminal reports whether s is a final state.
func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// Valid reports whether s is one of the five states.
func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Errors.
var (
	ErrNotFound    = errors.New("job not found")
	ErrUnknownKind = errors.New("unknown job kind")
	// ErrNotCreatable is returned by Create for a kind the API may not start.
	ErrNotCreatable = errors.New("job kind cannot be created through the API")
	// ErrConflict means a queued or running job (of any kind) already works
	// on the same target.
	ErrConflict = errors.New("a queued or running job already works on this target")
	// ErrInvalidParams wraps every parameter validation error.
	ErrInvalidParams = errors.New("invalid job parameters")
	// ErrFinished is returned by Cancel for a job that already ended.
	ErrFinished = errors.New("job already finished")

	// ErrCancelled is the cause of a handler's context when a cancel was
	// requested. A handler that returns an error after it is marked
	// cancelled; one that returns nil still succeeded.
	ErrCancelled = errors.New("job cancelled")
	// ErrShutdown is the cause of a handler's context when the runner stops.
	// The job is queued again for the next runner, and resumes there.
	ErrShutdown = errors.New("job runner shutting down")
	// ErrLeaseLost is the cause when the job's lease could not be renewed
	// (another runner reclaimed it). Nothing is written for the job after it.
	ErrLeaseLost = errors.New("job lease lost")
)

// Job is one row of the jobs table.
type Job struct {
	ID              string          `json:"id"`
	OrgID           string          `json:"org_id"`
	Kind            string          `json:"kind"`
	Target          string          `json:"target,omitempty"`
	Status          Status          `json:"status"`
	Params          json.RawMessage `json:"params"`
	Progress        json.RawMessage `json:"progress,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
	CreatedBy       string          `json:"created_by,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	CancelRequested bool            `json:"cancel_requested"`
	// Attempt counts claims: 1 on the first run, more after each requeue.
	// A handler with Attempt > 1 is resuming work a previous runner began.
	Attempt    int        `json:"attempt"`
	LeaseOwner string     `json:"lease_owner,omitempty"`
	LeaseUntil *time.Time `json:"lease_until,omitempty"`
	// Priority orders a queued job among the jobs of organizations with
	// equally many running jobs of its kind (fairqueue.go): higher first,
	// 0 normal, within MinPriority..MaxPriority.
	Priority int `json:"priority,omitempty"`
}

// Resume reports whether this run of the job continues an earlier one.
func (j *Job) Resume() bool { return j.Attempt > 1 }

// DecodeParams decodes the job's params into v.
func (j *Job) DecodeParams(v any) error {
	if len(j.Params) == 0 {
		return nil
	}
	return json.Unmarshal(j.Params, v)
}

// Filter selects jobs for List.
type Filter struct {
	Kind   string
	Status Status
	Target string
	Limit  int // default 50, at most 500
	// Before pages backwards: only jobs created before this job id's
	// creation (ids sort by creation time).
	Before string
}

// Page is one page of List.
type Page struct {
	Jobs []Job `json:"jobs"`
	// Next is the Before value for the next page, empty on the last one.
	Next string `json:"next,omitempty"`
}

// LogLine is one job_logs row.
type LogLine struct {
	Seq   int64     `json:"seq"`
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Line  string    `json:"line"`
}

// LogPage is the job's log lines after a cursor.
type LogPage struct {
	JobID  string    `json:"job_id"`
	Status Status    `json:"status"`
	Lines  []LogLine `json:"lines"`
	// Next is the cursor for the next poll (the last seq returned, or the
	// cursor given when nothing is new).
	Next int64 `json:"next"`
	// Dropped counts lines left out between the oldest kept lines and the
	// newest kept lines, so the log stays bounded.
	Dropped int64 `json:"dropped"`
}

const jobColumns = `id, org_id, kind, target, status, params, progress, result, error, created_by,
	created_at, started_at, finished_at, cancel_requested, attempt, lease_owner, lease_until, priority`

type rowScanner interface{ Scan(dest ...any) error }

func scanJob(row rowScanner) (*Job, error) {
	var (
		j                             Job
		status, params                string
		progress, result, owner       sql.NullString
		created                       int64
		started, finished, leaseUntil sql.NullInt64
		cancelRequested               int64
	)
	if err := row.Scan(&j.ID, &j.OrgID, &j.Kind, &j.Target, &status, &params, &progress, &result, &j.Error, &j.CreatedBy,
		&created, &started, &finished, &cancelRequested, &j.Attempt, &owner, &leaseUntil, &j.Priority); err != nil {
		return nil, err
	}
	j.Status = Status(status)
	j.Params = json.RawMessage(params)
	if progress.Valid && progress.String != "" {
		j.Progress = json.RawMessage(progress.String)
	}
	if result.Valid && result.String != "" {
		j.Result = json.RawMessage(result.String)
	}
	j.CreatedAt = fromMillis(created)
	j.StartedAt = nullTime(started)
	j.FinishedAt = nullTime(finished)
	j.CancelRequested = cancelRequested != 0
	j.LeaseOwner = owner.String
	j.LeaseUntil = nullTime(leaseUntil)
	return &j, nil
}

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func nullTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMillis(v.Int64)
	return &t
}

// Store reads and writes jobs, always within one organization.
type Store struct {
	db  *db.DB
	now func() time.Time
}

// NewStore returns a store over d.
func NewStore(d *db.DB) *Store { return &Store{db: d, now: time.Now} }

func (s *Store) q(query string) string { return s.db.Rebind(query) }

func (s *Store) millis() int64 { return s.now().UnixMilli() }

// insert writes a new queued job.
func (s *Store) insert(ctx context.Context, j *Job) error {
	params := string(j.Params)
	if params == "" {
		params = "{}"
	}
	_, err := s.db.SQL().ExecContext(ctx, s.q(`INSERT INTO jobs (id, org_id, kind, target, status, params, error, created_by, created_at, cancel_requested, attempt, priority)
		VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, 0, 0, ?)`),
		j.ID, j.OrgID, j.Kind, j.Target, string(StatusQueued), params, j.CreatedBy, j.CreatedAt.UnixMilli(), j.Priority)
	if err != nil {
		if db.IsUnique(err) {
			return fmt.Errorf("%w: %s %s", ErrConflict, j.Kind, j.Target)
		}
		return fmt.Errorf("insert job: %w", err)
	}
	return nil
}

// Get returns one job of the organization.
func (s *Store) Get(ctx context.Context, orgID, id string) (*Job, error) {
	row := s.db.SQL().QueryRowContext(ctx, s.q(`SELECT `+jobColumns+` FROM jobs WHERE org_id = ? AND id = ?`), orgID, id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("read job %s: %w", id, err)
	}
	return j, nil
}

// List returns the organization's jobs, newest first.
func (s *Store) List(ctx context.Context, orgID string, f Filter) (*Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	where := []string{"org_id = ?"}
	args := []any{orgID}
	if f.Kind != "" {
		where, args = append(where, "kind = ?"), append(args, f.Kind)
	}
	if f.Status != "" {
		where, args = append(where, "status = ?"), append(args, string(f.Status))
	}
	if f.Target != "" {
		where, args = append(where, "target = ?"), append(args, f.Target)
	}
	if f.Before != "" {
		where, args = append(where, "id < ?"), append(args, f.Before)
	}
	args = append(args, limit+1)
	rows, err := s.db.SQL().QueryContext(ctx, s.q(`SELECT `+jobColumns+` FROM jobs WHERE `+strings.Join(where, " AND ")+
		` ORDER BY id DESC LIMIT ?`), args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	page := &Page{Jobs: []Job{}}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("list jobs: %w", err)
		}
		page.Jobs = append(page.Jobs, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	if len(page.Jobs) > limit {
		page.Jobs = page.Jobs[:limit]
		page.Next = page.Jobs[limit-1].ID
	}
	return page, nil
}

// Latest returns the newest job of a kind on a target, or ErrNotFound.
func (s *Store) Latest(ctx context.Context, orgID, kind, target string) (*Job, error) {
	row := s.db.SQL().QueryRowContext(ctx, s.q(`SELECT `+jobColumns+` FROM jobs WHERE org_id = ? AND kind = ? AND target = ?
		ORDER BY id DESC LIMIT 1`), orgID, kind, target)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: no %s job on %s", ErrNotFound, kind, target)
	}
	if err != nil {
		return nil, fmt.Errorf("read job: %w", err)
	}
	return j, nil
}

// LatestByTarget returns, for every target of kind in the organization, its
// newest job's status: one query for a whole list page.
func (s *Store) LatestByTarget(ctx context.Context, orgID, kind string) (map[string]Status, error) {
	rows, err := s.db.SQL().QueryContext(ctx, s.q(`SELECT target, status, id FROM jobs WHERE org_id = ? AND kind = ? AND target <> ''
		ORDER BY id`), orgID, kind)
	if err != nil {
		return nil, fmt.Errorf("list job targets: %w", err)
	}
	defer rows.Close()
	out := map[string]Status{}
	for rows.Next() {
		var target, status, id string
		if err := rows.Scan(&target, &status, &id); err != nil {
			return nil, err
		}
		out[target] = Status(status) // ordered by id: the newest wins
	}
	return out, rows.Err()
}

// cancelBetweenUpdates and afterRecoverScan are test hooks: nil outside
// tests, they let a test move a row between two steps of Cancel or Recover.
var (
	cancelBetweenUpdates func(*sql.Tx)
	afterRecoverScan     func()
)

// Cancel requests cancellation. A queued job is cancelled at once; a running
// job's handler sees its context cancelled at the runner's next poll. A job
// that already ended answers ErrFinished.
func (s *Store) Cancel(ctx context.Context, orgID, id string) (*Job, error) {
	now := s.millis()
	// Each transition is one conditional UPDATE whose affected-row count
	// decides the outcome. Under PostgreSQL's READ COMMITTED a SELECT of
	// 'queued' followed by an UPDATE can race a concurrent claim, leaving the
	// row running with no cancel request while the caller is told it worked.
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, s.q(`UPDATE jobs SET status = ?, cancel_requested = 1, finished_at = ?, error = ?
			WHERE org_id = ? AND id = ? AND status = ?`), string(StatusCancelled), now, "cancelled before it started", orgID, id, string(StatusQueued))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 1 {
			return nil
		}
		if cancelBetweenUpdates != nil {
			cancelBetweenUpdates(tx)
		}
		res, err = tx.ExecContext(ctx, s.q(`UPDATE jobs SET cancel_requested = 1 WHERE org_id = ? AND id = ? AND status = ?`),
			orgID, id, string(StatusRunning))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 1 {
			return nil
		}
		var status string
		err = tx.QueryRowContext(ctx, s.q(`SELECT status FROM jobs WHERE org_id = ? AND id = ?`), orgID, id).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if err != nil {
			return err
		}
		if Status(status) == StatusQueued || Status(status) == StatusRunning {
			// The row moved between the two updates (claimed, then requeued);
			// report a conflict rather than an acknowledged no-op.
			return fmt.Errorf("%w: job %s changed state during cancel; retry", ErrConflict, id)
		}
		return fmt.Errorf("%w: %s is %s", ErrFinished, id, status)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, id)
}

// Logs returns the job's log lines with seq > since, at most limit (default
// 500, at most 2000).
func (s *Store) Logs(ctx context.Context, orgID, id string, since int64, limit int) (*LogPage, error) {
	j, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 500
	}
	limit = min(limit, 2000)
	page := &LogPage{JobID: id, Status: j.Status, Lines: []LogLine{}, Next: since}
	rows, err := s.db.SQL().QueryContext(ctx, s.q(`SELECT seq, at, level, line FROM job_logs WHERE job_id = ? AND seq > ? ORDER BY seq LIMIT ?`),
		id, since, limit)
	if err != nil {
		return nil, fmt.Errorf("read job logs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l LogLine
		var at int64
		if err := rows.Scan(&l.Seq, &at, &l.Level, &l.Line); err != nil {
			return nil, fmt.Errorf("read job logs: %w", err)
		}
		l.At = fromMillis(at)
		page.Lines = append(page.Lines, l)
		page.Next = l.Seq
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read job logs: %w", err)
	}
	var maxSeq, count int64
	if err := s.db.SQL().QueryRowContext(ctx, s.q(`SELECT COALESCE(MAX(seq), 0), COUNT(*) FROM job_logs WHERE job_id = ?`), id).
		Scan(&maxSeq, &count); err != nil {
		return nil, fmt.Errorf("count job logs: %w", err)
	}
	page.Dropped = max(maxSeq-count, 0)
	return page, nil
}

// appendLog writes one line with the job's next seq and drops the oldest
// line of the tail window once the job has more than head+tail lines, so the
// first head lines and the newest tail lines are kept. The seq is allocated
// in the database, in the inserting transaction: a resumed handler and the
// recovery that requeued it may write at the same moment, and two writers
// that each counted their own seq would collide on (job_id, seq). A
// collision of two such transactions is retried with a fresh seq.
func (s *Store) appendLog(ctx context.Context, jobID string, at time.Time, level, line string, head, tail int64) (int64, error) {
	for try := 0; ; try++ {
		var seq int64
		err := s.db.Tx(ctx, func(tx *sql.Tx) error {
			if err := tx.QueryRowContext(ctx, s.q(`SELECT COALESCE(MAX(seq), 0) + 1 FROM job_logs WHERE job_id = ?`), jobID).Scan(&seq); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO job_logs (job_id, seq, at, level, line) VALUES (?, ?, ?, ?, ?)`),
				jobID, seq, at.UnixMilli(), level, line); err != nil {
				return err
			}
			if seq > head+tail {
				if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM job_logs WHERE job_id = ? AND seq > ? AND seq <= ?`),
					jobID, head, seq-tail); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil || !db.IsUnique(err) || try >= 8 {
			return seq, err
		}
	}
}

// KindStatusCount is how many jobs of one kind are in one status.
type KindStatusCount struct {
	Kind   string
	Status Status
	Count  int
}

// CountByStatus counts every organization's jobs by kind and status, for
// the server's /metrics (queue depth, runs by status).
func (s *Store) CountByStatus(ctx context.Context) ([]KindStatusCount, error) {
	rows, err := s.db.SQL().QueryContext(ctx, s.q(`SELECT kind, status, COUNT(*) FROM jobs GROUP BY kind, status ORDER BY kind, status`))
	if err != nil {
		return nil, fmt.Errorf("count jobs: %w", err)
	}
	defer rows.Close()
	var out []KindStatusCount
	for rows.Next() {
		var c KindStatusCount
		var st string
		if err := rows.Scan(&c.Kind, &st, &c.Count); err != nil {
			return nil, err
		}
		c.Status = Status(st)
		out = append(out, c)
	}
	return out, rows.Err()
}
