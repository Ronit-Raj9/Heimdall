package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Throttles for bookkeeping writes on every authenticated request.
const (
	sessionTouchEvery = time.Minute
	tokenTouchEvery   = 5 * time.Minute
	purgeEvery        = 10 * time.Minute
	oauthStateTTL     = 10 * time.Minute
	dbTimeout         = 10 * time.Second
)

// AuditEvent is one audit_log row written by the auth area.
type AuditEvent struct {
	OrgID      string
	ActorType  string // user | token | runner | system | anonymous
	ActorID    string
	Action     string // auth.login, auth.login_failed, auth.token_create ...
	TargetType string
	TargetID   string
	IP         string
	Details    map[string]any
}

// Auditor records audit events. The database area's sink can be adapted to
// it; with none, Service writes audit_log rows itself.
type Auditor interface {
	Audit(ctx context.Context, e AuditEvent) error
}

// Deps are what a Service runs on.
type Deps struct {
	DB *db.DB
	// Audit receives every auth event; nil writes audit_log directly.
	Audit Auditor
	// Hasher hashes passwords; nil uses DefaultArgon2Params.
	Hasher *Hasher
	// HTTPClient talks to GitHub; nil uses a client with a 10 s timeout.
	HTTPClient *http.Client
	// Now is the clock; nil is time.Now.
	Now    func() time.Time
	Logger schemas.Logger
}

// Service is the hosted-mode auth area: sessions, logins, signups, API
// tokens, invitations and GitHub sign-in. Safe for concurrent use.
type Service struct {
	cfg    Config
	st     *store
	audit  Auditor
	hasher *Hasher
	http   *http.Client
	now    func() time.Time
	logger schemas.Logger

	loginIP, loginEmail, signupIP, signupEmail, probeIP *Limiter

	purgeMu   sync.Mutex
	lastPurge time.Time
}

// New builds the service. cfg must be hosted; deps.DB must be migrated.
func New(cfg Config, deps Deps) (*Service, error) {
	if !cfg.Hosted {
		return nil, errors.New("auth: New needs auth.mode hosted; local mode needs no service")
	}
	if deps.DB == nil {
		return nil, errors.New("auth: database is required in hosted mode")
	}
	if cfg.PublicURL == "" {
		return nil, errors.New("auth: public_url is required in hosted mode")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	s := &Service{
		cfg:    cfg,
		st:     &store{d: deps.DB, now: now},
		audit:  deps.Audit,
		hasher: deps.Hasher,
		http:   deps.HTTPClient,
		now:    now,
		logger: deps.Logger,
	}
	if s.hasher == nil {
		s.hasher = NewHasher(DefaultArgon2Params)
	}
	if s.http == nil {
		s.http = &http.Client{Timeout: 10 * time.Second}
	}
	if s.logger == nil {
		s.logger = schemas.NoopLogger{}
	}
	if s.audit == nil {
		s.audit = &sqlAuditor{st: s.st}
	}
	if cfg.GitHub == nil && cfg.GitHubOff != "" {
		s.logger.Info(cfg.GitHubOff)
	}
	if s.cfg.SessionTTL <= 0 {
		s.cfg.SessionTTL = 168 * time.Hour
	}
	if s.cfg.SessionIdle <= 0 || s.cfg.SessionIdle > s.cfg.SessionTTL {
		s.cfg.SessionIdle = s.cfg.SessionTTL
	}
	if s.cfg.InviteTTL <= 0 {
		s.cfg.InviteTTL = 168 * time.Hour
	}
	s.loginIP = NewLimiter(LoginPerIP, now)
	s.loginEmail = NewLimiter(LoginPerEmail, now)
	s.signupIP = NewLimiter(SignupPerIP, now)
	s.signupEmail = NewLimiter(SignupPerEmail, now)
	s.probeIP = NewLimiter(TokenPerIP, now)
	return s, nil
}

// Config returns the resolved configuration.
func (s *Service) Config() Config { return s.cfg }

func dbCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), dbTimeout)
}

