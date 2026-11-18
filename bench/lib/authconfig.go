package lib

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Auth modes (Auth area, docs/platform-design.md and docs/auth.md).
const (
	AuthModeLocal  = "local"  // default: single user, write token guards writes
	AuthModeHosted = "hosted" // multi-user: sessions, API tokens, organizations
)

// Signup policies for hosted mode.
const (
	SignupInvite = "invite" // default: new accounts only through an invitation
	SignupOpen   = "open"   // anyone may sign up and gets an organization of their own
	SignupClosed = "closed" // no web signup; accounts come from the CLI
)

// Auth defaults.
const (
	DefaultSessionTTLHours  = 168 // absolute session lifetime
	DefaultSessionIdleHours = 24  // a session unused this long expires
	DefaultInviteTTLHours   = 168
)

// AuthConfig is the auth section of deploy/bench.yaml:
//
//	auth:
//	  mode: local                 # local | hosted
//	  public_url: https://heimdall.example.com
//	  session_ttl_h: 168
//	  session_idle_h: 24
//	  signup: invite              # invite | open | closed
//	  invite_ttl_h: 168
//	  github:
//	    client_id_env: HEIMDALL_GITHUB_CLIENT_ID
//	    client_secret_env: HEIMDALL_GITHUB_CLIENT_SECRET
//
// Secrets are named by environment variable, never written in the file.
type AuthConfig struct {
	Mode         string `yaml:"mode" json:"mode,omitempty"`
	PublicURL    string `yaml:"public_url" json:"public_url,omitempty"`
	SessionTTLH  int    `yaml:"session_ttl_h" json:"session_ttl_h,omitempty"`
	SessionIdleH int    `yaml:"session_idle_h" json:"session_idle_h,omitempty"`
	Signup       string `yaml:"signup" json:"signup,omitempty"`
	InviteTTLH   int    `yaml:"invite_ttl_h" json:"invite_ttl_h,omitempty"`
	// TrustedProxies are the IPs or CIDRs of reverse proxies in front of
	// the server (Caddy in deploy/hosted). Only a request arriving from one
	// of them has its client address read from X-Forwarded-For, for rate
	// limits and audit rows. Empty trusts no header.
	TrustedProxies []string         `yaml:"trusted_proxies" json:"trusted_proxies,omitempty"`
	GitHub         GitHubAuthConfig `yaml:"github" json:"github,omitzero"`
}

// GitHubAuthConfig names the environment variables of the GitHub OAuth app.
// GitHub sign-in is on only when both variables are set and non-empty.
type GitHubAuthConfig struct {
	ClientIDEnv     string `yaml:"client_id_env" json:"client_id_env,omitempty"`
	ClientSecretEnv string `yaml:"client_secret_env" json:"client_secret_env,omitempty"`
}

// IsHosted reports whether auth.mode is hosted.
func (a AuthConfig) IsHosted() bool {
	return strings.EqualFold(strings.TrimSpace(a.Mode), AuthModeHosted)
}

// SignupPolicy returns the signup policy with its default.
func (a AuthConfig) SignupPolicy() string {
	if s := strings.ToLower(strings.TrimSpace(a.Signup)); s != "" {
		return s
	}
	return SignupInvite
}

// Validate checks the auth section. Local mode ignores every other field.
func (a AuthConfig) Validate() error {
	switch strings.ToLower(strings.TrimSpace(a.Mode)) {
	case "", AuthModeLocal:
		return nil
	case AuthModeHosted:
	default:
		return fmt.Errorf("auth.mode must be local or hosted, got %q", a.Mode)
	}
	if a.PublicURL == "" {
		return fmt.Errorf("auth.public_url is required in hosted mode")
	}
	u, err := url.Parse(a.PublicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("auth.public_url %q must be an http(s) origin such as https://heimdall.example.com", a.PublicURL)
	}
	if a.SessionTTLH < 0 || a.SessionIdleH < 0 || a.InviteTTLH < 0 {
		return fmt.Errorf("auth.session_ttl_h, auth.session_idle_h and auth.invite_ttl_h must not be negative")
	}
	for _, p := range a.TrustedProxies {
		if _, err := ParseIPOrCIDR(p); err != nil {
			return fmt.Errorf("auth.trusted_proxies: %w", err)
		}
	}
	switch a.SignupPolicy() {
	case SignupInvite, SignupOpen, SignupClosed:
	default:
		return fmt.Errorf("auth.signup must be invite, open or closed, got %q", a.Signup)
	}
	return nil
}

// ParseIPOrCIDR parses "10.0.0.0/8" or a single address ("10.0.0.5", which
// becomes a /32 or /128).
func ParseIPOrCIDR(s string) (*net.IPNet, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP or CIDR", s)
		}
		return n, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("%q is not an IP or CIDR", s)
	}
	bits := 128
	if v4 := ip.To4(); v4 != nil {
		ip, bits = v4, 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
}
