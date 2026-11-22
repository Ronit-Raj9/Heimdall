package connectlocal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// PullPrefix is where Heimdall serves pull-mode turns (Y11, docs/pull-agents.md).
const PullPrefix = "/api/v1/pull"

// PullTurn is one unit of work for a pull agent.
type PullTurn struct {
	ID       string      `json:"id"`
	Agent    string      `json:"agent"`
	RunID    string      `json:"run_id"`
	Deadline time.Time   `json:"deadline"`
	Payload  PullPayload `json:"payload"`
}

// PullPayload is the part of a turn the agent reads.
type PullPayload struct {
	Kind         string    `json:"kind"` // task | conversation | tool_results
	TaskID       string    `json:"task_id"`
	SessionID    string    `json:"session_id"`
	SessionIndex int       `json:"session_index"`
	Output       string    `json:"output"`
	Instruction  string    `json:"instruction"`
	Message      *string   `json:"message"`
	Turn         int       `json:"turn"`
	Messages     []Message `json:"-"`
	RawMessages  []any     `json:"messages"`
}

// Input is the text the agent answers: the customer's message in a chat,
// else the task instruction.
func (p PullPayload) Input() string {
	if p.Message != nil && *p.Message != "" {
		return *p.Message
	}
	return p.Instruction
}

