package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Roles, highest first.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleViewer = "viewer"
)

// ErrLastOwner refuses removing or demoting an organization's only owner.
var ErrLastOwner = errors.New("db: an organization needs at least one owner")

// ValidRole reports whether r is owner, admin, member or viewer.
func ValidRole(r string) bool {
	return r == RoleOwner || r == RoleAdmin || r == RoleMember || r == RoleViewer
}

// RoleRank orders roles: viewer 1, member 2, admin 3, owner 4; unknown 0.
func RoleRank(r string) int {
	switch r {
	case RoleViewer:
		return 1
	case RoleMember:
		return 2
	case RoleAdmin:
		return 3
	case RoleOwner:
		return 4
	}
	return 0
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidSlug reports whether s is a usable org slug: lowercase letters,
// digits and dashes, 1-63 characters, not starting with a dash.
func ValidSlug(s string) bool { return slugPattern.MatchString(s) }

// NormalizeEmail lowercases and trims an address; users.email holds this form.
func NormalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// ---- orgs ----

// Org is a row of orgs.
type Org struct {
	ID         string    `json:"id"`
	Slug       string    `json:"slug"`
	Name       string    `json:"name"`
	PublicRead bool      `json:"public_read"`
	CreatedAt  time.Time `json:"created_at"`
}

// NewOrg is what CreateOrg takes. ID is generated when empty. OwnerID, when
// set, becomes the first owner in the same transaction.
type NewOrg struct {
	ID         string
	Slug       string
	Name       string
	PublicRead bool
	OwnerID    string
}

const orgCols = "id, slug, name, public_read, created_at"

func scanOrg(row interface{ Scan(...any) error }) (*Org, error) {
	var o Org
	var pub int
	var created int64
	if err := row.Scan(&o.ID, &o.Slug, &o.Name, &pub, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	o.PublicRead, o.CreatedAt = pub != 0, Time(created)
	return &o, nil
}

// CreateOrg inserts an organization (and its first owner). A taken slug or
// id is ErrConflict.
func (d *DB) CreateOrg(ctx context.Context, n NewOrg) (*Org, error) {
	if !ValidSlug(n.Slug) {
		return nil, fmt.Errorf("db: org slug %q: lowercase letters, digits and dashes, 1-63 characters", n.Slug)
	}
	if strings.TrimSpace(n.Name) == "" {
		n.Name = n.Slug
	}
	if n.ID == "" {
		n.ID = NewID("org")
	}
	o := &Org{ID: n.ID, Slug: n.Slug, Name: n.Name, PublicRead: n.PublicRead, CreatedAt: Time(Now())}
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, d.Rebind(`INSERT INTO orgs (`+orgCols+`) VALUES (?, ?, ?, ?, ?)`),
			o.ID, o.Slug, o.Name, Bool(o.PublicRead), Millis(o.CreatedAt)); err != nil {
			return err
		}
		if n.OwnerID != "" {
			_, err := tx.ExecContext(ctx, d.Rebind(`INSERT INTO memberships (org_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`),
				o.ID, n.OwnerID, RoleOwner, Millis(o.CreatedAt))
			return err
		}
		return nil
	})
	if err != nil {
		if IsUnique(err) {
			return nil, fmt.Errorf("%w: org %s", ErrConflict, n.Slug)
		}
		if IsForeignKey(err) {
			return nil, fmt.Errorf("%w: user %s", ErrNotFound, n.OwnerID)
		}
		return nil, err
	}
	return o, nil
}

// OrgByID returns an organization or ErrNotFound.
func (d *DB) OrgByID(ctx context.Context, id string) (*Org, error) {
	return scanOrg(d.QueryRow(ctx, `SELECT `+orgCols+` FROM orgs WHERE id = ?`, id))
}

// OrgBySlug returns an organization or ErrNotFound.
func (d *DB) OrgBySlug(ctx context.Context, slug string) (*Org, error) {
	return scanOrg(d.QueryRow(ctx, `SELECT `+orgCols+` FROM orgs WHERE slug = ?`, slug))
}

