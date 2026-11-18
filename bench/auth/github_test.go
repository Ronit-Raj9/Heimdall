package auth

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/valyala/fasthttp"
)

// fakeGitHub is a fake OAuth provider: one code per user, configurable
// emails. Tests never reach github.com.
type fakeGitHub struct {
	mu       sync.Mutex
	srv      *httptest.Server
	users    map[string]fakeGHUser // code -> user
	exchange int
}

type fakeGHUser struct {
	ID     int64
	Login  string
	Emails []map[string]any
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{users: map[string]fakeGHUser{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.exchange++
		u, ok := f.users[r.PostForm.Get("code")]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !ok || r.PostForm.Get("client_secret") != "shh" || r.Header.Get("Accept") != "application/json" {
			json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		_ = u
		json.NewEncoder(w).Encode(map[string]string{"access_token": "at-" + r.PostForm.Get("code"), "token_type": "bearer"})
	})
	who := func(r *http.Request) (fakeGHUser, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		u, ok := f.users[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer at-")]
		return u, ok
	}
	mux.HandleFunc("/api/user", func(w http.ResponseWriter, r *http.Request) {
		u, ok := who(r)
		if !ok {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": u.ID, "login": u.Login, "name": strings.ToUpper(u.Login)})
	})
	mux.HandleFunc("/api/user/emails", func(w http.ResponseWriter, r *http.Request) {
		u, ok := who(r)
		if !ok {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(u.Emails)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) add(code string, u fakeGHUser) {
	f.mu.Lock()
	f.users[code] = u
	f.mu.Unlock()
}

func primary(email string, verified bool) []map[string]any {
	return []map[string]any{
		{"email": "noreply@users.example", "primary": false, "verified": true},
		{"email": email, "primary": true, "verified": verified},
	}
}

func withGitHub(e *testEnv, f *fakeGitHub) {
	e.svc.cfg.GitHub = &GitHubConfig{ClientID: "cid", ClientSecret: "shh",
		AuthorizeURL: f.srv.URL + "/login/oauth/authorize", TokenURL: f.srv.URL + "/login/oauth/access_token", APIURL: f.srv.URL + "/api"}
}

// ghFlow runs start and callback the way a browser would and returns the
// callback's outcome.
func ghFlow(t *testing.T, e *testEnv, code, returnTo string, tamper func(state, cookie *string)) (*NewSession, string, error) {
	t.Helper()
	start := &fasthttp.RequestCtx{}
	start.SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP("198.51.100.1"), Port: 1})
	to, err := e.svc.GitHubStart(start, returnTo)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to)
	q := u.Query()
	if q.Get("client_id") != "cid" || q.Get("redirect_uri") != "https://heimdall.example"+GitHubCallbackPath || q.Get("scope") != githubScope {
		t.Fatalf("authorize URL = %s", to)
	}
	state := q.Get("state")
	cookie := string(start.Response.Header.PeekCookie(OAuthStateCookie))
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "path="+githubCookiePath) {
		t.Fatalf("state cookie = %s", cookie)
	}
	cookieVal := state
	if tamper != nil {
		tamper(&state, &cookieVal)
	}
	cb := &fasthttp.RequestCtx{}
	cb.SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP("198.51.100.1"), Port: 1})
	cb.Request.SetRequestURI(GitHubCallbackPath + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state))
	if cookieVal != "" {
		cb.Request.Header.SetCookie(OAuthStateCookie, cookieVal)
	}
	return e.svc.GitHubCallback(cb, "")
}

