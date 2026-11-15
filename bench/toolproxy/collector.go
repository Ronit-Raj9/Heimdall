package toolproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
)

// The agent event collector (docs/agent-events.md) is served by the tool
// proxy beside its routes: POST /events and POST /otlp/v1/traces. Its ledger
// (agent-events.jsonl) and counters sit next to the tool ledger, in the
// ledger volume the agent cannot reach. It opens on the first report, so a
// run whose agent reports nothing has no ledger, and it never fails a tool
// call or the run: a write problem only drops events.

// collectors holds each server's collector, opened lazily.
var collectors sync.Map // *Server -> *agentevents.Collector

// serveCollectorPath answers a collector endpoint and reports whether the
// path was one.
func (s *Server) serveCollectorPath(w http.ResponseWriter, r *http.Request) bool {
	if !agentevents.Handles(r.URL.Path) {
		return false
	}
	s.serveCollector(w, r)
	return true
}

// serveCollector answers a collector endpoint.
func (s *Server) serveCollector(w http.ResponseWriter, r *http.Request) {
	c, err := s.collector()
	if err != nil {
		http.Error(w, "event collector unavailable", http.StatusServiceUnavailable)
		return
	}
	c.ServeHTTP(w, r)
}

func (s *Server) collector() (*agentevents.Collector, error) {
	if c, ok := collectors.Load(s); ok {
		return c.(*agentevents.Collector), nil
	}
	if s.cfg.LedgerPath == "" {
		return nil, errors.New("no ledger path")
	}
	c, err := agentevents.NewCollector(filepath.Join(filepath.Dir(s.cfg.LedgerPath), agentevents.LedgerName), agentevents.Limits{})
	if err != nil {
		return nil, err
	}
	if prev, loaded := collectors.LoadOrStore(s, c); loaded {
		_ = c.Close()
		return prev.(*agentevents.Collector), nil
	}
	return c, nil
}

// CollectorConfig starts a collector-only server: the event collector and
// /healthz, no routes. The environment kernel runs it, in place of the tool
// proxy, for a run that has no proxied services.
type CollectorConfig struct {
	Listen     string // e.g. :9200
	LedgerPath string // agent-events.jsonl
}

// ServeCollector serves a collector-only server until ctx ends.
func ServeCollector(ctx context.Context, cfg CollectorConfig) error {
	c, err := agentevents.NewCollector(cfg.LedgerPath, agentevents.Limits{})
	if err != nil {
		return err
	}
	defer c.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := c.Healthy(); err != nil {
			http.Error(w, "ledger: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.Handle(agentevents.PathEvents, c)
	mux.Handle(agentevents.PathOTLP, c)
	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return fmt.Errorf("event collector on %s: %w", cfg.Listen, err)
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shut down event collector: %w", err)
	}
	return nil
}
