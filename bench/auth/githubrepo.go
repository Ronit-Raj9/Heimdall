package auth

// GitHub repository connection (web parity F6): a second OAuth flow, apart
// from GitHub sign-in, that asks for the `repo` scope so an org can list its
// repositories, read their refs and open the CI-install pull request. It
// reuses the sign-in OAuth app (HEIMDALL_GITHUB_CLIENT_ID and
// HEIMDALL_GITHUB_CLIENT_SECRET). Its callback is a subdirectory of the
// sign-in callback, so the OAuth app's single registered callback URL
// accepts it (GitHub requires the redirect path to sit under it).
//
// The access token is never stored here: the caller keeps it in the org's
// keystore (Kind secret, provider "github").

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/valyala/fasthttp"
)

const (
	// GitHubRepoCallbackPath is where GitHub sends the browser back after a
	// repository connect. It sits under GitHubCallbackPath on purpose.
	GitHubRepoCallbackPath = GitHubCallbackPath + "/repo"
	// GitHubRepoProvider is the keystore provider of a stored repo token.
	GitHubRepoProvider = "github"
	// GitHubRepoStateCookie binds a connect to the browser that started it.
	GitHubRepoStateCookie = "heimdall_gh_repo_state"

	githubRepoScope  = "repo"
	repoStateTTL     = 10 * time.Minute
	maxRepoStates    = 1000
	maxGitHubAPIBody = 4 << 20
)

// ErrGitHubRepoDisabled answers when no OAuth app is configured.
var ErrGitHubRepoDisabled = errors.New("GitHub is not configured on this server: set HEIMDALL_GITHUB_CLIENT_ID and HEIMDALL_GITHUB_CLIENT_SECRET (auth.github in the config) and restart")

// ErrGitHubRepoState is a callback whose state does not match a pending connect.
var ErrGitHubRepoState = errors.New("GitHub connect: state unknown, expired or from another browser; start again from Settings > GitHub")

// RepoConnect is a pending connect, recorded by Start, taken by Callback.
type RepoConnect struct {
	OrgID, UserID, ReturnTo string
	created                 time.Time
}

// RepoToken is what a finished connect yields.
type RepoToken struct {
	Token  string // secret: store it, never log or return it
	Login  string
	Scopes []string
	OrgID  string
	UserID string
	Return string
}

// GitHubRepoConnector runs the repo-scope OAuth flow and calls the GitHub
// API with a stored token. Pending states live in memory (one hosted
// server); a restart only means the user clicks Connect again.
type GitHubRepoConnector struct {
	gh        *GitHubConfig
	publicURL string
	secure    bool
	HTTP      *http.Client
	now       func() time.Time

	mu     sync.Mutex
	states map[string]RepoConnect // key: HashSecret(state)
}

// NewGitHubRepoConnector builds the connector from the auth config. A nil
// cfg.GitHub gives a connector whose calls answer ErrGitHubRepoDisabled.
func NewGitHubRepoConnector(cfg Config) *GitHubRepoConnector {
	return &GitHubRepoConnector{
		gh: cfg.GitHub, publicURL: strings.TrimRight(cfg.PublicURL, "/"), secure: cfg.SecureCookies(),
		HTTP: &http.Client{Timeout: 30 * time.Second}, now: time.Now, states: map[string]RepoConnect{},
	}
}

// Configured reports whether an OAuth app is set.
func (c *GitHubRepoConnector) Configured() bool {
	return c != nil && c.gh != nil && c.gh.ClientID != "" && c.gh.ClientSecret != ""
}

// PublicURL is the origin the callback redirects the browser back to.
func (c *GitHubRepoConnector) PublicURL() string { return c.publicURL }

// Start records a pending connect for (org, user), sets the state cookie
// and returns the GitHub authorize URL.
func (c *GitHubRepoConnector) Start(ctx *fasthttp.RequestCtx, orgID, userID, returnTo string) (string, error) {
	if !c.Configured() {
		return "", ErrGitHubRepoDisabled
	}
	state, err := randomSecret()
	if err != nil {
		return "", err
	}
	now := c.now()
	c.mu.Lock()
	for k, v := range c.states {
		if now.Sub(v.created) > repoStateTTL {
			delete(c.states, k)
		}
	}
	if len(c.states) >= maxRepoStates {
		c.mu.Unlock()
		return "", errors.New("too many pending GitHub connects; try again in a few minutes")
	}
	c.states[HashSecret(state)] = RepoConnect{OrgID: orgID, UserID: userID, ReturnTo: safeReturnTo(returnTo), created: now}
	c.mu.Unlock()

	ck := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(ck)
	ck.SetKey(GitHubRepoStateCookie)
	ck.SetValue(state)
	ck.SetPath(GitHubRepoCallbackPath)
	ck.SetHTTPOnly(true)
	ck.SetSecure(c.secure)
	ck.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	ck.SetMaxAge(int(repoStateTTL / time.Second))
	ctx.Response.Header.SetCookie(ck)

	q := url.Values{}
	q.Set("client_id", c.gh.ClientID)
	q.Set("redirect_uri", c.publicURL+GitHubRepoCallbackPath)
	q.Set("scope", githubRepoScope)
	q.Set("state", state)
	q.Set("allow_signup", "false")
	return c.gh.authorizeURL() + "?" + q.Encode(), nil
}

