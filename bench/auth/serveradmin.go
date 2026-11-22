package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// Server admins (audit B1). A server admin lists every user of every tenant,
// disables users, edits any org's quota and flips the kill switch, so the
// role never comes from an unverified email match. A signed-in user is a
// server admin when:
//
//   - their user id is listed in HEIMDALL_SERVER_ADMIN_IDS, or
//   - an operator granted it with `bifrost-bench admin grant -email X`
//     (server_admin_grants, source cli), or
//   - their email is listed in HEIMDALL_SERVER_ADMINS and verified. The
//     first time that happens the address is bound to the user id
//     (source email), which is the migration path for an existing admin:
//     verify the address once and the next request binds it. A bound
//     grant applies only while the address is still listed.
//
// API tokens, runners, anonymous and local principals are never server
// admins.

// ServerAdminsEnv lists the server admins' emails, comma-separated. A listed
// email counts only once verified.
const ServerAdminsEnv = "HEIMDALL_SERVER_ADMINS"

// ServerAdminIDsEnv lists server admins by user id, comma-separated.
const ServerAdminIDsEnv = "HEIMDALL_SERVER_ADMIN_IDS"

// Grant sources in server_admin_grants.source.
const (
	GrantCLI   = "cli"
	GrantEmail = "email"
)

var (
	serverAdminsOnce sync.Once
	serverAdmins     map[string]bool
	serverAdminIDs   map[string]bool
)

func loadServerAdmins() {
	serverAdmins, serverAdminIDs = splitSet(os.Getenv(ServerAdminsEnv), true), splitSet(os.Getenv(ServerAdminIDsEnv), false)
}

func splitSet(v string, lower bool) map[string]bool {
	out := map[string]bool{}
	for _, e := range strings.Split(v, ",") {
		e = strings.TrimSpace(e)
		if lower {
			e = strings.ToLower(e)
		}
		if e != "" {
			out[e] = true
		}
	}
	return out
}

// IsServerAdmin reports whether p is a signed-in user who is a server admin:
// listed by user id, or resolved as one when the session was read (a grant,
// or a listed and verified email). An email match alone is never enough.
func IsServerAdmin(p *Principal) bool {
	if p == nil || p.Kind != KindUser || p.UserID == "" {
		return false
	}
	return p.serverAdmin || isServerAdminID(p.UserID)
}

func isServerAdminID(id string) bool {
	serverAdminsOnce.Do(loadServerAdmins)
	return id != "" && serverAdminIDs[id]
}

func isServerAdminEmail(email string) bool {
	serverAdminsOnce.Do(loadServerAdmins)
	email = strings.ToLower(strings.TrimSpace(email))
	return email != "" && serverAdmins[email]
}

// resetServerAdminsForTest rereads the environment (tests only).
func resetServerAdminsForTest() { serverAdminsOnce = sync.Once{} }

// grantRow is a server_admin_grants row.
type grantRow struct {
	email, source string
}

