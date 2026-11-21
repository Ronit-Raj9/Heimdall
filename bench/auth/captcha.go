package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Account safety (L22): optional hCaptcha on self-serve signup. Off unless
// HEIMDALL_CAPTCHA_SECRET is set. HEIMDALL_CAPTCHA_SITEKEY is the public
// site key the sign-up page renders the widget with (GET /auth/config).
// Invitation signups skip it: the invitation token already vouches.

// Captcha environment variables.
const (
	CaptchaSecretEnv  = "HEIMDALL_CAPTCHA_SECRET"
	CaptchaSiteKeyEnv = "HEIMDALL_CAPTCHA_SITEKEY"
)

// CaptchaVerifyURL is hCaptcha's verification endpoint (a var for tests).
var CaptchaVerifyURL = "https://api.hcaptcha.com/siteverify"

// ErrCaptcha answers a signup whose captcha is missing or failed.
var ErrCaptcha = &Error{Status: http.StatusBadRequest, Type: "invalid_request", Msg: "captcha check failed; complete the captcha and try again"}

type captchaConfig struct {
	secret, siteKey string
}

func captchaFromEnv(getenv func(string) string) *captchaConfig {
	secret := strings.TrimSpace(getenv(CaptchaSecretEnv))
	if secret == "" {
		return nil
	}
	return &captchaConfig{secret: secret, siteKey: strings.TrimSpace(getenv(CaptchaSiteKeyEnv))}
}

// envCaptcha is read by New; tests replace it.
var envCaptcha = func() *captchaConfig { return captchaFromEnv(os.Getenv) }

// verifyCaptcha asks hCaptcha whether response is a solved challenge. Any
// transport or decoding failure counts as failed: the check fails closed.
// The secret is sent only to CaptchaVerifyURL and never logged.
func (s *Service) verifyCaptcha(ctx context.Context, response, ip string) error {
	c := s.captcha
	if c == nil {
		return nil
	}
	response = strings.TrimSpace(response)
	if response == "" || len(response) > 8192 {
		return ErrCaptcha
	}
	form := url.Values{"secret": {c.secret}, "response": {response}}
	if ip != "" {
		form.Set("remoteip", ip)
	}
	if c.siteKey != "" {
		form.Set("sitekey", c.siteKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, CaptchaVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrCaptcha
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		s.logger.Warn("captcha verify request failed: %v", err)
		return errUnavailable("captcha service unreachable; try again in a minute")
	}
	defer resp.Body.Close()
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil || !out.Success {
		return ErrCaptcha
	}
	return nil
}
