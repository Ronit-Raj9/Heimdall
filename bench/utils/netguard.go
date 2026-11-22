package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Outbound guard for organization-supplied URLs (G3 B6). A hosted server or
// the shared worker calls URLs an organization typed in (sandbox platform
// endpoints, webhooks, deployed agents). Without a guard such a URL can name
// a service on the compose networks (bifrost:8080, the metrics listener, the
// image registry) or the cloud metadata address. The guard resolves the name
// itself and checks the address the socket is about to connect to (the
// dialer's Control hook), so a public name that resolves, or later
// rebinds, to an internal address is still refused.

// ErrBlockedAddress is returned when a connection would reach a private,
// loopback, link-local, metadata or otherwise non-public address.
var ErrBlockedAddress = errors.New("refused to connect to a private or local address")

// blockedPrefixes are the ranges net.IP's helpers do not cover: shared
// address space (carrier-grade NAT), "this network", benchmarking, the
// reserved class E block, the IPv4 broadcast address, NAT64 and the
// documentation ranges.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("100::/64"),
}

// BlockedIP reports whether ip is not a public unicast address: loopback,
// private (RFC 1918 and IPv6 unique local, which holds fd00:ec2::254),
// link-local (169.254.0.0/16, which holds the metadata address
// 169.254.169.254), multicast, unspecified, carrier-grade NAT and the other
// special-use ranges above. IPv4-mapped IPv6 addresses are checked as IPv4.
// It is pure.
func BlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if addr.Is4() && addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// GuardControl is a net.Dialer Control hook that refuses every non-public
// address at connect time.
func GuardControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || BlockedIP(ip) {
		return fmt.Errorf("%w %s", ErrBlockedAddress, host)
	}
	return nil
}

// GuardedDialer returns a dialer that refuses every non-public address at
// connect time.
func GuardedDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second, Control: GuardControl}
}

// GuardedTransport is an HTTP transport for organization-supplied URLs: the
// guarded dialer, no proxy from the environment (a proxy would dial the
// address for us, unchecked), and the default transport's timeouts.
func GuardedTransport() *http.Transport {
	return &http.Transport{
		DialContext:           GuardedDialer(10 * time.Second).DialContext,
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// CheckPublicURL accepts an absolute URL an organization supplied for a
// server-side call: https only (http too when allowHTTP), no user info, and
// no host that is localhost, a single-label name (a compose service such as
// "bifrost" or "registry") or a literal non-public address. Names are checked
// again at connect time by the guarded dialer. It is pure.
func CheckPublicURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("want an absolute https URL")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && allowHTTP:
	default:
		return errors.New("only https URLs are allowed")
	}
	if u.User != nil {
		return errors.New("a URL with user info is not allowed")
	}
	return CheckPublicHost(u.Hostname())
}

// CheckPublicHost refuses a host name that cannot be a public internet
// address: empty, localhost, a single-label name, an internal-only suffix,
// or a literal non-public address. It is pure.
func CheckPublicHost(host string) error {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "" {
		return errors.New("the URL has no host")
	}
	if ip := net.ParseIP(h); ip != nil {
		if BlockedIP(ip) {
			return fmt.Errorf("%w %s", ErrBlockedAddress, h)
		}
		return nil
	}
	if h == "localhost" || !strings.Contains(h, ".") {
		return fmt.Errorf("%s is not a public host name", h)
	}
	for _, suf := range []string{".localhost", ".local", ".internal", ".lan", ".home.arpa", ".svc", ".cluster.local"} {
		if strings.HasSuffix(h, suf) {
			return fmt.Errorf("%s is not a public host name", h)
		}
	}
	return nil
}

// GuardedDial dials address through the guard; for callers that hold a
// plain address rather than a URL.
func GuardedDial(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
	return GuardedDialer(timeout).DialContext(ctx, network, address)
}
