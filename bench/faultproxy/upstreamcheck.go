package faultproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// upstreamCheckTimeout bounds one provider's check: DNS lookup, TCP connect
// and TLS handshake together. Providers are checked concurrently, so
// GET /upstreams answers within about this long.
const upstreamCheckTimeout = 5 * time.Second

// ErrUpstreamCheckUnsupported means the proxy answered GET /upstreams with
// 404 or 405: it is older than the check. Callers treat the upstreams as
// unknown, not as failed.
var ErrUpstreamCheckUnsupported = errors.New("fault proxy is too old for the upstream check")

// UpstreamReport is the body of GET /upstreams.
type UpstreamReport struct {
	CheckedAt time.Time       `json:"checked_at"`
	Upstreams []UpstreamCheck `json:"upstreams"`
}

// UpstreamCheck is one provider's reachability from the proxy. Host is the
// host:port the proxy dials, never a path, a query or a key. Skipped, when
// set, says why the provider was listed but not dialled; it is not a
// failure.
type UpstreamCheck struct {
	Provider  string  `json:"provider"`
	Host      string  `json:"host"`
	OK        bool    `json:"ok"`
	LatencyMS float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
	Skipped   string  `json:"skipped,omitempty"`
}

// upstreamDialer opens and closes one connection per provider. It takes the
// TLS settings and the HTTP proxy rule from the client forward uses, so the
// check trusts and routes exactly what forwarding does.
type upstreamDialer struct {
	timeout  time.Duration
	tls      *tls.Config
	proxy    func(*http.Request) (*url.URL, error)
	resolver *net.Resolver // nil: the default resolver
	logger   schemas.Logger
}

// newUpstreamDialer returns the dialer GET /upstreams uses.
func (s *Server) newUpstreamDialer() upstreamDialer {
	d := upstreamDialer{timeout: upstreamCheckTimeout, logger: s.logger}
	if tr, ok := s.client.Transport.(*http.Transport); ok {
		d.proxy = tr.Proxy
		if tr.TLSClientConfig != nil {
			d.tls = tr.TLSClientConfig.Clone()
		}
	}
	return d
}

// checkUpstreams checks, concurrently, every provider the proxy answers from
// its real API with a real key, in provider table order. Scripted and
// unconfigured providers are left out because no model call of theirs
// leaves the proxy, and so are keyless providers (ollama), whose default
// address only exists inside some deployments; GET /health lists every
// provider's mode. The check is a TCP connect plus, for https, a TLS
// handshake: no HTTP request is sent, so no key leaves the proxy and no
// token is spent.
func (s *Server) checkUpstreams(ctx context.Context, d upstreamDialer) UpstreamReport {
	var names []string
	for _, name := range s.providers.order {
		if up := s.upstreams[name]; up.mode == modeReal && up.key != "" {
			names = append(names, name)
		}
	}
	out := make([]UpstreamCheck, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() { out[i] = d.check(ctx, name, s.upstreams[name].base) })
	}
	wg.Wait()
	return UpstreamReport{CheckedAt: time.Now().UTC(), Upstreams: out}
}

// check dials one provider's base URL. An upstream reached through an HTTP
// proxy from the environment is skipped rather than dialled directly, because
// a direct dial would test a route forwarding never takes.
func (d upstreamDialer) check(ctx context.Context, provider, base string) UpstreamCheck {
	c := UpstreamCheck{Provider: provider}
	u, err := url.Parse(base)
	if err != nil {
		c.Error = "parse base url: " + redactURLError(err)
		return c
	}
	c.Host = hostPort(u)
	if d.proxy != nil {
		pu, err := d.proxy(&http.Request{URL: u})
		if err != nil {
			c.Error = "http proxy from the environment: " + redactURLError(err)
			return c
		}
		if pu != nil {
			c.Skipped = "upstream traffic goes through the HTTP proxy at " + pu.Host + "; not dialled"
			return c
		}
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	start := time.Now()
	conn, err := d.dial(ctx, u, c.Host)
	c.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		c.Error = redactURLError(err)
		return c
	}
	if err := conn.Close(); err != nil {
		d.logger.Debug("close upstream check connection", "provider", provider, "error", err.Error())
	}
	c.OK = true
	return c
}

// dial connects to addr and, for an https base URL, completes a TLS
// handshake that verifies the certificate for the base URL's host name.
func (d upstreamDialer) dial(ctx context.Context, u *url.URL, addr string) (net.Conn, error) {
	nd := &net.Dialer{Resolver: d.resolver}
	if u.Scheme != "https" {
		return nd.DialContext(ctx, "tcp", addr)
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if d.tls != nil {
		cfg = d.tls.Clone()
	}
	if cfg.ServerName == "" {
		cfg.ServerName = u.Hostname()
	}
	return (&tls.Dialer{NetDialer: nd, Config: cfg}).DialContext(ctx, "tcp", addr)
}

// hostPort is the base URL's host with its port, 443 or 80 by scheme when
// the URL names none.
func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}
