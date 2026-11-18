package auth

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("auth", migrations) }

// querier is what *sql.DB and *sql.Tx share.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// User is a row of the core users table (owned by the database area; the
// auth area reads and writes the columns the contract fixes).
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	GitHubID     string    `json:"-"`
	Disabled     bool      `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Org is a row of the core orgs table.
type Org struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	PublicRead bool   `json:"public_read,omitempty"`
}

// OrgRole is one of a user's organizations with the user's role in it.
type OrgRole struct {
	Org
	Role Role `json:"role"`
}

// NormalizeEmail is how emails are stored and compared: trimmed, lowercase.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// validEmail is a deliberately loose shape check: one @, a dot in the
// domain, no spaces or control characters, at most 254 bytes.
func validEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 {
		return false
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") || !strings.Contains(domain, ".") ||
		strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	for _, c := range email {
		if c <= ' ' || c == 0x7f || c == '<' || c == '>' || c == '"' || c == '\\' || c == ',' || c == ';' {
			return false
		}
	}
	return true
}

// store is the SQL of the auth area. Core tables (users, orgs, memberships)
// are touched only through the columns docs/platform-design.md fixes.
type store struct {
	d   *db.DB
	now func() time.Time
}

func (s *store) ms() int64 { return s.now().UnixMilli() }

func (s *store) exec(ctx context.Context, q querier, query string, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, s.d.Rebind(query), args...)
}

func (s *store) row(ctx context.Context, q querier, query string, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, s.d.Rebind(query), args...)
}

func (s *store) rows(ctx context.Context, q querier, query string, args ...any) (*sql.Rows, error) {
	return q.QueryContext(ctx, s.d.Rebind(query), args...)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	return err
}

// ---- users, orgs, memberships (core tables) ----

const userCols = `id, email, name, password_hash, COALESCE(github_id, ''), disabled, created_at`

func scanUser(r interface{ Scan(...any) error }) (*User, error) {
	var u User
	var disabled int
	var created int64
	if err := r.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.GitHubID, &disabled, &created); err != nil {
		return nil, notFound(err)
	}
	u.Disabled = disabled != 0
	u.CreatedAt = db.Time(created)
	return &u, nil
}

func (s *store) userByID(ctx context.Context, q querier, id string) (*User, error) {
	return scanUser(s.row(ctx, q, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *store) userByEmail(ctx context.Context, q querier, email string) (*User, error) {
	return scanUser(s.row(ctx, q, `SELECT `+userCols+` FROM users WHERE email = ?`, NormalizeEmail(email)))
}

func (s *store) userByGitHubID(ctx context.Context, q querier, ghID string) (*User, error) {
	return scanUser(s.row(ctx, q, `SELECT `+userCols+` FROM users WHERE github_id = ?`, ghID))
}

func (s *store) countUsers(ctx context.Context, q querier) (int, error) {
	var n int
	err := s.row(ctx, q, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// insertUser creates a user; email is normalised. A taken email is
// db.ErrConflict.
func (s *store) insertUser(ctx context.Context, q querier, email, name, passwordHash, githubID string) (*User, error) {
	u := &User{ID: db.NewID("usr"), Email: clip(NormalizeEmail(email), 254), Name: clip(strings.TrimSpace(name), 200), PasswordHash: passwordHash,
		GitHubID: githubID, CreatedAt: time.UnixMilli(s.ms()).UTC()}
	var gh any
	if githubID != "" {
		gh = githubID
	}
	_, err := s.exec(ctx, q, `INSERT INTO users (id, email, name, password_hash, github_id, disabled, created_at) VALUES (?, ?, ?, ?, ?, 0, ?)`,
		u.ID, u.Email, u.Name, u.PasswordHash, gh, u.CreatedAt.UnixMilli())
	if err != nil {
		if db.IsUnique(err) {
			return nil, db.ErrConflict
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

func (s *store) setPasswordHash(ctx context.Context, q querier, userID, hash string) error {
	_, err := s.exec(ctx, q, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID)
	return err
}

func (s *store) linkGitHub(ctx context.Context, q querier, userID, ghID string) error {
	_, err := s.exec(ctx, q, `UPDATE users SET github_id = ? WHERE id = ? AND github_id IS NULL`, ghID, userID)
	if db.IsUnique(err) {
		return db.ErrConflict
	}
	return err
}

func (s *store) orgByID(ctx context.Context, q querier, id string) (*Org, error) {
	var o Org
	var pr int
	if err := s.row(ctx, q, `SELECT id, slug, name, public_read FROM orgs WHERE id = ?`, id).Scan(&o.ID, &o.Slug, &o.Name, &pr); err != nil {
		return nil, notFound(err)
	}
	o.PublicRead = pr != 0
	return &o, nil
}

func (s *store) orgBySlug(ctx context.Context, q querier, slug string) (*Org, error) {
	var o Org
	var pr int
	if err := s.row(ctx, q, `SELECT id, slug, name, public_read FROM orgs WHERE slug = ?`, slug).Scan(&o.ID, &o.Slug, &o.Name, &pr); err != nil {
		return nil, notFound(err)
	}
	o.PublicRead = pr != 0
	return &o, nil
}

// insertOrg creates an organization with a slug derived from name; a taken
// slug gets a short random suffix.
func (s *store) insertOrg(ctx context.Context, q querier, name string) (*Org, error) {
	name = clip(strings.TrimSpace(name), 100)
	base := Slugify(name)
	for attempt := 0; attempt < 5; attempt++ {
		slug := base
		if attempt > 0 {
			suffix, err := randomLookup(4)
			if err != nil {
				return nil, err
			}
			slug = base + "-" + suffix
		}
		o := &Org{ID: db.NewID("org"), Slug: slug, Name: name}
		_, err := s.exec(ctx, q, `INSERT INTO orgs (id, slug, name, public_read, created_at) VALUES (?, ?, ?, 0, ?)`, o.ID, o.Slug, o.Name, s.ms())
		if err == nil {
			return o, nil
		}
		if !db.IsUnique(err) {
			return nil, fmt.Errorf("insert org: %w", err)
		}
	}
	return nil, fmt.Errorf("insert org: no free slug for %q", name)
}

// reservedSlugs can not name an organization: they collide with routes or
// with the local-mode organization.
var reservedSlugs = map[string]bool{"local": true, "api": true, "auth": true, "admin": true, "orgs": true, "new": true, "settings": true}

// Slugify turns a name into a slug of [a-z0-9-], 3 to 40 characters.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(name) {
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteRune(c)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) < 3 || reservedSlugs[slug] {
		slug = "org-" + slug
		slug = strings.Trim(slug, "-")
	}
	return slug
}

// membershipRole returns userID's role in orgID, db.ErrNotFound when none.
func (s *store) membershipRole(ctx context.Context, q querier, orgID, userID string) (Role, error) {
	var role string
	if err := s.row(ctx, q, `SELECT role FROM memberships WHERE org_id = ? AND user_id = ?`, orgID, userID).Scan(&role); err != nil {
		return "", notFound(err)
	}
	return Role(role), nil
}

// memberships lists a user's organizations, oldest membership first.
func (s *store) memberships(ctx context.Context, q querier, userID string) ([]OrgRole, error) {
	rs, err := s.rows(ctx, q, `SELECT o.id, o.slug, o.name, o.public_read, m.role FROM memberships m JOIN orgs o ON o.id = m.org_id
		WHERE m.user_id = ? ORDER BY m.created_at, o.slug`, userID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []OrgRole{}
	for rs.Next() {
		var or OrgRole
		var pr int
		var role string
		if err := rs.Scan(&or.ID, &or.Slug, &or.Name, &pr, &role); err != nil {
			return nil, err
		}
		or.PublicRead, or.Role = pr != 0, Role(role)
		out = append(out, or)
	}
	return out, rs.Err()
}

// grantMembership adds userID to orgID with role, or raises an existing
// membership to role. It never lowers a role.
func (s *store) grantMembership(ctx context.Context, q querier, orgID, userID string, role Role) error {
	cur, err := s.membershipRole(ctx, q, orgID, userID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		_, err = s.exec(ctx, q, `INSERT INTO memberships (org_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`, orgID, userID, string(role), s.ms())
		return err
	case err != nil:
		return err
	case cur.AtLeast(role):
		return nil
	}
	_, err = s.exec(ctx, q, `UPDATE memberships SET role = ? WHERE org_id = ? AND user_id = ?`, string(role), orgID, userID)
	return err
}

// ---- sessions ----

type sessionRow struct {
	ID        string // sha256 hex
	UserID    string
	OrgID     string
	CSRF      string
	CreatedAt int64
	ExpiresAt int64
	LastSeen  int64
}

func (s *store) insertSession(ctx context.Context, q querier, r sessionRow, ip, ua string) error {
	var org any
	if r.OrgID != "" {
		org = r.OrgID
	}
	_, err := s.exec(ctx, q, `INSERT INTO sessions (id, user_id, org_id, csrf, created_at, expires_at, last_seen, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.UserID, org, r.CSRF, r.CreatedAt, r.ExpiresAt, r.LastSeen, clip(ip, 64), clip(ua, 256))
	return err
}

