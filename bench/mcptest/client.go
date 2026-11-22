// Package mcptest benchmarks an organization's own MCP server: the server is
// the thing under test, not the agent (docs/mcp-server-testing.md).
//
// Inspect connects to a streamable HTTP MCP endpoint and measures what a
// server author can act on: reliability (initialize, repeated tools/list and
// ping, listing stability), error recovery (unknown tool, malformed JSON,
// missing arguments, a healthy call afterwards), latency percentiles, input
// schema quality and tool description quality, plus the admission scan's
// poisoning rules over everything the server shows a model. Draft turns the
// inspected tools into a benchmark whose tasks standard reference agents run
// against the live server through the tool proxy, and ServerReport reads
// those runs' tool ledgers back into a per-tool view (how often agents picked
// a tool, called it with valid arguments and got a result).
package mcptest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/admitscan"
)

// ProtocolVersion is the MCP protocol version the client offers.
const ProtocolVersion = "2025-06-18"

// Client limits.
const (
	maxAnswerBytes = 4 << 20
	maxListPages   = 20
	clientName     = "heimdall-mcp-test"
	clientVersion  = "1"
)

// Target is the server under test.
type Target struct {
	URL string
	// Headers are sent on every request: the credential (Authorization or
	// the header the server wants) and any static header.
	Headers map[string]string
}

// Answer is one JSON-RPC exchange as the client saw it.
type Answer struct {
	Status  int
	Latency time.Duration
	Result  json.RawMessage
	Error   *admitscan.RPCError
	// Transport is set when no HTTP answer came back (refused, timeout, TLS).
	Transport string
}

// Failed reports whether the exchange gave no usable result.
func (a Answer) Failed() bool {
	return a.Transport != "" || a.Status >= 400 || a.Error != nil || a.Result == nil
}

// Client speaks streamable HTTP MCP to one server: POST per message, the
// session id the server hands out on initialize, JSON or SSE answers.
type Client struct {
	http    *http.Client
	target  Target
	session string
	nextID  int
	now     func() time.Time
}

// NewClient returns a client for t over hc (nil: a client with a 30 s
// timeout that follows no redirect).
func NewClient(t Target, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Client{http: hc, target: t, now: time.Now}
}

// Call sends one request and waits for its answer.
func (c *Client) Call(ctx context.Context, method string, params any) Answer {
	c.nextID++
	msg := map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method}
	if params != nil {
		msg["params"] = params
	}
	body, _ := json.Marshal(msg)
	return c.post(ctx, body, c.nextID)
}

// Raw posts body as it is and returns the answer, matched to no id.
func (c *Client) Raw(ctx context.Context, body []byte) Answer {
	return c.post(ctx, body, 0)
}

// Notify sends a notification; the server answers 202 with no body.
func (c *Client) Notify(ctx context.Context, method string) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	a := c.post(ctx, body, -1)
	if a.Transport != "" {
		return errors.New(a.Transport)
	}
	if a.Status >= 400 {
		return fmt.Errorf("%s answered HTTP %d", method, a.Status)
	}
	return nil
}

