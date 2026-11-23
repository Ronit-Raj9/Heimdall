package auth

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
)

// Signup modes (Wave A, audit B10). Who may create an account from the web:
//
//   - invite: only someone holding an invitation (the default).
//   - allowlist: an invitation, or an address at one of the allowed email
//     domains, which gets an organization of its own. With no domains it is
//     the same as invite.
//   - open: anyone; every new account gets an organization of its own.
//   - closed: nobody; accounts come from `bifrost-bench user create`.
//
// The first account of an empty install (bootstrap) may always sign up,
// except under closed. The server's config (auth.signup and
// auth.signup_domains, set on hosted installs by HEIMDALL_SIGNUP and
// HEIMDALL_SIGNUP_DOMAINS) is the default; a server admin can override both
// from Admin, Signup, which stores the override in auth_settings. Every
// replica rereads the override at most signupReload after a change.

// signupReload is how long a replica keeps the signup override before it
// reads auth_settings again.
const signupReload = 30 * time.Second

// MaxSignupDomains bounds the allowlist.
const MaxSignupDomains = 100

// Setting keys in auth_settings.
const (
	settingSignupMode    = "signup.mode"
	settingSignupDomains = "signup.domains"
)

// SignupSettings is the effective signup mode, the body of GET
// /admin/signup.
type SignupSettings struct {
	Mode    SignupPolicy `json:"mode"`
	Domains []string     `json:"domains"`
	// Source is config (deploy/bench.yaml or the environment) or admin (a
	// server admin changed it from the web).
	Source string `json:"source"`
	// ConfigMode and ConfigDomains are what the server config says, the
	// value a reset returns to.
	ConfigMode    SignupPolicy `json:"config_mode"`
	ConfigDomains []string     `json:"config_domains"`
	UpdatedBy     string       `json:"updated_by,omitempty"`
	UpdatedAt     *time.Time   `json:"updated_at,omitempty"`
	// UpdatedByName and UpdatedByEmail name the admin who changed the
	// settings (ActorDisplay); the page prints the name and keeps the email
	// as a tooltip (fix wave 3, L3).
	UpdatedByName  string `json:"updated_by_name,omitempty"`
	UpdatedByEmail string `json:"updated_by_email,omitempty"`
	// EmailDelivery is email when the server sends verification mail, log
	// when the links only go to the server log.
	EmailDelivery string `json:"email_delivery"`
}

// SignupSettingsRequest is the body of PUT /admin/signup. An empty mode
// drops the override: the server config applies again.
type SignupSettingsRequest struct {
	Mode    string   `json:"mode"`
	Domains []string `json:"domains"`
}

// signupState caches the override.
type signupState struct {
	mu       sync.Mutex
	loaded   time.Time
	mode     SignupPolicy // "" when no override
	domains  []string
	by       string
	at       time.Time
	override bool
}

// ValidSignupPolicy reports whether p is one of the four modes.
func ValidSignupPolicy(p SignupPolicy) bool {
	switch p {
	case SignupInvite, SignupAllowlist, SignupOpen, SignupClosed:
		return true
	}
	return false
}

// signupPolicy returns the effective mode and domains.
func (s *Service) signupPolicy(ctx context.Context) (SignupPolicy, []string) {
	st := s.loadSignup(ctx)
	if st.override {
		return st.mode, st.domains
	}
	return s.cfg.Signup, s.cfg.SignupDomains
}

// loadSignup returns a snapshot of the override, rereading auth_settings
// when the cache is older than signupReload. A read failure keeps the last
// value (or the config) and is logged.
func (s *Service) loadSignup(ctx context.Context) signupState {
	s.signup.mu.Lock()
	defer s.signup.mu.Unlock()
	now := s.now()
	if !s.signup.loaded.IsZero() && now.Sub(s.signup.loaded) < signupReload {
		return s.signup.snapshot()
	}
	s.signup.loaded = now
	mode, domains, by, at, ok, err := s.st.readSignupSettings(ctx)
	if err != nil {
		s.logger.Warn(fmt.Sprintf("auth: read signup settings: %v", err))
		return s.signup.snapshot()
	}
	s.signup.override, s.signup.mode, s.signup.domains, s.signup.by, s.signup.at = ok, mode, domains, by, at
	return s.signup.snapshot()
}

func (st *signupState) snapshot() signupState {
	return signupState{mode: st.mode, domains: append([]string(nil), st.domains...), by: st.by, at: st.at, override: st.override}
}

// signupMode returns the effective mode.
func (s *Service) signupMode(ctx context.Context) SignupPolicy {
	mode, _ := s.signupPolicy(ctx)
	return mode
}

// selfServeAllowed reports whether email may sign up without an invitation
// under mode (bootstrap aside).
func selfServeAllowed(mode SignupPolicy, domains []string, email string) bool {
	switch mode {
	case SignupOpen:
		return true
	case SignupAllowlist:
		_, domain, ok := strings.Cut(NormalizeEmail(email), "@")
		if !ok {
			return false
		}
		for _, d := range domains {
			if d == domain {
				return true
			}
		}
	}
	return false
}

