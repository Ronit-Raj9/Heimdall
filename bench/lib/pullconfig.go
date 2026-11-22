package lib

import (
	"strings"
)

// PullConfig is how pull-mode agents (heimdall.serve, lane Y11,
// docs/pull-agents.md) reach this server. Both fields may also be set
// through BENCH_PULL_URL and BENCH_PULL_PUBLIC_URL.
type PullConfig struct {
	// URL is how a run's launcher reaches this server's pull endpoint, from
	// the run's egress forwarder: an address on the internet or the host,
	// never a compose-internal name. Empty uses auth.public_url.
	URL string `yaml:"url" json:"url,omitempty"`
	// PublicURL is the origin written into the signed repository URLs a
	// remote coding agent downloads. Empty uses auth.public_url, else the
	// origin the upload came in on.
	PublicURL string `yaml:"public_url" json:"public_url,omitempty"`
}

// PullRunURL is the base URL a run's launcher posts pull turns to
// (<origin>/api/bench/pull/run), or "" when none is configured.
func (c *Config) PullRunURL() string {
	base := strings.TrimRight(c.Pull.URL, "/")
	if base == "" {
		base = strings.TrimRight(c.Auth.PublicURL, "/")
	}
	if base == "" {
		return ""
	}
	return base + "/api/bench/pull/run"
}

// PullPublicOrigin is the origin of signed repository URLs, or "" to use
// the request's own.
func (c *Config) PullPublicOrigin() string {
	if c.Pull.PublicURL != "" {
		return strings.TrimRight(c.Pull.PublicURL, "/")
	}
	return strings.TrimRight(c.Auth.PublicURL, "/")
}