// sqlAuditor writes audit rows through the database area's audit API.
type sqlAuditor struct{ st *store }

func (a *sqlAuditor) Audit(ctx context.Context, e AuditEvent) error {
	var details json.RawMessage
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return fmt.Errorf("audit details: %w", err)
		}
		details = b
	}
	_, err := a.st.d.AppendAudit(ctx, db.AuditEntry{OrgID: e.OrgID, ActorType: e.ActorType, ActorID: e.ActorID, Action: e.Action,
		TargetType: e.TargetType, TargetID: e.TargetID, IP: clip(e.IP, 64), At: a.st.now().UTC(), Details: details})
	return err
}

// record writes an audit event; a failure is logged, never shown to the
// caller (the action itself already happened).
func (s *Service) record(e AuditEvent) {
	ctx, cancel := dbCtx()
	defer cancel()
	if err := s.audit.Audit(ctx, e); err != nil {
		s.logger.Error(fmt.Sprintf("auth: audit %s failed: %v", e.Action, err))
	}
}

func userEvent(u *User, orgID, action, ip string, details map[string]any) AuditEvent {
	return AuditEvent{OrgID: orgID, ActorType: "user", ActorID: u.ID, Action: action, TargetType: "user", TargetID: u.ID, IP: ip, Details: details}
}

func principalEvent(p *Principal, action, targetType, targetID, ip string, details map[string]any) AuditEvent {
	return AuditEvent{OrgID: p.OrgID, ActorType: p.ActorType(), ActorID: p.ActorID(), Action: action,
		TargetType: targetType, TargetID: targetID, IP: ip, Details: details}
}

// maybePurge drops expired sessions and OAuth states at most every 10 min.
func (s *Service) maybePurge(ctx context.Context) {
	s.purgeMu.Lock()
	if s.now().Sub(s.lastPurge) < purgeEvery {
		s.purgeMu.Unlock()
		return
	}
	s.lastPurge = s.now()
	s.purgeMu.Unlock()
	idleCutoff := s.now().Add(-s.cfg.SessionIdle).UnixMilli()
	oauthCutoff := s.now().Add(-oauthStateTTL).UnixMilli()
	if err := s.st.purgeExpired(ctx, s.st.d.SQL(), idleCutoff, oauthCutoff); err != nil {
		s.logger.Warn(fmt.Sprintf("auth: purge expired sessions: %v", err))
	}
}

// ---- sessions ----

// NewSession is a created session: the cookie value (shown to the browser
// once, stored only hashed) and its CSRF value.
type NewSession struct {
	Cookie  string
	CSRF    string
	Expires time.Time
	OrgID   string
}

// createSession stores a session for user in orgID (may be empty). When
// replace names an existing session (its hash), that session is deleted:
// the session id rotates on every login.
func (s *Service) createSession(ctx context.Context, q querier, userID, orgID, replace, ip, ua string) (*NewSession, error) {
	cookie, err := randomSecret()
	if err != nil {
		return nil, err
	}
	csrf, err := randomSecret()
	if err != nil {
		return nil, err
	}
	now := s.now()
	row := sessionRow{ID: HashSecret(cookie), UserID: userID, OrgID: orgID, CSRF: csrf,
		CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(s.cfg.SessionTTL).UnixMilli(), LastSeen: now.UnixMilli()}
	if replace != "" {
		if err := s.st.deleteSession(ctx, q, replace); err != nil {
			return nil, err
		}
	}
	if err := s.st.insertSession(ctx, q, row, ip, ua); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &NewSession{Cookie: cookie, CSRF: csrf, Expires: time.UnixMilli(row.ExpiresAt), OrgID: orgID}, nil
}