// Callback checks the state against the cookie and the pending connect
// (single use, 10 minutes, same user), exchanges the code and reads the
// GitHub login. userID is the signed-in caller.
func (c *GitHubRepoConnector) Callback(ctx *fasthttp.RequestCtx, userID string) (*RepoToken, error) {
	if !c.Configured() {
		return nil, ErrGitHubRepoDisabled
	}
	args := ctx.QueryArgs()
	state, code := string(args.Peek("state")), string(args.Peek("code"))
	cookie := string(ctx.Request.Header.Cookie(GitHubRepoStateCookie))
	clear := fasthttp.AcquireCookie()
	clear.SetKey(GitHubRepoStateCookie)
	clear.SetPath(GitHubRepoCallbackPath)
	clear.SetHTTPOnly(true)
	clear.SetSecure(c.secure)
	clear.SetMaxAge(-1)
	ctx.Response.Header.SetCookie(clear)
	fasthttp.ReleaseCookie(clear)
	if e := string(args.Peek("error")); e != "" {
		return nil, fmt.Errorf("GitHub connect cancelled (%s)", clip(e, 64))
	}
	if state == "" || code == "" || len(state) > 128 || len(code) > 512 || cookie == "" ||
		subtle.ConstantTimeCompare([]byte(state), []byte(cookie)) != 1 {
		return nil, ErrGitHubRepoState
	}
	h := HashSecret(state)
	c.mu.Lock()
	pc, ok := c.states[h]
	delete(c.states, h)
	c.mu.Unlock()
	if !ok || c.now().Sub(pc.created) > repoStateTTL || pc.UserID != userID {
		return nil, ErrGitHubRepoState
	}
	dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, scopes, err := c.exchange(dctx, code)
	if err != nil {
		return nil, err
	}
	if !hasRepoScope(scopes) {
		return nil, errors.New("GitHub connect: the token was granted without the repo scope; approve the repo access GitHub asks for")
	}
	u, err := c.User(dctx, tok)
	if err != nil {
		return nil, err
	}
	return &RepoToken{Token: tok, Login: u.Login, Scopes: scopes, OrgID: pc.OrgID, UserID: pc.UserID, Return: pc.ReturnTo}, nil
}

func hasRepoScope(scopes []string) bool {
	for _, s := range scopes {
		if s == "repo" {
			return true
		}
	}
	return false
}

func (c *GitHubRepoConnector) exchange(ctx context.Context, code string) (string, []string, error) {
	form := url.Values{}
	form.Set("client_id", c.gh.ClientID)
	form.Set("client_secret", c.gh.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", c.publicURL+GitHubRepoCallbackPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gh.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if _, err := c.do(req, &out); err != nil {
		return "", nil, fmt.Errorf("GitHub connect: exchange code: %w", err)
	}
	if out.Error != "" || out.AccessToken == "" {
		return "", nil, fmt.Errorf("GitHub connect: exchange code: %s %s", clip(out.Error, 64), clip(out.ErrorDesc, 200))
	}
	var scopes []string
	for _, s := range strings.FieldsFunc(out.Scope, func(r rune) bool { return r == ',' || r == ' ' }) {
		scopes = append(scopes, strings.TrimSpace(s))
	}
	return out.AccessToken, scopes, nil
}

// GitHubAPIError is a non-2xx GitHub API answer. Msg is GitHub's own
// message (it never holds the token).
type GitHubAPIError struct {
	Status int
	Msg    string
}

func (e *GitHubAPIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "GitHub rejected the stored token (revoked or expired); reconnect GitHub in Settings > GitHub"
	case http.StatusNotFound:
		return "not found on GitHub, or the connected account has no access to it"
	}
	if e.Msg != "" {
		return fmt.Sprintf("GitHub answered %d: %s", e.Status, e.Msg)
	}
	return fmt.Sprintf("GitHub answered %d", e.Status)
}

// do sends req and decodes a 2xx JSON answer into into (when non-nil).
func (c *GitHubRepoConnector) do(req *http.Request, into any) (int, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGitHubAPIBody))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &m)
		return resp.StatusCode, &GitHubAPIError{Status: resp.StatusCode, Msg: clip(m.Message, 300)}
	}
	if into == nil || len(body) == 0 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.Unmarshal(body, into)
}

