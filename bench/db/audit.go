package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Actor types of an audit row.
const (
	ActorUser      = "user"
	ActorToken     = "token"
	ActorRunner    = "runner"
	ActorSystem    = "system"
	ActorAnonymous = "anonymous" // e.g. a failed login
)

// AuditEntry is one row of audit_log: who did what to which target, when,
// from where. Details is a JSON object (nil is {}).
type AuditEntry struct {
	ID         string          `json:"id"`
	OrgID      string          `json:"org_id"` // "" for instance-level events
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id"`
	Action     string          `json:"action"` // dotted verb: member.add, token.create, experiment.start
	TargetType string          `json:"target_type,omitempty"`
	TargetID   string          `json:"target_id,omitempty"`
	IP         string          `json:"ip,omitempty"`
	At         time.Time       `json:"at"`
	Details    json.RawMessage `json:"details,omitempty"`
	// Seq, PrevHash and Hash place the row in its org's hash chain
	// (auditchain.go). Seq 0 is a row written before the chain existed.
	Seq      int64  `json:"seq"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

func (e *AuditEntry) prepare() error {
	switch e.ActorType {
	case ActorUser, ActorToken, ActorRunner, ActorSystem, ActorAnonymous:
	default:
		return fmt.Errorf("db: audit actor_type %q is not user, token, runner, system or anonymous", e.ActorType)
	}
	if e.Action == "" {
		return errors.New("db: audit action is empty")
	}
	if e.ID == "" {
		e.ID = NewID("aud")
	}
	if e.At.IsZero() {
		e.At = Time(Now())
	}
	if len(e.Details) == 0 {
		e.Details = json.RawMessage("{}")
	} else if !json.Valid(e.Details) {
		return errors.New("db: audit details are not JSON")
	}
	// The hash covers the stored bytes; compact JSON is what every reader
	// and exporter re-encodes it to, so the stored form must be compact too.
	var buf bytes.Buffer
	if err := json.Compact(&buf, e.Details); err != nil {
		return fmt.Errorf("db: audit details: %w", err)
	}
	e.Details = buf.Bytes()
	// Millisecond precision is what the column stores and the hash covers.
	e.At = Time(Millis(e.At))
	return nil
}

const auditInsert = `INSERT INTO audit_log (id, org_id, actor_type, actor_id, action, target_type, target_id, ip, at, details, seq, prev_hash, hash)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func auditArgs(e *AuditEntry) []any {
	return []any{e.ID, e.OrgID, e.ActorType, e.ActorID, e.Action, e.TargetType, e.TargetID, e.IP, Millis(e.At), string(e.Details),
		e.Seq, e.PrevHash, e.Hash}
}

// AppendAudit writes one audit row at the head of its org's hash chain and
// returns it with ID, At, Seq and the hashes filled.
func (d *DB) AppendAudit(ctx context.Context, e AuditEntry) (AuditEntry, error) {
	if err := e.prepare(); err != nil {
		return e, err
	}
	err := d.Tx(ctx, func(tx *sql.Tx) error { return d.appendChained(ctx, tx, &e) })
	return e, err
}

// AppendAuditTx writes the audit row inside the caller's transaction, so the
// change and its record commit together.
func (d *DB) AppendAuditTx(ctx context.Context, tx *sql.Tx, e AuditEntry) (AuditEntry, error) {
	if err := e.prepare(); err != nil {
		return e, err
	}
	err := d.appendChained(ctx, tx, &e)
	return e, err
}

// AuditDetails marshals v for AuditEntry.Details (nil on failure: {}).
func AuditDetails(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// AuditFilter selects audit rows, newest first. Empty fields match all.
// OrgID is matched exactly, "" included, unless AllOrgs is set. Action ending
// in ".*" matches the prefix ("member.*"). Since is inclusive, Until
// exclusive. Limit defaults to 50, at most 500. Cursor is AuditPage.Next of
// the previous page.
type AuditFilter struct {
	OrgID      string
	AllOrgs    bool
	ActorType  string
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	Since      time.Time
	Until      time.Time
	Limit      int
	Cursor     string
}

// AuditPage is one page of ListAudit. Next is "" on the last page.
type AuditPage struct {
	Entries []AuditEntry `json:"entries"`
	Next    string       `json:"next,omitempty"`
}

// ListAudit returns audit rows newest first, keyset paged on (at, id).
func (d *DB) ListAudit(ctx context.Context, f AuditFilter) (AuditPage, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	var where []string
	var args []any
	add := func(cond string, a ...any) { where = append(where, cond); args = append(args, a...) }
	if !f.AllOrgs {
		add("org_id = ?", f.OrgID)
	}
	for _, p := range []struct{ col, v string }{{"actor_type", f.ActorType}, {"actor_id", f.ActorID},
		{"target_type", f.TargetType}, {"target_id", f.TargetID}} {
		if p.v != "" {
			add(p.col+" = ?", p.v)
		}
	}
	if f.Action != "" {
		if prefix, ok := strings.CutSuffix(f.Action, ".*"); ok {
			add(`action LIKE ? ESCAPE '\'`, likePrefix(prefix+"."))
		} else {
			add("action = ?", f.Action)
		}
	}
	if !f.Since.IsZero() {
		add("at >= ?", Millis(f.Since))
	}
	if !f.Until.IsZero() {
		add("at < ?", Millis(f.Until))
	}
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return AuditPage{}, err
		}
		add("(at < ? OR (at = ? AND id < ?))", at, at, id)
	}
	q := `SELECT ` + auditColumns + ` FROM audit_log`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY at DESC, id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := d.Query(ctx, q, args...)
	if err != nil {
		return AuditPage{}, err
	}
	defer rows.Close()
	page := AuditPage{Entries: []AuditEntry{}}
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return AuditPage{}, err
		}
		page.Entries = append(page.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, err
	}
	if len(page.Entries) > limit {
		page.Entries = page.Entries[:limit]
		last := page.Entries[limit-1]
		page.Next = encodeCursor(Millis(last.At), last.ID)
	}
	return page, nil
}

// likePrefix escapes s for `LIKE ? ESCAPE '\'` and appends %.
func likePrefix(s string) string { return likeEscape(s) + "%" }

// likeEscape escapes the LIKE wildcards and the escape character itself.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func encodeCursor(at int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at, 10) + ":" + id))
}

func decodeCursor(c string) (int64, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err == nil {
		if ms, id, ok := strings.Cut(string(b), ":"); ok {
			if at, perr := strconv.ParseInt(ms, 10, 64); perr == nil {
				return at, id, nil
			}
		}
	}
	return 0, "", ErrBadCursor
}

// ErrBadCursor is an audit page cursor ListAudit did not issue.
var ErrBadCursor = errors.New("db: bad page cursor")
