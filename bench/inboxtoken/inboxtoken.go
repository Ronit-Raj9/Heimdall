// Package inboxtoken keeps the per-org ingest tokens of the failure inbox
// (gap G02). A tool pushes a flagged trace into an org's inbox on the
// hosted server with one of these tokens as its bearer credential. Only the
// token's sha256 is stored; the token is returned once, by Create.
package inboxtoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
)

// Prefix starts every inbox ingest token. The hosted auth middleware lets a
// bearer with this prefix through as anonymous, so the ingest handler can
// check it against the org's tokens.
const Prefix = auth.InboxTokenPrefix

// MaxPerOrg caps the live tokens of one org.
const MaxPerOrg = 20

// MaxLabel caps a token label.
const MaxLabel = 80

// Token is one ingest token as listed: never the token itself.
type Token struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	Label      string     `json:"label"`
	Last4      string     `json:"last4"`
	CreatedBy  string     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// Store keeps the tokens.
type Store interface {
	List(ctx context.Context, orgID string) ([]Token, error)
	// Create makes a token and returns it with its plaintext, shown once.
	Create(ctx context.Context, orgID, label, actor string) (*Token, string, error)
	// Revoke deletes a token; ErrNotFound when orgID has no such token.
	Revoke(ctx context.Context, orgID, id string) error
	// Verify returns the org's token whose sha256 matches raw and records
	// the use; ErrNotFound for an unknown, revoked or other org's token.
	Verify(ctx context.Context, orgID, raw string, at time.Time) (*Token, error)
}

// Errors.
var (
	ErrNotFound = errors.New("inbox token not found")
	ErrInvalid  = errors.New("invalid inbox token request")
	ErrLimit    = fmt.Errorf("an organization keeps at most %d inbox tokens; revoke one first", MaxPerOrg)
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("inboxtoken", migrations) }

// Hash is the stored form of a token: hex sha256.
func Hash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// newToken returns a fresh token: Prefix + 32 random bytes base64url.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func newID() (string, error) {
	buf := make([]byte, 9)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "itok_" + hex.EncodeToString(buf), nil
}

func cleanLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if len(label) > MaxLabel {
		return "", fmt.Errorf("%w: label is longer than %d characters", ErrInvalid, MaxLabel)
	}
	for _, c := range label {
		if c < 0x20 || c == 0x7f {
			return "", fmt.Errorf("%w: label has a control character", ErrInvalid)
		}
	}
	return label, nil
}

// plausible rejects a presented token that cannot be one of ours before
// any lookup.
func plausible(raw string) bool {
	return strings.HasPrefix(raw, Prefix) && len(raw) > len(Prefix)+8 && len(raw) < 200
}

func last4(raw string) string { return raw[len(raw)-4:] }

// Open returns the database store.
func Open(d *db.DB) Store { return &dbStore{d: d} }

type dbStore struct{ d *db.DB }

const cols = `id, org_id, label, last4, created_by, created_at, last_used_at`

func scan(s interface{ Scan(...any) error }) (*Token, error) {
	var (
		t       Token
		created int64
		used    sql.NullInt64
	)
	if err := s.Scan(&t.ID, &t.OrgID, &t.Label, &t.Last4, &t.CreatedBy, &created, &used); err != nil {
		return nil, err
	}
	t.CreatedAt = time.UnixMilli(created).UTC()
	if used.Valid {
		u := time.UnixMilli(used.Int64).UTC()
		t.LastUsedAt = &u
	}
	return &t, nil
}