func (c *GitHubRepoConnector) api(ctx context.Context, token, method, path string, in, out any) (int, error) {
	if c.gh == nil {
		return 0, ErrGitHubRepoDisabled
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.gh.apiURL()+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, out)
}

// GitHubUser is the connected account.
type GitHubUser struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

// User reads the token's account.
func (c *GitHubRepoConnector) User(ctx context.Context, token string) (*GitHubUser, error) {
	var u GitHubUser
	if _, err := c.api(ctx, token, http.MethodGet, "/user", nil, &u); err != nil {
		return nil, err
	}
	if u.Login == "" {
		return nil, errors.New("GitHub connect: the account has no login")
	}
	u.Login, u.Name = clip(u.Login, 100), clip(u.Name, 200)
	return &u, nil
}

// GitHubRepo is one repository the account can see.
type GitHubRepo struct {
	FullName      string `json:"full_name"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	Description   string `json:"description,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	CanPush       bool   `json:"can_push"`
}

// Repos lists one page (1-based, 100 per page) of the account's repos,
// most recently updated first. more says whether another page may exist.
func (c *GitHubRepoConnector) Repos(ctx context.Context, token string, page int) (repos []GitHubRepo, more bool, err error) {
	if page < 1 {
		page = 1
	}
	var raw []struct {
		FullName      string `json:"full_name"`
		Name          string `json:"name"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
		HTMLURL       string `json:"html_url"`
		Description   string `json:"description"`
		UpdatedAt     string `json:"updated_at"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	path := "/user/repos?per_page=100&sort=updated&affiliation=owner,collaborator,organization_member&page=" + strconv.Itoa(page)
	if _, err := c.api(ctx, token, http.MethodGet, path, nil, &raw); err != nil {
		return nil, false, err
	}
	repos = make([]GitHubRepo, 0, len(raw))
	for _, r := range raw {
		repos = append(repos, GitHubRepo{
			FullName: r.FullName, Owner: r.Owner.Login, Name: r.Name, Private: r.Private, DefaultBranch: r.DefaultBranch,
			HTMLURL: r.HTMLURL, Description: clip(r.Description, 300), UpdatedAt: r.UpdatedAt, CanPush: r.Permissions.Push,
		})
	}
	return repos, len(raw) == 100, nil
}

// GitHubRef is a branch or tag.
type GitHubRef struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // branch | tag
	SHA  string `json:"sha"`
}

// GitHubRefs is a repository's default branch, branches and tags (first 100 each).
type GitHubRefs struct {
	DefaultBranch string      `json:"default_branch"`
	Refs          []GitHubRef `json:"refs"`
}

var (
	repoPartRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	branchRE   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
)

// ValidRepo reports whether owner and repo are safe GitHub names.
func ValidRepo(owner, repo string) bool {
	ok := func(s string) bool { return repoPartRE.MatchString(s) && s != "." && s != ".." }
	return ok(owner) && ok(repo)
}

// ValidBranch reports whether b is a safe branch name.
func ValidBranch(b string) bool {
	return branchRE.MatchString(b) && !strings.Contains(b, "..") && !strings.HasPrefix(b, "/") &&
		!strings.HasSuffix(b, "/") && !strings.HasSuffix(b, ".lock") && !strings.HasPrefix(b, "-")
}

func repoPath(owner, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

// escapeRefPath escapes each segment of a branch name or file path.
func escapeRefPath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// Refs lists branches and tags of owner/repo.
func (c *GitHubRepoConnector) Refs(ctx context.Context, token, owner, repo string) (*GitHubRefs, error) {
	if !ValidRepo(owner, repo) {
		return nil, &GitHubAPIError{Status: http.StatusBadRequest, Msg: "invalid owner or repository name"}
	}
	var meta struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := c.api(ctx, token, http.MethodGet, repoPath(owner, repo), nil, &meta); err != nil {
		return nil, err
	}
	var items []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	out := &GitHubRefs{DefaultBranch: meta.DefaultBranch, Refs: []GitHubRef{}}
	if _, err := c.api(ctx, token, http.MethodGet, repoPath(owner, repo)+"/branches?per_page=100", nil, &items); err != nil {
		return nil, err
	}
	for _, b := range items {
		out.Refs = append(out.Refs, GitHubRef{Name: b.Name, Kind: "branch", SHA: b.Commit.SHA})
	}
	items = nil
	if _, err := c.api(ctx, token, http.MethodGet, repoPath(owner, repo)+"/tags?per_page=100", nil, &items); err != nil {
		return nil, err
	}
	for _, t := range items {
		out.Refs = append(out.Refs, GitHubRef{Name: t.Name, Kind: "tag", SHA: t.Commit.SHA})
	}
	return out, nil
}

// CIInstall is a request to open the CI-install pull request.
type CIInstall struct {
	Owner, Repo string
	Base        string // "" = the default branch
	Branch      string // the new branch; "" = heimdall/ci-install-<unix>
	Path        string // the workflow file, e.g. .github/workflows/heimdall.yml
	Content     string // the workflow
	Title, Body string
	Force       bool // replace an existing workflow file
}

// CIInstallResult is the opened pull request.
type CIInstallResult struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Path   string `json:"path"`
}

