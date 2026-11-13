package faultproxy

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	maxPlanBody          = 64 << 10
	defaultClientTimeout = 10 * time.Second
	maxLedgerResponse    = 64 << 20
)

// ControlHandler returns the control API. Writes need the control token as a
// bearer token; reads do not, because the control listener is bound to
// loopback and the agent can never reach the proxy.
//
//	PUT    /plans/{slot}     body schemas.FaultPlan; resets the slot's counters
//	DELETE /plans/{slot}     clears the plan; resets the slot's counters
//	GET    /ledger?run_id=   {"run_id":..., "entries":[...]}
//	GET    /health           {"status":"ok", ...}
func (s *Server) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /plans/{slot}", s.handleSetPlan)
	mux.HandleFunc("DELETE /plans/{slot}", s.handleClearPlan)
	mux.HandleFunc("GET /ledger", s.handleLedger)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}

func (s *Server) handleSetPlan(w http.ResponseWriter, r *http.Request) {
	slot, ok := s.controlSlot(w, r)
	if !ok {
		return
	}
	var p schemas.FaultPlan
	dec := json.NewDecoder(io.LimitReader(r.Body, maxPlanBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		s.controlError(w, http.StatusBadRequest, "decode plan: "+err.Error())
		return
	}
	if err := s.plans.set(slot, p); err != nil {
		s.controlError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.logger.Info("fault plan set", "slot", slot, "run_id", p.RunID, "mode", p.Mode,
		"from_call", p.FromCall, "call", p.Call, "status", p.Status)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClearPlan(w http.ResponseWriter, r *http.Request) {
	slot, ok := s.controlSlot(w, r)
	if !ok {
		return
	}
	s.plans.clear(slot)
	s.logger.Info("fault plan cleared", "slot", slot)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLedger(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run_id")
	if runID == "" {
		s.controlError(w, http.StatusBadRequest, "run_id is required")
		return
	}
	entries, err := s.ledger.forRun(runID)
	if err != nil {
		s.logger.Error("read ledger", "run_id", runID, "error", err.Error())
		s.controlError(w, http.StatusInternalServerError, "read ledger failed")
		return
	}
	s.writeJSON(w, http.StatusOK, ledgerResponse{RunID: runID, Entries: entries})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "upstream": s.upstreamLabel(), "slots": len(s.names),
	})
}

// controlSlot authorizes a write and resolves its slot name.
func (s *Server) controlSlot(w http.ResponseWriter, r *http.Request) (string, bool) {
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
		s.controlError(w, http.StatusUnauthorized, "control token required")
		return "", false
	}
	slot := r.PathValue("slot")
	if !s.names[slot] {
		s.controlError(w, http.StatusNotFound, fmt.Sprintf("unknown slot %q", slot))
		return "", false
	}
	return slot, true
}

func (s *Server) controlError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, map[string]string{"error": msg})
}

// ledgerResponse is the body of GET /ledger.
type ledgerResponse struct {
	RunID   string                `json:"run_id"`
	Entries []schemas.LedgerEntry `json:"entries"`
}

// Client talks to the control API. It implements schemas.FaultController.
type Client struct {
	base  string
	token string
	http  *http.Client
}

var _ schemas.FaultController = (*Client)(nil)

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithToken sets the bearer token for plan writes (Server.ControlToken).
func WithToken(token string) ClientOption {
	return func(c *Client) { c.token = token }
}

// WithHTTPClient replaces the default client, which times out after 10s.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) { c.http = hc }
}

// NewClient returns a control client for the proxy at baseURL, for example
// http://127.0.0.1:9101.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: defaultClientTimeout}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// SetPlan binds plan to slot and resets the slot's call counter.
func (c *Client) SetPlan(ctx context.Context, slot string, plan schemas.FaultPlan) error {
	body, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode plan for slot %s: %w", slot, err)
	}
	if err := c.do(ctx, http.MethodPut, "/plans/"+url.PathEscape(slot), body, nil); err != nil {
		return fmt.Errorf("set plan for slot %s: %w", slot, err)
	}
	return nil
}

// ClearPlan removes slot's plan and resets its call counter.
func (c *Client) ClearPlan(ctx context.Context, slot string) error {
	if err := c.do(ctx, http.MethodDelete, "/plans/"+url.PathEscape(slot), nil, nil); err != nil {
		return fmt.Errorf("clear plan for slot %s: %w", slot, err)
	}
	return nil
}

// Ledger returns every ledger entry the proxy recorded for runID.
func (c *Client) Ledger(ctx context.Context, runID string) ([]schemas.LedgerEntry, error) {
	var out ledgerResponse
	if err := c.do(ctx, http.MethodGet, "/ledger?run_id="+url.QueryEscape(runID), nil, &out); err != nil {
		return nil, fmt.Errorf("ledger for run %s: %w", runID, err)
	}
	if out.Entries == nil {
		out.Entries = []schemas.LedgerEntry{}
	}
	return out.Entries, nil
}

// Health returns nil when the proxy answers its health check.
func (c *Client) Health(ctx context.Context) error {
	if err := c.do(ctx, http.MethodGet, "/health", nil, nil); err != nil {
		return fmt.Errorf("fault proxy health: %w", err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLedgerResponse))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("status %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
