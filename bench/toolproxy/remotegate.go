package toolproxy

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The remote tool gate publishes one run's tool proxy to a remote agent
// (Remote coverage, docs/remote-execution.md). It runs in its own container
// (bench-rt-<run id>), the only one with a published port: the tool proxy
// stays on the internal service network with no route out. The gate admits
// only requests that carry the run's bearer token, under /r/<run id>/, before
// its deadline, within a rate and a body cap; it removes the token and
// forwards the rest to the tool proxy unchanged, so every call is recorded
// in the ledger exactly like a local agent's.
const (
	// GatePort is the port the gate listens on inside its container.
	GatePort = 9201
	// GateTokenEnv carries the sha256 (hex) of the run's token into the gate.
	// The gate never sees the token itself.
	GateTokenEnv = "BENCH_GATE_TOKEN_SHA256"
	// gateMaxBody caps a request body the gate forwards.
	gateMaxBody = 8 << 20
	// gateRate and gateBurst cap requests per second (token bucket).
	gateRate  = 20.0
	gateBurst = 40
)

// GateConfig configures a gate.
type GateConfig struct {
	Listen   string    // e.g. :9201
	Upstream string    // the tool proxy, e.g. http://toolproxy:9200
	RunID    string    // the only run it serves
	TokenSHA [32]byte  // sha256 of the bearer token
	Until    time.Time // refuse everything after; zero means no deadline
	MaxBody  int64     // zero means gateMaxBody
	Rate     float64   // requests per second; zero means gateRate
	Burst    int       // zero means gateBurst
	Logger   schemas.Logger
}

// Gate is the remote tool gate.
type Gate struct {
	cfg   GateConfig
	proxy *httputil.ReverseProxy
	now   func() time.Time

	mu     sync.Mutex
	tokens float64
	last   time.Time
	closed bool
}

// ParseTokenSHA reads a hex sha256 as GateTokenEnv carries it.
func ParseTokenSHA(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != len(out) {
		return out, errors.New("token sha256 must be 64 hex characters")
	}
	copy(out[:], b)
	return out, nil
}

// NewGate checks the config and returns a gate ready to serve.
func NewGate(cfg GateConfig) (*Gate, error) {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	if cfg.RunID == "" || strings.ContainsAny(cfg.RunID, "/ ") {
		return nil, fmt.Errorf("gate: run id %q is not valid", cfg.RunID)
	}
	if cfg.TokenSHA == ([32]byte{}) {
		return nil, errors.New("gate: no token sha256")
	}
	up, err := url.Parse(cfg.Upstream)
	if err != nil || (up.Scheme != "http" && up.Scheme != "https") || up.Host == "" {
		return nil, fmt.Errorf("gate: upstream %q is not an http URL", cfg.Upstream)
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = gateMaxBody
	}
	if cfg.Rate <= 0 {
		cfg.Rate = gateRate
	}
	if cfg.Burst <= 0 {
		cfg.Burst = gateBurst
	}
	g := &Gate{cfg: cfg, now: time.Now, tokens: float64(cfg.Burst)}
	g.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(up)
			pr.Out.Host = up.Host
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
		},
		FlushInterval: -1, // MCP answers may be event streams
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "tool proxy unreachable", http.StatusBadGateway)
			cfg.Logger.Warn("remote tool gate: upstream", "error", err.Error())
		},
	}
	return g, nil
}

// Close makes every later request answer 410: the run ended.
func (g *Gate) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// ServeHTTP implements http.Handler. GET /healthz answers without the token
// and says nothing about the run. Everything else must be
// /r/<run id>/s/<service>/... or /r/<run id>/x/<api>/... with
// "Authorization: Bearer <token>".
func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	if !g.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="heimdall-remote-tools"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/r/")
	if !ok {
		http.Error(w, "not found: tools are under /r/<run id>/s/<service id>/", http.StatusNotFound)
		return
	}
	run, rest, _ := strings.Cut(rest, "/")
	if subtle.ConstantTimeCompare([]byte(run), []byte(g.cfg.RunID)) != 1 {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}
	if g.ended() {
		http.Error(w, "the run has ended", http.StatusGone)
		return
	}
	rest = "/" + rest
	decoded, err := url.PathUnescape(rest)
	if err != nil || dotSegment(decoded) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(decoded, "/s/") && !strings.HasPrefix(decoded, "/x/") {
		http.Error(w, "not found: tools are under /r/<run id>/s/<service id>/", http.StatusNotFound)
		return
	}
	if !g.allow() {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limit", http.StatusTooManyRequests)
		return
	}
	if r.ContentLength > g.cfg.MaxBody {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, g.cfg.MaxBody)
	r2 := r.Clone(r.Context())
	r2.URL.Path, r2.URL.RawPath = decoded, rest
	g.proxy.ServeHTTP(w, r2)
}

// dotSegment reports whether a path has a "." or ".." segment, which the
// gate refuses rather than resolving.
func dotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// authorized compares the bearer token's sha256 with the run's in constant
// time. The token is never logged or kept.
func (g *Gate) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || tok == "" {
		return false
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(tok)))
	return subtle.ConstantTimeCompare(sum[:], g.cfg.TokenSHA[:]) == 1
}

// ended reports whether the gate was closed or its deadline passed.
func (g *Gate) ended() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed || (!g.cfg.Until.IsZero() && g.now().After(g.cfg.Until))
}

// allow takes one token from the bucket.
func (g *Gate) allow() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if !g.last.IsZero() {
		g.tokens = min(float64(g.cfg.Burst), g.tokens+now.Sub(g.last).Seconds()*g.cfg.Rate)
	}
	g.last = now
	if g.tokens < 1 {
		return false
	}
	g.tokens--
	return true
}

// ListenAndServe serves until ctx ends or the deadline passes.
func (g *Gate) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{Addr: g.cfg.Listen, Handler: g, ReadHeaderTimeout: 10 * time.Second}
	if !g.cfg.Until.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, g.cfg.Until.Add(time.Minute))
		defer cancel()
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return fmt.Errorf("remote tool gate on %s: %w", g.cfg.Listen, err)
	case <-ctx.Done():
	}
	g.Close()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shut down remote tool gate: %w", err)
	}
	return nil
}