// ChatMessages returns the chat so far as role and text pairs; tool call
// messages become their text (a relayed agent answers in text only).
func (p PullPayload) ChatMessages() []Message {
	var out []Message
	for _, raw := range p.RawMessages {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		var content string
		switch c := m["content"].(type) {
		case string:
			content = c
		case []any:
			var parts []string
			for _, part := range c {
				if pm, ok := part.(map[string]any); ok {
					if t, ok := pm["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
			content = strings.Join(parts, "\n")
		}
		if role == "" || (content == "" && role != "assistant") {
			continue
		}
		if role == "tool" {
			role = "user"
		}
		out = append(out, Message{Role: role, Content: content})
	}
	if len(out) == 0 && p.Input() != "" {
		out = []Message{{Role: "user", Content: p.Input()}}
	}
	return out
}

// PullError is a refused pull call.
type PullError struct {
	Status  int
	Message string
}

func (e *PullError) Error() string {
	return fmt.Sprintf("Heimdall answered %d: %s", e.Status, e.Message)
}

// Fatal reports a refusal that retrying can not fix (bad token, no such agent).
func (e *PullError) Fatal() bool { return e.Status == 401 || e.Status == 403 || e.Status == 404 }

// PullClient talks to Heimdall's pull routes with an org API token.
type PullClient struct {
	Base   string
	Token  string
	Client *http.Client
	UA     string
}

func (c *PullClient) call(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	ua := c.UA
	if ua == "" {
		ua = "heimdall-relay/1"
	}
	req.Header.Set("User-Agent", ua)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.Client
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(data))
		var eb struct {
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal(data, &eb) == nil && eb.Error.Message != "" {
			msg = eb.Error.Message
		}
		return resp.StatusCode, &PullError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && resp.StatusCode != http.StatusNoContent && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s: %v", path, err)
		}
	}
	return resp.StatusCode, nil
}

// Next waits up to wait for the agent's next turn; nil when none came.
func (c *PullClient) Next(ctx context.Context, agent, client string, wait time.Duration) (*PullTurn, error) {
	ws := int(wait / time.Second)
	var t PullTurn
	status, err := c.call(ctx, "POST", PullPrefix+"/next", map[string]any{"agent": agent, "client": client, "wait_s": ws}, &t)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent || t.ID == "" {
		return nil, nil
	}
	t.Payload.Messages = t.Payload.ChatMessages()
	return &t, nil
}

// Answer sends a turn's answer, or its error when errMsg is set.
func (c *PullClient) Answer(ctx context.Context, id string, answer map[string]any, errMsg string) error {
	body := map[string]any{}
	if errMsg != "" {
		body["error"] = errMsg
	} else {
		body["answer"] = answer
	}
	_, err := c.call(ctx, "POST", PullPrefix+"/turns/"+url.PathEscape(id)+"/answer", body, nil)
	return err
}

// Forwarder calls the internal agent with a Mapping.
type Forwarder struct {
	Mapping *Mapping
	Client  *http.Client
	Getenv  func(string) string
}

// Forward sends one turn to the agent and returns the answer object.
func (f *Forwarder) Forward(ctx context.Context, t *PullTurn) (map[string]any, error) {
	m := f.Mapping
	input := t.Payload.Input()
	msgs := t.Payload.Messages
	if m.History != "client" {
		msgs = nil
	}
	body, err := m.Render(input, msgs, t.Payload.SessionID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, m.Method, m.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream, text/plain")
	req.Header.Set("User-Agent", "heimdall-relay/1")
	for k, v := range m.ExpandedHeaders(f.Getenv) {
		req.Header.Set(k, v)
	}
	hc := f.Client
	if hc == nil {
		hc = NoRedirectClient(10 * time.Minute)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call %s: %v", m.URL, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s %s answered %d: %s", m.Method, m.URL, resp.StatusCode, snippet(data))
	}
	text, err := m.Extract(data, resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	out := map[string]any{"text": text}
	if m.Kind == KindOpenAI {
		var u struct {
			Usage struct {
				Prompt     int64 `json:"prompt_tokens"`
				Completion int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(data, &u) == nil && (u.Usage.Prompt > 0 || u.Usage.Completion > 0) {
			out["usage"] = map[string]int64{"input_tokens": u.Usage.Prompt, "output_tokens": u.Usage.Completion}
		}
	}
	return out, nil
}

// Relay takes an agent's turns from Heimdall and forwards each one.
type Relay struct {
	Pull        *PullClient
	Agent       string
	ClientID    string
	Concurrency int
	Forward     func(ctx context.Context, t *PullTurn) (map[string]any, error)
	// MaxTurns stops the relay after this many turns (0: run until ctx ends).
	MaxTurns int
	Log      func(format string, args ...any)
	// Wait is how long one poll waits for a turn; default 25 s.
	Wait time.Duration
}

// Run serves turns until ctx ends, MaxTurns turns were answered, or
// Heimdall refuses the token or the agent. It returns the turns handled.
func (r *Relay) Run(ctx context.Context) (int, error) {
	if r.Agent == "" {
		return 0, errors.New("relay needs the Heimdall agent name")
	}
	if r.ClientID == "" {
		host, _ := os.Hostname()
		r.ClientID = fmt.Sprintf("%s-relay-%d", host, os.Getpid())
	}
	n := r.Concurrency
	if n < 1 {
		n = 1
	}
	wait := r.Wait
	if wait <= 0 {
		wait = 25 * time.Second
	}
	say := r.Log
	if say == nil {
		say = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var handled atomic.Int64
	var fatal error
	var once sync.Once
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				t, err := r.Pull.Next(ctx, r.Agent, r.ClientID, wait)
				if err != nil {
					var pe *PullError
					if errors.As(err, &pe) && pe.Fatal() {
						once.Do(func() { fatal = err; cancel() })
						return
					}
					if ctx.Err() != nil {
						return
					}
					say("next: %v", err)
					select {
					case <-ctx.Done():
					case <-time.After(2 * time.Second):
					}
					continue
				}
				if t == nil {
					continue
				}
				tctx := ctx
				var tcancel context.CancelFunc = func() {}
				if !t.Deadline.IsZero() {
					tctx, tcancel = context.WithDeadline(ctx, t.Deadline)
				}
				ans, ferr := r.Forward(tctx, t)
				tcancel()
				msg := ""
				if ferr != nil {
					msg = ferr.Error()
					say("turn %s (%s): %v", t.ID, t.Payload.Kind, ferr)
				} else {
					say("turn %s (%s) answered", t.ID, t.Payload.Kind)
				}
				for attempt := 0; attempt < 3; attempt++ {
					err := r.Pull.Answer(context.Background(), t.ID, ans, msg)
					if err == nil {
						break
					}
					var pe *PullError
					if errors.As(err, &pe) {
						say("answer %s: %v", t.ID, err)
						break
					}
					time.Sleep(time.Duration(attempt+1) * time.Second)
				}
				if c := handled.Add(1); r.MaxTurns > 0 && int(c) >= r.MaxTurns {
					cancel()
				}
			}
		}()
	}
	wg.Wait()
	return int(handled.Load()), fatal
}
