// Package egress is Heimdall's recorded internet: one forward proxy a task's
// agent reaches through HTTP_PROXY and HTTPS_PROXY. It covers package
// installs (pip, npm, Go modules, apt, uv), git clones over HTTPS and plain
// downloads with one mechanism, docs/egress.md.
//
// Content is recorded once, when the reference solution runs at admission
// (`bifrost-bench egress record`), into a cache tar in the task folder. The
// tar is a service file, so its sha256 is part of the task's fixtures hash in
// the suite lock: a changed cache is a changed task. Every later run answers
// from that tar byte for byte (mode replay). A request the recording does not
// hold gets a 404 and a row in the service's request log, which the run
// exports and seals.
//
// Run modes (HEIMDALL_EGRESS_MODE, Experiment.Egress):
//
//	offline           every request is refused with 403 and logged
//	replay            answered from the recorded cache only (the default)
//	live-allowlisted  forwarded to the real host when it is on the allowlist
//	                  (default: the hosts the recording saw); not repeatable
//	record            forwarded and written to the cache (admission only)
//
// HTTPS is served with a per-task certificate authority: the importer bakes
// its certificate into the task image (CAPath, BundlePath) and only the
// egress service holds the key. The agent can see that certificate.
package egress

import (
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Run modes.
const (
	ModeOffline = "offline"
	ModeReplay  = "replay"
	ModeLive    = "live-allowlisted"
	ModeRecord  = "record"
)

// Names shared by the importer, the environment and the service.
const (
	// ServiceRole is the role of the egress service in task.yaml; the
	// environment hands proxy variables to the agent when a run has one.
	ServiceRole = "egress"
	// ServiceID is the host name the importer gives the service.
	ServiceID = "egress"
	// Port is where the service listens.
	Port = 3128

	// ServiceCachePath is the recorded cache inside the service.
	ServiceCachePath = "/egress/cache.tar"
	// ServiceCAPath is the certificate and key inside the service.
	ServiceCAPath = "/egress/ca.pem"
	// ServiceLogPath is the request log inside the service, exported and
	// sealed after the run as state/egress/requests.
	ServiceLogPath = "/egress/log/requests.jsonl"

	// CAPath is the certificate alone in the task image.
	CAPath = "/etc/heimdall-egress/ca.crt"
	// BundlePath is the image's system roots plus CAPath.
	BundlePath = "/etc/heimdall-egress/bundle.crt"

	// EnvMode overrides the service's mode for one run.
	EnvMode = "HEIMDALL_EGRESS_MODE"
	// EnvAllow is a comma list of hosts for live-allowlisted and record.
	EnvAllow = "HEIMDALL_EGRESS_ALLOW"

	// DefaultImage is the service image `bifrost-bench egress image` builds.
	DefaultImage = "bench/egress:1"

	// TaskCacheFile and TaskCAFile are the files under a task folder.
	TaskCacheFile = "egress/cache.tar"
	TaskCAFile    = "egress/ca.pem"
	// ImageCAFile is the certificate copied into the task's build context.
	ImageCAFile = "heimdall-egress-ca.crt"
)

// ValidMode reports whether m is a run mode (record included).
func ValidMode(m string) bool {
	switch m {
	case ModeOffline, ModeReplay, ModeLive, ModeRecord:
		return true
	}
	return false
}

// RunMode reports whether m may be set on an experiment: record is for
// admission only and empty keeps each service's own mode.
func RunMode(m string) bool {
	return m == "" || m == ModeOffline || m == ModeReplay || m == ModeLive
}

// Unrepeatable reports whether runs in mode m reached real hosts.
func Unrepeatable(m string) bool { return m == ModeLive || m == ModeRecord }

// ProxyURL is the proxy address for a service host name.
func ProxyURL(host string) string { return "http://" + host + ":" + strconv.Itoa(Port) }

// ClientEnv is what a process needs to use the proxy: the proxy variables in
// both cases, NO_PROXY for hosts that must be reached directly (model
// gateway, other services), and the certificate variables of the common
// clients (Python ssl and requests, pip, curl, git, Node, cargo, uv).
func ClientEnv(proxy string, noProxy []string) map[string]string {
	np := dedupe(append([]string{"localhost", "127.0.0.1", "::1"}, noProxy...))
	env := map[string]string{
		"HTTP_PROXY": proxy, "HTTPS_PROXY": proxy, "http_proxy": proxy, "https_proxy": proxy,
		"NO_PROXY": strings.Join(np, ","), "no_proxy": strings.Join(np, ","),
		"SSL_CERT_FILE": BundlePath, "REQUESTS_CA_BUNDLE": BundlePath, "CURL_CA_BUNDLE": BundlePath,
		"PIP_CERT": BundlePath, "GIT_SSL_CAINFO": BundlePath, "CARGO_HTTP_CAINFO": BundlePath,
		"NODE_EXTRA_CA_CERTS": CAPath, "NODE_USE_ENV_PROXY": "1", "UV_NATIVE_TLS": "1",
	}
	return env
}

// HostsOf returns the host names of the values in env that are URLs, for
// NO_PROXY: the agent's model gateway and tool URLs must not go through the
// proxy.
func HostsOf(env map[string]string) []string {
	var out []string
	for _, v := range env {
		if !strings.Contains(v, "://") {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(v))
		if err != nil || u.Host == "" {
			continue
		}
		out = append(out, u.Hostname())
	}
	sort.Strings(out)
	return dedupe(out)
}

// Allowed reports whether host is on the list: an exact name, "*" for all,
// or "*.example.com" / ".example.com" for the domain and its subdomains.
func Allowed(list []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	for _, a := range list {
		a = strings.ToLower(strings.TrimSpace(a))
		switch {
		case a == "":
		case a == "*":
			return true
		case strings.HasPrefix(a, "*.") || strings.HasPrefix(a, "."):
			d := strings.TrimPrefix(strings.TrimPrefix(a, "*"), ".")
			if host == d || strings.HasSuffix(host, "."+d) {
				return true
			}
		case a == host:
			return true
		}
	}
	return false
}

// SplitList splits a comma or space separated list.
func SplitList(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	return dedupe(f)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