func TestGitHubBootstrapLinkAndPolicy(t *testing.T) {
	f := newFakeGitHub(t)
	e := newTestEnv(t, SignupInvite)
	withGitHub(e, f)
	f.add("c1", fakeGHUser{ID: 101, Login: "ada", Emails: primary("Ada@Example.com", true)})
	sess, back, err := ghFlow(t, e, "c1", "/experiments?x=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if back != "/experiments?x=1" {
		t.Fatalf("return_to = %q", back)
	}
	p := e.principal(t, sess)
	if p.Email != "ada@example.com" || p.Role != RoleOwner {
		t.Fatalf("bootstrap via GitHub = %+v", p)
	}
	// Same GitHub id again: same user, no new org.
	f.add("c2", fakeGHUser{ID: 101, Login: "ada", Emails: primary("ada@example.com", true)})
	sess2, back, err := ghFlow(t, e, "c2", "https://evil.example/", nil)
	if err != nil || back != "/" || e.principal(t, sess2).UserID != p.UserID {
		t.Fatalf("second sign-in = %v %q", err, back)
	}
	// Invite policy: a stranger without an invitation is refused.
	f.add("c3", fakeGHUser{ID: 202, Login: "stranger", Emails: primary("stranger@example.com", true)})
	if _, _, err := ghFlow(t, e, "c3", "", nil); !errors.Is(err, ErrSignupNotAllowed) {
		t.Fatalf("stranger = %v", err)
	}
	// With a pending invitation the stranger joins that org.
	if _, err := e.svc.CreateInvitation(bg, p, p.OrgID, "stranger@example.com", RoleViewer, ""); err != nil {
		t.Fatal(err)
	}
	f.add("c4", fakeGHUser{ID: 202, Login: "stranger", Emails: primary("stranger@example.com", true)})
	s4, _, err := ghFlow(t, e, "c4", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sp := e.principal(t, s4); sp.OrgID != p.OrgID || sp.Role != RoleViewer {
		t.Fatalf("invited GitHub user = %+v", sp)
	}
}

func TestGitHubLinksExistingAccountByVerifiedEmail(t *testing.T) {
	f := newFakeGitHub(t)
	e := newTestEnv(t, SignupOpen)
	withGitHub(e, f)
	u, _ := e.signup(t, "ada@example.com")
	f.add("unverified", fakeGHUser{ID: 7, Login: "ada", Emails: primary("ada@example.com", false)})
	if _, _, err := ghFlow(t, e, "unverified", "", nil); err == nil {
		t.Fatal("unverified email signed in")
	}
	f.add("ok", fakeGHUser{ID: 7, Login: "ada", Emails: primary("ada@example.com", true)})
	sess, _, err := ghFlow(t, e, "ok", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.principal(t, sess).UserID != u.ID {
		t.Fatal("not linked to the existing account")
	}
	got, _ := e.svc.st.userByID(bg, e.d.SQL(), u.ID)
	if got.GitHubID != "7" {
		t.Fatalf("github_id = %q", got.GitHubID)
	}
	// Another GitHub account with the same verified email can not take it over.
	f.add("other", fakeGHUser{ID: 8, Login: "imposter", Emails: primary("ada@example.com", true)})
	if _, _, err := ghFlow(t, e, "other", "", nil); err == nil {
		t.Fatal("second GitHub account took over a linked account")
	}
}

func TestGitHubStateChecks(t *testing.T) {
	f := newFakeGitHub(t)
	e := newTestEnv(t, SignupOpen)
	withGitHub(e, f)
	f.add("c", fakeGHUser{ID: 1, Login: "a", Emails: primary("a@example.com", true)})
	cases := map[string]func(state, cookie *string){
		"no cookie (another browser)": func(_, c *string) { *c = "" },
		"cookie mismatch":             func(_, c *string) { *c = "something-else" },
		"forged state":                func(s, c *string) { *s, *c = "forged", "forged" },
	}
	for name, tamper := range cases {
		if _, _, err := ghFlow(t, e, "c", "", tamper); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A state works once.
	start := &fasthttp.RequestCtx{}
	to, _ := e.svc.GitHubStart(start, "/")
	u, _ := url.Parse(to)
	state := u.Query().Get("state")
	call := func() error {
		cb := &fasthttp.RequestCtx{}
		cb.Request.SetRequestURI(GitHubCallbackPath + "?code=c&state=" + url.QueryEscape(state))
		cb.Request.Header.SetCookie(OAuthStateCookie, state)
		_, _, err := e.svc.GitHubCallback(cb, "")
		return err
	}
	if err := call(); err != nil {
		t.Fatal(err)
	}
	if err := call(); err == nil {
		t.Fatal("state replayed")
	}
	// A bad code fails without creating anything.
	if _, _, err := ghFlow(t, e, "wrong-code", "", nil); err == nil {
		t.Fatal("bad code accepted")
	}
	// The state is stored hashed.
	e.svc.GitHubStart(&fasthttp.RequestCtx{}, "/")
	var n int
	e.d.QueryRow(bg, `SELECT COUNT(*) FROM oauth_states WHERE length(state_hash) = 64`).Scan(&n)
	if n == 0 {
		t.Fatal("oauth state not stored as sha256")
	}
}

func TestGitHubDisabled(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	if _, err := e.svc.GitHubStart(&fasthttp.RequestCtx{}, "/"); !errors.Is(err, ErrGitHubDisabled) {
		t.Fatalf("start without app = %v", err)
	}
}
