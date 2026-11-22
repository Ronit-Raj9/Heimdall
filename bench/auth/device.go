package auth

// Device login (RFC 8628, lane Z02): `heimdall login` on a machine with a
// terminal asks for a device code, prints a short user code and a link, and
// polls the token endpoint. The user opens the link in a browser where they
// are signed in, checks the code and approves; the next poll gets a fresh
// org API token in the user's current org. Nobody copies a token by hand.
//
// Pending grants live in memory: they last ten minutes, and a restart only
// makes the CLI ask for a new code. The token itself is a normal api_tokens
// row (CreateToken's rules: hashed, revocable, at most the approver's role).

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// Device flow constants.
const (
	// DeviceGrantType is the RFC 8628 grant_type of the token request.
	DeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	// DeviceCodeTTL is how long a code may wait for approval.
	DeviceCodeTTL = 10 * time.Minute
	// DeviceInterval is the poll interval the CLI is told to keep.
	DeviceInterval = 5 * time.Second
	// DeviceTokenLifetime is the expiry of a token minted by device login.
	DeviceTokenLifetime = 90 * 24 * time.Hour
	// deviceMaxPending bounds the pending grants held in memory.
	deviceMaxPending = 10_000
	// userCodeAlphabet has no vowels (no words) and no look-alike letters.
	userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
	userCodeLen      = 8
)

// RFC 8628 section 3.5 error codes of the token endpoint.
const (
	DeviceErrPending  = "authorization_pending"
	DeviceErrSlowDown = "slow_down"
	DeviceErrDenied   = "access_denied"
	DeviceErrExpired  = "expired_token"
	DeviceErrInvalid  = "invalid_grant"
	DeviceErrBadReq   = "invalid_request"
	DeviceErrGrant    = "unsupported_grant_type"
)

// DeviceCodeRequest starts a device login. RFC 8628 names client_id and
// scope; client_name is what the approval page shows ("heimdall CLI on
// raj-laptop").
type DeviceCodeRequest struct {
	ClientID   string `json:"client_id,omitempty"`
	ClientName string `json:"client_name,omitempty"`
	// Scope is read, evaluate (default) or admin; the approver's role caps it.
	Scope string `json:"scope,omitempty"`
}

// DeviceCodeResponse is RFC 8628 section 3.2's answer.
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceTokenRequest is RFC 8628 section 3.4's poll.
type DeviceTokenRequest struct {
	GrantType  string `json:"grant_type"`
	DeviceCode string `json:"device_code"`
	ClientID   string `json:"client_id,omitempty"`
}

// DeviceTokenResponse is the access token, given once.
type DeviceTokenResponse struct {
	AccessToken string   `json:"access_token"`
	TokenType   string   `json:"token_type"`
	ExpiresIn   int      `json:"expires_in"`
	Scope       string   `json:"scope"`
	OrgID       string   `json:"org_id"`
	Role        Role     `json:"role"`
	TokenID     string   `json:"token_id"`
	Scopes      []string `json:"scopes"`
}

// DeviceTokenError is RFC 8628's error body ({"error", "error_description"}).
type DeviceTokenError struct {
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
	// Interval is the new poll interval after slow_down, in seconds.
	Interval int `json:"interval,omitempty"`
}

func (e *DeviceTokenError) Error() string { return e.Code + ": " + e.Description }

