// Package connectlocal is the machine side of connecting an agent (lane
// Z02, audit/INFRA_I2_AGENT_ONBOARDING.md sections 4.3, 4.4 and 4.7): what
// the `heimdall` CLI and the local MCP server do on the user's own machine.
//
//   - DetectDir reads a local repository (files only, nothing runs) and
//     proposes how to serve the agent in it (`heimdall serve file:object`).
//   - ParseCurl turns a pasted curl command into a request.
//   - Probe runs the nonce ladder against an HTTP agent from this machine
//     and returns a Mapping: which request to send and where the answer is.
//   - Relay takes the agent's turns from Heimdall over outbound HTTPS (pull
//     mode, Y11) and forwards each to an internal URL with a Mapping, so a
//     service inside a private network is benchmarked with no inbound port.
//   - ProjectFile is .heimdall/agent.yaml, the committed agent config.
//
// The server side of detection is bench/connect (lane Z01); this package
// never decides alone where the server can probe from.
package connectlocal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Mapping kinds.
const (
	KindOpenAI   = "openai"   // OpenAI-compatible chat completions
	KindMessages = "messages" // a body with a messages array
	KindField    = "field"    // one string field carries the input
	KindTemplate = "template" // a body taken from a pasted curl command
)

// Template placeholders. A JSON string equal to MessagesVar is replaced by
// the chat so far (OpenAI roles); InputVar and SessionVar are replaced
// inside strings.
const (
	InputVar    = "{{input}}"
	MessagesVar = "{{messages}}"
	SessionVar  = "{{session_id}}"
)

// Mapping says how to call an HTTP agent and where its answer is.
type Mapping struct {
	Kind   string `json:"kind" yaml:"kind"`
	Method string `json:"method" yaml:"method"`
	URL    string `json:"url" yaml:"url"`
	// Headers are sent as they are, after ${VAR} expansion from this
	// process's environment, so a secret stays in the environment.
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	// Body is the JSON request template.
	Body json.RawMessage `json:"body" yaml:"-"`
	// BodyText mirrors Body for YAML files.
	BodyText string `json:"-" yaml:"body"`
	// Answer is the dotted path of the answer in the JSON response
	// (choices.0.message.content; -1 is the last element); empty means the
	// whole body is the answer (plain text).
	Answer string `json:"answer" yaml:"answer"`
	// Stream is true when the agent answers server-sent events; Answer is
	// then read from each event's data and the pieces are joined.
	Stream bool `json:"stream,omitempty" yaml:"stream,omitempty"`
	// History is client when the body carries the whole chat, server when
	// only the latest message is sent with a session id, none otherwise.
	History string `json:"history" yaml:"history"`
	// Model is the model id an OpenAI-compatible server listed.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
}

// Message is one chat message in OpenAI roles.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Validate checks a mapping read from a file or flags.
func (m *Mapping) Validate() error {
	if m.URL == "" {
		return errors.New("mapping has no url")
	}
	if !strings.HasPrefix(m.URL, "http://") && !strings.HasPrefix(m.URL, "https://") {
		return fmt.Errorf("mapping url %q is not http(s)", m.URL)
	}
	if len(m.Body) == 0 && m.BodyText != "" {
		m.Body = json.RawMessage(m.BodyText)
	}
	if len(m.Body) == 0 {
		return errors.New("mapping has no body template")
	}
	var v any
	if err := json.Unmarshal(m.Body, &v); err != nil {
		return fmt.Errorf("mapping body is not JSON: %v", err)
	}
	if !bytes.Contains(m.Body, []byte(InputVar)) && !bytes.Contains(m.Body, []byte(MessagesVar)) {
		return fmt.Errorf("mapping body has neither %s nor %s: nothing would carry the task", InputVar, MessagesVar)
	}
	if m.Method == "" {
		m.Method = "POST"
	}
	return nil
}