// sessionPrincipal resolves a session cookie to a user principal. It
// returns (nil, nil) for a cookie that names no live session.
func (s *Service) sessionPrincipal(ctx context.Context, cookie string) (*Principal, error) {
	if cookie == "" || len(cookie) > 128 {
		return nil, nil
	}
	id := HashSecret(cookie)
	row, err := s.st.sessionByID(ctx, s.st.d.SQL(), id)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	nowMS := now.UnixMilli()
	// last_seen is written at most once per sessionTouchEvery, so activity
	// inside that window is not recorded. Allow that window on top of the
	// idle interval: a session expires only after at least SessionIdle of
	// real inactivity (and at most SessionIdle plus one touch interval).
	if nowMS >= row.ExpiresAt || nowMS >= row.LastSeen+s.cfg.SessionIdle.Milliseconds()+sessionTouchEvery.Milliseconds() {
		_ = s.st.deleteSession(ctx, s.st.d.SQL(), id)
		return nil, nil
	}
	u, err := s.st.userByID(ctx, s.st.d.SQL(), row.UserID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if u.Disabled {
		_ = s.st.deleteUserSessions(ctx, s.st.d.SQL(), u.ID, "")
		return nil, nil
	}
	p := &Principal{Kind: KindUser, UserID: u.ID, Email: u.Email, sessionID: id, csrf: row.CSRF}
	if row.OrgID != "" {
		role, err := s.st.membershipRole(ctx, s.st.d.SQL(), row.OrgID, u.ID)
		switch {
		case err == nil:
			p.OrgID, p.Role = row.OrgID, role
		case errors.Is(err, db.ErrNotFound):
			// Removed from the org since: the session stays, with no org.
		default:
			return nil, err
		}
	}
	if nowMS-row.LastSeen >= sessionTouchEvery.Milliseconds() {
		if err := s.st.touchSession(ctx, s.st.d.SQL(), id, nowMS); err != nil {
			s.logger.Warn(fmt.Sprintf("auth: touch session: %v", err))
		}
	}
	return p, nil
}

// Logout deletes the principal's session. Tokens have no session to end.
func (s *Service) Logout(ctx context.Context, p *Principal, ip string) error {
	if p == nil || p.sessionID == "" {
		return nil
	}
	if err := s.st.deleteSession(ctx, s.st.d.SQL(), p.sessionID); err != nil {
		return err
	}
	s.record(principalEvent(p, "auth.logout", "user", p.UserID, ip, nil))
	return nil
}

// afterCredentialCheck, when set (tests only), runs after a password was
// verified against a snapshot and before the result is committed, to
// interleave a concurrent credential change deterministically.
var afterCredentialCheck func(userID string)

// ---- login ----

// LoginRequest is the body of POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login checks an email and password. Every failure is
// ErrInvalidCredentials after the same argon2id work, whether the account
// exists or not, or a 429 when a rate limit is hit. replace is the hashed
// id of the caller's current session, deleted on success.
func (s *Service) Login(ctx context.Context, req LoginRequest, ip, ua, replace string) (*User, *NewSession, error) {
	email := NormalizeEmail(req.Email)
	if ok, wait := s.loginIP.Allow(ip); !ok {
		s.record(AuditEvent{ActorType: "anonymous", Action: "auth.login_rate_limited", TargetType: "ip", TargetID: clip(ip, 64), IP: ip})
		return nil, nil, errRateLimited(wait)
	}
	// Every account's email passed validEmail when it was created, so an
	// address that fails it can not log in. It is refused before the
	// per-email limiter, which would otherwise keep an attacker-sized key per
	// request; the IP limit above still applies, and the same argon2id work
	// is burned below.
	validAddr := validEmail(email)
	if validAddr {
		if ok, wait := s.loginEmail.Allow(email); !ok {
			s.record(AuditEvent{ActorType: "anonymous", Action: "auth.login_rate_limited", TargetType: "email", TargetID: clip(email, 254), IP: ip})
			return nil, nil, errRateLimited(wait)
		}
	}
	s.maybePurge(ctx)
	fail := func(u *User) (*User, *NewSession, error) {
		ev := AuditEvent{ActorType: "anonymous", Action: "auth.login_failed", TargetType: "email", TargetID: clip(email, 254), IP: ip}
		if u != nil {
			ev.TargetType, ev.TargetID = "user", u.ID
		}
		s.record(ev)
		return nil, nil, ErrInvalidCredentials
	}
	if !validAddr || req.Password == "" || len(req.Password) > MaxPasswordBytes {
		s.hasher.BurnVerify(clip(req.Password, MaxPasswordBytes))
		return fail(nil)
	}
	u, err := s.st.userByEmail(ctx, s.st.d.SQL(), email)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return nil, nil, err
	}
	if u == nil || u.Disabled || u.PasswordHash == "" {
		s.hasher.BurnVerify(req.Password)
		return fail(u)
	}
	rehash, err := s.hasher.Verify(u.PasswordHash, req.Password)
	if err != nil {
		if !errors.Is(err, ErrPasswordMismatch) {
			s.logger.Error(fmt.Sprintf("auth: stored hash of %s unreadable: %v", u.ID, err))
		}
		return fail(u)
	}
	if afterCredentialCheck != nil {
		afterCredentialCheck(u.ID)
	}
	newHash := ""
	if rehash {
		if h, err := s.hasher.Hash(req.Password); err == nil {
			newHash = h
		}
	}
	// The password was verified against a snapshot read outside any
	// transaction. Issue the session only if that snapshot is still the
	// committed credential: a reset or change committed in between (and
	// the session revocation it made) must not be undone by this login.
	var (
		orgID string
		sess  *NewSession
		stale bool
	)
	err = s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		cur, err := s.st.lockUser(ctx, tx, u.ID)
		if errors.Is(err, db.ErrNotFound) {
			stale = true
			return nil
		}
		if err != nil {
			return err
		}
		if cur.Disabled || cur.PasswordHash != u.PasswordHash {
			stale = true
			return nil
		}
		if newHash != "" {
			if _, err := s.st.swapPasswordHash(ctx, tx, u.ID, u.PasswordHash, newHash); err != nil {
				return err
			}
		}
		orgs, err := s.st.memberships(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		if len(orgs) > 0 {
			orgID = orgs[0].ID
		}
		sess, err = s.createSession(ctx, tx, u.ID, orgID, replace, ip, ua)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	if stale {
		return fail(u)
	}
	s.loginEmail.Reset(email)
	s.record(userEvent(u, orgID, "auth.login", ip, map[string]any{"method": "password"}))
	return u, sess, nil
}

// ---- signup ----

// SignupRequest is the body of POST /auth/signup.
type SignupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name,omitempty"`
	// OrgName names the organization created for the new user (open signup
	// and bootstrap). Empty uses the name or email.
	OrgName string `json:"org_name,omitempty"`
	// Invitation is an invitation token; the user joins that organization.
	Invitation string `json:"invitation,omitempty"`
}

