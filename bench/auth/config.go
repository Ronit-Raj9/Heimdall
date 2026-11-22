package auth

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// SignupPolicy says who may create an account through the web.
type SignupPolicy string

// Signup policies.
const (
	SignupInvite SignupPolicy = lib.SignupInvite
	SignupOpen   SignupPolicy = lib.SignupOpen
	SignupClosed SignupPolicy = lib.SignupClosed
	// SignupAllowlist admits invitations, and self-serve signups from the
	// email domains in Config.SignupDomains (signupmode.go).
	SignupAllowlist SignupPolicy = lib.SignupAllowlist
)

// Config is the resolved auth configuration the service and middleware use.
type Config struct {
	// Hosted switches on sessions, API tokens and organizations. False is
	// local mode: today's write token and one implicit organization.
	Hosted bool
	// ServerMode is the bench mode carried in error bodies.
	ServerMode schemas.Mode
	// PublicURL is the origin browsers use, e.g. https://heimdall.example.com.
	PublicURL string
	// SessionTTL is a session's absolute lifetime; SessionIdle expires a
	// session unused for that long.
	SessionTTL  time.Duration
	SessionIdle time.Duration
	// InviteTTL is how long an invitation stays valid.
	InviteTTL time.Duration
	Signup    SignupPolicy
	// SignupDomains are the domains signup allowlist admits without an
	// invitation, normalized (lowercase, no "@"). A server admin can
	// override Signup and SignupDomains from the web (signupmode.go).
	SignupDomains []string
	// AllowedOrigins are the origins a cookie-authenticated write may come
	// from: the public URL's origin plus server.cors_origins.
	AllowedOrigins []string
	// GitHub is GitHub sign-in; nil when not configured, or when the
	// environment variables auth.github names are empty or unset.
	GitHub *GitHubConfig
	// GitHubOff says why GitHub sign-in is off although auth.github is
	// set; New logs it once. Empty otherwise.
	GitHubOff string
	// WriteToken is the local-mode write token (BENCH_WRITE_TOKEN).
	WriteToken string
	// TrustedProxies are reverse proxies whose X-Forwarded-For is believed.
	TrustedProxies []*net.IPNet
}

// GitHubConfig is a GitHub OAuth app. The URLs default to github.com and
// are overridden only by tests.
type GitHubConfig struct {
	ClientID     string
	ClientSecret string
	AuthorizeURL string // https://github.com/login/oauth/authorize
	TokenURL     string // https://github.com/login/oauth/access_token
	APIURL       string // https://api.github.com
}

// ConfigFrom resolves lib.Config's auth section, reading the GitHub secrets
// from the environment variables it names. Empty or unset variables turn
// GitHub sign-in off (Config.GitHubOff says so); they are never an error.
func ConfigFrom(c *lib.Config, writeToken string) (Config, error) {
	a := c.Auth
	if err := a.Validate(); err != nil {
		return Config{}, err
	}
	out := Config{
		Hosted:      a.IsHosted(),
		ServerMode:  c.Server.Mode,
		PublicURL:   strings.TrimRight(a.PublicURL, "/"),
		SessionTTL:  hours(a.SessionTTLH, lib.DefaultSessionTTLHours),
		SessionIdle: hours(a.SessionIdleH, lib.DefaultSessionIdleHours),
		InviteTTL:   hours(a.InviteTTLH, lib.DefaultInviteTTLHours),
		Signup:      SignupPolicy(a.SignupPolicy()),
		WriteToken:  writeToken,
	}
	for _, d := range a.SignupDomains {
		out.SignupDomains = append(out.SignupDomains, lib.NormalizeSignupDomain(d))
	}
	if !out.Hosted {
		return out, nil
	}
	for _, p := range a.TrustedProxies {
		n, err := lib.ParseIPOrCIDR(p)
		if err != nil {
			return Config{}, fmt.Errorf("auth.trusted_proxies: %w", err)
		}
		out.TrustedProxies = append(out.TrustedProxies, n)
	}
	if o := originOf(out.PublicURL); o != "" {
		out.AllowedOrigins = append(out.AllowedOrigins, o)
	}
	for _, o := range c.Server.CORSOrigins {
		if n := originOf(o); n != "" {
			out.AllowedOrigins = append(out.AllowedOrigins, n)
		}
	}
	idEnv, secretEnv := a.GitHub.ClientIDEnv, a.GitHub.ClientSecretEnv
	if idEnv != "" || secretEnv != "" {
		var id, secret string
		if idEnv != "" {
			id = strings.TrimSpace(os.Getenv(idEnv))
		}
		if secretEnv != "" {
			secret = strings.TrimSpace(os.Getenv(secretEnv))
		}
		if id != "" && secret != "" {
			out.GitHub = &GitHubConfig{ClientID: id, ClientSecret: secret}
		} else {
			// Named but empty or unset: GitHub sign-in is off, never a
			// startup failure. New logs this once.
			out.GitHubOff = fmt.Sprintf("GitHub sign-in is off: auth.github names %s and %s, which are not both set",
				orDefault(idEnv, "(none)"), orDefault(secretEnv, "(none)"))
		}
	}
	return out, nil
}

func hours(n, def int) time.Duration {
	if n <= 0 {
		n = def
	}
	return time.Duration(n) * time.Hour
}

// SecureCookies reports whether cookies carry the Secure flag: whenever the
// public URL is https.
func (c Config) SecureCookies() bool { return strings.HasPrefix(c.PublicURL, "https://") }

// originOf normalises an origin or URL to scheme://host[:port], lowercase,
// with the default port dropped. It returns "" for anything else.
func originOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return u.Scheme + "://" + host
}
