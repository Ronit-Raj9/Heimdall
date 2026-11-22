package voiceconnect

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
)

// Conversation is what the bench-driven turn loop needs from an agent: the
// agent's reply to one user turn of session sessionID. Turns of one session
// are sent in order; different sessions may run at once.
type Conversation interface {
	Converse(ctx context.Context, sessionID, userTurn string) (agentTurn string, err error)
}

// Client is a platform connector: a Conversation that can also give the
// agent's opening line and release a session.
type Client interface {
	Conversation
	// Opening returns what the agent says before the user's first turn
	// ("" when the platform's text mode opens with the user). It is only
	// meaningful before the first Converse of the session.
	Opening(ctx context.Context, sessionID string) (string, error)
	// End releases the session's platform state. Safe to call on a session
	// that never started.
	End(ctx context.Context, sessionID string) error
	// Platform is the platform id (schemas.VoicePlatform*).
	Platform() string
}

// Error is a platform failure. Infra marks a failure of the platform or the
// network (5xx, 429, connection lost, timeout), which a driver records as
// infrastructure, never as the agent's answer.
type Error struct {
	Platform string
	Status   int
	Infra    bool
	Msg      string
}

func (e *Error) Error() string {
	name := PlatformName(e.Platform)
	if name == "" {
		name = "voice platform"
	}
	if e.Status != 0 {
		return fmt.Sprintf("%s answered HTTP %d: %s", name, e.Status, e.Msg)
	}
	return name + ": " + e.Msg
}

// IsInfra reports whether err is a platform or network failure.
func IsInfra(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Infra
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// Options tune New. Zero values take the defaults.
type Options struct {
	// HTTP is the client every request uses (the run's egress dialer).
	// nil uses a client with the spec's timeout.
	HTTP *http.Client
	// GreetingWait is how long an ElevenLabs session waits for the agent's
	// first message after the conversation opens. Default 3 s.
	GreetingWait time.Duration
	// Settle is how long a turn keeps listening after the agent's first
	// reply for more reply messages of the same turn. Default 250 ms.
	Settle time.Duration
}

// New returns the connector for spec. apiKey is the resolved value of
// spec.APIKeySecret ("" when none).
func New(spec *schemas.VoicePlatformAgent, apiKey string, opt Options) (Client, error) {
	if err := Check(spec); err != nil {
		return nil, err
	}
	if !SupportsText(spec.Platform) {
		return nil, fmt.Errorf("a %s agent has no text mode: it can only run voice benchmarks", PlatformName(spec.Platform))
	}
	timeout := time.Duration(spec.TimeoutS) * time.Second
	if timeout <= 0 {
		timeout = DefaultTimeoutS * time.Second
	}
	if opt.HTTP == nil {
		opt.HTTP = &http.Client{Timeout: timeout}
	}
	if opt.GreetingWait <= 0 {
		opt.GreetingWait = 3 * time.Second
	}
	if opt.Settle <= 0 {
		opt.Settle = 250 * time.Millisecond
	}
	b := base{platform: spec.Platform, agentID: spec.AgentID, key: apiKey, api: APIBase(spec), hc: opt.HTTP, timeout: timeout, opt: opt, locks: &sync.Map{}}
	switch spec.Platform {
	case schemas.VoicePlatformVapi:
		return &vapi{base: b, chats: map[string]string{}}, nil
	case schemas.VoicePlatformRetell:
		return &retell{base: b, chats: map[string]string{}}, nil
	default:
		return &elevenLabs{base: b, convs: map[string]*elConv{}}, nil
	}
}

// base holds what every connector shares.
type base struct {
	platform string
	agentID  string
	key      string
	api      string
	hc       *http.Client
	timeout  time.Duration
	opt      Options
	locks    *sync.Map // session id -> *sync.Mutex: one turn at a time per session
}

func (b *base) Platform() string { return b.platform }

func (b *base) lock(session string) func() {
	m, _ := b.locks.LoadOrStore(session, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (b *base) err(status int, infra bool, msg string) *Error {
	return &Error{Platform: b.platform, Status: status, Infra: infra, Msg: msg}
}

// do sends one JSON request with the platform's auth header and decodes a
// 2xx JSON answer into out (nil skips the body).
func (b *base) do(ctx context.Context, method, path string, header http.Header, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.api+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	resp, err := b.hc.Do(req)
	if err != nil {
		return b.err(0, true, "request failed: "+scrub(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return b.err(resp.StatusCode, true, "reading the answer: "+scrub(err.Error()))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e := statusError(resp.StatusCode, raw)
		e.Platform = b.platform
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return b.err(resp.StatusCode, false, "the answer is not the JSON the API documents: "+scrub(err.Error()))
	}
	return nil
}

func (b *base) bearer() http.Header {
	h := http.Header{}
	if b.key != "" {
		h.Set("Authorization", "Bearer "+b.key)
	}
	return h
}

// statusError is the Error of a non-2xx answer: 5xx and 429 are the
// platform's trouble, any other status a refusal.
func statusError(status int, body []byte) *Error {
	msg := strings.TrimSpace(string(body))
	var j struct {
		Message any `json:"message"`
		Error   any `json:"error"`
		Detail  any `json:"detail"`
	}
	if json.Unmarshal(body, &j) == nil {
		for _, v := range []any{j.Message, j.Error, j.Detail} {
			if v != nil {
				if s, ok := v.(string); ok {
					msg = s
				} else if raw, err := json.Marshal(v); err == nil {
					msg = string(raw)
				}
				break
			}
		}
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &Error{Status: status, Infra: status >= 500 || status == http.StatusTooManyRequests, Msg: scrub(msg)}
}

var secretish = regexp.MustCompile(`(?i)(bearer\s+|xi-api-key[=:]\s*|token=|api_key=|key=)[^\s&"']+`)

// scrub removes anything that looks like a credential from a message.
func scrub(s string) string { return secretish.ReplaceAllString(s, "${1}[redacted]") }

func checkTurn(platform, sessionID, userTurn string) error {
	if sessionID == "" {
		return &Error{Platform: platform, Msg: "a turn needs a session id"}
	}
	if strings.TrimSpace(userTurn) == "" {
		return &Error{Platform: platform, Msg: "the user turn is empty"}
	}
	return nil
}