// Signup creates an account under the signup policy:
//   - the first account of an empty install (bootstrap) owns a new org,
//     unless the policy is closed;
//   - with an invitation token (policy invite or open), the account joins
//     the inviting org with the invited role; the email must match;
//   - with policy open, any account owns a new org of its own;
//   - otherwise signup is refused.
func (s *Service) Signup(ctx context.Context, req SignupRequest, ip, ua, replace string) (*User, *NewSession, error) {
	email := NormalizeEmail(req.Email)
	// deny audits a refusal before any account work (every signup outcome
	// leaves an audit row) and returns it.
	deny := func(e *Error) (*User, *NewSession, error) {
		s.record(AuditEvent{ActorType: "anonymous", Action: "auth.signup_failed", TargetType: "email", TargetID: clip(email, 254), IP: ip,
			Details: map[string]any{"reason": e.Msg}})
		return nil, nil, e
	}
	if ok, wait := s.signupIP.Allow(ip); !ok {
		return deny(errRateLimited(wait))
	}
	if !validEmail(email) {
		return deny(errBadRequest("a valid email address is required"))
	}
	if ok, wait := s.signupEmail.Allow(email); !ok {
		return deny(errRateLimited(wait))
	}
	if err := CheckPasswordPolicy(req.Password, email); err != nil {
		return deny(errBadRequest("%s", err.Error()))
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 200 {
		return deny(errBadRequest("name is longer than 200 characters"))
	}
	if s.cfg.Signup == SignupClosed {
		// Closed: nothing on the web creates accounts, not even the first
		// one; `bifrost-bench user create` does.
		return deny(ErrSignupNotAllowed)
	}
	hash, err := s.hasher.Hash(req.Password)
	if err != nil {
		return nil, nil, err
	}
	var (
		u       *User
		orgID   string
		inv     *Invitation
		created *Org
		sess    *NewSession
	)
	err = s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.st.lockAccounts(ctx, tx); err != nil {
			return err
		}
		if req.Invitation != "" {
			in, err := s.pendingInvite(ctx, tx, req.Invitation)
			if err != nil {
				return err
			}
			if in.Email != email {
				return ErrInvitationEmail
			}
			inv = in
		} else {
			n, err := s.st.countUsers(ctx, tx)
			if err != nil {
				return err
			}
			if n > 0 && s.cfg.Signup != SignupOpen {
				return ErrSignupNotAllowed
			}
		}
		nu, err := s.st.insertUser(ctx, tx, email, name, hash, "")
		if errors.Is(err, db.ErrConflict) {
			return ErrSignupFailed
		}
		if err != nil {
			return err
		}
		u = nu
		if inv != nil {
			if ok, err := s.st.markInviteAccepted(ctx, tx, inv.ID); err != nil {
				return err
			} else if !ok {
				return ErrInvalidInvitation
			}
			if err := s.st.grantMembership(ctx, tx, inv.OrgID, u.ID, inv.Role); err != nil {
				return err
			}
			orgID = inv.OrgID
		} else {
			o, err := s.st.insertOrg(ctx, tx, orgNameFor(req.OrgName, name, email))
			if err != nil {
				return err
			}
			if err := s.st.grantMembership(ctx, tx, o.ID, u.ID, RoleOwner); err != nil {
				return err
			}
			created, orgID = o, o.ID
		}
		sess, err = s.createSession(ctx, tx, u.ID, orgID, replace, ip, ua)
		return err
	})
	if err != nil {
		if e, ok := AsError(err); ok && e.Status < 500 {
			s.record(AuditEvent{ActorType: "anonymous", Action: "auth.signup_failed", TargetType: "email", TargetID: clip(email, 254), IP: ip,
				Details: map[string]any{"reason": e.Msg}})
		}
		return nil, nil, err
	}
	details := map[string]any{"method": "password"}
	if created != nil {
		details["org_created"] = created.ID
	}
	s.record(userEvent(u, orgID, "auth.signup", ip, details))
	if inv != nil {
		s.record(AuditEvent{OrgID: inv.OrgID, ActorType: "user", ActorID: u.ID, Action: "auth.invite_accept", TargetType: "invitation", TargetID: inv.ID, IP: ip,
			Details: map[string]any{"role": string(inv.Role)}})
	}
	return u, sess, nil
}

