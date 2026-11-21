package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
)

// Organization deletion (lane X16, audit N10). An owner's request is one
// transaction (ScheduleOrgDeletion) that records the deletion and cuts every
// way in at once: memberships, API and runner tokens, invitations, password
// resets and public reading go, and sessions drop the org. The slow part
// (folders, mirror, every other row) runs afterwards in a background worker
// that reads PendingOrgDeletions, so a restart resumes it. The audit log is
// never touched, and the org_deletions row stays as the deletion receipt.

// Org deletion states.
const (
	OrgDeletionScheduled = "scheduled"
	OrgDeletionRunning   = "running"
	OrgDeletionDone      = "done"
	OrgDeletionFailed    = "failed"
)

// OrgDeletion is one org_deletions row.
type OrgDeletion struct {
	OrgID       string          `json:"org_id"`
	Slug        string          `json:"slug"`
	Status      string          `json:"status"`
	RequestedBy string          `json:"requested_by,omitempty"`
	RequestedAt time.Time       `json:"requested_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	Error       string          `json:"error,omitempty"`
	Details     json.RawMessage `json:"details,omitempty"`
}

// accessTables lose an org's rows when its deletion is scheduled: every
// credential or invitation that would let anyone in. A table missing from
// this database (its area is not linked in) is skipped.
var accessTables = []string{"memberships", "api_tokens", "runner_tokens", "invitations", "password_resets", "inbox_tokens"}

// keptTables are never purged by org id: the audit trail and the receipts.
var keptTables = []string{"audit_log", "org_deletions"}

// ScheduleOrgDeletion records the deletion of orgID and removes all access
// to it in one transaction, with the audit row (when given) committed in
// the same transaction. ErrConflict when a deletion is already recorded;
// ErrNotFound when the org does not exist.
func (d *DB) ScheduleOrgDeletion(ctx context.Context, orgID, requestedBy string, audit *AuditEntry) (OrgDeletion, error) {
	var out OrgDeletion
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		if err := d.lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		var slug string
		if err := tx.QueryRowContext(ctx, d.Rebind(`SELECT slug FROM orgs WHERE id = ?`), orgID).Scan(&slug); err != nil {
			return err
		}
		now := Now()
		_, err := tx.ExecContext(ctx, d.Rebind(`INSERT INTO org_deletions (org_id, slug, status, requested_by, requested_at) VALUES (?, ?, ?, ?, ?)`),
			orgID, slug, OrgDeletionScheduled, requestedBy, now)
		if IsUnique(err) {
			return fmt.Errorf("%w: organization %s is already being deleted", ErrConflict, slug)
		}
		if err != nil {
			return err
		}
		tables, err := d.orgTables(ctx, tx)
		if err != nil {
			return err
		}
		for _, t := range accessTables {
			if !slices.Contains(tables, t) {
				continue
			}
			if _, err := tx.ExecContext(ctx, d.Rebind(`DELETE FROM `+t+` WHERE org_id = ?`), orgID); err != nil {
				return fmt.Errorf("revoke %s: %w", t, err)
			}
		}
		if slices.Contains(tables, "sessions") {
			if _, err := tx.ExecContext(ctx, d.Rebind(`UPDATE sessions SET org_id = NULL WHERE org_id = ?`), orgID); err != nil {
				return fmt.Errorf("detach sessions: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, d.Rebind(`UPDATE orgs SET public_read = 0 WHERE id = ?`), orgID); err != nil {
			return err
		}
		if audit != nil {
			if _, err := d.AppendAuditTx(ctx, tx, *audit); err != nil {
				return err
			}
		}
		out = OrgDeletion{OrgID: orgID, Slug: slug, Status: OrgDeletionScheduled, RequestedBy: requestedBy, RequestedAt: Time(now)}
		return nil
	})
	return out, err
}

// OrgDeletionOf returns the deletion record of orgID, ErrNotFound if none.
func (d *DB) OrgDeletionOf(ctx context.Context, orgID string) (OrgDeletion, error) {
	row := d.QueryRow(ctx, `SELECT org_id, slug, status, requested_by, requested_at, finished_at, error, details FROM org_deletions WHERE org_id = ?`, orgID)
	od, err := scanOrgDeletion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OrgDeletion{}, ErrNotFound
	}
	return od, err
}

// PendingOrgDeletions lists the deletions the worker still has to finish:
// scheduled, interrupted (running) and failed ones, oldest first.
func (d *DB) PendingOrgDeletions(ctx context.Context) ([]OrgDeletion, error) {
	rows, err := d.Query(ctx, `SELECT org_id, slug, status, requested_by, requested_at, finished_at, error, details FROM org_deletions
		WHERE status <> ? ORDER BY requested_at, org_id`, OrgDeletionDone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrgDeletion
	for rows.Next() {
		od, err := scanOrgDeletion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, od)
	}
	return out, rows.Err()
}

// MarkOrgDeletion sets the state of a deletion; a terminal state stamps
// finished_at. details (nil keeps the stored value) is a JSON object.
func (d *DB) MarkOrgDeletion(ctx context.Context, orgID, status, errMsg string, details any) error {
	var finished sql.NullInt64
	if status == OrgDeletionDone || status == OrgDeletionFailed {
		finished = sql.NullInt64{Int64: Now(), Valid: true}
	}
	q := `UPDATE org_deletions SET status = ?, error = ?, finished_at = ? WHERE org_id = ?`
	args := []any{status, errMsg, finished, orgID}
	if details != nil {
		b, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("org deletion details: %w", err)
		}
		q = `UPDATE org_deletions SET status = ?, error = ?, finished_at = ?, details = ? WHERE org_id = ?`
		args = []any{status, errMsg, finished, string(b), orgID}
	}
	return expectOne(d.Exec(ctx, q, args...))
}

// PurgeOrgRows deletes every database row of orgID in one transaction: the
// org row (its cascades with it), then every other table with an org_id
// column, job logs through their jobs, and sessions are detached. The audit
// log and the deletion receipts stay. It returns the rows removed per
// table. Running it again after it succeeded removes nothing.
func (d *DB) PurgeOrgRows(ctx context.Context, orgID string) (map[string]int64, error) {
	counts := map[string]int64{}
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		tables, err := d.orgTables(ctx, tx)
		if err != nil {
			return err
		}
		exec := func(name, q string) error {
			res, err := tx.ExecContext(ctx, d.Rebind(q), orgID)
			if err != nil {
				return fmt.Errorf("purge %s: %w", name, err)
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				counts[name] += n
			}
			return nil
		}
		if slices.Contains(tables, "jobs") && d.hasTable(ctx, tx, "job_logs") {
			if err := exec("job_logs", `DELETE FROM job_logs WHERE job_id IN (SELECT id FROM jobs WHERE org_id = ?)`); err != nil {
				return err
			}
		}
		if slices.Contains(tables, "sessions") {
			if err := exec("sessions", `UPDATE sessions SET org_id = NULL WHERE org_id = ?`); err != nil {
				return err
			}
		}
		if err := exec("orgs", `DELETE FROM orgs WHERE id = ?`); err != nil {
			return err
		}
		for _, t := range tables {
			if t == "sessions" || slices.Contains(keptTables, t) {
				continue
			}
			if err := exec(t, `DELETE FROM `+t+` WHERE org_id = ?`); err != nil {
				return err
			}
		}
		return nil
	})
	return counts, err
}

// orgTables lists the tables of this database that have an org_id column,
// sorted, so a table added by a later migration is purged without a change
// here.
func (d *DB) orgTables(ctx context.Context, tx *sql.Tx) ([]string, error) {
	q := `SELECT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type = 'table' AND p.name = 'org_id'`
	if d.driver == Postgres {
		q = `SELECT table_name FROM information_schema.columns WHERE column_name = 'org_id' AND table_schema = current_schema()`
	}
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list org tables: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		if setName.MatchString(t) && !slices.Contains(out, t) { // table names are identifiers we splice into SQL
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out, rows.Err()
}

// hasTable reports whether table exists in this database.
func (d *DB) hasTable(ctx context.Context, tx *sql.Tx, table string) bool {
	q := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	if d.driver == Postgres {
		q = `SELECT COUNT(*) FROM information_schema.tables WHERE table_name = $1 AND table_schema = current_schema()`
	}
	var n int
	return tx.QueryRowContext(ctx, q, table).Scan(&n) == nil && n > 0
}

func scanOrgDeletion(row interface{ Scan(...any) error }) (OrgDeletion, error) {
	var od OrgDeletion
	var req int64
	var fin sql.NullInt64
	var details string
	if err := row.Scan(&od.OrgID, &od.Slug, &od.Status, &od.RequestedBy, &req, &fin, &od.Error, &details); err != nil {
		return OrgDeletion{}, err
	}
	od.RequestedAt = Time(req)
	if fin.Valid {
		t := Time(fin.Int64)
		od.FinishedAt = &t
	}
	if details != "" && details != "{}" {
		od.Details = json.RawMessage(details)
	}
	return od, nil
}
