package main

// Remote MCP endpoint (lane Y08, audit G4-09): the hosted server answers the
// Model Context Protocol over Streamable HTTP at /mcp, so an MCP client
// connects with no install:
//
//	claude mcp add --transport http heimdall https://heimdall.example.com/mcp \
//	    --header "Authorization: Bearer $HEIMDALL_TOKEN"
//
// It is the same server as `bifrost-bench mcp serve` (mcpserve.go), with one
// difference: each tool's API call is replayed in process through the
// hosted server's own handler, as the caller's API token, so the call meets
// the auth middleware, the tenancy policy, rate limits and audit exactly as
// a direct API call would. The endpoint is stateless: no session id, no
// server-sent stream (GET and DELETE answer 405).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/valyala/fasthttp"
)

const (
	// mcpRemoteMaxWait bounds run_evaluation's wait on the remote endpoint:
	// one HTTP request should not hold a proxy for hours. Longer waits poll
	// get_evaluation instead.
	mcpRemoteMaxWait = 10 * time.Minute
	// mcpRemoteTimeout bounds one POST /mcp.
	mcpRemoteTimeout = mcpRemoteMaxWait + 2*time.Minute
	// mcpRemoteBatch caps the messages of one batch.
	mcpRemoteBatch = 32
)

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		return &mcpHTTP{
			mode:      env.base.cfg.Server.Mode,
			publicURL: env.base.cfg.Auth.PublicURL,
			handler: func() fasthttp.RequestHandler {
				if env.server == nil {
					return nil
				}
				return env.server.Handler()
			},
		}, nil
	})
}

// mcpHTTP serves /mcp.
type mcpHTTP struct {
	mode      schemas.Mode
	publicURL string
	handler   func() fasthttp.RequestHandler // the hosted server's full handler; nil until it is built
}

// RegisterRoutes registers POST, GET and DELETE /mcp.
func (m *mcpHTTP) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.POST(handlers.MCPPath, lib.ChainMiddlewares(m.post, middlewares...))
	r.GET(handlers.MCPPath, lib.ChainMiddlewares(m.notAllowed, middlewares...))
	r.DELETE(handlers.MCPPath, lib.ChainMiddlewares(m.notAllowed, middlewares...))
}

func (m *mcpHTTP) notAllowed(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Allow", "POST")
	if mcpBrowserGet(ctx) {
		mcpHelpPage(ctx)
		return
	}
	lib.SendError(ctx, m.mode, fasthttp.StatusMethodNotAllowed, lib.ErrTypeMethodNotAllowed,
		"this MCP endpoint is stateless: POST one JSON-RPC message (or a batch) per request; there is no event stream or session to end")
}

// unauthorized answers 401 with the bearer challenge MCP clients read.
func (m *mcpHTTP) unauthorized(ctx *fasthttp.RequestCtx, msg string) {
	ctx.Response.Header.Set("WWW-Authenticate", `Bearer realm="heimdall"`)
	lib.SendError(ctx, m.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, msg)
}