func orgNameFor(orgName, name, email string) string {
	if n := strings.TrimSpace(orgName); n != "" {
		return clip(n, 100)
	}
	if name != "" {
		return name + "'s organization"
	}
	local, _, _ := strings.Cut(email, "@")
	return local + "'s organization"
}

// ---- me, switch org, password ----

// MeOrg is one organization in GET /auth/me.
type MeOrg struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// Me is the body of GET /auth/me.
type Me struct {
	AuthMode   string  `json:"auth_mode"` // local | hosted
	Kind       string  `json:"kind"`      // user | token | runner | local
	User       *User   `json:"user,omitempty"`
	TokenID    string  `json:"token_id,omitempty"`
	Orgs       []MeOrg `json:"orgs"`
	CurrentOrg string  `json:"current_org,omitempty"`
	Role       Role    `json:"role,omitempty"`
	// CSRF is the value cookie-authenticated writes send in X-Heimdall-CSRF.
	CSRF string `json:"csrf,omitempty"`
	// ServerAdmin is true for a user listed in HEIMDALL_SERVER_ADMINS (serveradmin.go).
	ServerAdmin bool `json:"server_admin"`
}

// LocalMe is GET /auth/me in local mode.
func LocalMe() Me {
	return Me{AuthMode: "local", Kind: string(KindLocal), Orgs: []MeOrg{{ID: LocalOrgID, Slug: LocalOrgID, Name: "Local", Role: RoleOwner}},
		CurrentOrg: LocalOrgID, Role: RoleOwner}
}

