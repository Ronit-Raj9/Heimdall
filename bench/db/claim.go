package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ClaimQuery describes claiming one row of a work table: pick the first row
// matching Where in OrderBy order, apply Set to it, return Returning. All SQL
// fragments use `?` placeholders; arguments bind SetArgs first, then Args.
//
// A queued job:
//
//	row, err := d.ClaimOne(ctx, db.ClaimQuery{
//		Table:     "jobs",
//		Where:     "status = ? AND cancel_requested = 0",
//		Args:      []any{"queued"},
//		OrderBy:   "created_at, id",
//		Set:       "status = ?, started_at = ?, lease_owner = ?, lease_until = ?, attempt = attempt + 1",
//		SetArgs:   []any{"running", now, owner, now + leaseMS},
//		Returning: "id, kind, params, attempt",
//	})
//	err = row.Scan(&id, &kind, &params, &attempt) // sql.ErrNoRows: nothing to claim
//
// An expired lease is the same call with Where "status = ? AND lease_until < ?".
type ClaimQuery struct {
	Table     string // table name
	Key       string // unique single-column key; default "id"
	Where     string // candidate predicate
	Args      []any  // arguments of Where
	OrderBy   string // which candidate first; empty means any
	Set       string // assignments applied to the claimed row
	SetArgs   []any  // arguments of Set
	Returning string // columns returned from the claimed row
}

// claimSQL builds one statement:
//
//	UPDATE t SET <set> WHERE key = (SELECT key FROM t WHERE <where>
//	  ORDER BY <order> LIMIT 1 [FOR UPDATE SKIP LOCKED]) RETURNING <returning>
//
// On Postgres the statement is its own transaction and the inner SELECT locks
// the chosen row with FOR UPDATE SKIP LOCKED, so concurrent claimers each get
// a different row and never wait on each other. On SQLite a writing statement
// takes the write lock before it reads (as BEGIN IMMEDIATE does), so the
// choice and the update happen under one lock.
func (d *DB) claimSQL(q ClaimQuery) (string, []any, error) {
	if q.Table == "" || q.Where == "" || q.Set == "" || q.Returning == "" {
		return "", nil, errors.New("db: ClaimQuery needs Table, Where, Set and Returning")
	}
	key := q.Key
	if key == "" {
		key = "id"
	}
	for _, s := range []string{q.Table, key, q.Where, q.OrderBy, q.Set, q.Returning} {
		if strings.ContainsRune(s, ';') {
			return "", nil, fmt.Errorf("db: ClaimQuery fragment %q contains ';'", s)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "UPDATE %s SET %s WHERE %s = (SELECT %s FROM %s WHERE %s", q.Table, q.Set, key, key, q.Table, q.Where)
	if q.OrderBy != "" {
		b.WriteString(" ORDER BY " + q.OrderBy)
	}
	b.WriteString(" LIMIT 1")
	if d.driver == Postgres {
		b.WriteString(" FOR UPDATE SKIP LOCKED")
	}
	b.WriteString(") RETURNING " + q.Returning)
	args := make([]any, 0, len(q.SetArgs)+len(q.Args))
	args = append(append(args, q.SetArgs...), q.Args...)
	return d.Rebind(b.String()), args, nil
}

// ClaimOne claims at most one row atomically and returns it; Scan reports
// sql.ErrNoRows when no row matched (on Postgres also when every match is
// locked by a claimer still committing: poll again). The error return is
// for a malformed query only; database errors surface from Scan.
func (d *DB) ClaimOne(ctx context.Context, q ClaimQuery) (*sql.Row, error) {
	query, args, err := d.claimSQL(q)
	if err != nil {
		return nil, err
	}
	return d.sql.QueryRowContext(ctx, query, args...), nil
}

// ClaimOneTx is ClaimOne inside a transaction the caller holds (Tx), for a
// claim that must commit together with other writes.
func (d *DB) ClaimOneTx(ctx context.Context, tx *sql.Tx, q ClaimQuery) (*sql.Row, error) {
	query, args, err := d.claimSQL(q)
	if err != nil {
		return nil, err
	}
	return tx.QueryRowContext(ctx, query, args...), nil
}

// ClaimOneScan is ClaimOne followed by Scan into dest. It reports false with
// a nil error when there was nothing to claim.
func (d *DB) ClaimOneScan(ctx context.Context, q ClaimQuery, dest ...any) (bool, error) {
	row, err := d.ClaimOne(ctx, q)
	if err != nil {
		return false, err
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
