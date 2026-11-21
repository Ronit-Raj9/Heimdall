package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// Account safety (L22): an org owner makes a one-time password reset link
// for a member who lost their password. The server sends no email; the
// owner passes the link on, as with invitations. The link is the only
// credential: it is shown once, stored as its sha256, works once and
// expires after ResetTTL.

// ResetPath is the UI page a reset link opens; the token is its ?token=.
const ResetPath = "/reset-password"

// ResetTTL is how long a reset link stays usable.
const ResetTTL = 24 * time.Hour

// ErrInvalidReset answers an unknown, expired or used reset token. It never
// says which, so a probe learns nothing.
var ErrInvalidReset = &Error{Status: 404, Type: "not_found", Msg: "reset link not found, expired or already used; ask your organization owner for a new one"}

// ResetLink is the answer to creating a reset link: the token and URL
// appear here once.
type ResetLink struct {
	Token     string    `json:"token"`
	URL       string    `json:"url"`
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ResetInfo is what the reset page shows before the new password is set.
type ResetInfo struct {
	Email     string    `json:"email"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ResetRequest is the body of POST /auth/reset/{token}.
type ResetRequest struct {
	Password string `json:"password"`
}

// CreateResetLink makes a reset link for userID, a member of orgID. The
// caller must be a signed-in user who is owner of orgID and owner of every
// other organization the target belongs to: otherwise an owner of one org
// could take over an account that also holds rights elsewhere. A pending
// link of the same user is replaced. Owners reset their own password from
// Settings, not here.
func (s *Service) CreateResetLink(ctx context.Context, p *Principal, orgID, userID, ip string) (*ResetLink, error) {
	if p == nil || p.Kind != KindUser || p.UserID == "" {
		return nil, errForbidden("reset links are made by a signed-in owner (not an API token)")
	}
	if orgID == "" || userID == "" {
		return nil, errBadRequest("organization and user are required")
	}
	if userID == p.UserID {
		return nil, errBadRequest("this is your own account; change your password in Settings instead")
	}
	raw, err := randomSecret()
	if err != nil {
		return nil, err
	}
	now := time.UnixMilli(s.now().UnixMilli()).UTC()
	exp := now.Add(ResetTTL)
	id := db.NewID("pwr")
	var target *User
	err = s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.st.lockOrgRow(ctx, tx, orgID); err != nil {
			return err
		}
		role, err := s.st.membershipRole(ctx, tx, orgID, p.UserID)
		if errors.Is(err, db.ErrNotFound) || (err == nil && role != RoleOwner) {
			return errForbidden("only an owner of the organization can make reset links")
		}
		if err != nil {
			return err
		}
		if _, err := s.st.membershipRole(ctx, tx, orgID, userID); errors.Is(err, db.ErrNotFound) {
			return errNotFound("that user is not a member of this organization")
		} else if err != nil {
			return err
		}
		u, err := s.st.lockUser(ctx, tx, userID)
		if errors.Is(err, db.ErrNotFound) {
			return errNotFound("user not found")
		}
		if err != nil {
			return err
		}
		if u.Disabled {
			return errConflict("that account is disabled; a server operator must enable it first")
		}
		theirs, err := s.st.memberships(ctx, tx, userID)
		if err != nil {
			return err
		}
		for _, o := range theirs {
			if o.ID == orgID {
				continue
			}
			r, err := s.st.membershipRole(ctx, tx, o.ID, p.UserID)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return err
			}
			if r != RoleOwner {
				return errForbidden("that user also belongs to another organization you do not own; a server operator can reset it with `bifrost-bench user password`")
			}
		}
		if _, err := s.st.exec(ctx, tx, `DELETE FROM password_resets WHERE user_id = ? AND used_at IS NULL`, userID); err != nil {
			return err
		}
		if _, err := s.st.exec(ctx, tx, `INSERT INTO password_resets (id, user_id, org_id, token_hash, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, userID, orgID, HashSecret(raw), p.UserID, now.UnixMilli(), exp.UnixMilli()); err != nil {
			return err
		}
		target = u
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.record(AuditEvent{OrgID: orgID, ActorType: "user", ActorID: p.UserID, Action: "auth.reset_link_create", TargetType: "user", TargetID: userID, IP: ip,
		Details: map[string]any{"reset_id": id, "expires_at": exp.UnixMilli()}})
	return &ResetLink{Token: raw, URL: s.resetURL(raw), UserID: userID, Email: target.Email, ExpiresAt: exp}, nil
}

func (s *Service) resetURL(token string) string {
	return trimSlash(s.cfg.PublicURL) + ResetPath + "?token=" + url.QueryEscape(token)
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

type resetRow struct {
	id, userID, orgID string
	expiresAt         int64
}

func (s *Service) pendingReset(ctx context.Context, q querier, token string) (*resetRow, error) {
	if token == "" || len(token) > 128 {
		return nil, ErrInvalidReset
	}
	var r resetRow
	err := s.st.row(ctx, q, `SELECT id, user_id, org_id, expires_at FROM password_resets WHERE token_hash = ? AND used_at IS NULL`, HashSecret(token)).
		Scan(&r.id, &r.userID, &r.orgID, &r.expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidReset
	}
	if err != nil {
		return nil, err
	}
	if r.expiresAt <= s.st.ms() {
		return nil, ErrInvalidReset
	}
	return &r, nil
}

// LookupReset returns what the reset page shows for token. Rate limited per
// IP like the other token probes.
func (s *Service) LookupReset(ctx context.Context, token, ip string) (*ResetInfo, error) {
	if ok, wait := s.probeIP.Allow(ip); !ok {
		return nil, errRateLimited(wait)
	}
	r, err := s.pendingReset(ctx, s.st.d.SQL(), token)
	if err != nil {
		return nil, err
	}
	u, err := s.st.userByID(ctx, s.st.d.SQL(), r.userID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, ErrInvalidReset
	}
	if err != nil {
		return nil, err
	}
	return &ResetInfo{Email: u.Email, ExpiresAt: db.Time(r.expiresAt)}, nil
}

// ResetPassword sets a new password with a reset token. The token is used
// up in the same transaction that writes the password, so it works exactly
// once; every session of the user ends.
func (s *Service) ResetPassword(ctx context.Context, token string, req ResetRequest, ip string) error {
	if ok, wait := s.probeIP.Allow(ip); !ok {
		return errRateLimited(wait)
	}
	r, err := s.pendingReset(ctx, s.st.d.SQL(), token)
	if err != nil {
		return err
	}
	u, err := s.st.userByID(ctx, s.st.d.SQL(), r.userID)
	if errors.Is(err, db.ErrNotFound) {
		return ErrInvalidReset
	}
	if err != nil {
		return err
	}
	if err := CheckPasswordPolicy(req.Password, u.Email); err != nil {
		// The token stays usable: the user fixes the password and retries.
		return errBadRequest("%s", err.Error())
	}
	hash, err := s.hasher.Hash(req.Password)
	if err != nil {
		return err
	}
	err = s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		res, err := s.st.exec(ctx, tx, `UPDATE password_resets SET used_at = ? WHERE id = ? AND used_at IS NULL AND expires_at > ?`, s.st.ms(), r.id, s.st.ms())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrInvalidReset
		}
		if err := s.st.setPasswordHash(ctx, tx, r.userID, hash); err != nil {
			return err
		}
		return s.st.deleteUserSessions(ctx, tx, r.userID, "")
	})
	if err != nil {
		return err
	}
	s.record(userEvent(u, r.orgID, "auth.password_reset", ip, map[string]any{"reset_id": r.id}))
	return nil
}