// SignupSettings returns the effective signup settings.
func (s *Service) SignupSettings(ctx context.Context) SignupSettings {
	st := s.loadSignup(ctx)
	out := SignupSettings{Mode: s.cfg.Signup, Domains: nonNil(s.cfg.SignupDomains), Source: "config",
		ConfigMode: s.cfg.Signup, ConfigDomains: nonNil(s.cfg.SignupDomains), EmailDelivery: s.emailDelivery()}
	if st.override {
		out.Mode, out.Domains, out.Source, out.UpdatedBy = st.mode, nonNil(st.domains), "admin", st.by
		at := st.at
		out.UpdatedAt = &at
		if st.by != "" && s.st != nil && s.st.d != nil {
			out.UpdatedByName, out.UpdatedByEmail = ActorDisplay(ctx, s.st.d, st.by)
		}
	}
	return out
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return append([]string(nil), v...)
}

// SetSignupSettings stores a server admin's override (or drops it, for an
// empty mode). The caller must be a server admin.
func (s *Service) SetSignupSettings(ctx context.Context, p *Principal, req SignupSettingsRequest, ip string) (SignupSettings, error) {
	if !IsServerAdmin(p) {
		return SignupSettings{}, errForbidden("server admins only")
	}
	mode := SignupPolicy(strings.ToLower(strings.TrimSpace(req.Mode)))
	if mode != "" && !ValidSignupPolicy(mode) {
		return SignupSettings{}, errBadRequest("mode must be invite, allowlist, open or closed")
	}
	domains, err := normalizeDomains(req.Domains)
	if err != nil {
		return SignupSettings{}, err
	}
	if mode == SignupAllowlist && len(domains) == 0 {
		return SignupSettings{}, errBadRequest("allowlist needs at least one email domain, such as acme.com")
	}
	now := s.now().UTC()
	err = s.st.d.Tx(ctx, func(tx *sql.Tx) error {
		if mode == "" {
			_, err := s.st.exec(ctx, tx, `DELETE FROM auth_settings WHERE key IN (?, ?)`, settingSignupMode, settingSignupDomains)
			return err
		}
		for k, v := range map[string]string{settingSignupMode: string(mode), settingSignupDomains: strings.Join(domains, ",")} {
			if err := s.st.putSetting(ctx, tx, k, v, p.UserID, now.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return SignupSettings{}, err
	}
	s.signup.mu.Lock()
	s.signup.loaded = time.Time{}
	s.signup.mu.Unlock()
	details := map[string]any{"mode": string(mode), "domains": domains}
	if mode == "" {
		details["mode"], details["reset_to_config"] = string(s.cfg.Signup), true
	}
	s.record(principalEvent(p, "auth.signup_settings", "server", "signup", ip, details))
	return s.SignupSettings(ctx), nil
}

func normalizeDomains(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, d := range in {
		d = lib.NormalizeSignupDomain(d)
		if d == "" || seen[d] {
			continue
		}
		if !lib.ValidSignupDomain(d) {
			return nil, errBadRequest("%q is not an email domain such as acme.com", clip(d, 80))
		}
		seen[d] = true
		out = append(out, d)
	}
	if len(out) > MaxSignupDomains {
		return nil, errBadRequest("at most %d domains", MaxSignupDomains)
	}
	sort.Strings(out)
	return out, nil
}

// ---- store ----

func (s *store) readSignupSettings(ctx context.Context) (mode SignupPolicy, domains []string, by string, at time.Time, ok bool, err error) {
	rows, err := s.rows(ctx, s.d.SQL(), `SELECT key, value, updated_by, updated_at FROM auth_settings WHERE key IN (?, ?)`, settingSignupMode, settingSignupDomains)
	if err != nil {
		return "", nil, "", time.Time{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v, who string
		var ms int64
		if err := rows.Scan(&k, &v, &who, &ms); err != nil {
			return "", nil, "", time.Time{}, false, err
		}
		switch k {
		case settingSignupMode:
			mode, by, at = SignupPolicy(v), who, time.UnixMilli(ms).UTC()
		case settingSignupDomains:
			for _, d := range strings.Split(v, ",") {
				if d = strings.TrimSpace(d); d != "" {
					domains = append(domains, d)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, "", time.Time{}, false, err
	}
	if !ValidSignupPolicy(mode) {
		return "", nil, "", time.Time{}, false, nil
	}
	return mode, domains, by, at, true, nil
}

func (s *store) putSetting(ctx context.Context, q querier, key, value, by string, at int64) error {
	res, err := s.exec(ctx, q, `UPDATE auth_settings SET value = ?, updated_by = ?, updated_at = ? WHERE key = ?`, value, by, at, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	_, err = s.exec(ctx, q, `INSERT INTO auth_settings (key, value, updated_by, updated_at) VALUES (?, ?, ?, ?)`, key, value, by, at)
	return err
}

// signupRefusal is the error a self-serve signup gets under mode.
func signupRefusal(mode SignupPolicy) *Error {
	if mode == SignupAllowlist {
		return ErrSignupDomain
	}
	return ErrSignupNotAllowed
}

// ErrSignupDomain answers a self-serve signup from a domain the allowlist
// does not name.
var ErrSignupDomain = &Error{Status: 403, Type: "forbidden",
	Msg: "signup on this server is by invitation, or for email addresses at the allowed domains only"}
