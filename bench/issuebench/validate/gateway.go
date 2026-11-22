package validate

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
)

const (
	chatCompletionsPath = "/v1/chat/completions"
	virtualKeyHeader    = "x-bf-vk"
	gatewayHTTPTimeout  = 3 * time.Minute
	revokeTimeout       = 15 * time.Second
	maxResponseBody     = 4 << 20
	defaultCheckTokens  = 4096
)

// GatewayModel is the org's model reached through the Bifrost gateway on a
// virtual key of its own: minted on the first call, revoked by Close (the
// same pattern as the judge grader). Every call is priced and a call is
// refused with ErrBudget once the spend reaches BudgetUSD.
type GatewayModel struct {
	Gateway  schemas.Gateway
	URL      string // the gateway's inference base URL
	HTTP     *http.Client
	Provider string
	Model    string
	// JobID names the key (a run id such as r_abc123) so a crashed job's
	// key is found and revoked by the janitor; ExpID binds it to the org.
	JobID string
	ExpID string
	// Price prices one call's usage; nil, or a model not in the snapshot,
	// counts every call at FallbackUSD so the budget still holds.
	Price       func(model string, tok schemas.Tokens) (float64, bool)
	FallbackUSD float64
	BudgetUSD   float64
	MaxTokens   int

	mu    sync.Mutex
	key   *schemas.RunKey
	spent float64
	calls int
}

// Spent is the dollars spent so far and the number of calls.
func (g *GatewayModel) Spent() (float64, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.spent, g.calls
}

// Complete sends one chat to the gateway at temperature 0.
func (g *GatewayModel) Complete(ctx context.Context, msgs []Message) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.BudgetUSD <= 0 || g.spent >= g.BudgetUSD {
		return "", fmt.Errorf("%w: spent $%.4f of $%.4f", ErrBudget, g.spent, g.BudgetUSD)
	}
	if g.Gateway == nil || g.URL == "" || g.Provider == "" || g.Model == "" {
		return "", errors.New("gateway model is not configured")
	}
	if g.key == nil {
		rc := &schemas.RunContext{
			Run:   &schemas.Run{RunID: g.JobID, ExpID: g.ExpID},
			Agent: &schemas.AgentConfig{ID: "issuebench-validate", Model: g.Model, Gateway: schemas.GatewayPolicy{Provider: g.Provider}},
		}
		key, err := g.Gateway.MintKey(ctx, rc)
		if err != nil {
			return "", fmt.Errorf("mint the validation key: %w", err)
		}
		g.key = key
	}
	hc := g.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: gatewayHTTPTimeout}
	}
	maxTok := g.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultCheckTokens
	}
	body, err := json.Marshal(map[string]any{
		"model":       g.Provider + "/" + g.Model,
		"messages":    msgs,
		"temperature": 0,
		"max_tokens":  maxTok,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.URL, "/")+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(virtualKeyHeader, g.key.Value)
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("call gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	g.calls++
	if resp.StatusCode/100 != 2 {
		g.spent += g.FallbackUSD
		s := string(raw)
		if len(s) > 300 {
			s = s[:300]
		}
		return "", fmt.Errorf("gateway answered %d: %q", resp.StatusCode, s)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		g.spent += g.FallbackUSD
		return "", fmt.Errorf("decode response: %w", err)
	}
	cost, priced := 0.0, false
	if out.Usage != nil && g.Price != nil {
		cost, priced = g.Price(g.Model, schemas.Tokens{Fresh: out.Usage.PromptTokens, Out: out.Usage.CompletionTokens})
	}
	if !priced {
		cost = g.FallbackUSD
	}
	g.spent += cost
	if len(out.Choices) == 0 {
		return "", errors.New("gateway response has no choices")
	}
	return out.Choices[0].Message.Content, nil
}

// Close revokes the key on a context that outlives a cancelled job.
func (g *GatewayModel) Close(ctx context.Context) error {
	g.mu.Lock()
	key := g.key
	g.key = nil
	g.mu.Unlock()
	if key == nil {
		return nil
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeTimeout)
	defer cancel()
	return g.Gateway.RevokeKey(rctx, key)
}
