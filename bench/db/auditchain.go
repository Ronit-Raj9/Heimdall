package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
)

// The audit log is hash-chained per org (lane X16, audit N11): row n of an
// org carries seq n, the hash of row n-1 as prev_hash, and its own hash over
// every stored field plus prev_hash (AuditHash). Editing, removing,
// inserting or reordering a row changes a hash that a later row has
// committed to, so VerifyAuditChain finds it. The chain survives the org:
// audit rows are never deleted with an org.

// AuditHashVersion is the first field of the hashed tuple; a future change
// of the hashed form bumps it.
const AuditHashVersion = 1

// auditColumns are the audit_log columns every reader selects, in scanAudit
// order.
const auditColumns = `id, org_id, actor_type, actor_id, action, target_type, target_id, ip, at, details, seq, prev_hash, hash`

// scanAudit reads one row selected with auditColumns.
func scanAudit(row interface{ Scan(...any) error }) (AuditEntry, error) {
	var e AuditEntry
	var at int64
	var details string
	if err := row.Scan(&e.ID, &e.OrgID, &e.ActorType, &e.ActorID, &e.Action, &e.TargetType, &e.TargetID, &e.IP, &at, &details,
		&e.Seq, &e.PrevHash, &e.Hash); err != nil {
		return AuditEntry{}, err
	}
	e.At, e.Details = Time(at), json.RawMessage(details)
	return e, nil
}

