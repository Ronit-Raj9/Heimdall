package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/valyala/fasthttp"
)

// GitHub defaults.
const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubAPIURL       = "https://api.github.com"
	githubScope        = "read:user user:email"
	// GitHubCallbackPath is registered as the OAuth app's callback URL,
	// after the public URL.
	GitHubCallbackPath = "/api/bench/auth/github/callback"
	githubCookiePath   = "/api/bench/auth/github/"
	maxGitHubBody      = 1 << 20
)

// ErrGitHubDisabled answers the GitHub routes when no OAuth app is set.
var ErrGitHubDisabled = &Error{Status: http.StatusNotFound, Type: "not_found", Msg: "GitHub sign-in is not configured on this server"}

func (g *GitHubConfig) authorizeURL() string { return orDefault(g.AuthorizeURL, githubAuthorizeURL) }
func (g *GitHubConfig) tokenURL() string     { return orDefault(g.TokenURL, githubTokenURL) }
func (g *GitHubConfig) apiURL() string {
	return strings.TrimRight(orDefault(g.APIURL, githubAPIURL), "/")
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// GitHubStart begins a sign-in: it stores the hashed state with the
// return path, binds the state to this browser with a short-lived cookie,
// and returns the GitHub authorize URL to redirect to.
func (s *Service) GitHubStart(ctx *fasthttp.RequestCtx, returnTo string) (string, error) {
	g := s.cfg.GitHub
	if g == nil {
		return "", ErrGitHubDisabled
	}
	if ok, wait := s.probeIP.Allow(s.ClientIP(ctx)); !ok {
		return "", errRateLimited(wait)
	}
	state, err := randomSecret()
	if err != nil {
		return "", err
	}
	dctx, cancel := dbCtx()
	defer cancel()
	s.maybePurge(dctx)
	if err := s.st.insertOAuthState(dctx, s.st.d.SQL(), HashSecret(state), safeReturnTo(returnTo)); err != nil {
		return "", fmt.Errorf("store oauth state: %w", err)
	}
	c := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(c)
	c.SetKey(OAuthStateCookie)
	c.SetValue(state)
	c.SetPath(githubCookiePath)
	c.SetHTTPOnly(true)
	c.SetSecure(s.cfg.SecureCookies())
	c.SetSameSite(fasthttp.CookieSameSiteLaxMode) // sent on GitHub's top-level redirect back
	c.SetMaxAge(int(oauthStateTTL / time.Second))
	ctx.Response.Header.SetCookie(c)
	q := url.Values{}
	q.Set("client_id", g.ClientID)
	q.Set("redirect_uri", s.cfg.PublicURL+GitHubCallbackPath)
	q.Set("scope", githubScope)
	q.Set("state", state)
	q.Set("allow_signup", "false")
	return g.authorizeURL() + "?" + q.Encode(), nil
}

// GitHubIdentity is what the callback learns from GitHub.
type GitHubIdentity struct {
	ID    string
	Login string
	Name  string
	Email string // primary and verified
}

// GitHubCallback finishes a sign-in: checks the state against the cookie
// and the stored hash (single use, 10 minutes), exchanges the code, reads
// the GitHub user and its primary verified email, then signs in the user
// linked to that GitHub id, or links the account with that verified email,
// or creates one when the signup policy allows (bootstrap, open, or a
// pending invitation for that email). It returns the session and the
// same-site path to send the browser to.
func (s *Service) GitHubCallback(ctx *fasthttp.RequestCtx, replace string) (*NewSession, string, error) {
	g := s.cfg.GitHub
	if g == nil {
		return nil, "", ErrGitHubDisabled
	}
	ip := s.ClientIP(ctx)
	if ok, wait := s.probeIP.Allow(ip); !ok {
		return nil, "", errRateLimited(wait)
	}
	args := ctx.QueryArgs()
	state, code := string(args.Peek("state")), string(args.Peek("code"))
	cookie := string(ctx.Request.Header.Cookie(OAuthStateCookie))
	s.clearCookie(ctx, OAuthStateCookie, githubCookiePath)
	if state == "" || code == "" || len(state) > 128 || len(code) > 512 || cookie == "" ||
		subtle.ConstantTimeCompare([]byte(state), []byte(cookie)) != 1 {
		return nil, "", errBadRequest("GitHub sign-in: state does not match this browser; start again")
	}
	dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created, returnTo, err := s.st.takeOAuthState(dctx, s.st.d.SQL(), HashSecret(state))
	if errors.Is(err, db.ErrNotFound) {
		return nil, "", errBadRequest("GitHub sign-in: state unknown or already used; start again")
	}
	if err != nil {
		return nil, "", err
	}
	if s.now().Sub(time.UnixMilli(created)) > oauthStateTTL {
		return nil, "", errBadRequest("GitHub sign-in: took longer than 10 minutes; start again")
	}
	id, err := s.githubIdentity(dctx, g, code)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("auth: GitHub sign-in failed: %v", err))
		s.record(AuditEvent{ActorType: "anonymous", Action: "auth.login_failed", IP: ip, Details: map[string]any{"method": "github"}})
		return nil, "", errForbidden("GitHub sign-in failed")
	}
	u, sess, err := s.githubSignIn(dctx, id, ip, string(ctx.Request.Header.UserAgent()), replace)
	if err != nil {
		if e, ok := AsError(err); ok && e.Status < 500 {
			s.record(AuditEvent{ActorType: "anonymous", Action: "auth.login_failed", TargetType: "email", TargetID: clip(id.Email, 254), IP: ip,
				Details: map[string]any{"method": "github", "reason": e.Msg}})
		}
		return nil, "", err
	}
	s.record(userEvent(u, sess.OrgID, "auth.login", ip, map[string]any{"method": "github", "github_login": id.Login}))
	return sess, returnTo, nil
}

