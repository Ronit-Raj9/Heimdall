// Package authoring is the assist engine (INFRA_I3 section 4.3 and 4.4): it
// drafts the parts of a task a person would otherwise write by hand, such
// as rubric criteria, one wrong reply per criterion, near-miss wrong
// answers and a per-task exploit. Every call goes through the gateway on its
// own virtual key for one pinned model, is priced, stops at a hard budget,
// and must answer strict JSON that passes the op's schema check. Source
// text enters the prompt fenced as data; the op has no tools.
//
// The assist never decides that a task is ready. What it writes is labelled
// on the task (provenance.generated) and the task is pending review: it
// counts nowhere until a person accepts it, and the readiness check (sealed
// control runs in the sandbox) proves each wrong answer fails.
package authoring

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/modelid"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	chatCompletionsPath = "/v1/chat/completions"
	virtualKeyHeader    = "x-bf-vk"
	maxResponseBody     = 1 << 20
	defaultMaxTokens    = 1500
	defaultHTTPTimeout  = 2 * time.Minute
	revokeTimeout       = 30 * time.Second
	// Retries is how many times an op is asked again after a reply that is
	// not valid JSON or fails its schema check. A reply still invalid after
	// that is reported, never replaced by a guess.
	Retries = 2
	// AgentID names the assist's key on the gateway, as the judge's does.
	AgentID = "authoring"
)

// ErrBudget is returned when a call could pass the budget.
var ErrBudget = errors.New("assist budget reached")

// Config wires the assistant to the gateway, one pinned model and a budget.
type Config struct {
	Gateway schemas.Gateway
	URL     string // the gateway's inference base URL
	HTTP    *http.Client

	Provider string // gateway provider, e.g. openai
	Model    string // model id, without the provider

	// Price prices a call's usage from the pinned price snapshot. A model
	// that can not be priced is refused: the budget would not hold.
	Price func(model string, tok schemas.Tokens) (float64, bool)
	// BudgetUSD is the hard cap for every call this assistant makes.
	BudgetUSD float64
	// ExpID names the experiment the key is minted under; a hosted server
	// binds it to the org so the gateway uses the org's own provider key.
	ExpID string
	// MaxTokens caps each reply; 0 uses 1500.
	MaxTokens int
	Logger    schemas.Logger
}

// Call is one op's outcome: what it cost and which prompt and model wrote
// it.
type Call struct {
	Op        Op      `json:"op"`
	Model     string  `json:"model"` // provider/model
	Served    string  `json:"served,omitempty"`
	PromptSHA string  `json:"prompt_sha"`
	Attempts  int     `json:"attempts"`
	CostUSD   float64 `json:"cost_usd"`
	At        string  `json:"at"`
}

// Field labels one field this call wrote.
func (c *Call) Field(name string) schemas.GeneratedField {
	return schemas.GeneratedField{Field: name, Op: string(c.Op), Model: c.Model, PromptSHA: c.PromptSHA, CostUSD: c.CostUSD, At: c.At}
}

