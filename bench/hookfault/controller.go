// Package hookfault serves cooperative adapter hooks inside a private run container.
package hookfault

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

type operation struct {
	request  schemas.AdapterFaultRequest
	decision schemas.AdapterFaultDecision
	ack      *schemas.AdapterFaultRequest
	seq      int
}
type session struct {
	hooks    string
	finished bool
	healthy  bool
}

// Controller owns one immutable rule and one bounded ledger. Its admin token
// is available only inside the controller container, never to the agent.
type Controller struct {
	mu             sync.Mutex
	cfg            schemas.AdapterFaultConfig
	file           io.Writer
	entries        []schemas.AdapterFaultEntry
	operations     map[string]*operation
	sessions       map[string]*session
	matched, bytes int
	failure        string
	frozen         *schemas.AdapterFaultEvidence
}

// New validates all configuration before accepting runtime traffic. The caller
// owns the ledger writer and must sync it before copying artifacts.
func New(cfg schemas.AdapterFaultConfig, ledger io.Writer) (*Controller, error) {
	if cfg.RunID == "" || len(cfg.Token) < 24 || len(cfg.AdminToken) < 24 || cfg.Token == cfg.AdminToken || len(cfg.Nonce) < 16 || cfg.WallMS <= 0 || len(cfg.Sessions) == 0 {
		return nil, fmt.Errorf("invalid private adapter fault configuration")
	}
	if err := utils.ValidateAdapterFault(cfg.Rule, cfg.WallMS); err != nil {
		return nil, err
	}
	if err := utils.ValidateAdapterFaultHooks(cfg.Hooks, cfg.Rule); err != nil {
		return nil, err
	}
	// Copy the rule and capabilities so callers cannot mutate an armed rule.
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &Controller{cfg: cfg, file: ledger, entries: []schemas.AdapterFaultEntry{}, operations: map[string]*operation{}, sessions: map[string]*session{}}, nil
}