// Render fills the body template for one turn.
func (m *Mapping) Render(input string, messages []Message, session string) ([]byte, error) {
	var tpl any
	if err := json.Unmarshal(m.Body, &tpl); err != nil {
		return nil, fmt.Errorf("body template: %v", err)
	}
	if len(messages) == 0 {
		messages = []Message{{Role: "user", Content: input}}
	}
	out := fill(tpl, input, messages, session)
	return json.Marshal(out)
}

func fill(v any, input string, messages []Message, session string) any {
	switch t := v.(type) {
	case string:
		if t == MessagesVar {
			return messages
		}
		t = strings.ReplaceAll(t, InputVar, input)
		return strings.ReplaceAll(t, SessionVar, session)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = fill(val, input, messages, session)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = fill(val, input, messages, session)
		}
		return out
	}
	return v
}

// ExpandedHeaders returns the headers with ${VAR} taken from getenv.
func (m *Mapping) ExpandedHeaders(getenv func(string) string) map[string]string {
	if getenv == nil {
		getenv = os.Getenv
	}
	out := map[string]string{}
	for k, v := range m.Headers {
		out[k] = os.Expand(v, getenv)
	}
	return out
}

// Extract reads the answer out of a response body.
func (m *Mapping) Extract(body []byte, contentType string) (string, error) {
	if m.Stream || strings.HasPrefix(strings.ToLower(contentType), "text/event-stream") {
		return extractSSE(body, m.Answer)
	}
	if m.Answer == "" {
		return strings.TrimSpace(string(body)), nil
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("the answer is not JSON (%v), but the mapping reads %s from it", err, m.Answer)
	}
	v, ok := Lookup(doc, m.Answer)
	if !ok {
		return "", fmt.Errorf("the response has no %s", m.Answer)
	}
	return asText(v), nil
}

func extractSSE(body []byte, path string) (string, error) {
	var b strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var doc any
		if path == "" || json.Unmarshal([]byte(data), &doc) != nil {
			b.WriteString(data)
			continue
		}
		if v, ok := Lookup(doc, path); ok {
			b.WriteString(asText(v))
		}
	}
	return b.String(), sc.Err()
}

func asText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Lookup walks a dotted path through decoded JSON. A numeric segment
// indexes a list; -1 is its last element.
func Lookup(doc any, path string) (any, bool) {
	cur := doc
	if path == "" {
		return cur, true
	}
	for _, seg := range strings.Split(path, ".") {
		switch t := cur.(type) {
		case map[string]any:
			v, ok := t[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil {
				return nil, false
			}
			if i < 0 {
				i += len(t)
			}
			if i < 0 || i >= len(t) {
				return nil, false
			}
			cur = t[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// FindString returns the shortest dotted path to a string holding needle,
// generalized to the last element when it sits in a list of messages
// (objects with a role). Strings that also hold echo (the whole prompt: the
// agent repeating its input) are skipped. ok is false when no string holds it.
func FindString(doc any, needle, echo string) (string, bool) {
	type hit struct{ path []string }
	var hits []hit
	var walk func(v any, path []string)
	walk = func(v any, path []string) {
		switch t := v.(type) {
		case string:
			if strings.Contains(t, needle) && (echo == "" || !strings.Contains(t, echo)) {
				hits = append(hits, hit{append([]string(nil), path...)})
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k], append(path, k))
			}
		case []any:
			for i, e := range t {
				seg := strconv.Itoa(i)
				if i == len(t)-1 && len(t) > 1 && isMessage(e) {
					seg = "-1"
				}
				walk(e, append(path, seg))
			}
		}
	}
	walk(doc, nil)
	if len(hits) == 0 {
		return "", false
	}
	sort.SliceStable(hits, func(i, j int) bool { return len(hits[i].path) < len(hits[j].path) })
	return strings.Join(hits[0].path, "."), true
}

func isMessage(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, role := m["role"]
	return role
}
