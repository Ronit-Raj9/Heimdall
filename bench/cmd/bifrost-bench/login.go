package main

// `heimdall login` and `heimdall logout` (lane Z02): device login (RFC
// 8628) against a hosted server, or a pasted token with -with-token (CI,
// local servers). The token is stored 0600 under ~/.config/heimdall
// (connectlocal.Credentials) and used by connect, first-look, serve and
// mcp serve when HEIMDALL_TOKEN is not set.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/connectlocal"
)

const loginUsage = `usage: bifrost-bench login [-url https://heimdall.example.com] [-scope evaluate|read|admin] [-with-token] [-no-browser] [-json]

Signs this machine in to a Heimdall server. Prints a short code and a link;
open the link where you are signed in, check the code and approve. The new
org API token (scope evaluate by default, 90 days, revocable under Settings >
API tokens) is stored in ~/.config/heimdall/credentials.json (mode 0600).

  -with-token   read a token from stdin instead (CI, or a local server's
                write token): echo "$HEIMDALL_TOKEN" | heimdall login -with-token
The server is -url, else HEIMDALL_URL, else the last one you logged in to.`

const logoutUsage = `usage: bifrost-bench logout [-url https://heimdall.example.com]

Forgets the stored token for the server. The token stays valid until you
revoke it under Settings > API tokens.`

func init() {
	register("login", "sign this machine in to a Heimdall server (device code in the browser, or -with-token)", runLogin)
	register("logout", "forget the stored token for a Heimdall server", runLogout)
}

func runLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("url", "", "Heimdall server address (env HEIMDALL_URL)")
	scope := fs.String("scope", "evaluate", "what the token may do: read, evaluate or admin")
	withToken := fs.Bool("with-token", false, "read the token from stdin instead of the browser")
	noBrowser := fs.Bool("no-browser", false, "print the link only; do not open a browser")
	asJSON := fs.Bool("json", false, "print the result as JSON (no token in it)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, loginUsage)
		return exitUsage
	}
	srv, _, _ := connectlocal.Resolve(*server, "")
	if srv == "" {
		return fail("login: which server? pass -url https://your-heimdall or set HEIMDALL_URL")
	}
	if u, err := url.Parse(srv); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fail("login: -url must be an http(s) address, got %q", srv)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := doLogin(ctx, loginOptions{server: srv, scope: *scope, withToken: *withToken, openBrowser: !*noBrowser,
		stdin: os.Stdin, out: os.Stderr})
	if err != nil {
		return fail("login: %v", err)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(b))
		return exitOK
	}
	org := res.Org
	if org == "" {
		org = res.OrgID
	}
	fmt.Fprintf(os.Stderr, "Logged in to %s as organization %s (role %s, scope %s). Token saved to %s.\n",
		res.Server, org, res.Role, strings.Join(res.Scopes, ","), res.Path)
	fmt.Fprintln(os.Stderr, "next: heimdall connect <your agent's URL, a curl command, or run it inside your repository>")
	return exitOK
}

type loginOptions struct {
	server      string
	scope       string
	withToken   bool
	openBrowser bool
	stdin       io.Reader
	out         io.Writer
	// poll overrides the device poll interval (tests).
	poll time.Duration
}

// loginResult is what login stored (never the token itself).
type loginResult struct {
	Server string   `json:"server"`
	OrgID  string   `json:"org_id,omitempty"`
	Org    string   `json:"org,omitempty"`
	Role   string   `json:"role,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
	Local  bool     `json:"local,omitempty"`
	Path   string   `json:"path"`
}

func doLogin(ctx context.Context, o loginOptions) (*loginResult, error) {
	api := newOnboardAPI(o.server, "")
	var info struct {
		AuthMode string `json:"auth_mode"`
	}
	if _, err := api.call(ctx, http.MethodGet, "/api/bench/auth/config", nil, &info); err != nil {
		return nil, err
	}
	local := info.AuthMode != "hosted"
	cred := &connectlocal.ServerCred{CreatedAt: time.Now().UTC(), Local: local}
	switch {
	case o.withToken:
		tok, err := readToken(o.stdin)
		if err != nil {
			return nil, err
		}
		cred.Token = tok
	case local:
		return nil, errors.New("this is a local Heimdall server: it has no accounts to log in to; pass its write token with " +
			"`echo \"$BENCH_WRITE_TOKEN\" | heimdall login -with-token -url " + o.server + "`")
	default:
		tok, err := deviceLogin(ctx, api, o)
		if err != nil {
			return nil, err
		}
		cred.Token, cred.OrgID, cred.Role, cred.Scopes, cred.TokenID = tok.AccessToken, tok.OrgID, tok.Role, tok.Scopes, tok.TokenID
		if tok.ExpiresIn > 0 {
			cred.ExpiresAt = time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second)
		}
	}
	// Check the token and learn the org's name.
	authed := newOnboardAPI(o.server, cred.Token)
	if !local {
		var orgs struct {
			Orgs []struct {
				ID, Slug, Name, Role string
			} `json:"orgs"`
		}
		if _, err := authed.call(ctx, http.MethodGet, "/api/bench/orgs", nil, &orgs); err != nil {
			return nil, fmt.Errorf("the token does not work: %w", err)
		}
		if len(orgs.Orgs) > 0 {
			cred.OrgID, cred.Org = orgs.Orgs[0].ID, orgs.Orgs[0].Slug
			if cred.Role == "" {
				cred.Role = orgs.Orgs[0].Role
			}
		}
	} else if _, err := authed.call(ctx, http.MethodGet, "/api/v1/agents", nil, nil); err != nil {
		return nil, fmt.Errorf("the token does not work: %w", err)
	}
	c, err := connectlocal.LoadCredentials()
	if err != nil {
		return nil, err
	}
	c.Servers[o.server] = cred
	c.Default = o.server
	path, err := c.Save()
	if err != nil {
		return nil, err
	}
	return &loginResult{Server: o.server, OrgID: cred.OrgID, Org: cred.Org, Role: cred.Role, Scopes: cred.Scopes, Local: local, Path: path}, nil
}

