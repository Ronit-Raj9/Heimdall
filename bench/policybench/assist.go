package policybench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	chatCompletionsPath = "/v1/chat/completions"
	virtualKeyHeader    = "x-bf-vk"
	maxResponseBody     = 2 << 20
	defaultMaxTokens    = 4000
	revokeTimeout       = 30 * time.Second
	// Retries is how many times a clause is asked again after a reply that
	// is not valid JSON or fails the checks; every attempt is charged.
	Retries = 2
	// AgentID names the generator's key on the gateway.
	AgentID = "policy-generator"
)

// ErrBudget is returned when a call could pass the budget.
var ErrBudget = errors.New("generation budget reached")

// GenConfig wires the generator to the gateway, one pinned model and a
// hard budget.
type GenConfig struct {
	Gateway  schemas.Gateway
	URL      string // the gateway's inference base URL
	HTTP     *http.Client
	Provider string
	Model    string
	// Price prices usage from the pinned price snapshot; a model that is
	// not priced is refused, since the budget could not hold.
	Price     func(model string, tok schemas.Tokens) (float64, bool)
	BudgetUSD float64
	ExpID     string // the experiment the key is minted under (hosted: per org)
	MaxTokens int
	Logger    schemas.Logger
}

// Generator makes pinned, budgeted model calls. The key is minted with an
// equal_budget fairness rule so the gateway itself caps the key at the
// budget too, not only this process.
type Generator struct {
	cfg   GenConfig
	mu    sync.Mutex
	key   *schemas.RunKey
	spent float64
	calls int
}

var modelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}$`)

// NewGenerator checks cfg; it makes no call.
func NewGenerator(cfg GenConfig) (*Generator, error) {
	switch {
	case cfg.Gateway == nil || cfg.URL == "":
		return nil, errors.New("generation is not wired to the gateway (bench.yaml gateway.url)")
	case !modelNameRe.MatchString(cfg.Provider) || strings.Contains(cfg.Provider, "/"):
		return nil, fmt.Errorf("provider %q is not a valid name", cfg.Provider)
	case !modelNameRe.MatchString(cfg.Model) || strings.Contains(cfg.Model, ".."):
		return nil, fmt.Errorf("model %q is not a valid name", cfg.Model)
	case cfg.BudgetUSD <= 0:
		return nil, errors.New("generation needs a budget above $0")
	case cfg.Price == nil:
		return nil, errors.New("no price snapshot: the generation budget can not be enforced")
	}
	if _, ok := cfg.Price(cfg.Model, schemas.Tokens{Fresh: 1000, Out: 1000}); !ok {
		return nil, fmt.Errorf("model %s is not in the price snapshot, so the budget can not be enforced; pick a priced model", cfg.Model)
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 3 * time.Minute}
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokens
	}
	if cfg.ExpID == "" {
		cfg.ExpID = AgentID
	}
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	return &Generator{cfg: cfg}, nil
}

// ModelRef is provider/model.
func (g *Generator) ModelRef() string { return g.cfg.Provider + "/" + g.cfg.Model }

// Spent is the priced spend and the number of calls so far.
func (g *Generator) Spent() (float64, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.spent, g.calls
}

// Budget is the cap.
func (g *Generator) Budget() float64 { return g.cfg.BudgetUSD }

// Open mints the generator's own virtual key, capped at the budget.
func (g *Generator) Open(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.key != nil {
		return nil
	}
	runID := "r_pg" + strings.TrimPrefix(utils.NewID("x", 10), "x_")
	key, err := g.cfg.Gateway.MintKey(ctx, &schemas.RunContext{
		Run: &schemas.Run{RunID: runID, ExpID: g.cfg.ExpID},
		Agent: &schemas.AgentConfig{ID: AgentID, Model: g.cfg.Model, Gateway: schemas.GatewayPolicy{Provider: g.cfg.Provider},
			Budgets: schemas.Budgets{USD: g.cfg.BudgetUSD}},
		Experiment: &schemas.Experiment{ID: g.cfg.ExpID, Fairness: schemas.FairnessEqualBudget},
	})
	if err != nil {
		return fmt.Errorf("mint the generator key: %w", err)
	}
	g.key = key
	return nil
}

// Close revokes the key on a context that outlives a cancelled job.
func (g *Generator) Close(ctx context.Context) {
	g.mu.Lock()
	key := g.key
	g.key = nil
	g.mu.Unlock()
	if key == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeTimeout)
	defer cancel()
	if err := g.cfg.Gateway.RevokeKey(rctx, key); err != nil {
		g.cfg.Logger.Warn("generator key not revoked; the janitor removes it", "virtual_key_id", key.ID, "error", err.Error())
	}
}

// Validator is a reply's decoded form.
type Validator interface{ Validate() error }

// Ask sends system and prompt, decodes the strict JSON reply into out and
// validates it, asking again with the reason up to Retries times. It
// returns the cost of every attempt. A call that could pass the budget is
// not made (ErrBudget).
func (g *Generator) Ask(ctx context.Context, system, prompt string, out Validator) (float64, int, error) {
	if err := g.Open(ctx); err != nil {
		return 0, 0, err
	}
	msgs := []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: prompt}}
	var cost float64
	var lastErr error
	for attempt := 0; attempt <= Retries; attempt++ {
		if err := g.reserve(msgs); err != nil {
			return cost, attempt, err
		}
		reply, usd, err := g.chat(ctx, msgs)
		cost += usd
		if err != nil {
			return cost, attempt + 1, err
		}
		if err := decodeStrict(reply, out); err != nil {
			lastErr = err
		} else if err := out.Validate(); err != nil {
			lastErr = err
		} else {
			return cost, attempt + 1, nil
		}
		msgs = append(msgs, chatMessage{Role: "assistant", Content: reply},
			chatMessage{Role: "user", Content: "Your reply was rejected: " + lastErr.Error() + ". Reply again with the JSON object only, following every rule."})
	}
	return cost, Retries + 1, fmt.Errorf("no valid reply after %d attempts: %w", Retries+1, lastErr)
}

func (g *Generator) worst(msgs []chatMessage) float64 {
	chars := 0
	for _, m := range msgs {
		chars += len(m.Content)
	}
	w, _ := g.cfg.Price(g.cfg.Model, schemas.Tokens{Fresh: int64(chars/3 + 64), Out: int64(g.cfg.MaxTokens)})
	return w
}

func (g *Generator) reserve(msgs []chatMessage) error {
	worst := g.worst(msgs)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.spent+worst > g.cfg.BudgetUSD {
		return fmt.Errorf("%w: $%.4f spent of $%.2f, and the next call could cost up to $%.4f", ErrBudget, g.spent, g.cfg.BudgetUSD, worst)
	}
	return nil
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
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

// chat sends one request; a reply without usage is charged at the worst
// case so the budget still holds.
func (g *Generator) chat(ctx context.Context, msgs []chatMessage) (string, float64, error) {
	body, err := json.Marshal(chatRequest{Model: g.ModelRef(), Messages: msgs, Temperature: 0.2, MaxTokens: g.cfg.MaxTokens})
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.cfg.URL, "/")+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(virtualKeyHeader, g.key.Value)
	resp, err := g.cfg.HTTP.Do(req)
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	if err != nil {
		return "", 0, fmt.Errorf("call the gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return "", 0, fmt.Errorf("read the response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", 0, fmt.Errorf("gateway answered %d: %s", resp.StatusCode, clip(string(raw), 300))
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, fmt.Errorf("decode the response: %w", err)
	}
	if !sameModel(out.Model, g.cfg.Model) {
		return "", 0, fmt.Errorf("gateway served model %s, generation is pinned to %s", out.Model, g.cfg.Model)
	}
	var tok schemas.Tokens
	if u := out.Usage; u != nil {
		cached := int64(0)
		if u.PromptTokensDetails != nil {
			cached = u.PromptTokensDetails.CachedTokens
		}
		tok = schemas.Tokens{Fresh: max(u.PromptTokens-cached, 0), CacheRead: cached, Out: u.CompletionTokens}
	} else {
		chars := 0
		for _, m := range msgs {
			chars += len(m.Content)
		}
		tok = schemas.Tokens{Fresh: int64(chars/3 + 64), Out: int64(g.cfg.MaxTokens)}
	}
	usd, _ := g.cfg.Price(g.cfg.Model, tok)
	g.mu.Lock()
	g.spent += usd
	g.mu.Unlock()
	reply := ""
	if len(out.Choices) > 0 {
		reply = contentText(out.Choices[0].Message.Content)
	}
	return reply, usd, nil
}

// decodeStrict reads one JSON object (a single ```json fence allowed),
// refusing unknown keys and trailing text.
func decodeStrict(reply string, out any) error {
	s := strings.TrimSpace(reply)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(s)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("not the JSON object asked for: %v", err)
	}
	if dec.More() {
		return errors.New("text after the JSON object")
	}
	return nil
}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
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

var dateSuffixRe = regexp.MustCompile(`-\d{4}-?\d{2}-?\d{2}$`)

// sameModel is true when the served id is the pinned one, maybe with a
// provider prefix or a release date suffix.
func sameModel(served, pinned string) bool {
	if served == "" {
		return true
	}
	if i := strings.LastIndex(served, "/"); i >= 0 {
		served = served[i+1:]
	}
	if served == pinned {
		return true
	}
	return dateSuffixRe.ReplaceAllString(served, "") == dateSuffixRe.ReplaceAllString(pinned, "")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
