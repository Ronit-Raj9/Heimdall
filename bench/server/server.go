// Package server assembles the bench HTTP server: every handler of the
// /api/bench API, the built UI with SPA fallback, the middlewares, and a
// graceful shutdown. It mirrors transports/bifrost-http/server in Bifrost.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Server timeouts. There is no write timeout: server-sent event streams stay
// open for the whole experiment.
const (
	readTimeout     = 30 * time.Second
	idleTimeout     = 2 * time.Minute
	shutdownTimeout = 10 * time.Second
)

// Deps are the implementations cmd/bifrost-bench wires in. Any of them may be
// nil; the routes that need a missing one answer 503.
type Deps struct {
	Index    handlers.ReadIndex
	Analyzer handlers.Analyzer
	Launcher handlers.Launcher
	Catalog  handlers.CatalogSource
	Auditor  handlers.Auditor
	Doctor   handlers.DoctorFunc
	Releases handlers.ReleaseLog
	// UI is the built UI with index.html at its root, for example
	// fs.Sub(ui.Dist, "dist"). Nil serves the API only.
	UI      fs.FS
	Logger  schemas.Logger
	Version string
}

// Server is the bench HTTP server.
type Server struct {
	cfg            *lib.Config
	logger         schemas.Logger
	handler        fasthttp.RequestHandler
	server         *fasthttp.Server
	experiments    *handlers.ExperimentsHandler
	token          string
	tokenGenerated bool
	announceOnce   sync.Once
}

// New builds the server and registers every route. The write token comes
// from BENCH_WRITE_TOKEN or is generated here and logged once when serving
// starts.
func New(cfg *lib.Config, deps Deps) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("new server: config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("new server: %w", err)
	}
	token, generated, err := lib.ResolveWriteToken()
	if err != nil {
		return nil, fmt.Errorf("new server: %w", err)
	}
	logger := deps.Logger
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	s := &Server{cfg: cfg, logger: logger, token: token, tokenGenerated: generated}
	mode := cfg.Server.Mode
	write := lib.RequireWriteToken(mode, token)
	common := []lib.Middleware{lib.BodyLimit(mode, lib.MaxBodyBytes)}

	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found: "+string(ctx.Path()))
	}
	r.MethodNotAllowed = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, mode, fasthttp.StatusMethodNotAllowed, lib.ErrTypeMethodNotAllowed,
			"method "+string(ctx.Method())+" not allowed on "+string(ctx.Path()))
	}
	s.experiments = handlers.NewExperimentsHandler(mode, deps.Index, deps.Analyzer, deps.Launcher, deps.Catalog, write)
	handlers.NewHealthHandler(mode, deps.Version, deps.Doctor).RegisterRoutes(r, common...)
	handlers.NewCatalogHandler(mode, deps.Catalog).RegisterRoutes(r, common...)
	s.experiments.RegisterRoutes(r, common...)
	handlers.NewRunsHandler(mode, deps.Index).RegisterRoutes(r, common...)
	handlers.NewPairsHandler(mode, deps.Index).RegisterRoutes(r, common...)
	handlers.NewTriageHandler(mode, deps.Index, deps.Catalog).RegisterRoutes(r, common...)
	handlers.NewLeaderboardHandler(mode, deps.Index).RegisterRoutes(r, common...)
	handlers.NewAuditHandler(mode, deps.Auditor, write).RegisterRoutes(r, common...)
	handlers.NewReleasesHandler(mode, deps.Releases, write).RegisterRoutes(r, common...)
	if deps.UI != nil {
		NewUIHandler(mode, deps.UI).RegisterRoutes(r)
	}

	// Host check, CORS and security headers wrap the router itself, so they
	// cover the UI, the 404 answer and every API route alike.
	s.handler = lib.ChainMiddlewares(r.Handler,
		lib.SecurityHeaders(),
		lib.HostGuard(mode, cfg.Server.AllowedHosts),
		lib.CORS(cfg.Server.CORSOrigins),
	)
	s.server = &fasthttp.Server{
		Handler:               s.handler,
		Name:                  "bifrost-bench",
		MaxRequestBodySize:    lib.MaxBodyBytes,
		ErrorHandler:          lib.RequestErrorHandler(mode, lib.MaxBodyBytes),
		ReadTimeout:           readTimeout,
		IdleTimeout:           idleTimeout,
		NoDefaultServerHeader: true,
		Logger:                fasthttpLogger{logger},
	}
	return s, nil
}

// Handler returns the root handler with every middleware applied.
func (s *Server) Handler() fasthttp.RequestHandler {
	return s.handler
}

// WriteToken returns the token POST routes require.
func (s *Server) WriteToken() string {
	return s.token
}

// ListenAndServe listens on server.listen and serves until ctx ends, then
// shuts down gracefully. A non-loopback address is allowed but warned about:
// loopback binding is the API's first defence.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if !s.cfg.ListenIsLoopback() {
		s.logger.Warn(fmt.Sprintf("bench API listens on %s, which is not loopback; only the write token guards it", s.cfg.Server.Listen))
	}
	ln, err := net.Listen("tcp", s.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Server.Listen, err)
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx ends or the listener fails. On ctx end it
// closes the live event streams first, then waits up to shutdownTimeout for
// open requests.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.announceOnce.Do(func() {
		s.logger.Info(fmt.Sprintf("bench API on http://%s/ (mode %s)", ln.Addr(), s.cfg.Server.Mode))
		if s.tokenGenerated {
			s.logger.Info(fmt.Sprintf("bench write token (shown once; set %s to fix it): %s", lib.WriteTokenEnv, s.token))
		}
	})
	errCh := make(chan error, 1)
	go func() { errCh <- s.server.Serve(ln) }()
	select {
	case err := <-errCh:
		s.experiments.Close()
		if err != nil {
			return fmt.Errorf("serve bench API: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	s.experiments.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.server.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("shut down bench API: %w", err)
	}
	if err := <-errCh; err != nil {
		return fmt.Errorf("serve bench API: %w", err)
	}
	return nil
}

// fasthttpLogger routes fasthttp's connection errors (an oversized body, a
// client that hung up) to the bench logger at debug level.
type fasthttpLogger struct{ logger schemas.Logger }

// Printf implements fasthttp.Logger.
func (l fasthttpLogger) Printf(format string, args ...any) {
	l.logger.Debug(fmt.Sprintf(format, args...))
}
