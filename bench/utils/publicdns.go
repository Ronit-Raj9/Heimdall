package utils

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Public-only name resolution for guarded outbound calls (fix wave 2, N104).
// On the hosted server the container resolver is Docker's embedded DNS,
// which answers for compose service names (bifrost, heimdall-postgres-1,
// registry). A guarded call must not learn that such a name exists, so the
// guarded dialers and the hosted Connect checks resolve names only through
// public DNS servers: a compose name then fails exactly like a name that
// does not exist anywhere.

// PublicDNSEnv names the environment variable with the public DNS servers:
// a comma list of ip:port. Empty or unset uses DefaultPublicDNS; "system"
// keeps the container's own resolver.
const PublicDNSEnv = "HEIMDALL_PUBLIC_DNS"

// DefaultPublicDNS are the servers used when PublicDNSEnv is unset.
var DefaultPublicDNS = []string{"1.1.1.1:53", "8.8.8.8:53"}

var publicDNSWarn sync.Once

// PublicDNSServers returns the configured public DNS servers, or nil when
// PublicDNSEnv is "system". Invalid entries are skipped with one warning;
// when no entry is valid the default list is used.
func PublicDNSServers() []string {
	raw := strings.TrimSpace(os.Getenv(PublicDNSEnv))
	if raw == "" {
		return append([]string(nil), DefaultPublicDNS...)
	}
	if strings.EqualFold(raw, "system") {
		return nil
	}
	var out, bad []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ap, err := netip.ParseAddrPort(part); err == nil && ap.Port() != 0 {
			out = append(out, ap.String())
			continue
		}
		bad = append(bad, part)
	}
	if len(bad) > 0 {
		publicDNSWarn.Do(func() {
			slog.Warn(PublicDNSEnv+" has entries that are not ip:port; they are skipped", "entries", strings.Join(bad, ","))
		})
	}
	if len(out) == 0 {
		return append([]string(nil), DefaultPublicDNS...)
	}
	return out
}

// PublicResolver returns a resolver that sends every query to the public
// DNS servers (PublicDNSServers), one after the other, and never to the
// container resolver. It returns nil (the default resolver) when
// PublicDNSEnv is "system". Host files are still read first, as Go's
// resolver always does.
func PublicResolver() *net.Resolver {
	servers := PublicDNSServers()
	if len(servers) == 0 {
		return nil
	}
	return publicResolverFor(servers)
}

func publicResolverFor(servers []string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			var last error
			for _, s := range servers {
				c, err := d.DialContext(ctx, network, s)
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
	}
}

// LookupIPAddr resolves host through r, or the default resolver when r is
// nil.
func LookupIPAddr(ctx context.Context, r *net.Resolver, host string) ([]net.IPAddr, error) {
	if r == nil {
		r = net.DefaultResolver
	}
	return r.LookupIPAddr(ctx, host)
}

// addrInText matches IPv4 literals and IPv6 literals (with at least two
// colons), optionally in brackets.
var addrInText = regexp.MustCompile(`\[?(?:[0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}(?:%[0-9A-Za-z]+)?\]?|\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)

// HidePrivateAddresses replaces every IPv4 or IPv6 literal in s that is not
// a public address (private, loopback, link-local, carrier-grade NAT,
// unique-local and the other ranges of BlockedIP) with "a private address".
// Public addresses and text that only looks like an address (a time such as
// 12:30, a version number) are left as they are. It is pure.
func HidePrivateAddresses(s string) string {
	return addrInText.ReplaceAllStringFunc(s, func(m string) string {
		lit := strings.TrimSuffix(strings.TrimPrefix(m, "["), "]")
		if i := strings.IndexByte(lit, '%'); i >= 0 {
			lit = lit[:i]
		}
		ip := net.ParseIP(lit)
		if ip == nil || !BlockedIP(ip) {
			return m
		}
		return "a private address"
	})
}