// Me describes the principal: the user, their organizations with roles,
// the current organization and the session's CSRF value.
func (s *Service) Me(ctx context.Context, p *Principal) (*Me, error) {
	if !p.IsAuthenticated() {
		return nil, ErrUnauthenticated
	}
	me := &Me{AuthMode: "hosted", Kind: string(p.Kind), CurrentOrg: p.OrgID, Role: p.Role, CSRF: p.csrf, Orgs: []MeOrg{}}
	switch p.Kind {
	case KindUser:
		u, err := s.st.userByID(ctx, s.st.d.SQL(), p.UserID)
		if err != nil {
			return nil, err
		}
		me.User = u
		me.ServerAdmin = IsServerAdmin(p) || isServerAdminEmail(u.Email)
		orgs, err := s.st.memberships(ctx, s.st.d.SQL(), u.ID)
		if err != nil {
			return nil, err
		}
		for _, o := range orgs {
			me.Orgs = append(me.Orgs, MeOrg{ID: o.ID, Slug: o.Slug, Name: o.Name, Role: o.Role})
		}
	case KindToken, KindRunner:
		me.TokenID = p.TokenID
		if o, err := s.st.orgByID(ctx, s.st.d.SQL(), p.OrgID); err == nil {
			me.Orgs = append(me.Orgs, MeOrg{ID: o.ID, Slug: o.Slug, Name: o.Name, Role: p.Role})
		}
	}
	return me, nil
}

// SwitchOrgRequest is the body of POST /auth/switch-org: an org id or slug.
type SwitchOrgRequest struct {
	Org string `json:"org"`
}