// Assistant makes pinned, budgeted, logged model calls for drafting. Open
// mints its key, Close revokes it; Run is safe for one goroutine at a time.
type Assistant struct {
	cfg   Config
	mu    sync.Mutex
	key   *schemas.RunKey
	spent float64
	calls int
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}$`)

// New checks cfg and returns an assistant. It makes no call.
func New(cfg Config) (*Assistant, error) {
	switch {
	case cfg.Gateway == nil || cfg.URL == "":
		return nil, errors.New("the assist is not wired to the gateway")
	case !nameRe.MatchString(cfg.Provider) || strings.Contains(cfg.Provider, "/"):
		return nil, fmt.Errorf("provider %q is not a valid name", cfg.Provider)
	case !nameRe.MatchString(cfg.Model) || strings.Contains(cfg.Model, ".."):
		return nil, fmt.Errorf("model %q is not a valid name", cfg.Model)
	case cfg.BudgetUSD <= 0:
		return nil, errors.New("the assist needs a budget above $0")
	case cfg.Price == nil:
		return nil, errors.New("no price snapshot: the assist budget can not be enforced")
	}
	if _, ok := cfg.Price(cfg.Model, schemas.Tokens{Fresh: 1000, Out: 1000}); !ok {
		return nil, fmt.Errorf("model %s is not in the price snapshot, so the budget can not be enforced; pick a priced model", cfg.Model)
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: defaultHTTPTimeout}
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
	return &Assistant{cfg: cfg}, nil
}

// ModelRef is provider/model.
func (a *Assistant) ModelRef() string { return a.cfg.Provider + "/" + a.cfg.Model }

// Spent is the priced spend so far and the number of calls.
func (a *Assistant) Spent() (float64, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.spent, a.calls
}

// Budget is the cap.
func (a *Assistant) Budget() float64 { return a.cfg.BudgetUSD }

// Open mints the assistant's virtual key.
func (a *Assistant) Open(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.key != nil {
		return nil
	}
	runID := "r_au" + strings.TrimPrefix(utils.NewID("x", 10), "x_")
	key, err := a.cfg.Gateway.MintKey(ctx, &schemas.RunContext{
		Run:   &schemas.Run{RunID: runID, ExpID: a.cfg.ExpID},
		Agent: &schemas.AgentConfig{ID: AgentID, Model: a.cfg.Model, Gateway: schemas.GatewayPolicy{Provider: a.cfg.Provider}},
	})
	if err != nil {
		return fmt.Errorf("mint the assist key: %w", err)
	}
	a.key = key
	return nil
}

// Close revokes the key on a context that outlives a cancelled job.
func (a *Assistant) Close(ctx context.Context) {
	a.mu.Lock()
	key := a.key
	a.key = nil
	a.mu.Unlock()
	if key == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeTimeout)
	defer cancel()
	if err := a.cfg.Gateway.RevokeKey(rctx, key); err != nil {
		a.cfg.Logger.Warn("assist key not revoked; the janitor removes it", "virtual_key_id", key.ID, "error", err.Error())
	}
}

// Validator is an op's output: Validate checks it against the op's rules.
type Validator interface {
	Validate() error
}

// Run asks the pinned model to do op on in and decodes its JSON reply into
// out, which must then pass out.Validate. An invalid reply is asked again
// up to Retries times with the reason; every attempt is charged. A call
// that could pass the budget is not made (ErrBudget).
func (a *Assistant) Run(ctx context.Context, op Op, in any, out Validator) (*Call, error) {
	spec, ok := ops[op]
	if !ok {
		return nil, fmt.Errorf("unknown assist op %q", op)
	}
	if a.key == nil {
		if err := a.Open(ctx); err != nil {
			return nil, err
		}
	}
	prompt, err := renderPrompt(spec.prompt, in)
	if err != nil {
		return nil, err
	}
	call := &Call{Op: op, Model: a.ModelRef(), PromptSHA: spec.sha, At: time.Now().UTC().Format(time.RFC3339)}
	msgs := []chatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: prompt}}
	var lastErr error
	for attempt := 0; attempt <= Retries; attempt++ {
		if err := a.reserve(msgs); err != nil {
			return call, err
		}
		reply, served, usd, err := a.chat(ctx, msgs)
		call.Attempts++
		call.CostUSD += usd
		if served != "" {
			call.Served = served
		}
		if err != nil {
			return call, err
		}
		if err := decodeStrict(reply, out); err != nil {
			lastErr = err
		} else if err := out.Validate(); err != nil {
			lastErr = err
		} else {
			return call, nil
		}
		msgs = append(msgs, chatMessage{Role: "assistant", Content: reply},
			chatMessage{Role: "user", Content: "Your reply was rejected: " + lastErr.Error() + ". Reply again with the JSON object only, following every rule."})
	}
	return call, fmt.Errorf("%s: no valid reply after %d attempts: %w", op, Retries+1, lastErr)
}

// reserve refuses a call whose worst case (the prompt plus a full reply)
// would pass the budget.
func (a *Assistant) reserve(msgs []chatMessage) error {
	chars := 0
	for _, m := range msgs {
		chars += len(m.Content)
	}
	worst, _ := a.cfg.Price(a.cfg.Model, schemas.Tokens{Fresh: int64(chars/3 + 64), Out: int64(a.cfg.MaxTokens)})
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.spent+worst > a.cfg.BudgetUSD {
		return fmt.Errorf("%w: $%.4f spent of $%.2f, and the next call could cost up to $%.4f", ErrBudget, a.spent, a.cfg.BudgetUSD, worst)
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
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// chat sends one request and returns the reply text, the served model and
// the priced cost. A reply without usage is charged at the worst case, so
// the budget still holds.
func (a *Assistant) chat(ctx context.Context, msgs []chatMessage) (string, string, float64, error) {
	body, err := json.Marshal(chatRequest{Model: a.ModelRef(), Messages: msgs, Temperature: 0, MaxTokens: a.cfg.MaxTokens})
	if err != nil {
		return "", "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.URL, "/")+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(virtualKeyHeader, a.key.Value)
	resp, err := a.cfg.HTTP.Do(req)
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	if err != nil {
		return "", "", 0, fmt.Errorf("call gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return "", "", 0, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", "", 0, fmt.Errorf("gateway answered %d: %s", resp.StatusCode, clip(string(raw), 300))
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", 0, fmt.Errorf("decode response: %w", err)
	}
	if !sameModel(out.Model, a.cfg.Model) {
		return "", out.Model, 0, fmt.Errorf("gateway served model %s, the assist is pinned to %s", out.Model, a.cfg.Model)
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
		tok = schemas.Tokens{Fresh: int64(chars/3 + 64), Out: int64(a.cfg.MaxTokens)}
	}
	usd, _ := a.cfg.Price(a.cfg.Model, tok)
	a.mu.Lock()
	a.spent += usd
	a.mu.Unlock()
	reply := ""
	if len(out.Choices) > 0 {
		reply = contentText(out.Choices[0].Message.Content)
	}
	return reply, out.Model, usd, nil
}

// decodeStrict reads reply as one JSON object into out: surrounding spaces
// and a single ```json fence are allowed, unknown keys and trailing text
// are not.
func decodeStrict(reply string, out any) error {
	s := strings.TrimSpace(reply)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimPrefix(s, "json")
		var ok bool
		if s, ok = strings.CutSuffix(strings.TrimSpace(s), "```"); !ok {
			return errors.New("the reply opens a code fence and never closes it")
		}
		s = strings.TrimSpace(s)
	}
	if !strings.HasPrefix(s, "{") {
		return errors.New("the reply is not a JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("the reply is not valid JSON for this op: %v", err)
	}
	if dec.More() {
		return errors.New("the reply has text after the JSON object")
	}
	return nil
}

func contentText(raw json.RawMessage) string {
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

// sameModel is modelid.SameModel, the one "same model" rule: an empty served
// model is accepted, a provider prefix is ignored, a dated snapshot of the
// pinned alias matches.
func sameModel(served, pinned string) bool {
	return modelid.SameModel(pinned, served)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