func (m *mcpHTTP) post(ctx *fasthttp.RequestCtx) {
	if !m.originAllowed(string(ctx.Request.Header.Peek("Origin"))) {
		lib.SendError(ctx, m.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "this Origin may not call the MCP endpoint")
		return
	}
	authz := strings.TrimSpace(string(ctx.Request.Header.Peek("Authorization")))
	p := auth.PrincipalFrom(ctx)
	switch {
	case !strings.HasPrefix(strings.ToLower(authz), "bearer "):
		m.unauthorized(ctx, "send an org API token: Authorization: Bearer hm_... (Settings > API tokens)")
		return
	case p == nil || !p.IsAuthenticated():
		m.unauthorized(ctx, "the API token is not valid")
		return
	case p.Kind != auth.KindToken:
		lib.SendError(ctx, m.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "the MCP endpoint takes an org API token (hm_...), not a runner token")
		return
	}
	h := m.handler()
	if h == nil {
		lib.SendError(ctx, m.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "the server is starting")
		return
	}
	body := bytes.TrimSpace(ctx.PostBody())
	if len(body) == 0 {
		writeRPC(ctx, fasthttp.StatusBadRequest, &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error: empty body"}})
		return
	}
	api := &heimdallAPI{
		base:  "http://heimdall.internal",
		token: strings.TrimSpace(authz[len("bearer "):]),
		client: &http.Client{Transport: &inProcessTransport{
			handler: h, host: string(ctx.Host()), remote: ctx.RemoteAddr(),
			forwardedFor: string(ctx.Request.Header.Peek("X-Forwarded-For")),
		}},
	}
	srv := &mcpServer{api: api, version: benchVersion, poll: 10 * time.Second, maxWait: mcpRemoteMaxWait}
	work, cancel := context.WithTimeout(context.Background(), mcpRemoteTimeout)
	defer cancel()

	if body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			writeRPC(ctx, fasthttp.StatusBadRequest, &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()}})
			return
		}
		if len(batch) == 0 || len(batch) > mcpRemoteBatch {
			writeRPC(ctx, fasthttp.StatusBadRequest, &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32600, Message: "a batch holds 1 to 32 messages"}})
			return
		}
		var out []*rpcResponse
		for _, msg := range batch {
			if r := srv.handle(work, msg); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			ctx.SetStatusCode(fasthttp.StatusAccepted)
			return
		}
		writeRPC(ctx, fasthttp.StatusOK, out)
		return
	}
	r := srv.handle(work, body)
	if r == nil { // a notification or a response from the client
		ctx.SetStatusCode(fasthttp.StatusAccepted)
		return
	}
	writeRPC(ctx, fasthttp.StatusOK, r)
}

// originAllowed admits requests without an Origin (MCP clients, curl) and
// browser requests from this server's own origin only (DNS rebinding).
func (m *mcpHTTP) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(m.publicURL)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(strings.TrimRight(origin, "/"), u.Scheme+"://"+u.Host)
}

func writeRPC(ctx *fasthttp.RequestCtx, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		ctx.Error("encode response", fasthttp.StatusInternalServerError)
		return
	}
	ctx.SetStatusCode(status)
	ctx.SetContentType("application/json")
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetBody(b)
}

// inProcessTransport sends an http.Request through a fasthttp handler in
// the same process, as if it had arrived over the network from remote with
// Host host: the hosted server's middleware chain and dispatcher see it as
// an ordinary API call.
type inProcessTransport struct {
	handler      fasthttp.RequestHandler
	host         string
	remote       net.Addr
	forwardedFor string
}

// maxInProcessBody bounds a replayed request body.
const maxInProcessBody = 8 << 20

func (t *inProcessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.handler == nil {
		return nil, errors.New("no handler")
	}
	var freq fasthttp.Request
	freq.Header.SetMethod(req.Method)
	freq.SetRequestURI(req.URL.RequestURI())
	for k, vs := range req.Header {
		for _, v := range vs {
			freq.Header.Add(k, v)
		}
	}
	freq.Header.SetHost(t.host)
	if t.forwardedFor != "" {
		freq.Header.Set("X-Forwarded-For", t.forwardedFor)
	}
	if req.Body != nil {
		b, err := io.ReadAll(io.LimitReader(req.Body, maxInProcessBody+1))
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(b) > maxInProcessBody {
			return nil, errors.New("request body too large")
		}
		freq.SetBody(b)
	}
	remote := t.remote
	if remote == nil {
		remote = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
	}
	var fctx fasthttp.RequestCtx
	fctx.Init(&freq, remote, nil)
	t.handler(&fctx)
	body := append([]byte(nil), fctx.Response.Body()...)
	resp := &http.Response{
		Status:        http.StatusText(fctx.Response.StatusCode()),
		StatusCode:    fctx.Response.StatusCode(),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
	fctx.Response.Header.VisitAll(func(k, v []byte) { resp.Header.Add(string(k), string(v)) })
	return resp, nil
}
