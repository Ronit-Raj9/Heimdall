// Package investigator is the one budgeted model caller of wave 4. Every
// call goes through the gateway on a virtual key minted for one job, is
// redacted before it leaves the server, is priced from the price snapshot,
// reserves its worst case before it is made and stops at the job's cap.
package investigator

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

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceimport"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	chatCompletionsPath = "/v1/chat/completions"
	virtualKeyHeader    = "x-bf-vk"
	maxResponseBody     = 1 << 20
	defaultMaxTokens    = 1500
	defaultMaxCalls     = 200
	defaultHTTPTimeout  = 2 * time.Minute
	revokeTimeout       = 30 * time.Second
	// Retries is how many times a reply that does not match the request's
	// schema is asked again. Each retry is a new reserved call.
	Retries = 2
	// AgentID names the job's key on the gateway.
	AgentID = "investigator"
)

// Config wires a runner to the gateway, one pinned model and a cap.
type Config struct {
	Gateway   schemas.Gateway
	URL       string // the gateway's inference base URL
	HTTP      *http.Client
	Provider  string
	Model     string
	Price     func(model string, tok schemas.Tokens) (float64, bool)
	BudgetUSD float64 // hard cap; must be > 0
	MaxCalls  int     // 0 means 200
	MaxTokens int     // per reply; 0 means 1500
	JobID     string  // names the key: AgentID "investigator", RunID "r_iv_" + JobID
	OrgID     string  // hosted: bound with BindExperimentOrg(JobID, OrgID) by the caller
	Logger    schemas.Logger
}

// Runner makes budgeted calls for one job. It implements Caller.
type Runner struct {
	cfg    Config
	mu     sync.Mutex
	key    *schemas.RunKey
	closed bool // Close ran: no new key is minted
	spent  float64
	rows   []LedgerRow
}