func readToken(in io.Reader) (string, error) {
	if in == nil {
		return "", errors.New("-with-token reads the token from stdin")
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	tok := strings.TrimSpace(line)
	if tok == "" {
		return "", errors.New("no token on stdin: echo \"$HEIMDALL_TOKEN\" | heimdall login -with-token")
	}
	return tok, nil
}

type deviceToken struct {
	AccessToken string   `json:"access_token"`
	ExpiresIn   int      `json:"expires_in"`
	OrgID       string   `json:"org_id"`
	Role        string   `json:"role"`
	TokenID     string   `json:"token_id"`
	Scopes      []string `json:"scopes"`
}

// deviceLogin runs RFC 8628: get a code, show it, poll until approved.
func deviceLogin(ctx context.Context, api *onboardAPI, o loginOptions) (*deviceToken, error) {
	host, _ := os.Hostname()
	var code struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if _, err := api.call(ctx, http.MethodPost, "/api/bench/auth/device/code",
		map[string]any{"client_id": "heimdall-cli", "client_name": "heimdall CLI on " + host, "scope": o.scope}, &code); err != nil {
		if missingRoute(err) {
			return nil, errors.New("this server has no device login yet; create a token under Settings > API tokens and run `heimdall login -with-token`")
		}
		return nil, err
	}
	fmt.Fprintf(o.out, "\nOpen %s and enter the code\n\n    %s\n\n", code.VerificationURI, code.UserCode)
	if o.openBrowser && openBrowser(code.VerificationURIComplete) == nil {
		fmt.Fprintln(o.out, "(opened your browser; check the code matches, then approve)")
	}
	fmt.Fprintf(o.out, "Waiting for approval (the code expires in %d minutes)...\n", max(1, code.ExpiresIn/60))
	interval := time.Duration(code.Interval) * time.Second
	if o.poll > 0 {
		interval = o.poll
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		var tok deviceToken
		_, err := api.call(ctx, http.MethodPost, "/api/bench/auth/device/token",
			map[string]any{"grant_type": "urn:ietf:params:oauth:grant-type:device_code", "device_code": code.DeviceCode, "client_id": "heimdall-cli"}, &tok)
		if err == nil && tok.AccessToken != "" {
			return &tok, nil
		}
		var e *mcpAPIError
		if !errors.As(err, &e) || e.Status != http.StatusBadRequest {
			return nil, err
		}
		var body struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
			Interval    int    `json:"interval"`
		}
		_ = json.Unmarshal([]byte(e.Message), &body)
		switch body.Error {
		case "authorization_pending":
		case "slow_down":
			if body.Interval > 0 && o.poll == 0 {
				interval = time.Duration(body.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
		case "access_denied":
			return nil, errors.New("the login was denied in the browser")
		case "expired_token":
			return nil, errors.New("the code expired before it was approved: run heimdall login again")
		default:
			return nil, fmt.Errorf("device login: %s %s", body.Error, body.Description)
		}
		if code.ExpiresIn > 0 && time.Now().After(deadline) {
			return nil, errors.New("the code expired before it was approved: run heimdall login again")
		}
	}
}

// openBrowser opens a link with the desktop's opener, when there is one.
func openBrowser(link string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no display")
		}
		cmd = exec.Command("xdg-open", link)
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Start()
}

func runLogout(args []string) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	server := fs.String("url", "", "Heimdall server address (env HEIMDALL_URL)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, logoutUsage)
		return exitUsage
	}
	c, err := connectlocal.LoadCredentials()
	if err != nil {
		return fail("logout: %v", err)
	}
	srv := connectlocal.NormalizeServer(*server)
	if srv == "" {
		srv = connectlocal.NormalizeServer(os.Getenv("HEIMDALL_URL"))
	}
	if srv == "" {
		srv = c.Default
	}
	if _, ok := c.Servers[srv]; !ok {
		fmt.Fprintf(os.Stderr, "not logged in to %q\n", srv)
		return exitOK
	}
	delete(c.Servers, srv)
	if c.Default == srv {
		c.Default = ""
	}
	if _, err := c.Save(); err != nil {
		return fail("logout: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Forgot the token for %s. It stays valid until you revoke it under Settings > API tokens.\n", srv)
	return exitOK
}