// ErrWorkflowExists says the workflow file is already in the base branch.
var ErrWorkflowExists = errors.New("the workflow file already exists in the repository; tick Replace to overwrite it")

// OpenCIInstallPR creates a branch from the base, commits the workflow file
// on it and opens a pull request into the base.
func (c *GitHubRepoConnector) OpenCIInstallPR(ctx context.Context, token string, in CIInstall) (*CIInstallResult, error) {
	if !ValidRepo(in.Owner, in.Repo) {
		return nil, &GitHubAPIError{Status: http.StatusBadRequest, Msg: "invalid owner or repository name"}
	}
	rp := repoPath(in.Owner, in.Repo)
	if in.Base == "" {
		var meta struct {
			DefaultBranch string `json:"default_branch"`
		}
		if _, err := c.api(ctx, token, http.MethodGet, rp, nil, &meta); err != nil {
			return nil, err
		}
		in.Base = meta.DefaultBranch
	}
	if in.Branch == "" {
		in.Branch = "heimdall/ci-install-" + strconv.FormatInt(c.now().Unix(), 10)
	}
	if !ValidBranch(in.Base) || !ValidBranch(in.Branch) {
		return nil, &GitHubAPIError{Status: http.StatusBadRequest, Msg: "invalid branch name"}
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if _, err := c.api(ctx, token, http.MethodGet, rp+"/git/ref/heads/"+escapeRefPath(in.Base), nil, &ref); err != nil {
		return nil, fmt.Errorf("read base branch %s: %w", in.Base, err)
	}
	// An existing file on the base branch: refuse unless Force, else update it by sha.
	var existing struct {
		SHA string `json:"sha"`
	}
	fileURL := rp + "/contents/" + escapeRefPath(in.Path)
	_, err := c.api(ctx, token, http.MethodGet, fileURL+"?ref="+url.QueryEscape(in.Base), nil, &existing)
	var ae *GitHubAPIError
	switch {
	case err == nil:
		if !in.Force {
			return nil, ErrWorkflowExists
		}
	case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
		existing.SHA = ""
	default:
		return nil, err
	}
	if _, err := c.api(ctx, token, http.MethodPost, rp+"/git/refs",
		map[string]string{"ref": "refs/heads/" + in.Branch, "sha": ref.Object.SHA}, nil); err != nil {
		return nil, fmt.Errorf("create branch %s: %w", in.Branch, err)
	}
	put := map[string]string{
		"message": "ci: add the Heimdall pull-request gate",
		"content": base64.StdEncoding.EncodeToString([]byte(in.Content)),
		"branch":  in.Branch,
	}
	if existing.SHA != "" {
		put["sha"] = existing.SHA
	}
	if _, err := c.api(ctx, token, http.MethodPut, fileURL, put, nil); err != nil {
		return nil, fmt.Errorf("commit %s: %w", in.Path, err)
	}
	var pr struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if _, err := c.api(ctx, token, http.MethodPost, rp+"/pulls",
		map[string]string{"title": in.Title, "head": in.Branch, "base": in.Base, "body": in.Body}, &pr); err != nil {
		return nil, fmt.Errorf("open pull request: %w", err)
	}
	return &CIInstallResult{Number: pr.Number, URL: pr.HTMLURL, Branch: in.Branch, Base: in.Base, Path: in.Path}, nil
}

// FindRepoToken returns the org's stored GitHub repo token and its key
// metadata, or keystore.ErrNotFound when GitHub is not connected. Other
// areas (task mining, integrations) use it; the token stays server-side.
func FindRepoToken(ctx context.Context, keys keystore.Store, orgID string) (string, *keystore.Key, error) {
	k, err := RepoKey(ctx, keys, orgID)
	if err != nil {
		return "", nil, err
	}
	tok, err := keys.Reveal(ctx, orgID, k.ID)
	if err != nil {
		return "", nil, err
	}
	return tok, k, nil
}

// RepoKey returns the metadata of the org's GitHub repo token.
func RepoKey(ctx context.Context, keys keystore.Store, orgID string) (*keystore.Key, error) {
	if keys == nil {
		return nil, keystore.ErrNotFound
	}
	list, err := keys.List(ctx, orgID, keystore.KindSecret)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Provider == GitHubRepoProvider {
			return &list[i], nil
		}
	}
	return nil, keystore.ErrNotFound
}
