package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// InvitePath is the UI path an invitation link opens; the token follows.
const InvitePath = "/invite/"

// InvitationCreated is the answer to an invitation: the token and link
// appear here once. The server sends no email; the inviter passes the link
// on.
type InvitationCreated struct {
	Token      string     `json:"token"`
	URL        string     `json:"url"`
	Invitation Invitation `json:"invitation"`
}

// InvitationInfo is GET /auth/invitations/{token}: enough for the
// acceptance page, nothing about other members.
type InvitationInfo struct {
	OrgName   string    `json:"org_name"`
	OrgSlug   string    `json:"org_slug"`
	Email     string    `json:"email"`
	Role      Role      `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
	// AccountExists tells the page whether to offer sign-in or signup. The
	// token holder already knows the address it was sent to.
	AccountExists bool `json:"account_exists"`
	// SignupAllowed is false under signup closed: only existing accounts
	// may accept.
	SignupAllowed bool `json:"signup_allowed"`
}

// CreateInvitation invites email to orgID with role. The inviter must be
// admin or owner of orgID and can not grant a role above their own. A
// pending invitation for the same address in the org is replaced.
func (s *Service) CreateInvitation(ctx context.Context, inviter *Principal, orgID, email string, role Role, ip string) (*InvitationCreated, error) {
	if inviter == nil || inviter.OrgID != orgID || !inviter.Can(RoleAdmin) || inviter.Kind == KindRunner {
		return nil, errForbidden("inviting members needs role admin in the organization")
	}
	if role.Valid() && !inviter.Role.AtLeast(role) {
		return nil, errForbidden("you can not invite with a role above yours (%s)", inviter.Role)
	}
	createdBy := inviter.UserID
	if createdBy == "" {
		createdBy = inviter.ActorID()
	}
	return s.CreateInvitationFor(ctx, orgID, email, role, createdBy, inviter.ActorType(), ip)
}

// CreateInvitationFor creates an invitation for a caller whose permission
// the caller already checked (the members handler, behind the tenancy
// policy): it validates the email and role, replaces a pending invitation
// of the same address, and writes the auth.invite_create audit row with
// createdBy as the actor (actorType user, token ...).
func (s *Service) CreateInvitationFor(ctx context.Context, orgID, email string, role Role, createdBy, actorType, ip string) (*InvitationCreated, error) {
	email = NormalizeEmail(email)
	if !validEmail(email) {
		return nil, errBadRequest("a valid email address is required")
	}
	if !role.Valid() {
		return nil, errBadRequest("role must be viewer, member, admin or owner")
	}
	if orgID == "" {
		return nil, errBadRequest("organization is required")
	}
	raw, err := randomSecret()
	if err != nil {
		return nil, err
	}
	now := time.UnixMilli(s.now().UnixMilli()).UTC()
	in := Invitation{ID: db.NewID("inv"), OrgID: orgID, Email: email, Role: role, CreatedBy: createdBy,
		CreatedAt: now, ExpiresAt: now.Add(s.cfg.InviteTTL)}
	err = s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		// One pending invitation per org and address: lock the org row so
		// concurrent re-invites on Postgres (READ COMMITTED) see each
		// other's INSERT before their DELETE, instead of both inserting.
		if err := s.st.lockOrgRow(ctx, tx, orgID); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return errNotFound("organization not found")
			}
			return err
		}
		if err := s.st.deletePendingInvites(ctx, tx, orgID, email); err != nil {
			return err
		}
		return s.st.insertInvite(ctx, tx, &in, HashSecret(raw))
	})
	if err != nil {
		return nil, fmt.Errorf("create invitation: %w", err)
	}
	if actorType == "" {
		actorType = "user"
	}
	s.record(AuditEvent{OrgID: orgID, ActorType: actorType, ActorID: createdBy, Action: "auth.invite_create", TargetType: "invitation", TargetID: in.ID, IP: ip,
		Details: map[string]any{"email": email, "role": string(role)}})
	return &InvitationCreated{Token: raw, URL: s.cfg.PublicURL + InvitePath + raw, Invitation: in}, nil
}

// ListInvitations lists orgID's invitations, newest first. The caller (the
// members handler) checks the caller's role.
func (s *Service) ListInvitations(ctx context.Context, orgID string) ([]Invitation, error) {
	return s.st.listInvites(ctx, s.st.d.SQL(), orgID)
}

// RevokeInvitation deletes a pending invitation of orgID; p must be admin
// or owner there.
func (s *Service) RevokeInvitation(ctx context.Context, p *Principal, orgID, id, ip string) error {
	if p == nil || p.OrgID != orgID || !p.Can(RoleAdmin) || p.Kind == KindRunner {
		return errForbidden("revoking invitations needs role admin in the organization")
	}
	return s.RevokeInvitationFor(ctx, orgID, id, p.ActorType(), p.ActorID(), ip)
}

// RevokeInvitationFor deletes a pending invitation for a caller whose
// permission was already checked, and audits auth.invite_revoke.
func (s *Service) RevokeInvitationFor(ctx context.Context, orgID, id, actorType, actorID, ip string) error {
	ok, err := s.st.deleteInvite(ctx, s.st.d.SQL(), orgID, id)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("invitation not found: %s", id)
	}
	s.record(AuditEvent{OrgID: orgID, ActorType: actorType, ActorID: actorID, Action: "auth.invite_revoke", TargetType: "invitation", TargetID: id, IP: ip})
	return nil
}

// pendingInvite looks a token up and insists it is unaccepted and unexpired.
func (s *Service) pendingInvite(ctx context.Context, q querier, raw string) (*Invitation, error) {
	if raw == "" || len(raw) > 128 {
		return nil, ErrInvalidInvitation
	}
	in, err := s.st.inviteByHash(ctx, q, HashSecret(raw))
	if errors.Is(err, db.ErrNotFound) {
		return nil, ErrInvalidInvitation
	}
	if err != nil {
		return nil, err
	}
	if in.AcceptedAt != nil || !s.now().Before(in.ExpiresAt) {
		return nil, ErrInvalidInvitation
	}
	return in, nil
}

// InvitationInfo describes a pending invitation for its acceptance page.
// Rate limited per IP like every credential lookup.
func (s *Service) InvitationInfo(ctx context.Context, raw, ip string) (*InvitationInfo, error) {
	if ok, wait := s.probeIP.Allow(ip); !ok {
		return nil, errRateLimited(wait)
	}
	in, err := s.pendingInvite(ctx, s.st.d.SQL(), raw)
	if err != nil {
		return nil, err
	}
	o, err := s.st.orgByID(ctx, s.st.d.SQL(), in.OrgID)
	if err != nil {
		return nil, ErrInvalidInvitation
	}
	_, uerr := s.st.userByEmail(ctx, s.st.d.SQL(), in.Email)
	return &InvitationInfo{OrgName: o.Name, OrgSlug: o.Slug, Email: in.Email, Role: in.Role, ExpiresAt: in.ExpiresAt,
		AccountExists: uerr == nil, SignupAllowed: s.cfg.Signup != SignupClosed}, nil
}

// AcceptInvitation adds the signed-in user to the invitation's org with
// its role (an existing membership is raised, never lowered), marks the
// invitation used and, for a session, makes that org current. The user's
// email must be the invited address.
func (s *Service) AcceptInvitation(ctx context.Context, p *Principal, raw, ip string) (*MeOrg, error) {
	if ok, wait := s.probeIP.Allow(ip); !ok {
		return nil, errRateLimited(wait)
	}
	if p.Kind != KindUser {
		return nil, ErrUnauthenticated
	}
	var (
		in   *Invitation
		org  *Org
		role Role
	)
	err := s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		in, err = s.pendingInvite(ctx, tx, raw)
		if err != nil {
			return err
		}
		u, err := s.st.userByID(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		if u.Email != in.Email {
			return ErrInvitationEmail
		}
		ok, err := s.st.markInviteAccepted(ctx, tx, in.ID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidInvitation
		}
		if err := s.st.grantMembership(ctx, tx, in.OrgID, u.ID, in.Role); err != nil {
			return err
		}
		if role, err = s.st.membershipRole(ctx, tx, in.OrgID, u.ID); err != nil {
			return err
		}
		if org, err = s.st.orgByID(ctx, tx, in.OrgID); err != nil {
			return err
		}
		if p.sessionID != "" {
			return s.st.setSessionOrg(ctx, tx, p.sessionID, in.OrgID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.record(AuditEvent{OrgID: in.OrgID, ActorType: "user", ActorID: p.UserID, Action: "auth.invite_accept", TargetType: "invitation", TargetID: in.ID, IP: ip,
		Details: map[string]any{"role": string(in.Role)}})
	return &MeOrg{ID: org.ID, Slug: org.Slug, Name: org.Name, Role: role}, nil
}