// SwitchOrg makes ref (an org id or slug the user belongs to) the session's
// current organization. A non-member gets the same answer as an org that
// does not exist.
func (s *Service) SwitchOrg(ctx context.Context, p *Principal, ref, ip string) (*MeOrg, error) {
	if p.Kind != KindUser {
		return nil, ErrSessionRequired
	}
	if p.sessionID == "" {
		return nil, ErrSessionRequired
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 128 {
		return nil, errBadRequest("org is required")
	}
	o, err := s.st.orgByID(ctx, s.st.d.SQL(), ref)
	if errors.Is(err, db.ErrNotFound) {
		o, err = s.st.orgBySlug(ctx, s.st.d.SQL(), ref)
	}
	if errors.Is(err, db.ErrNotFound) {
		return nil, errNotFound("organization not found")
	}
	if err != nil {
		return nil, err
	}
	role, err := s.st.membershipRole(ctx, s.st.d.SQL(), o.ID, p.UserID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, errNotFound("organization not found")
	}
	if err != nil {
		return nil, err
	}
	if err := s.st.setSessionOrg(ctx, s.st.d.SQL(), p.sessionID, o.ID); err != nil {
		return nil, err
	}
	s.record(AuditEvent{OrgID: o.ID, ActorType: "user", ActorID: p.UserID, Action: "auth.switch_org", TargetType: "org", TargetID: o.ID, IP: ip})
	return &MeOrg{ID: o.ID, Slug: o.Slug, Name: o.Name, Role: role}, nil
}

// PasswordChangeRequest is the body of POST /auth/password.
type PasswordChangeRequest struct {
	Current string `json:"current_password"`
	New     string `json:"new_password"`
}

// ChangePassword sets a new password after checking the current one (not
// needed when the account has none yet, as after GitHub sign-up). Every
// other session of the user is ended.
func (s *Service) ChangePassword(ctx context.Context, p *Principal, req PasswordChangeRequest, ip string) error {
	if p.Kind != KindUser || p.sessionID == "" {
		return ErrSessionRequired
	}
	failed := func(u *User, e *Error) error {
		ev := principalEvent(p, "auth.password_change_failed", "user", p.UserID, ip, map[string]any{"reason": e.Msg})
		if u != nil {
			ev = userEvent(u, p.OrgID, "auth.password_change_failed", ip, map[string]any{"reason": e.Msg})
		}
		s.record(ev)
		return e
	}
	if ok, wait := s.loginEmail.Allow(NormalizeEmail(p.Email)); !ok {
		return failed(nil, errRateLimited(wait))
	}
	u, err := s.st.userByID(ctx, s.st.d.SQL(), p.UserID)
	if err != nil {
		return err
	}
	if u.PasswordHash != "" {
		if _, err := s.hasher.Verify(u.PasswordHash, req.Current); err != nil {
			return failed(u, errForbidden("current password is wrong"))
		}
	}
	if err := CheckPasswordPolicy(req.New, u.Email); err != nil {
		return failed(u, errBadRequest("%s", err.Error()))
	}
	hash, err := s.hasher.Hash(req.New)
	if err != nil {
		return err
	}
	if afterCredentialCheck != nil {
		afterCredentialCheck(u.ID)
	}
	// Write only over the hash that was verified above: a reset or another
	// change committed since must not be overwritten by this request.
	changed := false
	err = s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		ok, err := s.st.swapPasswordHash(ctx, tx, u.ID, u.PasswordHash, hash)
		if err != nil || !ok {
			return err
		}
		changed = true
		return s.st.deleteUserSessions(ctx, tx, u.ID, p.sessionID)
	})
	if err != nil {
		return err
	}
	if !changed {
		return failed(u, errConflict("the password was changed by another request; sign in again"))
	}
	s.record(userEvent(u, p.OrgID, "auth.password_change", ip, nil))
	return nil
}

// AuthInfo is the body of GET /auth/config: what the sign-in page offers.
type AuthInfo struct {
	AuthMode string `json:"auth_mode"`
	Signup   string `json:"signup,omitempty"`
	GitHub   bool   `json:"github"`
	// Bootstrap is true on an empty install: the first signup creates the
	// owner account and its organization.
	Bootstrap bool `json:"bootstrap,omitempty"`
}

// Info describes the sign-in options.
func (s *Service) Info(ctx context.Context) AuthInfo {
	info := AuthInfo{AuthMode: "hosted", Signup: string(s.cfg.Signup), GitHub: s.cfg.GitHub != nil}
	if s.cfg.Signup != SignupClosed {
		if n, err := s.st.countUsers(ctx, s.st.d.SQL()); err == nil && n == 0 {
			info.Bootstrap = true
		}
	}
	return info
}

// SessionMe is Me for a session just created by login, signup or GitHub
// sign-in, before the browser has sent the cookie back.
func (s *Service) SessionMe(ctx context.Context, u *User, sess *NewSession) (*Me, error) {
	p := &Principal{Kind: KindUser, UserID: u.ID, Email: u.Email, OrgID: sess.OrgID, sessionID: HashSecret(sess.Cookie), csrf: sess.CSRF}
	if sess.OrgID != "" {
		role, err := s.st.membershipRole(ctx, s.st.d.SQL(), sess.OrgID, u.ID)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return nil, err
		}
		p.Role = role
	}
	return s.Me(ctx, p)
}
