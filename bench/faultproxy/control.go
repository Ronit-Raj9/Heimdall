package faultproxy

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
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
// loopback and the agent can never reach the proxy. GET /upstreams needs the
// token too: it makes the proxy open connections to the providers, so it is
// an action rather than a read.
//
//	PUT    /plans/{slot}     body schemas.FaultPlan; resets the slot's counters
//	DELETE /plans/{slot}     clears the plan; resets the slot's counters
//	                         (both: 409 while another owner's claim holds the slot, claims.go)
//	GET    /ledger?run_id=   {"run_id":..., "entries":[...]}
//	GET    /health           {"status":"ok", "plan_modes":[...], "scripted_providers":[...], ...}
//	GET    /upstreams        UpstreamReport: TCP and TLS reachability of each real upstream
func (s *Server) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /plans/{slot}", s.handleSetPlan)
	mux.HandleFunc("DELETE /plans/{slot}", s.handleClearPlan)
	mux.HandleFunc("GET /ledger", s.handleLedger)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /upstreams", s.handleUpstreams)
	// F1: key slot ownership across bench processes (claims.go).
	mux.HandleFunc("POST /claims", s.handleClaim)
	mux.HandleFunc("POST /claims/renew", s.handleRenew)
	mux.HandleFunc("POST /claims/release", s.handleRelease)
	mux.HandleFunc("GET /claims", s.handleClaims)
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
	held, err := s.plans.setOwned(slot, r.Header.Get(OwnerHeader), p) // F1: a slot another owner claimed keeps its plan
	if err != nil {
		s.controlError(w, http.StatusBadRequest, err.Error())
		return
	}
	if held != nil {
		s.planRefused(w, "set", slot, held)
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
	held, err := s.plans.clearOwned(slot, r.Header.Get(OwnerHeader))
	if err != nil {
		s.controlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if held != nil { // F1
		s.planRefused(w, "clear", slot, held)
		return
	}
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

// handleHealth reports liveness and what the proxy can do: the plan modes it
// honours and the providers a ModeScripted run can be answered for. A caller
// that needs a mode reads it here, because an older proxy answers /health
// without these fields and rejects an unknown mode only when a plan is set.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, Health{
		Status: "ok", Upstream: s.upstreamLabel(), Slots: len(s.names),
		PlanModes: PlanModes, ScriptedProviders: s.scriptedProviders(),
		PlanFields: PlanFields, // W9: model-filtered plans
		SlotClaims: true,       // F1: the claim API (claims.go)
	})
}

// scriptedProviders lists, in table order, every provider a ModeScripted run
// is answered for: those whose format has a scripted model.
func (s *Server) scriptedProviders() []string {
	out := []string{}
	for _, name := range s.providers.order {
		if ScriptedShape(s.providers.byName[name], "") != "" {
			out = append(out, name)
		}
	}
	return out
}

// handleUpstreams checks every real upstream and reports each one. A
// failed provider is still a 200: the report is the answer, and the caller
// decides what a failure blocks.
func (s *Server) handleUpstreams(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	rep := s.checkUpstreams(r.Context(), s.newUpstreamDialer())
	for _, c := range rep.Upstreams {
		if !c.OK && c.Skipped == "" {
			s.logger.Warn("upstream check failed", "provider", c.Provider, "host", c.Host, "error", c.Error)
		}
	}
	s.writeJSON(w, http.StatusOK, rep)
}

// authorized checks the control token and answers 401 when it is wrong.
func (s *Server) authorized(w http.ResponseWriter, r *http.Request) bool {
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
		s.controlError(w, http.StatusUnauthorized, "control token required")
		return false
	}
	return true
}

// controlSlot authorizes a write and resolves its slot name.
func (s *Server) controlSlot(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.authorized(w, r) {
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

// Health is the body of GET /health. PlanModes and ScriptedProviders are
// empty from a proxy older than plan mode scripted.
type Health struct {
	Status            string            `json:"status"`
	Upstream          map[string]string `json:"upstream"` // provider -> real base URL, scripted or unconfigured
	Slots             int               `json:"slots"`
	PlanModes         []string          `json:"plan_modes,omitempty"`
	ScriptedProviders []string          `json:"scripted_providers,omitempty"`
	PlanFields        []string          `json:"plan_fields,omitempty"` // W9: optional plan fields honoured; an older proxy rejects a plan that sets one
	SlotClaims        bool              `json:"slot_claims,omitempty"` // F1: the proxy enforces key slot claims (claims.go); false from an older proxy
}

// Supports reports whether the proxy honours plan mode.
func (h *Health) Supports(mode string) bool {
	return slices.Contains(h.PlanModes, mode)
}

// Scripts reports whether a ModeScripted run on provider is answered by the
// scripted model.
func (h *Health) Scripts(provider string) bool {
	return slices.Contains(h.ScriptedProviders, provider)
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
	owner string // F1: owner token for key slot claims, sent in X-Slot-Owner (claims.go)
	label string // F1: who this client is in claims, host and pid
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
// http://127.0.0.1:9101. Each client is its own slot owner, with a random
// owner token, unless WithOwner sets one: claims and plan writes of one bench
// process must go through one client.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: defaultClientTimeout}, label: defaultOwnerLabel()}
	c.owner, _ = randomHex(16) // F1: an empty owner (no randomness) claims nothing and writes only unclaimed slots
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

// Capabilities reads /health: the plan modes the proxy honours and the
// providers it can script. A proxy older than plan mode scripted answers with
// neither list, so Supports(ModeScripted) is false for it.
func (c *Client) Capabilities(ctx context.Context) (*Health, error) {
	var h Health
	if err := c.do(ctx, http.MethodGet, "/health", nil, &h); err != nil {
		return nil, fmt.Errorf("fault proxy health: %w", err)
	}
	return &h, nil
}

// CheckUpstreams asks the proxy to dial every provider it answers from the
// real API with a real key and returns its report. It needs the control
// token. A proxy older than the check answers 404 or 405, which comes back
// as ErrUpstreamCheckUnsupported so a caller can tell "unknown" from
// "unreachable".
func (c *Client) CheckUpstreams(ctx context.Context) (*UpstreamReport, error) {
	var out UpstreamReport
	if err := c.do(ctx, http.MethodGet, "/upstreams", nil, &out); err != nil {
		var se *statusError
		if errors.As(err, &se) && (se.code == http.StatusNotFound || se.code == http.StatusMethodNotAllowed) {
			return nil, fmt.Errorf("fault proxy upstream check: %w (%w)", ErrUpstreamCheckUnsupported, err)
		}
		return nil, fmt.Errorf("fault proxy upstream check: %w", err)
	}
	if out.Upstreams == nil {
		out.Upstreams = []UpstreamCheck{}
	}
	return &out, nil
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
	if c.owner != "" {
		req.Header.Set(OwnerHeader, c.owner) // F1: plan writes on a claimed slot need it
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
		se := &statusError{code: resp.StatusCode, body: data}
		if json.Unmarshal(data, &e) == nil {
			se.msg = e.Error
		}
		return se
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// statusError is a non-2xx answer from the control API. Its text keeps the
// status code first, as "status 404" or "status 401: control token required".
type statusError struct {
	code int
	msg  string
	body []byte // the raw answer, for callers that read more than the error text (claims.go)
}

func (e *statusError) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("status %d: %s", e.code, e.msg)
	}
	return fmt.Sprintf("status %d", e.code)
}
