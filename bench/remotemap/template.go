package remotemap

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// placeholderRe is one {{name}} reference.
var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.]*)\s*\}\}`)

// Placeholders a mapping may use (schemas/remotemap.go). secret.NAME and
// start.id are checked separately.
var knownVars = map[string]bool{
	"instruction": true, "messages": true, "tools": true, "metadata": true, "gateway": true,
	"session_id": true, "session_index": true, "output": true, "model": true, "uuid": true,
}

// Vars are the values a request is rendered with.
type Vars struct {
	Instruction  string
	Messages     []any // [{role, content}]; nil renders the instruction as one user message
	Tools        map[string]any
	Metadata     map[string]any
	Gateway      map[string]any // nil without model_traffic gateway
	SessionID    string
	SessionIndex int
	Output       string
	Model        string
	StartID      string
	ReplyID      string
	// Secrets resolves {{secret.NAME}} in headers and query; nil refuses
	// them.
	Secrets map[string]string
	// NewID mints {{uuid}}; nil uses 16 random bytes.
	NewID func() string
}

func (v *Vars) lookup(name string, secrets bool) (any, error) {
	switch name {
	case "instruction":
		return v.Instruction, nil
	case "messages":
		if v.Messages != nil {
			return v.Messages, nil
		}
		return []any{map[string]any{"role": "user", "content": v.Instruction}}, nil
	case "tools":
		if v.Tools == nil {
			return map[string]any{}, nil
		}
		return v.Tools, nil
	case "metadata":
		m := map[string]any{"session_id": v.SessionID, "session_index": v.SessionIndex, "output": v.Output}
		for k, x := range v.Metadata {
			m[k] = x
		}
		return m, nil
	case "gateway":
		if v.Gateway == nil {
			return nil, nil
		}
		return v.Gateway, nil
	case "session_id":
		return v.SessionID, nil
	case "session_index":
		return v.SessionIndex, nil
	case "output":
		return v.Output, nil
	case "model":
		return v.Model, nil
	case "uuid":
		if v.NewID != nil {
			return v.NewID(), nil
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b), nil
	case "start.id":
		return v.StartID, nil
	case "reply.id":
		return v.ReplyID, nil
	}
	if n, ok := strings.CutPrefix(name, "secret."); ok {
		if !secrets {
			return nil, fmt.Errorf("{{secret.%s}} may appear only in headers and query parameters", n)
		}
		val, ok := v.Secrets[n]
		if !ok || val == "" {
			return nil, fmt.Errorf("secret %s is not set: add it on Settings > Secrets", n)
		}
		return val, nil
	}
	return nil, fmt.Errorf("unknown placeholder {{%s}}", name)
}

// CheckPlaceholders refuses an unknown placeholder in s, and a secret
// reference where secrets is false.
func CheckPlaceholders(s string, secrets bool) error {
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		n := m[1]
		switch {
		case knownVars[n], n == "start.id", n == "reply.id":
		case strings.HasPrefix(n, "secret."):
			if !secrets {
				return fmt.Errorf("{{%s}} may appear only in headers and query parameters", n)
			}
			if len(n) == len("secret.") {
				return fmt.Errorf("{{secret.}} names no secret")
			}
		default:
			return fmt.Errorf("unknown placeholder {{%s}}: use instruction, messages, tools, metadata, gateway, session_id, session_index, output, model, uuid, start.id, reply.id or secret.NAME", n)
		}
	}
	return nil
}

// textOf is the text form of a value inside a longer string.
func textOf(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// RenderText substitutes every placeholder in s with its text form;
// escape, when set, is applied to each substituted value (url.PathEscape
// for URLs).
func RenderText(s string, v *Vars, secrets bool, escape func(string) string) (string, error) {
	var firstErr error
	out := placeholderRe.ReplaceAllStringFunc(s, func(ref string) string {
		name := placeholderRe.FindStringSubmatch(ref)[1]
		val, err := v.lookup(name, secrets)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return ref
		}
		t := textOf(val)
		if escape != nil {
			t = escape(t)
		}
		return t
	})
	return out, firstErr
}

// RenderURL renders a URL template: placeholders are path escaped, and a
// relative URL is resolved against base.
func RenderURL(tmpl string, base *url.URL, v *Vars) (*url.URL, error) {
	s, err := RenderText(tmpl, v, false, url.PathEscape)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("url %q: %v", tmpl, err)
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	return u, nil
}

// RenderBody renders a JSON request template. The template must be valid
// JSON; a string that is exactly one placeholder takes the value itself.
func RenderBody(tmpl string, v *Vars) ([]byte, error) {
	var doc any
	dec := json.NewDecoder(strings.NewReader(tmpl))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("request template is not valid JSON: %v", err)
	}
	out, err := renderNode(doc, v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func renderNode(n any, v *Vars) (any, error) {
	switch t := n.(type) {
	case string:
		if m := placeholderRe.FindStringSubmatchIndex(t); m != nil && m[0] == 0 && m[1] == len(t) {
			return v.lookup(t[m[2]:m[3]], false)
		}
		return RenderText(t, v, false, nil)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			r, err := renderNode(e, v)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			r, err := renderNode(e, v)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	}
	return n, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