func (s *store) sessionByID(ctx context.Context, q querier, id string) (*sessionRow, error) {
	var r sessionRow
	err := s.row(ctx, q, `SELECT id, user_id, COALESCE(org_id, ''), csrf, created_at, expires_at, last_seen FROM sessions WHERE id = ?`, id).
		Scan(&r.ID, &r.UserID, &r.OrgID, &r.CSRF, &r.CreatedAt, &r.ExpiresAt, &r.LastSeen)
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

func (s *store) touchSession(ctx context.Context, q querier, id string, at int64) error {
	_, err := s.exec(ctx, q, `UPDATE sessions SET last_seen = ? WHERE id = ? AND last_seen < ?`, at, id, at)
	return err
}

func (s *store) setSessionOrg(ctx context.Context, q querier, id, orgID string) error {
	_, err := s.exec(ctx, q, `UPDATE sessions SET org_id = ? WHERE id = ?`, orgID, id)
	return err
}

func (s *store) deleteSession(ctx context.Context, q querier, id string) error {
	_, err := s.exec(ctx, q, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// deleteUserSessions removes every session of userID except keep.
func (s *store) deleteUserSessions(ctx context.Context, q querier, userID, keep string) error {
	_, err := s.exec(ctx, q, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keep)
	return err
}

// purgeExpired drops expired sessions and OAuth states.
func (s *store) purgeExpired(ctx context.Context, q querier, idleCutoff, oauthCutoff int64) error {
	now := s.ms()
	if _, err := s.exec(ctx, q, `DELETE FROM sessions WHERE expires_at <= ? OR last_seen <= ?`, now, idleCutoff); err != nil {
		return err
	}
	_, err := s.exec(ctx, q, `DELETE FROM oauth_states WHERE created_at <= ?`, oauthCutoff)
	return err
}

// ---- API tokens ----

// APIToken is an api_tokens row without its hash.
type APIToken struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	UserID     string     `json:"user_id,omitempty"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       Role       `json:"role"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

const tokenCols = `id, org_id, COALESCE(user_id, ''), name, prefix, role, created_at, last_used_at, expires_at, revoked_at`

func optTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := db.Time(v.Int64)
	return &t
}

func scanToken(r interface{ Scan(...any) error }) (*APIToken, error) {
	var t APIToken
	var role string
	var created int64
	var used, exp, rev sql.NullInt64
	if err := r.Scan(&t.ID, &t.OrgID, &t.UserID, &t.Name, &t.Prefix, &role, &created, &used, &exp, &rev); err != nil {
		return nil, notFound(err)
	}
	t.Role, t.CreatedAt = Role(role), db.Time(created)
	t.LastUsedAt, t.ExpiresAt, t.RevokedAt = optTime(used), optTime(exp), optTime(rev)
	return &t, nil
}

func (s *store) insertToken(ctx context.Context, q querier, t *APIToken, hash string) error {
	var user any
	if t.UserID != "" {
		user = t.UserID
	}
	var exp sql.NullInt64
	if t.ExpiresAt != nil {
		exp = db.NullMillis(*t.ExpiresAt)
	}
	_, err := s.exec(ctx, q, `INSERT INTO api_tokens (id, org_id, user_id, name, prefix, hash, role, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.ID, t.OrgID, user, t.Name, t.Prefix, hash, string(t.Role), t.CreatedAt.UnixMilli(), exp)
	return err
}

// tokenByHash returns a token row and its stored hash.
func (s *store) tokenByHash(ctx context.Context, q querier, hash string) (*APIToken, string, error) {
	var stored string
	r := s.row(ctx, q, `SELECT `+tokenCols+`, hash FROM api_tokens WHERE hash = ?`, hash)
	var t APIToken
	var role string
	var created int64
	var used, exp, rev sql.NullInt64
	if err := r.Scan(&t.ID, &t.OrgID, &t.UserID, &t.Name, &t.Prefix, &role, &created, &used, &exp, &rev, &stored); err != nil {
		return nil, "", notFound(err)
	}
	t.Role, t.CreatedAt = Role(role), db.Time(created)
	t.LastUsedAt, t.ExpiresAt, t.RevokedAt = optTime(used), optTime(exp), optTime(rev)
	return &t, stored, nil
}

func (s *store) tokenByID(ctx context.Context, q querier, orgID, id string) (*APIToken, error) {
	return scanToken(s.row(ctx, q, `SELECT `+tokenCols+` FROM api_tokens WHERE org_id = ? AND id = ?`, orgID, id))
}

// listTokens lists an org's tokens, newest first; userID filters to one
// creator when not empty.
func (s *store) listTokens(ctx context.Context, q querier, orgID, userID string) ([]APIToken, error) {
	query := `SELECT ` + tokenCols + ` FROM api_tokens WHERE org_id = ?`
	args := []any{orgID}
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	rs, err := s.rows(ctx, q, query+` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []APIToken{}
	for rs.Next() {
		t, err := scanToken(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rs.Err()
}

func (s *store) revokeToken(ctx context.Context, q querier, orgID, id string) (bool, error) {
	res, err := s.exec(ctx, q, `UPDATE api_tokens SET revoked_at = ? WHERE org_id = ? AND id = ? AND revoked_at IS NULL`, s.ms(), orgID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *store) touchToken(ctx context.Context, q querier, id string, at, before int64) error {
	_, err := s.exec(ctx, q, `UPDATE api_tokens SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`, at, id, before)
	return err
}

// ---- invitations ----

// Invitation is an invitations row without its token hash.
type Invitation struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	Email      string     `json:"email"`
	Role       Role       `json:"role"`
	CreatedBy  string     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
}

const inviteCols = `id, org_id, email, role, COALESCE(created_by, ''), created_at, expires_at, accepted_at`

func scanInvite(r interface{ Scan(...any) error }) (*Invitation, error) {
	var in Invitation
	var role string
	var created, expires int64
	var accepted sql.NullInt64
	if err := r.Scan(&in.ID, &in.OrgID, &in.Email, &role, &in.CreatedBy, &created, &expires, &accepted); err != nil {
		return nil, notFound(err)
	}
	in.Role, in.CreatedAt, in.ExpiresAt, in.AcceptedAt = Role(role), db.Time(created), db.Time(expires), optTime(accepted)
	return &in, nil
}

func (s *store) insertInvite(ctx context.Context, q querier, in *Invitation, hash string) error {
	var by any
	if in.CreatedBy != "" {
		by = in.CreatedBy
	}
	_, err := s.exec(ctx, q, `INSERT INTO invitations (id, org_id, email, role, token_hash, created_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, in.ID, in.OrgID, in.Email, string(in.Role), hash, by, in.CreatedAt.UnixMilli(), in.ExpiresAt.UnixMilli())
	return err
}

func (s *store) inviteByHash(ctx context.Context, q querier, hash string) (*Invitation, error) {
	return scanInvite(s.row(ctx, q, `SELECT `+inviteCols+` FROM invitations WHERE token_hash = ?`, hash))
}

// pendingInviteForEmail returns the newest unexpired, unaccepted invitation
// for email in any organization.
func (s *store) pendingInviteForEmail(ctx context.Context, q querier, email string) (*Invitation, error) {
	return scanInvite(s.row(ctx, q, `SELECT `+inviteCols+` FROM invitations
		WHERE email = ? AND accepted_at IS NULL AND expires_at > ? ORDER BY created_at DESC, id DESC LIMIT 1`, NormalizeEmail(email), s.ms()))
}

func (s *store) listInvites(ctx context.Context, q querier, orgID string) ([]Invitation, error) {
	rs, err := s.rows(ctx, q, `SELECT `+inviteCols+` FROM invitations WHERE org_id = ? ORDER BY created_at DESC, id DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []Invitation{}
	for rs.Next() {
		in, err := scanInvite(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, *in)
	}
	return out, rs.Err()
}

// deletePendingInvites drops unaccepted invitations of email to orgID, so a
// re-invite replaces the old link.
func (s *store) deletePendingInvites(ctx context.Context, q querier, orgID, email string) error {
	_, err := s.exec(ctx, q, `DELETE FROM invitations WHERE org_id = ? AND email = ? AND accepted_at IS NULL`, orgID, email)
	return err
}

func (s *store) deleteInvite(ctx context.Context, q querier, orgID, id string) (bool, error) {
	res, err := s.exec(ctx, q, `DELETE FROM invitations WHERE org_id = ? AND id = ? AND accepted_at IS NULL`, orgID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// markInviteAccepted sets accepted_at once; false when it was already set.
func (s *store) markInviteAccepted(ctx context.Context, q querier, id string) (bool, error) {
	res, err := s.exec(ctx, q, `UPDATE invitations SET accepted_at = ? WHERE id = ? AND accepted_at IS NULL`, s.ms(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ---- OAuth states ----

func (s *store) insertOAuthState(ctx context.Context, q querier, hash, returnTo string) error {
	_, err := s.exec(ctx, q, `INSERT INTO oauth_states (state_hash, created_at, return_to) VALUES (?, ?, ?)`, hash, s.ms(), returnTo)
	return err
}

// takeOAuthState deletes and returns a state row: each state works once.
func (s *store) takeOAuthState(ctx context.Context, q querier, hash string) (createdAt int64, returnTo string, err error) {
	if err := s.row(ctx, q, `SELECT created_at, return_to FROM oauth_states WHERE state_hash = ?`, hash).Scan(&createdAt, &returnTo); err != nil {
		return 0, "", notFound(err)
	}
	res, err := s.exec(ctx, q, `DELETE FROM oauth_states WHERE state_hash = ?`, hash)
	if err != nil {
		return 0, "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, "", db.ErrNotFound // a concurrent callback took it first
	}
	return createdAt, returnTo, nil
}

// clip makes s valid UTF-8 and cuts it to at most n bytes on a rune
// boundary: header values and user input reach TEXT columns, and Postgres
// refuses invalid UTF-8.
func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