// ListOrgs returns every organization by slug (instance administration).
func (d *DB) ListOrgs(ctx context.Context) ([]Org, error) {
	rows, err := d.Query(ctx, `SELECT `+orgCols+` FROM orgs ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Org
	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// UserOrg is an organization with the user's role in it.
type UserOrg struct {
	Org
	Role string `json:"role"`
}

// OrgsForUser lists the organizations a user belongs to, by slug.
func (d *DB) OrgsForUser(ctx context.Context, userID string) ([]UserOrg, error) {
	rows, err := d.Query(ctx, `SELECT o.id, o.slug, o.name, o.public_read, o.created_at, m.role
		FROM memberships m JOIN orgs o ON o.id = m.org_id WHERE m.user_id = ? ORDER BY o.slug`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserOrg
	for rows.Next() {
		var u UserOrg
		var pub int
		var created int64
		if err := rows.Scan(&u.ID, &u.Slug, &u.Name, &pub, &created, &u.Role); err != nil {
			return nil, err
		}
		u.PublicRead, u.CreatedAt = pub != 0, Time(created)
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetOrgPublicRead switches anonymous read access (the demo organization).
func (d *DB) SetOrgPublicRead(ctx context.Context, orgID string, public bool) error {
	return expectOne(d.Exec(ctx, `UPDATE orgs SET public_read = ? WHERE id = ?`, Bool(public), orgID))
}

// RenameOrg changes an organization's display name.
func (d *DB) RenameOrg(ctx context.Context, orgID, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("db: org name is empty")
	}
	return expectOne(d.Exec(ctx, `UPDATE orgs SET name = ? WHERE id = ?`, name, orgID))
}

// DeleteOrg deletes an organization: its memberships and every row of the
// tables declared with ON DELETE CASCADE go with it, and its index rows and
// settings are removed. Audit rows stay. A table that references orgs
// without a cascade makes this fail with the foreign key error.
func (d *DB) DeleteOrg(ctx context.Context, orgID string) error {
	return d.Tx(ctx, func(tx *sql.Tx) error {
		if err := d.lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		for _, t := range []string{"idx_runs", "idx_analyses", "idx_rejected", "idx_experiments", "idx_meta", "settings"} {
			if _, err := tx.ExecContext(ctx, d.Rebind(`DELETE FROM `+t+` WHERE org_id = ?`), orgID); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, d.Rebind(`DELETE FROM orgs WHERE id = ?`), orgID)
		return expectOne(res, err)
	})
}

// expectOne turns an UPDATE or DELETE that touched no row into ErrNotFound.
func expectOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- users ----

// User is a row of users. GitHubID "" is NULL (not linked).
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	GitHubID     string    `json:"github_id,omitempty"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
}

const userCols = "id, email, name, password_hash, github_id, disabled, created_at"

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var gh sql.NullString
	var dis int
	var created int64
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &gh, &dis, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.GitHubID, u.Disabled, u.CreatedAt = gh.String, dis != 0, Time(created)
	return &u, nil
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// CreateUser inserts a user. ID is generated when empty, the email is
// normalized, CreatedAt is now. A taken email, GitHub id or id is ErrConflict.
func (d *DB) CreateUser(ctx context.Context, u User) (*User, error) {
	u.Email = NormalizeEmail(u.Email)
	if u.Email == "" || !strings.Contains(u.Email, "@") {
		return nil, fmt.Errorf("db: user email %q is not an address", u.Email)
	}
	if u.ID == "" {
		u.ID = NewID("usr")
	}
	u.CreatedAt = Time(Now())
	_, err := d.Exec(ctx, `INSERT INTO users (`+userCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, u.PasswordHash, nullString(u.GitHubID), Bool(u.Disabled), Millis(u.CreatedAt))
	if err != nil {
		if IsUnique(err) {
			return nil, fmt.Errorf("%w: user %s", ErrConflict, u.Email)
		}
		return nil, err
	}
	return &u, nil
}

// UserByID returns a user or ErrNotFound.
func (d *DB) UserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(d.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// UserByEmail returns a user by address (normalized first) or ErrNotFound.
func (d *DB) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(d.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, NormalizeEmail(email)))
}