// AuditHash is the chain hash of e: lowercase hex sha256 of the JSON array
// [version, org_id, seq, prev_hash, id, actor_type, actor_id, action,
// target_type, target_id, ip, at (unix ms), details (compact JSON text)].
// It reads only e's stored fields, so an exported row can be checked
// offline with the same function.
func AuditHash(e AuditEntry) string {
	details := string(e.Details)
	if details == "" {
		details = "{}"
	}
	b, err := json.Marshal([]any{AuditHashVersion, e.OrgID, e.Seq, e.PrevHash, e.ID, e.ActorType, e.ActorID, e.Action,
		e.TargetType, e.TargetID, e.IP, Millis(e.At), details})
	if err != nil {
		// Only strings and integers: Marshal cannot fail.
		panic(fmt.Sprintf("db: audit hash: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// appendChained links e to the head of its org's chain and inserts it in
// tx. On Postgres an advisory lock per org serializes writers (two
// transactions would otherwise read the same head); SQLite transactions
// begin IMMEDIATE and already hold the write lock. The unique index on
// (org_id, seq) refuses a fork either way.
func (d *DB) appendChained(ctx context.Context, tx *sql.Tx, e *AuditEntry) error {
	if d.driver == Postgres {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, auditLockKey(e.OrgID)); err != nil {
			return fmt.Errorf("db: lock audit chain: %w", err)
		}
	}
	var seq int64
	var prev string
	err := tx.QueryRowContext(ctx, d.Rebind(`SELECT seq, hash FROM audit_log WHERE org_id = ? AND seq > 0 ORDER BY seq DESC LIMIT 1`),
		e.OrgID).Scan(&seq, &prev)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("db: read audit chain head: %w", err)
	}
	e.Seq, e.PrevHash = seq+1, prev
	e.Hash = AuditHash(*e)
	if _, err := tx.ExecContext(ctx, d.Rebind(auditInsert), auditArgs(e)...); err != nil {
		return fmt.Errorf("db: append audit row: %w", err)
	}
	return nil
}

// auditLockKey is the advisory lock id of one org's chain.
func auditLockKey(orgID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("heimdall.audit\x00" + orgID))
	return int64(h.Sum64() >> 1)
}

// AuditChainPage returns up to limit rows of orgID's log in chain order:
// rows written before the chain (seq 0) first by time, then by seq. after
// is the last seq of the previous page (-1 for the first page; legacy rows
// are only on that one). It is what the export and the verifier read.
func (d *DB) AuditChainPage(ctx context.Context, orgID string, after int64, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	var out []AuditEntry
	if after < 0 {
		rows, err := d.Query(ctx, `SELECT `+auditColumns+` FROM audit_log WHERE org_id = ? AND seq = 0 ORDER BY at, id`, orgID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			e, err := scanAudit(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, e)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		after = 0
	}
	rows, err := d.Query(ctx, `SELECT `+auditColumns+` FROM audit_log WHERE org_id = ? AND seq > ? ORDER BY seq LIMIT ?`, orgID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EachAudit calls fn for every row of orgID's log in chain order (see
// AuditChainPage), a page at a time, stopping at fn's first error.
func (d *DB) EachAudit(ctx context.Context, orgID string, fn func(AuditEntry) error) error {
	after := int64(-1)
	for {
		page, err := d.AuditChainPage(ctx, orgID, after, 1000)
		if err != nil {
			return err
		}
		chained := 0
		for _, e := range page {
			if err := fn(e); err != nil {
				return err
			}
			if e.Seq > 0 {
				after, chained = e.Seq, chained+1
			}
		}
		if after < 0 {
			after = 0
		}
		if chained < 1000 {
			return nil
		}
	}
}

// AuditChainReport is the outcome of VerifyAuditChain.
type AuditChainReport struct {
	OK bool `json:"ok"`
	// Entries counts every row read; Chained the rows with a seq; Legacy
	// the rows written before the chain existed (not covered by it).
	Entries  int    `json:"entries"`
	Chained  int    `json:"chained"`
	Legacy   int    `json:"legacy"`
	HeadSeq  int64  `json:"head_seq"`
	HeadHash string `json:"head_hash,omitempty"`
	// BrokenAt is the seq of the first row that does not verify (0: none);
	// Reason says why.
	BrokenAt int64  `json:"broken_at,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// AuditVerifier checks rows in chain order, one at a time, so an export of
// any size verifies in constant memory. The zero value is ready.
type AuditVerifier struct {
	r      AuditChainReport
	org    string
	seen   bool
	broken bool
}

// Add checks the next row.
func (v *AuditVerifier) Add(e AuditEntry) {
	v.r.Entries++
	if v.broken {
		return
	}
	if !v.seen {
		v.org, v.seen = e.OrgID, true
	} else if e.OrgID != v.org {
		v.fail(e.Seq, fmt.Sprintf("row %s belongs to org %q, the chain to %q", e.ID, e.OrgID, v.org))
		return
	}
	if e.Seq == 0 {
		if v.r.Chained > 0 {
			v.fail(0, fmt.Sprintf("row %s has no chain position but follows chained rows", e.ID))
			return
		}
		if e.Hash != "" || e.PrevHash != "" {
			v.fail(0, fmt.Sprintf("row %s carries a hash without a chain position", e.ID))
			return
		}
		v.r.Legacy++
		return
	}
	switch {
	case e.Seq != v.r.HeadSeq+1:
		v.fail(e.Seq, fmt.Sprintf("expected position %d, found %d: a row is missing or out of order", v.r.HeadSeq+1, e.Seq))
	case e.PrevHash != v.r.HeadHash:
		v.fail(e.Seq, "previous hash does not match the row before it")
	case AuditHash(e) != e.Hash:
		v.fail(e.Seq, "row content does not match its hash: the row was changed")
	default:
		v.r.Chained++
		v.r.HeadSeq, v.r.HeadHash = e.Seq, e.Hash
	}
}

func (v *AuditVerifier) fail(seq int64, reason string) {
	v.broken = true
	v.r.BrokenAt, v.r.Reason = seq, reason
	if seq == 0 {
		v.r.BrokenAt = v.r.HeadSeq + 1
	}
}

// Report returns the outcome so far.
func (v *AuditVerifier) Report() AuditChainReport {
	r := v.r
	r.OK = !v.broken
	return r
}

// VerifyAuditChain walks orgID's whole log and checks every link.
func (d *DB) VerifyAuditChain(ctx context.Context, orgID string) (AuditChainReport, error) {
	var v AuditVerifier
	err := d.EachAudit(ctx, orgID, func(e AuditEntry) error { v.Add(e); return nil })
	if err != nil {
		return AuditChainReport{}, err
	}
	return v.Report(), nil
}
