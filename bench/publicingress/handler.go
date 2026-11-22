package publicingress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// GatewayPrefix and ToolsPrefix are the public paths Caddy forwards here.
const (
	GatewayPrefix = "/gw"
	ToolsPrefix   = "/rt"
)

// MaxBodyBytes caps one request body (model calls with images, tool calls).
const MaxBodyBytes = 32 << 20

// inferenceRoutes are the gateway path prefixes a public caller may use:
// Bifrost's provider-compatible inference routes and its MCP endpoint. Every
// other route (the admin API under /api, metrics, the A2A agent gateway, the
// UI) stays on the worker's networks.
var inferenceRoutes = []string{
	"/v1/", "/openai/", "/anthropic/", "/genai/", "/bedrock/", "/cohere/",
	"/litellm/", "/langchain/", "/pydanticai/", "/mcp",
}

// InferenceRoute reports whether p (the path after /gw) is a route a public
// caller may reach. It is pure.
func InferenceRoute(p string) bool {
	if strings.Contains(p, "..") || strings.Contains(p, "//") {
		return false
	}
	for _, pre := range inferenceRoutes {
		// A route ending in "/" is a prefix; any other is exact.
		if p == strings.TrimSuffix(pre, "/") || (strings.HasSuffix(pre, "/") && strings.HasPrefix(p, pre)) {
			return true
		}
	}
	return false
}

// keyHeaders are the headers a model client puts Bifrost's virtual key in.
var keyHeaders = []string{"x-bf-vk", "x-api-key", "x-goog-api-key", "api-key"}

// requestKey is the virtual key a request carries, or "".
func requestKey(r *http.Request) string {
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	for _, h := range keyHeaders {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	if v := r.URL.Query().Get("key"); v != "" { // the Gemini client's query key
		return v
	}
	return ""
}

// Handler serves the public ingress. Gateway is the gateway's base URL on
// the worker's network (gateway.url).
type Handler struct {
	Reg     *Registry
	Gateway *url.URL
	// Transport reaches the gateway and the gates; nil uses a plain one.
	Transport http.RoundTripper
	// TrustedPeer reports whether the direct peer is the reverse proxy whose
	// X-Forwarded-For names the client; nil trusts private addresses (on the
	// hosted stack only Caddy reaches this listener).
	TrustedPeer func(ip net.IP) bool
	Logf        func(format string, args ...any)

	gw *httputil.ReverseProxy
	rt *httputil.ReverseProxy
}

// NewHandler builds a handler over reg forwarding gateway calls to gateway.
func NewHandler(reg *Registry, gateway string) (*Handler, error) {
	u, err := url.Parse(strings.TrimRight(gateway, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("public ingress: gateway URL %q: want http(s)://host[:port]", gateway)
	}
	return &Handler{Reg: reg, Gateway: u}, nil
}

func (h *Handler) transport() http.RoundTripper {
	if h.Transport != nil {
		return h.Transport
	}
	return &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		MaxIdleConnsPerHost: 32, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 15 * time.Minute}
}

func (h *Handler) init() {
	if h.gw != nil {
		return
	}
	strip := func(pr *httputil.ProxyRequest) {
		// The caller's forwarding headers mean nothing past this point.
		for _, k := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Forwarded", "Cookie"} {
			pr.Out.Header.Del(k)
		}
	}
	h.gw = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			strip(pr)
			rest := strings.TrimPrefix(pr.In.URL.Path, GatewayPrefix)
			pr.Out.URL.Scheme, pr.Out.URL.Host = h.Gateway.Scheme, h.Gateway.Host
			pr.Out.URL.Path = strings.TrimRight(h.Gateway.Path, "/") + rest
			pr.Out.URL.RawPath = ""
			pr.Out.Host = h.Gateway.Host
		},
		Transport: h.transport(), FlushInterval: -1, ErrorHandler: h.upstreamError,
	}
	h.rt = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			strip(pr)
			addr, _ := pr.In.Context().Value(gateAddrKey{}).(string)
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", addr
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, ToolsPrefix)
			pr.Out.URL.RawPath = ""
			pr.Out.Host = addr
		},
		Transport: h.transport(), FlushInterval: -1, ErrorHandler: h.upstreamError,
	}
}

type gateAddrKey struct{}

// upstreamError answers a failed forward with a fixed text: the worker's
// internal addresses and errors never reach a public caller.
func (h *Handler) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	if h.Logf != nil {
		h.Logf("public ingress: forward %s: %v", r.URL.Path, err)
	}
	http.Error(w, "the worker could not reach the gateway or tool gate for this run", http.StatusBadGateway)
}

// clientIP is the caller's address: X-Forwarded-For's last hop when the peer
// is the trusted proxy, else the peer.
func (h *Handler) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	trusted := h.TrustedPeer
	if trusted == nil {
		trusted = func(ip net.IP) bool { return ip != nil && (ip.IsPrivate() || ip.IsLoopback()) }
	}
	if trusted(peer) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	return host
}

func (h *Handler) deny(w http.ResponseWriter, client string, status int, msg string) {
	h.Reg.failed(client)
	http.Error(w, msg, status)
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.init()
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/healthz" {
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	client := h.clientIP(r)
	if h.Reg.blocked(client) {
		http.Error(w, "too many failed requests from this address; wait a minute", http.StatusTooManyRequests)
		return
	}
	if r.ContentLength > MaxBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	switch {
	case r.URL.Path == GatewayPrefix || strings.HasPrefix(r.URL.Path, GatewayPrefix+"/"):
		h.serveGateway(w, r, client)
	case strings.HasPrefix(r.URL.Path, ToolsPrefix+"/r/"):
		h.serveTools(w, r, client)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (h *Handler) serveGateway(w http.ResponseWriter, r *http.Request, client string) {
	rest := strings.TrimPrefix(r.URL.Path, GatewayPrefix)
	if !InferenceRoute(rest) {
		h.deny(w, client, http.StatusNotFound, "not found: only model and MCP routes are published under "+GatewayPrefix)
		return
	}
	run := h.Reg.runForKey(requestKey(r))
	if run == "" {
		h.deny(w, client, http.StatusUnauthorized, "this key does not belong to a live evaluation run")
		return
	}
	if !h.Reg.allowRun(run) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limit for this run reached; retry shortly", http.StatusTooManyRequests)
		return
	}
	h.gw.ServeHTTP(w, r)
}

func (h *Handler) serveTools(w http.ResponseWriter, r *http.Request, client string) {
	rest := strings.TrimPrefix(r.URL.Path, ToolsPrefix+"/r/")
	run, _, _ := strings.Cut(rest, "/")
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	addr, ok := h.Reg.gateFor(run, strings.TrimSpace(tok))
	if !ok {
		h.deny(w, client, http.StatusUnauthorized, "this token does not belong to a live evaluation run")
		return
	}
	if !h.Reg.allowRun(run) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limit for this run reached; retry shortly", http.StatusTooManyRequests)
		return
	}
	h.rt.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), gateAddrKey{}, addr)))
}

// Serve listens on addr and serves h until ctx ends.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("public ingress: listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
