// Package builder is the environment builder agent of issues to benchmark: a
// Go tool loop on the org's model (through the Bifrost gateway) that installs
// a repository in a sandbox container, proves the PR tests run, and hands in
// an EnvSpec whose Dockerfile Heimdall writes from the commands that worked.
package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Message is one chat message of the builder conversation.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is one tool the model asked for.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ToolSpec describes one tool to the model (JSON schema parameters).
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Reply is one model turn: text, tool calls and what the turn cost.
type Reply struct {
	Content string
	Calls   []ToolCall
	CostUSD float64
	Priced  bool
}

// Model is the builder's model. The gateway client and the scripted fake
// implement it.
type Model interface {
	Next(ctx context.Context, msgs []Message, tools []ToolSpec) (Reply, error)
}

const (
	chatCompletionsPath = "/v1/chat/completions"
	virtualKeyHeader    = "x-bf-vk"
	maxResponseBody     = 4 << 20
	revokeTimeout       = 30 * time.Second
)

// GatewayConfig wires the builder to the Bifrost gateway the same way the
// judge grader is wired: the builder mints its own per-job virtual key on
// the org's provider and model, and revokes it when the build ends.
type GatewayConfig struct {
	Gateway     schemas.Gateway
	URL         string // gateway inference base URL
	HTTP        *http.Client
	Provider    string
	Model       string
	Temperature float64 // 0 to 0.2
	MaxTokens   int
	Price       func(model string, tok schemas.Tokens) (float64, bool)
	// KeyName is the run id the key is minted under (r_<id>), so a crashed
	// build's key is still found and revoked by the janitor.
	KeyName string
	ExpID   string
	Logger  schemas.Logger
	// Key is a key minted elsewhere, by the server for a worker. It is used
	// as is and never revoked here (the server revokes it).
	Key *schemas.RunKey
}

// GatewayModel is a Model that calls the org's model through Bifrost.
type GatewayModel struct {
	cfg  GatewayConfig
	mu   sync.Mutex
	key  *schemas.RunKey
	done bool
}

// NewGatewayModel returns a model client; the key is minted on first use.
func NewGatewayModel(cfg GatewayConfig) *GatewayModel {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}
	return &GatewayModel{cfg: cfg}
}

func (m *GatewayModel) ensureKey(ctx context.Context) (*schemas.RunKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done {
		return nil, errors.New("the builder's model key was already revoked")
	}
	if m.key != nil {
		return m.key, nil
	}
	if m.cfg.Key != nil {
		m.key = m.cfg.Key
		return m.key, nil
	}
	if m.cfg.Gateway == nil {
		return nil, errors.New("no gateway is wired to the builder")
	}
	rc := &schemas.RunContext{
		Run:   &schemas.Run{RunID: m.cfg.KeyName, ExpID: m.cfg.ExpID},
		Agent: &schemas.AgentConfig{ID: "issuebench-builder", Model: m.cfg.Model, Gateway: schemas.GatewayPolicy{Provider: m.cfg.Provider}},
	}
	key, err := m.cfg.Gateway.MintKey(ctx, rc)
	if err != nil {
		return nil, fmt.Errorf("mint the builder's key: %w", err)
	}
	m.key = key
	return key, nil
}

// Close revokes the builder's key on a context that outlives a cancelled
// build, so the key never stays usable. Safe to call more than once.
func (m *GatewayModel) Close(ctx context.Context) {
	m.mu.Lock()
	key := m.key
	m.key, m.done = nil, true
	m.mu.Unlock()
	if key == nil || m.cfg.Key != nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeTimeout)
	defer cancel()
	if err := m.cfg.Gateway.RevokeKey(rctx, key); err != nil && m.cfg.Logger != nil {
		m.cfg.Logger.Warn("builder key not revoked; the janitor removes it", "virtual_key_id", key.ID, "error", err.Error())
	}
}

type wireTool struct {
	Type     string   `json:"type"`
	Function ToolSpec `json:"function"`
}

type wireCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []wireCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type wireRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type wireResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []wireCall      `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// Next sends the conversation and returns the model's turn.
func (m *GatewayModel) Next(ctx context.Context, msgs []Message, tools []ToolSpec) (Reply, error) {
	key, err := m.ensureKey(ctx)
	if err != nil {
		return Reply{}, err
	}
	req := wireRequest{Model: m.cfg.Provider + "/" + m.cfg.Model, Temperature: m.cfg.Temperature, MaxTokens: m.cfg.MaxTokens}
	for _, t := range tools {
		req.Tools = append(req.Tools, wireTool{Type: "function", Function: t})
	}
	for _, msg := range msgs {
		w := wireMessage{Role: msg.Role, Content: msg.Content, ToolCallID: msg.ToolCallID}
		for _, c := range msg.ToolCalls {
			wc := wireCall{ID: c.ID, Type: "function"}
			wc.Function.Name, wc.Function.Arguments = c.Name, string(c.Args)
			w.ToolCalls = append(w.ToolCalls, wc)
		}
		req.Messages = append(req.Messages, w)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Reply{}, fmt.Errorf("encode request: %w", err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.cfg.URL, "/")+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return Reply{}, fmt.Errorf("build request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set(virtualKeyHeader, key.Value)
	for _, h := range utils.MaximHeadersFrom(ctx) {
		hr.Header.Set(h[0], h[1])
	}
	resp, err := m.cfg.HTTP.Do(hr)
	if err != nil {
		return Reply{}, fmt.Errorf("call gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return Reply{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return Reply{}, fmt.Errorf("gateway answered %d: %s", resp.StatusCode, tail(string(raw), 400))
	}
	var out wireResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Reply{}, fmt.Errorf("decode response: %w", err)
	}
	var r Reply
	if len(out.Choices) > 0 {
		c := out.Choices[0].Message
		r.Content = contentText(c.Content)
		for _, wc := range c.ToolCalls {
			args := json.RawMessage(wc.Function.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage(`{}`)
			}
			r.Calls = append(r.Calls, ToolCall{ID: wc.ID, Name: wc.Function.Name, Args: args})
		}
	}
	if u := out.Usage; u != nil && m.cfg.Price != nil {
		cached := int64(0)
		if u.PromptTokensDetails != nil {
			cached = u.PromptTokensDetails.CachedTokens
		}
		r.CostUSD, r.Priced = m.cfg.Price(m.cfg.Model, schemas.Tokens{Fresh: max(u.PromptTokens-cached, 0), CacheRead: cached, Out: u.CompletionTokens})
	}
	return r, nil
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// ScriptedModel is a fake Model for tests: it returns its replies in order
// and records every conversation it was shown.
type ScriptedModel struct {
	mu      sync.Mutex
	Replies []Reply
	Seen    [][]Message
}

// Next returns the next scripted reply, or an error when the script is over.
func (s *ScriptedModel) Next(_ context.Context, msgs []Message, _ []ToolSpec) (Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Seen = append(s.Seen, append([]Message(nil), msgs...))
	if len(s.Replies) == 0 {
		return Reply{}, errors.New("scripted model has no more replies")
	}
	r := s.Replies[0]
	s.Replies = s.Replies[1:]
	return r, nil
}

// Call is a helper for scripts: one tool call with JSON args.
func Call(id, name string, args any) ToolCall {
	b, _ := json.Marshal(args)
	return ToolCall{ID: id, Name: name, Args: b}
}