func (s *dbStore) List(ctx context.Context, orgID string) ([]Token, error) {
	rows, err := s.d.Query(ctx, `SELECT `+cols+` FROM inbox_tokens WHERE org_id = ? ORDER BY created_at, id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *dbStore) Create(ctx context.Context, orgID, label, actor string) (*Token, string, error) {
	label, err := cleanLabel(label)
	if err != nil {
		return nil, "", err
	}
	if orgID == "" {
		return nil, "", fmt.Errorf("%w: no organization", ErrInvalid)
	}
	var n int
	if err := s.d.QueryRow(ctx, `SELECT COUNT(*) FROM inbox_tokens WHERE org_id = ?`, orgID).Scan(&n); err != nil {
		return nil, "", err
	}
	if n >= MaxPerOrg {
		return nil, "", ErrLimit
	}
	raw, err := newToken()
	if err != nil {
		return nil, "", err
	}
	id, err := newID()
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	t := &Token{ID: id, OrgID: orgID, Label: label, Last4: last4(raw), CreatedBy: actor, CreatedAt: now}
	if _, err := s.d.Exec(ctx, `INSERT INTO inbox_tokens (id, org_id, label, token_sha256, last4, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.OrgID, t.Label, Hash(raw), t.Last4, t.CreatedBy, now.UnixMilli()); err != nil {
		return nil, "", err
	}
	return t, raw, nil
}

func (s *dbStore) Revoke(ctx context.Context, orgID, id string) error {
	res, err := s.d.Exec(ctx, `DELETE FROM inbox_tokens WHERE org_id = ? AND id = ?`, orgID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *dbStore) Verify(ctx context.Context, orgID, raw string, at time.Time) (*Token, error) {
	if !plausible(raw) || orgID == "" {
		return nil, ErrNotFound
	}
	t, err := scan(s.d.QueryRow(ctx, `SELECT `+cols+` FROM inbox_tokens WHERE org_id = ? AND token_sha256 = ?`, orgID, Hash(raw)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ms := at.UTC().UnixMilli()
	if _, err := s.d.Exec(ctx, `UPDATE inbox_tokens SET last_used_at = ? WHERE id = ?`, ms, t.ID); err == nil {
		u := time.UnixMilli(ms).UTC()
		t.LastUsedAt = &u
	}
	return t, nil
}

// NewMemory returns an in-memory store (tests and servers without a
// database).
func NewMemory() Store { return &memStore{byID: map[string]memRow{}} }

type memRow struct {
	t    Token
	hash string
}

type memStore struct {
	mu   sync.Mutex
	byID map[string]memRow
}

func (m *memStore) List(_ context.Context, orgID string) ([]Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Token{}
	for _, r := range m.byID {
		if r.t.OrgID == orgID {
			out = append(out, r.t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *memStore) Create(ctx context.Context, orgID, label, actor string) (*Token, string, error) {
	label, err := cleanLabel(label)
	if err != nil {
		return nil, "", err
	}
	if orgID == "" {
		return nil, "", fmt.Errorf("%w: no organization", ErrInvalid)
	}
	if l, _ := m.List(ctx, orgID); len(l) >= MaxPerOrg {
		return nil, "", ErrLimit
	}
	raw, err := newToken()
	if err != nil {
		return nil, "", err
	}
	id, err := newID()
	if err != nil {
		return nil, "", err
	}
	t := Token{ID: id, OrgID: orgID, Label: label, Last4: last4(raw), CreatedBy: actor, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	m.mu.Lock()
	m.byID[id] = memRow{t: t, hash: Hash(raw)}
	m.mu.Unlock()
	return &t, raw, nil
}

func (m *memStore) Revoke(_ context.Context, orgID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.byID[id]
	if !ok || r.t.OrgID != orgID {
		return ErrNotFound
	}
	delete(m.byID, id)
	return nil
}

func (m *memStore) Verify(_ context.Context, orgID, raw string, at time.Time) (*Token, error) {
	if !plausible(raw) || orgID == "" {
		return nil, ErrNotFound
	}
	h := Hash(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.byID {
		if r.t.OrgID == orgID && r.hash == h {
			u := at.UTC().Truncate(time.Millisecond)
			r.t.LastUsedAt = &u
			m.byID[id] = r
			t := r.t
			return &t, nil
		}
	}
	return nil, ErrNotFound
}
