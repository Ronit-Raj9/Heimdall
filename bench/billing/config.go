package billing

import (
	"errors"
	"os"
	"strings"
)

// Environment variables. Keys come from the environment only, never from a
// file in the repository.
const (
	// EnabledEnv turns billing on ("on", "1" or "true"). Unset, nothing is
	// enforced and checkout is refused: limits are only shown.
	EnabledEnv = "HEIMDALL_BILLING"
	// SecretKeyEnv is the Stripe secret key (sk_test_... by default).
	SecretKeyEnv = "HEIMDALL_STRIPE_SECRET_KEY"
	// WebhookSecretEnv is the signing secret of the Stripe webhook endpoint
	// (whsec_...).
	WebhookSecretEnv = "HEIMDALL_STRIPE_WEBHOOK_SECRET"
	// TeamPriceEnv is the Stripe price id of the Team plan (price_...), a
	// recurring monthly price per seat.
	TeamPriceEnv = "HEIMDALL_STRIPE_TEAM_PRICE"
	// AllowLiveEnv must be "1" for a live key (sk_live_ or rk_live_) to be
	// used; without it billing stays in Stripe test mode.
	AllowLiveEnv = "HEIMDALL_STRIPE_ALLOW_LIVE"
	// APIBaseEnv overrides the Stripe API address (tests).
	APIBaseEnv = "HEIMDALL_STRIPE_API_BASE"
	// ContactEnv is the address the Enterprise plan's "Contact us" opens.
	ContactEnv = "HEIMDALL_SALES_CONTACT"
)

// DefaultAPIBase is Stripe's API.
const DefaultAPIBase = "https://api.stripe.com"

// Stripe modes.
const (
	ModeTest = "test"
	ModeLive = "live"
)

// Config is billing's configuration, read from the environment.
type Config struct {
	Enabled       bool
	SecretKey     string
	WebhookSecret string
	TeamPrice     string
	AllowLive     bool
	APIBase       string
	Contact       string
}

// ConfigFromEnv reads Config from the environment.
func ConfigFromEnv() Config {
	return ConfigFrom(os.Getenv)
}

// ConfigFrom reads Config through getenv.
func ConfigFrom(getenv func(string) string) Config {
	on := strings.ToLower(strings.TrimSpace(getenv(EnabledEnv)))
	c := Config{
		Enabled:       on == "on" || on == "1" || on == "true",
		SecretKey:     strings.TrimSpace(getenv(SecretKeyEnv)),
		WebhookSecret: strings.TrimSpace(getenv(WebhookSecretEnv)),
		TeamPrice:     strings.TrimSpace(getenv(TeamPriceEnv)),
		AllowLive:     strings.TrimSpace(getenv(AllowLiveEnv)) == "1",
		APIBase:       strings.TrimRight(strings.TrimSpace(getenv(APIBaseEnv)), "/"),
		Contact:       strings.TrimSpace(getenv(ContactEnv)),
	}
	if c.APIBase == "" {
		c.APIBase = DefaultAPIBase
	}
	return c
}

// Mode is the Stripe mode the secret key belongs to: test, live or "" with
// no key.
func (c Config) Mode() string {
	switch {
	case c.SecretKey == "":
		return ""
	case strings.Contains(c.SecretKey, "_live_"):
		return ModeLive
	default:
		return ModeTest
	}
}

// Errors of StripeReady.
var (
	ErrBillingOff = errors.New("billing is off on this server; the operator turns it on with " + EnabledEnv + "=on")
	ErrNoStripe   = errors.New("Stripe is not configured on this server: the operator sets " + SecretKeyEnv + " and " + TeamPriceEnv)
	ErrLiveKey    = errors.New("the Stripe key is a live key; the server stays in test mode unless the operator sets " + AllowLiveEnv + "=1")
)

// StripeReady says why checkout can not start, or nil when it can.
func (c Config) StripeReady() error {
	switch {
	case !c.Enabled:
		return ErrBillingOff
	case c.SecretKey == "" || c.TeamPrice == "":
		return ErrNoStripe
	case c.Mode() == ModeLive && !c.AllowLive:
		return ErrLiveKey
	}
	return nil
}

// WebhookReady says why webhooks are refused, or nil when they are taken.
func (c Config) WebhookReady() error {
	switch {
	case !c.Enabled:
		return ErrBillingOff
	case c.WebhookSecret == "":
		return errors.New("the Stripe webhook secret is not set: the operator sets " + WebhookSecretEnv)
	case c.Mode() == ModeLive && !c.AllowLive:
		return ErrLiveKey
	}
	return nil
}