func (s *store) grantFor(ctx context.Context, q querier, userID string) (*grantRow, error) {
	var g grantRow
	err := s.row(ctx, q, `SELECT email, source FROM server_admin_grants WHERE user_id = ?`, userID).Scan(&g.email, &g.source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// grantApplies reports whether g makes u a server admin now.
func grantApplies(g *grantRow, u *User) bool {
	switch {
	case g == nil:
		return false
	case g.source == GrantCLI:
		return true
	case g.source == GrantEmail:
		return g.email == u.Email && isServerAdminEmail(u.Email)
	}
	return false
}

// serverAdminFor decides whether u (whose email verification is verified)
// is a server admin, binding a listed verified email to u on first sight.
func (s *Service) serverAdminFor(ctx context.Context, u *User, verified bool) (bool, error) {
	if isServerAdminID(u.ID) {
		return true, nil
	}
	g, err := s.st.grantFor(ctx, s.st.d.SQL(), u.ID)
	if err != nil {
		return false, err
	}
	if grantApplies(g, u) {
		return true, nil
	}
	if !verified || !isServerAdminEmail(u.Email) {
		return false, nil
	}
	if g == nil || g.source == GrantEmail {
		// First verified sign-in of a listed address: bind it to this id.
		if _, err := s.st.exec(ctx, s.st.d.SQL(), `DELETE FROM server_admin_grants WHERE user_id = ? AND source = ?`, u.ID, GrantEmail); err != nil {
			return false, err
		}
		if _, err := s.st.exec(ctx, s.st.d.SQL(), `INSERT INTO server_admin_grants (user_id, email, source, granted_by, granted_at) VALUES (?, ?, ?, ?, ?)`,
			u.ID, u.Email, GrantEmail, ServerAdminsEnv, s.st.ms()); err != nil && !db.IsUnique(err) {
			return false, err
		}
		s.record(AuditEvent{ActorType: "system", ActorID: ServerAdminsEnv, Action: "auth.server_admin_bound", TargetType: "user", TargetID: u.ID,
			Details: map[string]any{"email": u.Email, "source": GrantEmail}})
	}
	return true, nil
}

// ServerAdminEntry is one server admin as `bifrost-bench admin list` and
// the admin users page see it.
type ServerAdminEntry struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Source   string `json:"source"` // cli | email | ids_env | email_env (verified, not yet bound)
	Verified bool   `json:"verified"`
}

// ServerAdmins lists every user who is a server admin now, from the grants,
// HEIMDALL_SERVER_ADMIN_IDS and verified HEIMDALL_SERVER_ADMINS addresses.
func ServerAdmins(ctx context.Context, d *db.DB) ([]ServerAdminEntry, error) {
	st := &store{d: d, now: time.Now}
	rows, err := st.rows(ctx, d.SQL(), `SELECT users.id, users.email, users.name, users.password_hash, COALESCE(users.github_id, ''), users.disabled, users.created_at,
		COALESCE(g.source, ''), COALESCE(g.email, ''), COALESCE(v.email, '')
		FROM users LEFT JOIN server_admin_grants g ON g.user_id = users.id LEFT JOIN email_verifications v ON v.user_id = users.id
		ORDER BY users.created_at, users.id`)
	if err != nil {
		return nil, fmt.Errorf("list server admins: %w", err)
	}
	defer rows.Close()
	out := []ServerAdminEntry{}
	for rows.Next() {
		var u User
		var disabled int
		var created int64
		var source, gEmail, vEmail string
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.GitHubID, &disabled, &created, &source, &gEmail, &vEmail); err != nil {
			return nil, err
		}
		verified := u.GitHubID != "" || (vEmail != "" && vEmail == u.Email)
		var g *grantRow
		if source != "" {
			g = &grantRow{email: gEmail, source: source}
		}
		e := ServerAdminEntry{UserID: u.ID, Email: u.Email, Verified: verified}
		switch {
		case isServerAdminID(u.ID):
			e.Source = "ids_env"
		case grantApplies(g, &u):
			e.Source = source
		case verified && isServerAdminEmail(u.Email):
			e.Source = "email_env"
		default:
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GrantServerAdmin makes the user with that email a server admin by user
// id (`bifrost-bench admin grant`). It works whether or not the email is
// verified: the operator vouches for the account.
func GrantServerAdmin(ctx context.Context, d *db.DB, email string) (*User, error) {
	st := &store{d: d, now: time.Now}
	var u *User
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		u, err = st.userByEmail(ctx, tx, email)
		if errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("no user with email %s; the account must exist first (sign up, or bifrost-bench user create)", NormalizeEmail(email))
		}
		if err != nil {
			return err
		}
		if _, err := st.exec(ctx, tx, `DELETE FROM server_admin_grants WHERE user_id = ?`, u.ID); err != nil {
			return err
		}
		_, err = st.exec(ctx, tx, `INSERT INTO server_admin_grants (user_id, email, source, granted_by, granted_at) VALUES (?, ?, ?, ?, ?)`,
			u.ID, u.Email, GrantCLI, "cli", st.ms())
		return err
	})
	if err != nil {
		return nil, err
	}
	if _, err := d.AppendAudit(ctx, db.AuditEntry{ActorType: db.ActorSystem, ActorID: "cli", Action: "auth.server_admin_grant",
		TargetType: "user", TargetID: u.ID, Details: db.AuditDetails(map[string]any{"via": "cli", "email": u.Email})}); err != nil {
		return u, fmt.Errorf("%w: %s granted but its audit row was not written: %v", ErrAuditFailed, u.ID, err)
	}
	return u, nil
}

// RevokeServerAdmin drops the user's grant (`bifrost-bench admin revoke`).
// An address still listed in HEIMDALL_SERVER_ADMINS binds again on the
// user's next verified sign-in; remove it from the list too. It reports
// whether a grant existed.
func RevokeServerAdmin(ctx context.Context, d *db.DB, email string) (*User, bool, error) {
	st := &store{d: d, now: time.Now}
	u, err := st.userByEmail(ctx, d.SQL(), email)
	if errors.Is(err, db.ErrNotFound) {
		return nil, false, fmt.Errorf("no user with email %s", NormalizeEmail(email))
	}
	if err != nil {
		return nil, false, err
	}
	res, err := st.exec(ctx, d.SQL(), `DELETE FROM server_admin_grants WHERE user_id = ?`, u.ID)
	if err != nil {
		return nil, false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if _, err := d.AppendAudit(ctx, db.AuditEntry{ActorType: db.ActorSystem, ActorID: "cli", Action: "auth.server_admin_revoke",
			TargetType: "user", TargetID: u.ID, Details: db.AuditDetails(map[string]any{"via": "cli", "email": u.Email})}); err != nil {
			return u, true, fmt.Errorf("%w: %s revoked but its audit row was not written: %v", ErrAuditFailed, u.ID, err)
		}
	}
	return u, n > 0, nil
}

// ListedServerAdminEmail reports whether email is in HEIMDALL_SERVER_ADMINS
// (for CLI hints only; it grants nothing).
func ListedServerAdminEmail(email string) bool { return isServerAdminEmail(email) }