// Hello is what initialize told the client.
type Hello struct {
	ProtocolVersion string `json:"protocol_version"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Instructions    string `json:"instructions,omitempty"`
	HasTools        bool   `json:"has_tools"`
	Session         bool   `json:"session"` // the server issued Mcp-Session-Id
}

// Initialize opens a session and sends notifications/initialized.
func (c *Client) Initialize(ctx context.Context) (Hello, Answer, error) {
	c.session = ""
	a := c.Call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": clientName, "version": clientVersion},
	})
	if a.Failed() {
		return Hello{}, a, fmt.Errorf("initialize: %s", Describe(a))
	}
	var r struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
		Instructions string                     `json:"instructions"`
	}
	if err := json.Unmarshal(a.Result, &r); err != nil {
		return Hello{}, a, fmt.Errorf("initialize: the result is not an MCP initialize result: %w", err)
	}
	h := Hello{ProtocolVersion: r.ProtocolVersion, Name: r.ServerInfo.Name, Version: r.ServerInfo.Version, Instructions: r.Instructions,
		Session: c.session != ""}
	_, h.HasTools = r.Capabilities["tools"]
	if err := c.Notify(ctx, "notifications/initialized"); err != nil {
		return h, a, fmt.Errorf("notifications/initialized: %w", err)
	}
	return h, a, nil
}

// ListTools reads every page of tools/list. The answer is the first page's.
func (c *Client) ListTools(ctx context.Context) ([]admitscan.MCPTool, Answer, error) {
	var all []admitscan.MCPTool
	var first Answer
	cursor := ""
	for page := 0; page < maxListPages; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		a := c.Call(ctx, "tools/list", params)
		if page == 0 {
			first = a
		} else {
			first.Latency += a.Latency
		}
		if a.Failed() {
			return nil, a, fmt.Errorf("tools/list: %s", Describe(a))
		}
		tools, next, err := admitscan.ParseToolsList(a.Result)
		if err != nil {
			return nil, a, err
		}
		all = append(all, tools...)
		if next == "" {
			return all, first, nil
		}
		cursor = next
	}
	return all, first, fmt.Errorf("tools/list: more than %d pages", maxListPages)
}

// CallTool runs one tool.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) Answer {
	if args == nil {
		args = map[string]any{}
	}
	return c.Call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
}

// Close ends the session (DELETE), when the server issued one.
func (c *Client) Close(ctx context.Context) {
	if c.session == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.target.URL, nil)
	if err != nil {
		return
	}
	c.headers(req)
	if resp, err := c.http.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
	}
	c.session = ""
}

func (c *Client) headers(req *http.Request) {
	for k, v := range c.target.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
}

// post sends body and reads the answer to request id (0: any message with a
// result or error; -1: a notification, no body expected).
func (c *Client) post(ctx context.Context, body []byte, id int) Answer {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.target.URL, bytes.NewReader(body))
	if err != nil {
		return Answer{Transport: "bad request: " + err.Error()}
	}
	c.headers(req)
	start := c.now()
	resp, err := c.http.Do(req)
	if err != nil {
		return Answer{Transport: transportText(err), Latency: c.now().Sub(start)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
	a := Answer{Status: resp.StatusCode, Latency: c.now().Sub(start)}
	if err != nil {
		a.Transport = "read answer: " + transportText(err)
		return a
	}
	if len(raw) > maxAnswerBytes {
		a.Transport = fmt.Sprintf("answer over %d bytes", maxAnswerBytes)
		return a
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.session = s
	}
	if id < 0 || len(bytes.TrimSpace(raw)) == 0 {
		return a
	}
	msgs, perr := admitscan.ParseRPCMessages(raw, resp.Header.Get("Content-Type"))
	if perr != nil && len(msgs) == 0 {
		if a.Status < 400 {
			a.Transport = "answer is not JSON-RPC: " + perr.Error()
		}
		return a
	}
	want := fmt.Sprint(id)
	for _, m := range msgs {
		if m.Method != "" && len(m.Result) == 0 && m.Error == nil {
			continue // a request or notification from the server
		}
		if id > 0 && strings.Trim(string(bytes.TrimSpace(m.ID)), `"`) != want {
			continue
		}
		a.Result, a.Error = m.Result, m.Error
		return a
	}
	return a
}

// Describe says in a few words what went wrong with an exchange.
func Describe(a Answer) string {
	switch {
	case a.Transport != "":
		return a.Transport
	case a.Error != nil:
		return fmt.Sprintf("JSON-RPC error %d: %s", a.Error.Code, clip(a.Error.Message, 200))
	case a.Status >= 400:
		return fmt.Sprintf("HTTP %d", a.Status)
	case a.Result == nil:
		return fmt.Sprintf("HTTP %d with no JSON-RPC result", a.Status)
	}
	return "ok"
}

func transportText(err error) string {
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return "no answer in time"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "no answer in time"
	}
	return clip(err.Error(), 300)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}
