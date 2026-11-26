package investigator

import (
	"context"
	"errors"
)

// Message is one chat message sent to the model. Content is already redacted.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// Request is one model call. Purpose names the caller for the ledger
// ("findings", "trace_draft", "guard_clarity", "category", "replay_judge",
// "replay_step"). Schema, when set, is a JSON schema the reply must match;
// a reply that does not parse is asked again up to Retries times.
type Request struct {
	Purpose   string    `json:"purpose"`
	Messages  []Message `json:"messages"`
	Schema    string    `json:"schema,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	// Raw, when set, is sent as the whole chat completions body instead of
	// Messages (shadow replay sends a recorded request unchanged). It is
	// still redacted and still priced before the call.
	Raw []byte `json:"-"`
}

// Reply is the model's answer and what it cost.
type Reply struct {
	Text      string  `json:"text"`
	Raw       []byte  `json:"-"`      // the whole response body
	Model     string  `json:"model"`  // provider/model asked for
	Served    string  `json:"served"` // the model the gateway says answered
	CostUSD   float64 `json:"cost_usd"`
	Tokens    int     `json:"tokens"`
	LatencyMS int64   `json:"latency_ms"`
	Attempts  int     `json:"attempts"`
	PromptSHA string  `json:"prompt_sha"` // sha256 of the redacted messages
}

// Caller makes budgeted calls. Runner implements it; FakeModel does in tests.
type Caller interface {
	Call(ctx context.Context, req Request) (Reply, error)
	Spent() float64
	Budget() float64
}

// ErrBudget is returned when a call could pass the job's spending cap.
var ErrBudget = errors.New("the spending cap for this job is reached")

// FakeModel answers from a script; for tests only. Each Call pops the next
// reply; every reply costs CostUSD; a call that would pass Cap returns
// ErrBudget. Seen keeps what the code under test sent, for redaction asserts.
type FakeModel struct {
	Replies []string
	CostUSD float64
	Cap     float64
	Seen    []Request
	spent   float64
}

func (f *FakeModel) Call(ctx context.Context, req Request) (Reply, error) {
	if f.spent+f.CostUSD > f.Cap {
		return Reply{}, ErrBudget
	}
	if len(f.Replies) == 0 {
		return Reply{}, errors.New("fake model has no reply left")
	}
	f.Seen = append(f.Seen, req)
	text := f.Replies[0]
	f.Replies = f.Replies[1:]
	f.spent += f.CostUSD
	return Reply{Text: text, Model: "fake/fake", Served: "fake/fake", CostUSD: f.CostUSD, Attempts: 1}, nil
}

func (f *FakeModel) Spent() float64  { return f.spent }
func (f *FakeModel) Budget() float64 { return f.Cap }
