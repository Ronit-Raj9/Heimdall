package connectlocal

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// CurlRequest is a request read from a pasted curl command.
type CurlRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// LooksLikeCurl reports whether s is a curl command line.
func LooksLikeCurl(s string) bool {
	s = strings.TrimSpace(s)
	return s == "curl" || strings.HasPrefix(s, "curl ") || strings.HasPrefix(s, "curl\t") || strings.HasPrefix(s, "curl\\")
}

// ParseCurl reads the method, URL, headers and body of a curl command, the
// way a docs page or a browser's "copy as cURL" writes it. Line
// continuations are joined; unknown flags are skipped.
func ParseCurl(cmd string) (*CurlRequest, error) {
	cmd = strings.ReplaceAll(cmd, "\\\r\n", " ")
	cmd = strings.ReplaceAll(cmd, "\\\n", " ")
	words, err := SplitShell(cmd)
	if err != nil {
		return nil, err
	}
	if len(words) == 0 || words[0] != "curl" {
		return nil, errors.New("not a curl command")
	}
	req := &CurlRequest{Headers: map[string]string{}}
	var datas []string
	// Flags that take a value and mean nothing here.
	skipValue := map[string]bool{"-o": true, "--output": true, "-m": true, "--max-time": true, "--connect-timeout": true,
		"-w": true, "--write-out": true, "-A": true, "--user-agent": true, "-e": true, "--referer": true, "--retry": true,
		"-x": true, "--proxy": true, "--cacert": true, "--cert": true, "--key": true, "-b": true, "--cookie": true, "-c": true}
	for i := 1; i < len(words); i++ {
		w := words[i]
		next := func() (string, error) {
			if i+1 >= len(words) {
				return "", fmt.Errorf("%s needs a value", w)
			}
			i++
			return words[i], nil
		}
		// --flag=value
		if strings.HasPrefix(w, "--") && strings.Contains(w, "=") {
			k, v, _ := strings.Cut(w, "=")
			words = append(words[:i+1], append([]string{v}, words[i+1:]...)...)
			w = k
		}
		switch w {
		case "-X", "--request":
			v, err := next()
			if err != nil {
				return nil, err
			}
			req.Method = strings.ToUpper(v)
		case "-H", "--header":
			v, err := next()
			if err != nil {
				return nil, err
			}
			k, val, ok := strings.Cut(v, ":")
			if !ok {
				continue
			}
			req.Headers[strings.TrimSpace(k)] = strings.TrimSpace(val)
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii", "--json":
			v, err := next()
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(v, "@") {
				return nil, fmt.Errorf("%s %s reads the body from a file; paste the body itself", w, v)
			}
			datas = append(datas, v)
			if w == "--json" {
				req.Headers["Content-Type"] = "application/json"
			}
		case "-u", "--user":
			v, err := next()
			if err != nil {
				return nil, err
			}
			_ = v
			req.Headers["Authorization"] = "Basic ${HEIMDALL_AGENT_BASIC}"
		case "--url":
			v, err := next()
			if err != nil {
				return nil, err
			}
			req.URL = v
		default:
			if skipValue[w] {
				if _, err := next(); err != nil {
					return nil, err
				}
				continue
			}
			if strings.HasPrefix(w, "-") {
				continue
			}
			if req.URL == "" {
				req.URL = w
			}
		}
	}
	if req.URL == "" {
		return nil, errors.New("the curl command has no URL")
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("the curl URL %q is not http(s)", req.URL)
	}
	req.Body = strings.Join(datas, "&")
	if req.Method == "" {
		req.Method = "GET"
		if req.Body != "" {
			req.Method = "POST"
		}
	}
	return req, nil
}

// SecretHeaders returns the names of headers that carry a credential, so a
// caller can move their values into the environment.
func (c *CurlRequest) SecretHeaders() []string {
	var out []string
	for k := range c.Headers {
		switch strings.ToLower(k) {
		case "authorization", "x-api-key", "api-key", "x-auth-token", "proxy-authorization", "cookie":
			out = append(out, k)
		default:
			if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "secret") || strings.Contains(strings.ToLower(k), "key") {
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// inputFields are body fields that usually carry the user's message,
// strongest first.
var inputFields = []string{"message", "input", "query", "prompt", "question", "text", "content", "instruction", "q", "user_input", "msg"}

// TemplateFromBody turns a sample JSON body into a template: the messages
// array becomes MessagesVar, else the input field (by name, else the
// longest string) becomes InputVar. It returns the kind, the template and
// the dotted path it replaced.
func TemplateFromBody(body string) (string, json.RawMessage, string, error) {
	var doc any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return "", nil, "", fmt.Errorf("the curl body is not JSON: %v", err)
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return "", nil, "", errors.New("the curl body is not a JSON object")
	}
	if msgs, ok := obj["messages"].([]any); ok && len(msgs) > 0 {
		obj["messages"] = MessagesVar
		b, _ := json.Marshal(obj)
		return KindMessages, b, "messages", nil
	}
	path := ""
	for _, f := range inputFields {
		if _, ok := obj[f].(string); ok {
			path = f
			break
		}
	}
	if path == "" {
		best := 0
		var walk func(v any, p []string)
		walk = func(v any, p []string) {
			switch t := v.(type) {
			case string:
				if len(t) > best {
					best, path = len(t), strings.Join(p, ".")
				}
			case map[string]any:
				for k, val := range t {
					walk(val, append(append([]string(nil), p...), k))
				}
			}
		}
		walk(obj, nil)
	}
	if path == "" {
		return "", nil, "", errors.New("the curl body has no string field to put the task in")
	}
	setPath(obj, strings.Split(path, "."), InputVar)
	b, _ := json.Marshal(obj)
	return KindTemplate, b, path, nil
}

func setPath(m map[string]any, path []string, v any) {
	for i, seg := range path {
		if i == len(path)-1 {
			m[seg] = v
			return
		}
		next, ok := m[seg].(map[string]any)
		if !ok {
			return
		}
		m = next
	}
}

// SplitShell splits a command line like a POSIX shell: single quotes keep
// everything, double quotes allow \" \\ \$ escapes, a backslash outside
// quotes escapes the next character.
func SplitShell(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			inWord = true
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unclosed single quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			inWord = true
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New("unclosed double quote")
			}
		case c == '\\' && i+1 < len(s):
			inWord = true
			i++
			cur.WriteByte(s[i])
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}