var _ Caller = (*Runner)(nil)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}$`)

// New checks cfg and returns a runner. It makes no call.
func New(cfg Config) (*Runner, error) {
	switch {
	case cfg.Gateway == nil || cfg.URL == "":
		return nil, errors.New("the model caller is not wired to the gateway")
	case !nameRe.MatchString(cfg.Provider) || strings.Contains(cfg.Provider, "/"):
		return nil, fmt.Errorf("provider %q is not a valid name", cfg.Provider)
	case !nameRe.MatchString(cfg.Model) || strings.Contains(cfg.Model, ".."):
		return nil, fmt.Errorf("model %q is not a valid name", cfg.Model)
	case cfg.BudgetUSD <= 0:
		return nil, errors.New("the job needs a spending cap above $0")
	case cfg.Price == nil:
		return nil, errors.New("no price snapshot: the spending cap cannot hold")
	}
	if _, ok := cfg.Price(cfg.Model, schemas.Tokens{Fresh: 1000, Out: 1000}); !ok {
		return nil, fmt.Errorf("model %s has no price on file, so the spending cap cannot hold", cfg.Model)
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokens
	}
	if cfg.MaxCalls <= 0 {
		cfg.MaxCalls = defaultMaxCalls
	}
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	return &Runner{cfg: cfg}, nil
}

// ModelRef is provider/model.
func (r *Runner) ModelRef() string { return r.cfg.Provider + "/" + r.cfg.Model }

// Spent is the priced spend so far.
func (r *Runner) Spent() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.spent
}

// Budget is the cap.
func (r *Runner) Budget() float64 { return r.cfg.BudgetUSD }

// Calls is one row per gateway call, in order.
func (r *Runner) Calls() []LedgerRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]LedgerRow(nil), r.rows...)
}

// Open mints the job's virtual key. A second Open is a no-op.
func (r *Runner) Open(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.key != nil {
		return nil
	}
	if r.closed {
		return errors.New("the job's model key is closed")
	}
	expID := r.cfg.JobID
	if expID == "" {
		expID = AgentID
	}
	key, err := r.cfg.Gateway.MintKey(ctx, &schemas.RunContext{
		Run:   &schemas.Run{RunID: "r_iv_" + r.cfg.JobID, ExpID: expID},
		Agent: &schemas.AgentConfig{ID: AgentID, Model: r.cfg.Model, Gateway: schemas.GatewayPolicy{Provider: r.cfg.Provider}},
	})
	if err != nil {
		return fmt.Errorf("mint the job's model key: %w", err)
	}
	r.key = key
	return nil
}

// Close revokes the key on a context that outlives a cancelled job. It is
// safe to call twice: only the first call revokes.
func (r *Runner) Close(ctx context.Context) error {
	r.mu.Lock()
	key := r.key
	r.key = nil
	r.closed = true
	r.mu.Unlock()
	if key == nil {
		return nil
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeTimeout)
	defer cancel()
	if err := r.cfg.Gateway.RevokeKey(rctx, key); err != nil {
		r.cfg.Logger.Warn("investigator key not revoked; the janitor removes it", "virtual_key_id", key.ID, "error", err.Error())
		return err
	}
	return nil
}

// Call redacts the request, reserves its worst case, sends it and prices
// the real usage. With a Schema, a reply that does not match is asked again
// up to Retries times with the reason appended.
func (r *Runner) Call(ctx context.Context, req Request) (Reply, error) {
	red := traceimport.NewRedactor()
	msgs := make([]Message, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = Message{Role: m.Role, Content: red.Text(m.Content)}
	}
	var raw []byte
	if len(req.Raw) > 0 {
		b, err := r.pinRaw(red.JSON(json.RawMessage(req.Raw)))
		if err != nil {
			return Reply{}, err
		}
		raw = b
	}
	var sch *jsonschema.Schema
	if req.Schema != "" && raw == nil {
		s, err := compileSchema(req.Schema)
		if err != nil {
			return Reply{}, err
		}
		sch = s
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = r.cfg.MaxTokens
	}
	out := Reply{Model: r.ModelRef()}
	if err := r.Open(ctx); err != nil { // a no-op once the key is minted
		return out, err
	}
	var lastErr error
	for attempt := 0; attempt <= Retries; attempt++ {
		body := raw
		if body == nil {
			b, err := json.Marshal(chatRequest{Model: r.ModelRef(), Messages: msgs, Temperature: 0, MaxTokens: maxTok})
			if err != nil {
				return out, err
			}
			body = b
		}
		promptSHA := shaHex(body)
		out.PromptSHA = promptSHA
		if err := r.reserve(len(body), maxTok); err != nil {
			return out, err
		}
		start := time.Now()
		text, respRaw, served, usd, tokens, err := r.chat(ctx, body, maxTok)
		out.LatencyMS += time.Since(start).Milliseconds()
		out.Attempts++
		out.CostUSD += usd
		out.Tokens += tokens
		if served != "" {
			out.Served = served
		}
		row := LedgerRow{Purpose: req.Purpose, Model: r.ModelRef(), Served: served, PromptSHA: promptSHA, CostUSD: usd, Attempts: attempt + 1, At: time.Now().UTC().Format(time.RFC3339)}
		if err != nil {
			row.Error = err.Error()
			r.addRow(row)
			return out, err
		}
		out.Text, out.Raw = text, respRaw
		if sch == nil {
			r.addRow(row)
			return out, nil
		}
		verr := checkSchema(sch, text)
		if verr == nil {
			r.addRow(row)
			return out, nil
		}
		row.Error = verr.Error()
		r.addRow(row)
		lastErr = verr
		msgs = append(msgs, Message{Role: "assistant", Content: red.Text(text)},
			Message{Role: "user", Content: "Your reply was rejected: " + verr.Error() + ". Reply again with the JSON object only."})
	}
	return out, fmt.Errorf("no valid reply after %d attempts: %w", Retries+1, lastErr)
}

func (r *Runner) addRow(row LedgerRow) {
	r.mu.Lock()
	r.rows = append(r.rows, row)
	r.mu.Unlock()
}

// reserve refuses a call past MaxCalls or whose worst case (the prompt plus
// a full reply) could pass the cap.
func (r *Runner) reserve(chars, maxTok int) error {
	worst, _ := r.cfg.Price(r.cfg.Model, schemas.Tokens{Fresh: int64(chars/3 + 64), Out: int64(maxTok)})
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rows) >= r.cfg.MaxCalls {
		return fmt.Errorf("%w: the job made its %d calls", ErrBudget, r.cfg.MaxCalls)
	}
	if r.spent+worst > r.cfg.BudgetUSD {
		return fmt.Errorf("%w: $%.4f spent of $%.2f, and the next call could cost up to $%.4f", ErrBudget, r.spent, r.cfg.BudgetUSD, worst)
	}
	return nil
}

// pinRaw sets the body's model to the pinned one and stream to false;
// nothing else changes.
func (r *Runner) pinRaw(raw json.RawMessage) ([]byte, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("the recorded request is not a JSON object: %w", err)
	}
	m, _ := json.Marshal(r.ModelRef())
	body["model"] = m
	body["stream"] = json.RawMessage("false")
	return json.Marshal(body)
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
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

// chat sends one body and returns the reply text, the whole response, the
// served model, the priced cost and the tokens. A reply without usage is
// charged at the worst case, so the cap still holds.
func (r *Runner) chat(ctx context.Context, body []byte, maxTok int) (string, []byte, string, float64, int, error) {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.cfg.URL, "/")+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return "", nil, "", 0, 0, err
	}
	r.mu.Lock()
	key := r.key
	r.mu.Unlock()
	if key == nil {
		return "", nil, "", 0, 0, errors.New("the job's model key is closed")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set(virtualKeyHeader, key.Value)
	resp, err := r.cfg.HTTP.Do(hreq)
	if err != nil {
		// The call may have reached the provider: charge the worst case.
		usd, _ := r.cfg.Price(r.cfg.Model, schemas.Tokens{Fresh: int64(len(body)/3 + 64), Out: int64(maxTok)})
		r.addSpend(usd)
		return "", nil, "", usd, 0, fmt.Errorf("call gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return "", nil, "", 0, 0, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", raw, "", 0, 0, fmt.Errorf("gateway answered %d: %s", resp.StatusCode, clip(string(raw), 300))
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", raw, "", 0, 0, fmt.Errorf("decode response: %w", err)
	}
	// A served model that differs from the asked one is recorded, not
	// refused: callers compare it where it matters.
	served := out.Model
	var tok schemas.Tokens
	if u := out.Usage; u != nil {
		cached := int64(0)
		if u.PromptTokensDetails != nil {
			cached = u.PromptTokensDetails.CachedTokens
		}
		tok = schemas.Tokens{Fresh: max(u.PromptTokens-cached, 0), CacheRead: cached, Out: u.CompletionTokens}
	} else {
		tok = schemas.Tokens{Fresh: int64(len(body)/3 + 64), Out: int64(maxTok)}
	}
	usd, _ := r.cfg.Price(r.cfg.Model, tok)
	r.addSpend(usd)
	text := ""
	if len(out.Choices) > 0 {
		text = contentText(out.Choices[0].Message.Content)
	}
	return text, raw, served, usd, int(tok.Fresh + tok.CacheRead + tok.Out), nil
}

func (r *Runner) addSpend(usd float64) {
	r.mu.Lock()
	r.spent += usd
	r.mu.Unlock()
}

func compileSchema(s string) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(s))
	if err != nil {
		return nil, fmt.Errorf("the reply schema is not valid JSON: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("reply.schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("reply.schema.json")
}

// checkSchema parses the reply (one ```json fence allowed) and validates it.
func checkSchema(sch *jsonschema.Schema, text string) error {
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(StripFence(text)))
	if err != nil {
		return fmt.Errorf("the reply is not valid JSON: %v", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("the reply does not match the schema: %s", clip(err.Error(), 300))
	}
	return nil
}

// StripFence removes surrounding spaces and one ```json fence.
func StripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimPrefix(s, "json")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
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

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
