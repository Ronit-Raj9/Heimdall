package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// MaxTokenLifetime caps an API token's expiry.
const MaxTokenLifetime = 3650 * 24 * time.Hour

// TokenCreateRequest is the body of POST /auth/tokens.
type TokenCreateRequest struct {
	Name string `json:"name"`
	// Role is the token's role in the current org; at most the creator's.
	// Empty means the creator's own role.
	Role Role `json:"role,omitempty"`
	// ExpiresInDays makes the token expire; 0 never expires.
	ExpiresInDays int `json:"expires_in_days,omitempty"`
}

// TokenCreated is the answer to a token creation: the secret appears here
// once and is never shown again.
type TokenCreated struct {
	Token    string   `json:"token"`
	APIToken APIToken `json:"api_token"`
}

// CreateToken creates an API token in the principal's current org. Only a
// signed-in user (not another token) with role admin may create one, and
// its role can not exceed the creator's.
func (s *Service) CreateToken(ctx context.Context, p *Principal, req TokenCreateRequest, ip string) (*TokenCreated, error) {
	if p.Kind != KindUser {
		return nil, ErrSessionRequired
	}
	if p.OrgID == "" || !p.Can(RoleAdmin) {
		return nil, errForbidden("creating API tokens needs role admin in the current organization")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 100 {
		return nil, errBadRequest("name is required, at most 100 characters")
	}
	role := req.Role
	if role == "" {
		role = p.Role
	}
	if !role.Valid() {
		return nil, errBadRequest("role must be viewer, member, admin or owner")
	}
	if !p.Role.AtLeast(role) {
		return nil, errForbidden("a token's role can not exceed yours (%s)", p.Role)
	}
	if req.ExpiresInDays < 0 || time.Duration(req.ExpiresInDays)*24*time.Hour > MaxTokenLifetime {
		return nil, errBadRequest("expires_in_days must be between 0 (never) and 3650")
	}
	raw, prefix, err := NewAPIToken()
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	t := APIToken{ID: db.NewID("tok"), OrgID: p.OrgID, UserID: p.UserID, Name: name, Prefix: prefix, Role: role,
		CreatedAt: time.UnixMilli(now.UnixMilli()).UTC()}
	if req.ExpiresInDays > 0 {
		exp := time.UnixMilli(now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour).UnixMilli()).UTC()
		t.ExpiresAt = &exp
	}
	if err := s.st.insertToken(ctx, s.st.d.SQL(), &t, HashSecret(raw)); err != nil {
		return nil, fmt.Errorf("create token: %w", err)
	}
	s.record(principalEvent(p, "auth.token_create", "api_token", t.ID, ip, map[string]any{"name": name, "role": string(role), "prefix": prefix}))
	return &TokenCreated{Token: raw, APIToken: t}, nil
}

// ListTokens lists the current org's API tokens (admin only), without
// secrets or hashes.
func (s *Service) ListTokens(ctx context.Context, p *Principal) ([]APIToken, error) {
	if p.OrgID == "" || !p.Can(RoleAdmin) || p.Kind == KindRunner {
		return nil, errForbidden("listing API tokens needs role admin in the current organization")
	}
	return s.st.listTokens(ctx, s.st.d.SQL(), p.OrgID, "")
}

// RevokeToken revokes a token of the current org (admin only). Revoking a
// token that is already revoked, or of another org, is not found.
func (s *Service) RevokeToken(ctx context.Context, p *Principal, id, ip string) error {
	if p.OrgID == "" || !p.Can(RoleAdmin) || p.Kind == KindRunner {
		return errForbidden("revoking API tokens needs role admin in the current organization")
	}
	ok, err := s.st.revokeToken(ctx, s.st.d.SQL(), p.OrgID, id)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("API token not found: %s", id)
	}
	s.record(principalEvent(p, "auth.token_revoke", "api_token", id, ip, nil))
	return nil
}

// tokenPrincipal verifies an hm_ token. Unknown, malformed, revoked and
// expired tokens all return ErrInvalidToken. A token created by a user is
// worth at most that user's current role in the org: demoting or removing
// the user narrows or kills their tokens; a disabled user's tokens stop.
func (s *Service) tokenPrincipal(ctx context.Context, raw string) (*Principal, error) {
	prefix, ok := ParseAPIToken(raw)
	if !ok {
		return nil, ErrInvalidToken
	}
	hash := HashSecret(raw)
	t, stored, err := s.st.tokenByHash(ctx, s.st.d.SQL(), hash)
	if errors.Is(err, db.ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if !equalHashes(stored, hash) || t.Prefix != prefix || t.RevokedAt != nil || (t.ExpiresAt != nil && !now.Before(*t.ExpiresAt)) {
		return nil, ErrInvalidToken
	}
	role := t.Role
	p := &Principal{Kind: KindToken, TokenID: t.ID, OrgID: t.OrgID, UserID: t.UserID}
	if t.UserID != "" {
		u, err := s.st.userByID(ctx, s.st.d.SQL(), t.UserID)
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrInvalidToken
		}
		if err != nil {
			return nil, err
		}
		if u.Disabled {
			return nil, ErrInvalidToken
		}
		cur, err := s.st.membershipRole(ctx, s.st.d.SQL(), t.OrgID, t.UserID)
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrInvalidToken
		}
		if err != nil {
			return nil, err
		}
		if !cur.AtLeast(role) {
			role = cur
		}
		p.Email = u.Email
	}
	if !role.Valid() {
		return nil, ErrInvalidToken
	}
	p.Role = role
	nowMS := now.UnixMilli()
	if t.LastUsedAt == nil || nowMS-t.LastUsedAt.UnixMilli() >= tokenTouchEvery.Milliseconds() {
		if err := s.st.touchToken(ctx, s.st.d.SQL(), t.ID, nowMS, nowMS-tokenTouchEvery.Milliseconds()); err != nil {
			s.logger.Warn(fmt.Sprintf("auth: touch token: %v", err))
		}
	}
	return p, nil
}

// RunnerVerifier verifies a runner token (hmr_...). The runners area
// registers one; it returns a principal of kind runner scoped to the
// token's org and pool, or an error for a bad token.
type RunnerVerifier func(ctx context.Context, token string) (*Principal, error)

var runnerVerifier atomic.Pointer[RunnerVerifier]

// RegisterRunnerVerifier installs the runner token verifier. Until one is
// registered, every hmr_ token is rejected.
func RegisterRunnerVerifier(v RunnerVerifier) {
	if v == nil {
		runnerVerifier.Store(nil)
		return
	}
	runnerVerifier.Store(&v)
}

// verifyRunner hands an hmr_ token to the registered verifier and insists
// on what it returns: a runner principal with an org.
func verifyRunner(ctx context.Context, raw string) (*Principal, error) {
	v := runnerVerifier.Load()
	if v == nil {
		return nil, ErrInvalidToken
	}
	p, err := (*v)(ctx, raw)
	if err != nil || p == nil {
		if e, ok := AsError(err); ok && e.Status >= 500 {
			return nil, err
		}
		return nil, ErrInvalidToken
	}
	if p.Kind != KindRunner || p.OrgID == "" {
		return nil, ErrInvalidToken
	}
	return p, nil
}