// githubSignIn maps a GitHub identity to a user and opens a session.
func (s *Service) githubSignIn(ctx context.Context, id *GitHubIdentity, ip, ua, replace string) (*User, *NewSession, error) {
	var (
		u    *User
		sess *NewSession
	)
	err := s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		u, err = s.st.userByGitHubID(ctx, tx, id.ID)
		if errors.Is(err, db.ErrNotFound) {
			u, err = s.st.userByEmail(ctx, tx, id.Email)
			if err == nil {
				if u.GitHubID != "" && u.GitHubID != id.ID {
					return errForbidden("this email's account is linked to another GitHub user")
				}
				if err := s.st.linkGitHub(ctx, tx, u.ID, id.ID); err != nil {
					return err
				}
			}
		}
		if errors.Is(err, db.ErrNotFound) {
			u, err = s.githubCreate(ctx, tx, id)
		}
		if err != nil {
			return err
		}
		if u.Disabled {
			return ErrInvalidCredentials
		}
		orgs, err := s.st.memberships(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		orgID := ""
		if len(orgs) > 0 {
			orgID = orgs[0].ID
		}
		sess, err = s.createSession(ctx, tx, u.ID, orgID, replace, ip, ua)
		return err
	})
	return u, sess, err
}

// githubCreate creates an account for a GitHub identity under the signup
// policy: bootstrap and open get an org of their own; otherwise a pending
// invitation for the verified email is required and accepted.
func (s *Service) githubCreate(ctx context.Context, tx *sql.Tx, id *GitHubIdentity) (*User, error) {
	if s.cfg.Signup == SignupClosed {
		return nil, ErrSignupNotAllowed
	}
	n, err := s.st.countUsers(ctx, tx)
	if err != nil {
		return nil, err
	}
	inv, err := s.st.pendingInviteForEmail(ctx, tx, id.Email)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if inv == nil && n > 0 && s.cfg.Signup != SignupOpen {
		return nil, ErrSignupNotAllowed
	}
	name := id.Name
	if name == "" {
		name = id.Login
	}
	u, err := s.st.insertUser(ctx, tx, id.Email, name, "", id.ID)
	if errors.Is(err, db.ErrConflict) {
		return nil, ErrSignupFailed
	}
	if err != nil {
		return nil, err
	}
	if inv != nil {
		if ok, err := s.st.markInviteAccepted(ctx, tx, inv.ID); err != nil || !ok {
			return nil, ErrInvalidInvitation
		}
		return u, s.st.grantMembership(ctx, tx, inv.OrgID, u.ID, inv.Role)
	}
	o, err := s.st.insertOrg(ctx, tx, orgNameFor("", name, id.Email))
	if err != nil {
		return nil, err
	}
	return u, s.st.grantMembership(ctx, tx, o.ID, u.ID, RoleOwner)
}

// githubIdentity exchanges the code and reads the user and emails.
func (s *Service) githubIdentity(ctx context.Context, g *GitHubConfig, code string) (*GitHubIdentity, error) {
	form := url.Values{}
	form.Set("client_id", g.ClientID)
	form.Set("client_secret", g.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", s.cfg.PublicURL+GitHubCallbackPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := s.githubJSON(req, &tok); err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	if tok.Error != "" || tok.AccessToken == "" {
		return nil, fmt.Errorf("exchange code: %s %s", tok.Error, tok.ErrorDesc)
	}
	get := func(path string, into any) error {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiURL()+path, nil)
		if err != nil {
			return err
		}
		r.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		r.Header.Set("Accept", "application/vnd.github+json")
		r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		return s.githubJSON(r, into)
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := get("/user", &user); err != nil {
		return nil, fmt.Errorf("read user: %w", err)
	}
	if user.ID <= 0 {
		return nil, errors.New("read user: no id")
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := get("/user/emails", &emails); err != nil {
		return nil, fmt.Errorf("read emails: %w", err)
	}
	id := &GitHubIdentity{ID: strconv.FormatInt(user.ID, 10), Login: clip(user.Login, 100), Name: clip(user.Name, 200)}
	for _, e := range emails {
		if e.Primary && e.Verified {
			id.Email = NormalizeEmail(e.Email)
		}
	}
	if !validEmail(id.Email) {
		return nil, errors.New("the GitHub account has no verified primary email")
	}
	return id, nil
}

// githubJSON sends req and decodes a 200 JSON answer of at most 1 MiB.
func (s *Service) githubJSON(req *http.Request, into any) error {
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGitHubBody))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.Unmarshal(body, into)
}