// DeviceInfo is what the approval page shows about a pending code.
type DeviceInfo struct {
	UserCode   string    `json:"user_code"`
	ClientName string    `json:"client_name"`
	ClientIP   string    `json:"client_ip"`
	Scope      string    `json:"scope"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Status     string    `json:"status"` // pending | approved | denied
}

// DeviceDecision is POST /auth/device/approve and /auth/device/deny.
type DeviceDecision struct {
	UserCode string `json:"user_code"`
}

// DeviceApproved answers an approval: which org the token acts in.
type DeviceApproved struct {
	UserCode string   `json:"user_code"`
	OrgID    string   `json:"org_id"`
	Role     Role     `json:"role"`
	Scopes   []string `json:"scopes"`
	TokenID  string   `json:"token_id"`
}

type deviceGrant struct {
	deviceHash string
	userCode   string
	clientName string
	clientIP   string
	scope      string
	created    time.Time
	expires    time.Time
	interval   time.Duration
	lastPoll   time.Time
	status     string // pending | approved | denied
	// Set on approval; handed over once, then the grant is dropped.
	token    string
	approved DeviceTokenResponse
}

// DeviceFlow holds pending device logins. Safe for concurrent use.
type DeviceFlow struct {
	svc *Service
	now func() time.Time

	codeIP  *Limiter // POST /device/code per IP
	pollIP  *Limiter // POST /device/token per IP
	lookups *Limiter // user code lookups and decisions per user

	mu       sync.Mutex
	byDevice map[string]*deviceGrant
	byUser   map[string]*deviceGrant
}

// NewDeviceFlow returns the device login area over svc.
func NewDeviceFlow(svc *Service) *DeviceFlow {
	now := time.Now
	if svc != nil && svc.now != nil {
		now = svc.now
	}
	return &DeviceFlow{
		svc: svc, now: now,
		codeIP:   NewLimiter(RateLimit{Burst: 10, Every: 6 * time.Second}, now),
		pollIP:   NewLimiter(RateLimit{Burst: 60, Every: time.Second}, now),
		lookups:  NewLimiter(RateLimit{Burst: 20, Every: 3 * time.Second}, now),
		byDevice: map[string]*deviceGrant{},
		byUser:   map[string]*deviceGrant{},
	}
}

// NormalizeUserCode upper-cases a typed code and drops spaces and dashes,
// then puts the dash back: "bcdf ghjk" and "BCDF-GHJK" are the same code.
func NormalizeUserCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r == '-' || r == ' ' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	c := b.String()
	if len(c) != userCodeLen {
		return c
	}
	return c[:4] + "-" + c[4:]
}

func newUserCode() (string, error) {
	out := make([]byte, 0, userCodeLen)
	buf := make([]byte, 1)
	for len(out) < userCodeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("read random: %w", err)
		}
		// 240 = 12*20: reject the top values so every letter is equally likely.
		if buf[0] < 240 {
			out = append(out, userCodeAlphabet[int(buf[0])%len(userCodeAlphabet)])
		}
	}
	return string(out[:4]) + "-" + string(out[4:]), nil
}

func normalizeDeviceScope(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", ScopeEvaluate:
		return ScopeEvaluate, true
	case ScopeRead:
		return ScopeRead, true
	case ScopeAdmin:
		return ScopeAdmin, true
	}
	return "", false
}

// sweepLocked drops expired grants.
func (f *DeviceFlow) sweepLocked(now time.Time) {
	for h, g := range f.byDevice {
		if now.After(g.expires) {
			delete(f.byDevice, h)
			delete(f.byUser, g.userCode)
		}
	}
}

// Start issues a device code and a user code (RFC 8628 section 3.1).
func (f *DeviceFlow) Start(req DeviceCodeRequest, ip string) (*DeviceCodeResponse, error) {
	if ok, wait := f.codeIP.Allow(ip); !ok {
		return nil, errRateLimited(wait)
	}
	scope, ok := normalizeDeviceScope(req.Scope)
	if !ok {
		return nil, errBadRequest("scope must be read, evaluate or admin")
	}
	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = strings.TrimSpace(req.ClientID)
	}
	if name == "" {
		name = "heimdall CLI"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	device, err := randomSecret()
	if err != nil {
		return nil, err
	}
	now := f.now().UTC()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweepLocked(now)
	if len(f.byDevice) >= deviceMaxPending {
		return nil, errUnavailable("too many device logins are waiting; try again in a few minutes")
	}
	var user string
	for i := 0; i < 5; i++ {
		u, err := newUserCode()
		if err != nil {
			return nil, err
		}
		if _, taken := f.byUser[u]; !taken {
			user = u
			break
		}
	}
	if user == "" {
		return nil, errUnavailable("could not pick a free user code; try again")
	}
	g := &deviceGrant{deviceHash: HashSecret(device), userCode: user, clientName: name, clientIP: ip, scope: scope,
		created: now, expires: now.Add(DeviceCodeTTL), interval: DeviceInterval, status: "pending"}
	f.byDevice[g.deviceHash] = g
	f.byUser[user] = g
	base := ""
	if f.svc != nil {
		base = strings.TrimRight(f.svc.cfg.PublicURL, "/")
	}
	return &DeviceCodeResponse{
		DeviceCode: device, UserCode: user,
		VerificationURI:         base + "/device",
		VerificationURIComplete: base + "/device?code=" + user,
		ExpiresIn:               int(DeviceCodeTTL / time.Second),
		Interval:                int(DeviceInterval / time.Second),
	}, nil
}

// Poll answers the token endpoint (RFC 8628 section 3.4 and 3.5). The
// token is handed over once; the grant is then gone.
func (f *DeviceFlow) Poll(req DeviceTokenRequest, ip string) (*DeviceTokenResponse, *DeviceTokenError) {
	if ok, _ := f.pollIP.Allow(ip); !ok {
		return nil, &DeviceTokenError{Code: DeviceErrSlowDown, Description: "polling too fast from this address", Interval: int((2 * DeviceInterval) / time.Second)}
	}
	if req.GrantType != DeviceGrantType {
		return nil, &DeviceTokenError{Code: DeviceErrGrant, Description: "grant_type must be " + DeviceGrantType}
	}
	if strings.TrimSpace(req.DeviceCode) == "" {
		return nil, &DeviceTokenError{Code: DeviceErrBadReq, Description: "device_code is required"}
	}
	now := f.now().UTC()
	h := HashSecret(req.DeviceCode)
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.byDevice[h]
	if !ok {
		return nil, &DeviceTokenError{Code: DeviceErrInvalid, Description: "unknown device code: start again with heimdall login"}
	}
	if now.After(g.expires) {
		delete(f.byDevice, h)
		delete(f.byUser, g.userCode)
		return nil, &DeviceTokenError{Code: DeviceErrExpired, Description: "the code expired before it was approved: run heimdall login again"}
	}
	switch g.status {
	case "denied":
		delete(f.byDevice, h)
		delete(f.byUser, g.userCode)
		return nil, &DeviceTokenError{Code: DeviceErrDenied, Description: "the login was denied in the browser"}
	case "approved":
		delete(f.byDevice, h)
		delete(f.byUser, g.userCode)
		out := g.approved
		out.AccessToken = g.token
		return &out, nil
	}
	if !g.lastPoll.IsZero() && now.Sub(g.lastPoll) < g.interval {
		g.interval += DeviceInterval
		g.lastPoll = now
		return nil, &DeviceTokenError{Code: DeviceErrSlowDown, Description: "poll at most once per interval", Interval: int(g.interval / time.Second)}
	}
	g.lastPoll = now
	return nil, &DeviceTokenError{Code: DeviceErrPending, Description: "waiting for approval in the browser"}
}

// pendingFor finds a pending, unexpired grant by user code for a signed-in
// user, rate limited per user so codes can not be guessed.
func (f *DeviceFlow) pendingFor(p *Principal, userCode string) (*deviceGrant, error) {
	if p == nil || p.Kind != KindUser {
		return nil, ErrSessionRequired
	}
	if ok, wait := f.lookups.Allow(p.UserID); !ok {
		return nil, errRateLimited(wait)
	}
	code := NormalizeUserCode(userCode)
	g, ok := f.byUser[code]
	if !ok || f.now().After(g.expires) {
		return nil, errNotFound("no login is waiting for code %s: it expired or was mistyped; run heimdall login again", code)
	}
	return g, nil
}

// Lookup describes a pending code for the approval page.
func (f *DeviceFlow) Lookup(p *Principal, userCode string) (*DeviceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, err := f.pendingFor(p, userCode)
	if err != nil {
		return nil, err
	}
	return &DeviceInfo{UserCode: g.userCode, ClientName: g.clientName, ClientIP: g.clientIP, Scope: g.scope,
		CreatedAt: g.created, ExpiresAt: g.expires, Status: g.status}, nil
}

// deviceRoleAndScopes is what the token may do: the approver's role in the
// current org, narrowed to the requested scope. A viewer only ever gets read;
// admin scope needs role admin.
func deviceRoleAndScopes(role Role, scope string) (Role, []string, error) {
	switch {
	case scope == ScopeAdmin && !role.AtLeast(RoleAdmin):
		return "", nil, errForbidden("admin scope needs role admin in this organization; log in with the default scope (evaluate)")
	case !role.AtLeast(RoleMember):
		return role, []string{ScopeRead}, nil
	}
	return role, []string{scope}, nil
}

// Approve mints the token for a pending code in the approver's current org.
func (f *DeviceFlow) Approve(ctx context.Context, p *Principal, userCode, ip string) (*DeviceApproved, error) {
	if f.svc == nil {
		return nil, errUnavailable("device login needs hosted mode")
	}
	if p == nil || p.Kind != KindUser {
		return nil, ErrSessionRequired
	}
	if p.OrgID == "" || !p.Role.Valid() {
		return nil, errForbidden("pick an organization first: the token acts in your current organization")
	}
	f.mu.Lock()
	g, err := f.pendingFor(p, userCode)
	if err == nil && g.status != "pending" {
		err = errConflict("code %s was already %s", g.userCode, g.status)
	}
	if err != nil {
		f.mu.Unlock()
		return nil, err
	}
	role, scopes, err := deviceRoleAndScopes(p.Role, g.scope)
	if err != nil {
		f.mu.Unlock()
		return nil, err
	}
	g.status = "approving" // no second approval while the row is written
	clientName, code := g.clientName, g.userCode
	f.mu.Unlock()

	raw, prefix, err := NewAPIToken()
	if err == nil {
		now := f.now().UTC()
		exp := time.UnixMilli(now.Add(DeviceTokenLifetime).UnixMilli()).UTC()
		name := "CLI login: " + clientName
		if len(name) > 100 {
			name = name[:100]
		}
		t := APIToken{ID: db.NewID("tok"), OrgID: p.OrgID, UserID: p.UserID, Name: name, Prefix: prefix, Role: role, Scopes: scopes,
			CreatedAt: time.UnixMilli(now.UnixMilli()).UTC(), ExpiresAt: &exp}
		if err = f.svc.st.insertToken(ctx, f.svc.st.d.SQL(), &t, HashSecret(raw)); err == nil {
			f.svc.record(principalEvent(p, "auth.device_approve", "api_token", t.ID, ip,
				map[string]any{"name": name, "role": string(role), "prefix": prefix, "scopes": JoinScopes(scopes), "user_code": code}))
			f.mu.Lock()
			defer f.mu.Unlock()
			g.status = "approved"
			g.token = raw
			g.approved = DeviceTokenResponse{TokenType: "bearer", ExpiresIn: int(DeviceTokenLifetime / time.Second), Scope: JoinScopes(scopes),
				OrgID: p.OrgID, Role: role, TokenID: t.ID, Scopes: scopes}
			return &DeviceApproved{UserCode: code, OrgID: p.OrgID, Role: role, Scopes: scopes, TokenID: t.ID}, nil
		}
	}
	f.mu.Lock()
	g.status = "pending"
	f.mu.Unlock()
	return nil, fmt.Errorf("create device token: %w", err)
}

// Deny refuses a pending code: the CLI's next poll gets access_denied.
func (f *DeviceFlow) Deny(p *Principal, userCode, ip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, err := f.pendingFor(p, userCode)
	if err != nil {
		return err
	}
	if g.status != "pending" {
		return errConflict("code %s was already %s", g.userCode, g.status)
	}
	g.status = "denied"
	if f.svc != nil {
		f.svc.record(principalEvent(p, "auth.device_deny", "device_code", g.userCode, ip, map[string]any{"client": g.clientName}))
	}
	return nil
}