// UserByGitHubID returns the user linked to a GitHub account or ErrNotFound.
func (d *DB) UserByGitHubID(ctx context.Context, githubID string) (*User, error) {
	if githubID == "" {
		return nil, ErrNotFound
	}
	return scanUser(d.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE github_id = ?`, githubID))
}

// UpdateUser writes email, name, password hash, GitHub id and disabled of
// u.ID. ErrNotFound when the user does not exist, ErrConflict when the email
// or GitHub id belongs to someone else.
func (d *DB) UpdateUser(ctx context.Context, u User) error {
	u.Email = NormalizeEmail(u.Email)
	if u.Email == "" || !strings.Contains(u.Email, "@") {
		return fmt.Errorf("db: user email %q is not an address", u.Email)
	}
	err := expectOne(d.Exec(ctx, `UPDATE users SET email = ?, name = ?, password_hash = ?, github_id = ?, disabled = ? WHERE id = ?`,
		u.Email, u.Name, u.PasswordHash, nullString(u.GitHubID), Bool(u.Disabled), u.ID))
	if IsUnique(err) {
		return fmt.Errorf("%w: email or GitHub account in use", ErrConflict)
	}
	return err
}

// SetUserPassword replaces a user's password hash.
func (d *DB) SetUserPassword(ctx context.Context, userID, hash string) error {
	return expectOne(d.Exec(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID))
}

// SetUserDisabled disables or re-enables a user.
func (d *DB) SetUserDisabled(ctx context.Context, userID string, disabled bool) error {
	return expectOne(d.Exec(ctx, `UPDATE users SET disabled = ? WHERE id = ?`, Bool(disabled), userID))
}

// LinkGitHub sets (or with "" clears) a user's GitHub account id; one taken
// by another user is ErrConflict.
func (d *DB) LinkGitHub(ctx context.Context, userID, githubID string) error {
	err := expectOne(d.Exec(ctx, `UPDATE users SET github_id = ? WHERE id = ?`, nullString(githubID), userID))
	if IsUnique(err) {
		return fmt.Errorf("%w: GitHub account in use", ErrConflict)
	}
	return err
}

// CountUsers counts users (first-user bootstrap).
func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// ---- memberships ----

// Member is a membership joined with its user.
type Member struct {
	OrgID     string    `json:"org_id"`
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"` // joined
}

// AddMember adds a user to an organization. ErrConflict when already a
// member, ErrNotFound when the org or user does not exist.
func (d *DB) AddMember(ctx context.Context, orgID, userID, role string) error {
	if !ValidRole(role) {
		return fmt.Errorf("db: role %q is not owner, admin, member or viewer", role)
	}
	_, err := d.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`, orgID, userID, role, Now())
	switch {
	case IsUnique(err):
		return fmt.Errorf("%w: membership", ErrConflict)
	case IsForeignKey(err):
		return fmt.Errorf("%w: org or user", ErrNotFound)
	}
	return err
}

// lockOrg serializes membership changes of one org inside tx (Postgres row
// lock; SQLite transactions already hold the write lock). ErrNotFound when
// the org does not exist.
func (d *DB) lockOrg(ctx context.Context, tx *sql.Tx, orgID string) error {
	var id string
	err := tx.QueryRowContext(ctx, d.Rebind(`SELECT id FROM orgs WHERE id = ?`+d.ForUpdate()), orgID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ownerGuard returns ErrLastOwner when userID is the org's only owner.
func (d *DB) ownerGuard(ctx context.Context, tx *sql.Tx, orgID, userID string) (current string, err error) {
	err = tx.QueryRowContext(ctx, d.Rebind(`SELECT role FROM memberships WHERE org_id = ? AND user_id = ?`), orgID, userID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil || current != RoleOwner {
		return current, err
	}
	var owners int
	if err := tx.QueryRowContext(ctx, d.Rebind(`SELECT count(*) FROM memberships WHERE org_id = ? AND role = ?`), orgID, RoleOwner).Scan(&owners); err != nil {
		return current, err
	}
	if owners <= 1 {
		return current, ErrLastOwner
	}
	return current, nil
}

// SetRole changes a member's role. Demoting the only owner is ErrLastOwner;
// a non-member is ErrNotFound.
func (d *DB) SetRole(ctx context.Context, orgID, userID, role string) error {
	if !ValidRole(role) {
		return fmt.Errorf("db: role %q is not owner, admin, member or viewer", role)
	}
	return d.Tx(ctx, func(tx *sql.Tx) error {
		if err := d.lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		if role != RoleOwner {
			if _, err := d.ownerGuard(ctx, tx, orgID, userID); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, d.Rebind(`UPDATE memberships SET role = ? WHERE org_id = ? AND user_id = ?`), role, orgID, userID)
		return expectOne(res, err)
	})
}

// RemoveMember removes a membership. Removing the only owner is
// ErrLastOwner; a non-member is ErrNotFound.
func (d *DB) RemoveMember(ctx context.Context, orgID, userID string) error {
	return d.Tx(ctx, func(tx *sql.Tx) error {
		if err := d.lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		if _, err := d.ownerGuard(ctx, tx, orgID, userID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, d.Rebind(`DELETE FROM memberships WHERE org_id = ? AND user_id = ?`), orgID, userID)
		return expectOne(res, err)
	})
}

// Members lists an organization's members by email.
func (d *DB) Members(ctx context.Context, orgID string) ([]Member, error) {
	rows, err := d.Query(ctx, `SELECT m.org_id, m.user_id, u.email, u.name, m.role, u.disabled, m.created_at
		FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.org_id = ? ORDER BY u.email`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var dis int
		var created int64
		if err := rows.Scan(&m.OrgID, &m.UserID, &m.Email, &m.Name, &m.Role, &dis, &created); err != nil {
			return nil, err
		}
		m.Disabled, m.CreatedAt = dis != 0, Time(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

// RoleOf returns a user's role in an organization, ErrNotFound when the user
// is not a member.
func (d *DB) RoleOf(ctx context.Context, orgID, userID string) (string, error) {
	var role string
	err := d.QueryRow(ctx, `SELECT role FROM memberships WHERE org_id = ? AND user_id = ?`, orgID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

// ---- settings ----

// GetSetting returns a setting of an organization ("" for instance-wide);
// found is false when it was never set.
func (d *DB) GetSetting(ctx context.Context, orgID, key string) (value string, found bool, err error) {
	err = d.QueryRow(ctx, `SELECT value FROM settings WHERE org_id = ? AND key = ?`, orgID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetSetting writes a setting (insert or replace).
func (d *DB) SetSetting(ctx context.Context, orgID, key, value string) error {
	if key == "" {
		return errors.New("db: setting key is empty")
	}
	_, err := d.Exec(ctx, `INSERT INTO settings (org_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (org_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, orgID, key, value, Now())
	return err
}

// DeleteSetting removes a setting; a missing one is not an error.
func (d *DB) DeleteSetting(ctx context.Context, orgID, key string) error {
	_, err := d.Exec(ctx, `DELETE FROM settings WHERE org_id = ? AND key = ?`, orgID, key)
	return err
}

// Settings returns every setting of an organization.
func (d *DB) Settings(ctx context.Context, orgID string) (map[string]string, error) {
	rows, err := d.Query(ctx, `SELECT key, value FROM settings WHERE org_id = ?`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
