package connect

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Curl is a parsed curl command.
type Curl struct {
	Method  string
	URL     string
	Headers map[string]string // canonical names as typed
	Body    string
	User    string // -u user:password
}

// splitShell splits a command line like a POSIX shell: single and double
// quotes, backslash escapes and line continuations.
func splitShell(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && (s[i+1] == '\n' || s[i+1] == '\r'):
			i++
			if i+1 < len(s) && s[i] == '\r' && s[i+1] == '\n' {
				i++
			}
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("the command has an unclosed ' quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune(`"\$`+"`", rune(s[i+1])) {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New(`the command has an unclosed " quote`)
			}
			inWord = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}

// ParseCurl reads the method, URL, headers, body and basic auth of a curl
// command. Flags that do not change the request are ignored.
func ParseCurl(cmd string) (*Curl, error) {
	args, err := splitShell(strings.TrimSpace(cmd))
	if err != nil {
		return nil, err
	}
	if len(args) == 0 || !strings.HasPrefix(strings.ToLower(args[0]), "curl") {
		return nil, errors.New("not a curl command")
	}
	c := &Curl{Headers: map[string]string{}}
	var data []string
	takes := map[string]bool{"-o": true, "--output": true, "-m": true, "--max-time": true, "--connect-timeout": true, "-A": true, "--user-agent": true,
		"-e": true, "--referer": true, "-x": true, "--proxy": true, "-w": true, "--write-out": true, "--retry": true, "-b": true, "--cookie": true, "-c": true, "--cookie-jar": true}
	for i := 1; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		name, val, hasEq := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "--") {
			hasEq = false
		}
		switch {
		case a == "-X" || a == "--request" || hasEq && name == "--request":
			v := val
			if !hasEq {
				if v, err = next(); err != nil {
					return nil, err
				}
			}
			c.Method = strings.ToUpper(v)
		case strings.HasPrefix(a, "-X") && len(a) > 2:
			c.Method = strings.ToUpper(a[2:])
		case a == "-H" || a == "--header" || hasEq && name == "--header":
			v := val
			if !hasEq {
				if v, err = next(); err != nil {
					return nil, err
				}
			}
			k, hv, ok := strings.Cut(v, ":")
			if ok {
				c.Headers[strings.TrimSpace(k)] = strings.TrimSpace(hv)
			}
		case a == "-d" || a == "--data" || a == "--data-raw" || a == "--data-binary" || a == "--data-ascii" || a == "--json" ||
			hasEq && (name == "--data" || name == "--data-raw" || name == "--data-binary" || name == "--json"):
			v := val
			if !hasEq {
				if v, err = next(); err != nil {
					return nil, err
				}
			}
			if strings.HasPrefix(v, "@") {
				return nil, errors.New("the body is read from a file (" + v + "): paste the body itself")
			}
			if a == "--json" || name == "--json" {
				c.Headers["Content-Type"] = "application/json"
			}
			data = append(data, v)
		case a == "-u" || a == "--user":
			if c.User, err = next(); err != nil {
				return nil, err
			}
		case a == "--url":
			if c.URL, err = next(); err != nil {
				return nil, err
			}
		case takes[a]:
			if _, err = next(); err != nil {
				return nil, err
			}
		case strings.HasPrefix(a, "-"):
			// a flag without a value (-s, -L, -i, --compressed, -k)
		default:
			if c.URL == "" {
				c.URL = a
			}
		}
	}
	if c.URL == "" {
		return nil, errors.New("the curl command has no URL")
	}
	if !strings.Contains(c.URL, "://") {
		c.URL = "https://" + c.URL
	}
	if u, err := url.Parse(c.URL); err != nil || u.Host == "" {
		return nil, fmt.Errorf("the curl URL %q is not a URL", redactURL(c.URL))
	}
	c.Body = strings.Join(data, "&")
	if c.Method == "" {
		c.Method = "GET"
		if c.Body != "" {
			c.Method = "POST"
		}
	}
	return c, nil
}

// prompt field names, in preference order.
var promptFields = []string{"instruction", "message", "input", "query", "prompt", "question", "text", "content", "chatInput", "input_value", "inputText", "msg", "q"}

var sessionFields = map[string]bool{"session_id": true, "sessionId": true, "sessionid": true, "thread_id": true, "threadId": true,
	"conversation_id": true, "conversationId": true, "chat_id": true, "chatId": true}

var userFields = map[string]bool{"user_id": true, "userId": true, "user": true, "customer_id": true, "customerId": true, "resourceId": true}

// templateFromBody turns a pasted JSON body into a request template: the
// prompt field gets {{instruction}} (a messages list gets its last user
// turn replaced), session fields get {{session_id}}. It reports the field
// it picked and any user id field seen.
func templateFromBody(body string) (tmpl, field string, users []string, err error) {
	var doc any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return "", "", nil, errors.New("the body is not JSON")
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return "", "", nil, errors.New("the body is not a JSON object")
	}
	placed := false
	var walk func(m map[string]any, prefix string)
	walk = func(m map[string]any, prefix string) {
		for _, k := range sortedKeys(m) {
			switch v := m[k].(type) {
			case string:
				if sessionFields[k] {
					m[k] = "{{session_id}}"
				}
				if userFields[k] {
					users = append(users, prefix+k)
				}
			case map[string]any:
				walk(v, prefix+k+".")
			}
		}
	}
	// messages list: replace the last user turn's content.
	if msgs, ok := obj["messages"].([]any); ok && len(msgs) > 0 {
		for i := len(msgs) - 1; i >= 0; i-- {
			if m, ok := msgs[i].(map[string]any); ok && (m["role"] == "user" || m["role"] == nil) {
				if _, isStr := m["content"].(string); isStr {
					if len(msgs) == 1 {
						obj["messages"] = "{{messages}}"
					} else {
						m["content"] = "{{instruction}}"
					}
					placed, field = true, "messages"
					break
				}
			}
		}
	}
	if !placed {
		for _, name := range promptFields {
			if s, ok := obj[name].(string); ok && !strings.HasPrefix(s, "{{") {
				obj[name] = "{{instruction}}"
				placed, field = true, name
				break
			}
		}
	}
	if !placed { // one level down: {"input": {"message": "hi"}}
		for _, k := range sortedKeys(obj) {
			inner, ok := obj[k].(map[string]any)
			if !ok {
				continue
			}
			for _, name := range promptFields {
				if _, ok := inner[name].(string); ok {
					inner[name] = "{{instruction}}"
					placed, field = true, k+"."+name
					break
				}
			}
			if placed {
				break
			}
		}
	}
	walk(obj, "")
	if !placed {
		return "", "", users, errors.New("no field of the body looks like the message (message, input, query, prompt, question, text or messages)")
	}
	b, _ := json.Marshal(obj)
	return string(b), field, users, nil
}

// authHeaders are header names that carry a credential.
func isAuthHeader(name string) bool {
	n := strings.ToLower(name)
	return n == "authorization" || strings.Contains(n, "api-key") || strings.Contains(n, "apikey") || strings.Contains(n, "token") ||
		strings.Contains(n, "secret") || n == "x-key" || strings.HasSuffix(n, "-key")
}

// curlCandidate turns a parsed curl into a candidate and the secret values
// it carried (by suggested secret name), for the probe only.
func curlCandidate(c *Curl) (Candidate, map[string]string) {
	u, _ := url.Parse(c.URL)
	cand := Candidate{Kind: KindHTTP, Preset: PresetCustom, Label: "HTTP endpoint", Confidence: 0.6, Supported: true, Template: TemplateHTTP,
		URL: c.URL, Params: map[string]string{"url": c.URL}, Evidence: []string{"curl " + c.Method + " " + redactURL(c.URL)}}
	secrets := map[string]string{}
	m := &schemas.RemoteMapping{}
	if c.Method != "" && c.Method != "POST" {
		m.Method = c.Method
	}
	for _, k := range sortedKeys(c.Headers) {
		v := c.Headers[k]
		lk := strings.ToLower(k)
		switch {
		case lk == "content-type" || lk == "accept" || lk == "user-agent" || lk == "content-length" || lk == "host":
			continue
		case isAuthHeader(k) && cand.Params["auth_secret"] == "":
			name := secretNameFor(u.Hostname(), "KEY")
			tok := v
			if lk == "authorization" {
				if t, ok := strings.CutPrefix(v, "Bearer "); ok {
					tok = t
					name = secretNameFor(u.Hostname(), "TOKEN")
				} else {
					// Basic or another scheme: keep the scheme, the rest is the secret.
					scheme, rest, _ := strings.Cut(v, " ")
					setHeader(m, k, scheme+" {{secret."+name+"}}")
					secrets[name] = rest
					cand.Questions = append(cand.Questions, Question{Kind: "secret", Param: "headers", Label: k + " credential", Secret: name, Header: k, Prefill: rest})
					cand.Evidence = append(cand.Evidence, "header "+k+" moved into the secret "+name)
					continue
				}
			} else {
				cand.Params["auth_header"] = k
			}
			cand.Params["auth_secret"] = name
			secrets[name] = tok
			cand.Questions = append(cand.Questions, Question{Kind: "secret", Param: "auth_secret", Label: k, Secret: name, Header: k, Prefill: tok,
				Help: "Stored as the org secret " + name + "; the agent config keeps the name only."})
			cand.Evidence = append(cand.Evidence, "header "+k+" moved into the secret "+name)
		case isAuthHeader(k):
			name := secretNameFor(u.Hostname(), strings.ToUpper(strings.NewReplacer("-", "_").Replace(k)))
			setHeader(m, k, "{{secret."+name+"}}")
			secrets[name] = v
			cand.Questions = append(cand.Questions, Question{Kind: "secret", Param: "headers", Label: k, Secret: name, Header: k, Prefill: v})
		default:
			setHeader(m, k, v)
		}
	}
	if c.User != "" {
		name := secretNameFor(u.Hostname(), "BASIC")
		val := base64.StdEncoding.EncodeToString([]byte(c.User))
		setHeader(m, "Authorization", "Basic {{secret."+name+"}}")
		secrets[name] = val
		cand.Questions = append(cand.Questions, Question{Kind: "secret", Param: "headers", Label: "Basic auth (user:password, base64)", Secret: name, Header: "Authorization", Prefill: val})
	}
	if c.Body != "" {
		tmpl, field, users, err := templateFromBody(c.Body)
		if err != nil {
			cand.Evidence = append(cand.Evidence, "body: "+err.Error())
			cand.Questions = append(cand.Questions, Question{Kind: "text", Param: "body", Label: "Request template",
				Help: "Write the body with {{instruction}} where the task goes."})
		} else {
			m.Body = tmpl
			cand.Evidence = append(cand.Evidence, "field "+field+" carries the task")
			for _, uf := range users {
				cand.Evidence = append(cand.Evidence, uf+" looks like a user field: verification checks whether sessions leak through it")
			}
		}
	}
	if m.Body != "" || len(m.Headers) > 0 || m.Method != "" {
		cand.Mapping = m
	}
	return cand, secrets
}

func setHeader(m *schemas.RemoteMapping, k, v string) {
	if m.Headers == nil {
		m.Headers = map[string]string{}
	}
	m.Headers[k] = v
}