// Handle exposes only decisions to the runtime credential. Freeze needs a
// separate credential and is reached through docker exec on loopback.
func (c *Controller) Handle(ctx *fasthttp.RequestCtx) {
	if string(ctx.Path()) == "/healthz" && ctx.IsGet() {
		ctx.SetStatusCode(200)
		return
	}
	admin := string(ctx.Path()) == "/admin/freeze"
	token := c.cfg.Token
	if admin {
		token = c.cfg.AdminToken
	}
	if subtle.ConstantTimeCompare(ctx.Request.Header.Peek("Authorization"), []byte("Bearer "+token)) != 1 {
		ctx.Error("unauthorized", 401)
		return
	}
	if !ctx.IsPost() {
		ctx.Error("POST required", 405)
		return
	}
	if len(ctx.PostBody()) > utils.AdapterFaultMaxControl {
		ctx.Error("control message exceeds 64KiB", 413)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if admin {
		var m struct {
			Sessions []string `json:"sessions"`
		}
		if err := json.Unmarshal(ctx.PostBody(), &m); err != nil {
			ctx.Error("invalid freeze request", 400)
			return
		}
		c.respond(ctx, c.freeze(m.Sessions))
		return
	}
	var m schemas.AdapterFaultRequest
	if err := json.Unmarshal(ctx.PostBody(), &m); err != nil || m.Version != schemas.AdapterFaultProtocol || m.RunID != c.cfg.RunID || !slices.Contains(c.cfg.Sessions, m.Session) {
		c.reject(ctx, "invalid runtime identity")
		return
	}
	if c.frozen != nil {
		ctx.Error("controller is frozen", 409)
		return
	}
	if c.failure != "" {
		ctx.Error("controller evidence failed", 503)
		return
	}
	var d schemas.AdapterFaultDecision
	var err error
	switch string(ctx.Path()) {
	case "/v1/register":
		d, err = c.register(m)
	case "/v1/before":
		d, err = c.before(m)
	case "/v1/ack":
		d, err = c.ack(m)
	case "/v1/finish":
		d, err = c.finish(m)
	default:
		ctx.Error("unknown endpoint", 404)
		return
	}
	if err != nil {
		c.reject(ctx, err.Error())
		return
	}
	c.respond(ctx, d)
}

func (c *Controller) register(m schemas.AdapterFaultRequest) (schemas.AdapterFaultDecision, error) {
	if m.Phase != "register" {
		return schemas.AdapterFaultDecision{}, fmt.Errorf("expected register phase")
	}
	if err := utils.ValidateAdapterFaultHooks(m.Hooks, c.cfg.Rule); err != nil {
		return schemas.AdapterFaultDecision{}, err
	}
	expected, _ := utils.HashJSON(c.cfg.Hooks)
	hash, _ := utils.HashJSON(m.Hooks)
	if expected != hash {
		return schemas.AdapterFaultDecision{}, fmt.Errorf("runtime hooks differ from manifest capabilities")
	}
	if s := c.sessions[m.Session]; s != nil {
		if s.hooks != hash || s.finished {
			return schemas.AdapterFaultDecision{}, fmt.Errorf("session already closed or registered with different hooks")
		}
		return schemas.AdapterFaultDecision{Mode: "pass"}, nil
	}
	if err := c.append(m, "register", "", "", true); err != nil {
		return schemas.AdapterFaultDecision{}, err
	}
	c.sessions[m.Session] = &session{hooks: hash}
	return schemas.AdapterFaultDecision{Mode: "pass"}, nil
}
func (c *Controller) active(m schemas.AdapterFaultRequest) error {
	if s := c.sessions[m.Session]; s == nil || s.finished {
		return fmt.Errorf("session is not registered or already finished")
	}
	return nil
}
func (c *Controller) before(m schemas.AdapterFaultRequest) (schemas.AdapterFaultDecision, error) {
	if err := c.active(m); err != nil {
		return schemas.AdapterFaultDecision{}, err
	}
	if m.Phase != "before" || m.OperationID == "" || len(m.OperationID) > 128 {
		return schemas.AdapterFaultDecision{}, fmt.Errorf("invalid operation identity")
	}
	if old := c.operations[m.OperationID]; old != nil {
		if old.request.Session != m.Session || old.request.Kind != m.Kind || old.request.Name != m.Name {
			return schemas.AdapterFaultDecision{}, fmt.Errorf("operation id reused for a different hook")
		}
		return old.decision, nil
	}
	if len(c.operations) >= utils.AdapterFaultMaxOperations {
		return schemas.AdapterFaultDecision{}, fmt.Errorf("operation limit exceeded")
	}
	found := false
	for _, h := range c.cfg.Hooks {
		if h.Kind == m.Kind && h.Name == m.Name {
			found = true
			break
		}
	}
	if !found {
		return schemas.AdapterFaultDecision{}, fmt.Errorf("operation is not a declared hook")
	}
	d := schemas.AdapterFaultDecision{Mode: "pass"}
	r := c.cfg.Rule
	if r.Kind == m.Kind && r.Name == m.Name && (r.Session == "" || r.Session == m.Session) {
		c.matched++
		from := r.FromCall
		if from == 0 {
			from = 1
		}
		if c.matched >= from && (r.Calls == 0 || c.matched-from < r.Calls) {
			d.Mode = r.Mode
			d.TimeoutMS = r.TimeoutMS
			d.Message = strings.ReplaceAll(r.Message, "{{nonce}}", c.cfg.Nonce)
			if d.Message == "" {
				d.Message = "injected adapter " + d.Mode + " [" + c.cfg.Nonce + "]"
			}
			if r.Mode == "replace" {
				raw, err := json.Marshal(r.Result)
				if err != nil {
					return d, err
				}
				d.Result = json.RawMessage(strings.ReplaceAll(string(raw), "{{nonce}}", c.cfg.Nonce))
			}
		}
	}
	if err := c.append(m, "decision", d.Mode, d.Message, false); err != nil {
		return d, err
	}
	c.operations[m.OperationID] = &operation{request: m, decision: d, seq: len(c.entries)}
	return d, nil
}
func (c *Controller) ack(m schemas.AdapterFaultRequest) (schemas.AdapterFaultDecision, error) {
	if err := c.active(m); err != nil {
		return schemas.AdapterFaultDecision{}, err
	}
	op := c.operations[m.OperationID]
	if op == nil || op.request.Session != m.Session || op.request.Kind != m.Kind || op.request.Name != m.Name {
		return schemas.AdapterFaultDecision{}, fmt.Errorf("acknowledgement has no matching operation")
	}
	if (op.decision.Mode == "pass" && m.Phase != "completed") || (op.decision.Mode != "pass" && m.Phase != "applied") {
		return schemas.AdapterFaultDecision{}, fmt.Errorf("acknowledgement phase does not match decision")
	}
	if op.ack != nil {
		if op.ack.Phase != m.Phase || op.ack.Success != m.Success {
			return schemas.AdapterFaultDecision{}, fmt.Errorf("conflicting acknowledgement")
		}
		return schemas.AdapterFaultDecision{Mode: "pass"}, nil
	}
	if op.decision.Mode != "pass" && !m.Success {
		return schemas.AdapterFaultDecision{}, fmt.Errorf("injected decision was not applied")
	}
	if err := c.append(m, m.Phase, op.decision.Mode, "", m.Success); err != nil {
		return schemas.AdapterFaultDecision{}, err
	}
	op.ack = &m
	return schemas.AdapterFaultDecision{Mode: "pass"}, nil
}
func (c *Controller) finish(m schemas.AdapterFaultRequest) (schemas.AdapterFaultDecision, error) {
	s := c.sessions[m.Session]
	if s == nil || m.Phase != "finish" {
		return schemas.AdapterFaultDecision{}, fmt.Errorf("session was not registered")
	}
	if s.finished {
		if s.healthy != m.Healthy {
			return schemas.AdapterFaultDecision{}, fmt.Errorf("conflicting finish")
		}
		return schemas.AdapterFaultDecision{Mode: "pass"}, nil
	}
	for _, op := range c.operations {
		if op.request.Session == m.Session && op.ack == nil {
			return schemas.AdapterFaultDecision{}, fmt.Errorf("session has unacknowledged operations")
		}
	}
	if err := c.append(m, "finish", "", "", m.Healthy); err != nil {
		return schemas.AdapterFaultDecision{}, err
	}
	s.finished = true
	s.healthy = m.Healthy
	return schemas.AdapterFaultDecision{Mode: "pass"}, nil
}
func (c *Controller) append(m schemas.AdapterFaultRequest, phase, mode, message string, success bool) error {
	prev := ""
	if len(c.entries) > 0 {
		prev = c.entries[len(c.entries)-1].Hash
	}
	row := schemas.AdapterFaultEntry{Seq: len(c.entries) + 1, At: time.Now().UTC(), RunID: c.cfg.RunID, Session: m.Session, OperationID: m.OperationID, Kind: m.Kind, Name: m.Name, Phase: phase, Mode: mode, Message: message, Success: success, PrevHash: prev}
	hash, err := utils.AdapterFaultEntryHash(row)
	if err != nil {
		return err
	}
	row.Hash = hash
	raw, err := json.Marshal(row)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if c.bytes+len(raw) > utils.AdapterFaultMaxLedger {
		return fmt.Errorf("adapter fault ledger limit exceeded")
	}
	if c.file != nil {
		n, err := c.file.Write(raw)
		if err != nil {
			return fmt.Errorf("write adapter fault ledger: %w", err)
		}
		if n != len(raw) {
			return io.ErrShortWrite
		}
	}
	c.bytes += len(raw)
	c.entries = append(c.entries, row)
	return nil
}
func (c *Controller) freeze(executed []string) *schemas.AdapterFaultEvidence {
	if c.frozen != nil {
		return c.frozen
	}
	reason := c.failure
	if len(executed) == 0 {
		reason = "no executed sessions recorded"
	}
	for _, id := range executed {
		s := c.sessions[id]
		if !slices.Contains(c.cfg.Sessions, id) || s == nil || !s.finished || !s.healthy {
			reason = "missing registration or healthy finish for executed session"
		}
	}
	for id := range c.sessions {
		if !slices.Contains(executed, id) {
			reason = "registration for an unexecuted session"
		}
	}
	for _, op := range c.operations {
		if op.ack == nil {
			reason = "unacknowledged operation"
		}
	}
	if f, ok := c.file.(*os.File); ok {
		if err := f.Sync(); err != nil {
			reason = "sync adapter fault ledger failed"
		}
	}
	hash, _ := utils.HashJSON(c.cfg.Rule)
	last := ""
	if len(c.entries) > 0 {
		last = c.entries[len(c.entries)-1].Hash
	}
	c.frozen = &schemas.AdapterFaultEvidence{Version: schemas.AdapterFaultProtocol, RunID: c.cfg.RunID, RuleHash: hash, Complete: reason == "", Reason: reason, LedgerHash: last, Entries: append([]schemas.AdapterFaultEntry{}, c.entries...)}
	return c.frozen
}
func (c *Controller) reject(ctx *fasthttp.RequestCtx, reason string) {
	if c.failure == "" {
		c.failure = reason
	}
	ctx.Error(reason, 409)
}
func (c *Controller) respond(ctx *fasthttp.RequestCtx, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		c.reject(ctx, "encode control response")
		return
	}
	ctx.SetContentType("application/json")
	ctx.SetBody(raw)
}
