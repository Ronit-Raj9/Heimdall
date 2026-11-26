package traceimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Redaction kinds, as Report counts them.
const (
	RedactPrivateKey  = "private key"
	RedactHeader      = "header value"
	RedactBearer      = "bearer token"
	RedactJWT         = "jwt"
	RedactAPIKey      = "api key"
	RedactSecret      = "secret value"
	RedactEmail       = "email"
	RedactPhone       = "phone number"
	RedactLongNumber  = "long digit run"
	RedactPath        = "path" // the user name in a home folder path
	redactionTemplate = "[REDACTED %s]"
)

// rule is one redaction pattern. A rule with keep set replaces only the
// value group (the third), keeping the name and separator before it.
type rule struct {
	kind string
	re   *regexp.Regexp
	keep bool
}

// rules run in order on every text field. They cover the admission secret
// scan's patterns (suite.ScanSecrets) and more, so a redacted draft passes
// that scan.
var rules = []rule{
	{kind: RedactPrivateKey, re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?s:.*?)(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)},
	{kind: RedactHeader, keep: true, re: regexp.MustCompile(`(?i)\b(proxy-authorization|authorization|set-cookie|cookie|x-api-key|api-key|api_key|apikey|x-auth-token)(["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\r\n"',}]+)`)},
	{kind: RedactBearer, re: regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9\-._~+/]{8,}=*`)},
	{kind: RedactJWT, re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`)},
	{kind: RedactAPIKey, re: regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_\-]{16,}|sk_(?:live|test)_[A-Za-z0-9]{10,}|pk-lf-[A-Za-z0-9-]{8,}|(?:AKIA|ASIA)[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_\-]{30,}|glpat-[A-Za-z0-9_\-]{20,}|hf_[A-Za-z0-9]{30,})`)},
	{kind: RedactSecret, keep: true, re: regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:token|secret|password|passwd|api[_-]?key|access[_-]?key|private[_-]?key|_key))(["']?\s*[:=]\s*["']?)([^\s"'#,;}&]{6,})`)},
	{kind: RedactEmail, re: regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)},
	{kind: RedactPhone, re: regexp.MustCompile(`\+\d{1,3}[\s.-]?\(?\d{1,4}\)?(?:[\s.-]?\d{2,4}){2,4}\b|\(\d{3}\)\s?\d{3}[ -]\d{4}\b|\b\d{3}[ -]\d{3}[ -]\d{4}\b`)},
	{kind: RedactLongNumber, re: regexp.MustCompile(`\b(?:\d{4}[ -]){3}\d{1,7}\b|\d{9,}`)},
	// The user name of a home folder path: /home/<name> and /Users/<name>
	// keep the prefix and the rest of the path. The placeholder holds a
	// bracket and a space, so a second pass matches nothing.
	{kind: RedactPath, keep: true, re: regexp.MustCompile(`(^|[^A-Za-z0-9_./-])(/home/|/Users/)([A-Za-z0-9._-]+)`)},
}

// sensitiveKey matches JSON keys whose value is replaced whole.
var sensitiveKey = regexp.MustCompile(`(?i)^(proxy-authorization|authorization|set-cookie|cookie|x-api-key|api-key|apikey|x-auth-token)$|(token|secret|password|passwd|api[_-]?key|access[_-]?key|private[_-]?key|_key)$`)

// Report counts what a Redactor scrubbed, by kind. It never holds a value.
type Report map[string]int

// Total is the number of values scrubbed.
func (r Report) Total() int {
	n := 0
	for _, c := range r {
		n += c
	}
	return n
}

// String renders the counts as "2 email, 1 bearer token", sorted by kind,
// or "nothing" when there were none.
func (r Report) String() string {
	if r.Total() == 0 {
		return "nothing"
	}
	kinds := make([]string, 0, len(r))
	for k := range r {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%d %s", r[k], k))
	}
	return strings.Join(parts, ", ")
}

// Redactor scrubs personal data and credentials from text and JSON and
// counts what it scrubbed.
type Redactor struct {
	counts Report
}

// NewRedactor returns a Redactor with no counts.
func NewRedactor() *Redactor { return &Redactor{counts: Report{}} }

// Report returns a copy of the counts so far.
func (r *Redactor) Report() Report {
	out := Report{}
	for k, v := range r.counts {
		out[k] = v
	}
	return out
}

// Text scrubs one text field: private keys, the values of authorization,
// cookie and api-key headers, bearer and basic credentials, JWTs, API keys,
// values assigned to *token, *secret, *password and *_key names, emails,
// phone numbers and digit runs of nine or more.
func (r *Redactor) Text(s string) string {
	for _, ru := range rules {
		s = r.apply(ru, s)
	}
	return s
}

// ID scrubs an identifier: credentials and emails only, so a hex trace id
// keeps its digits and still names the session.
func (r *Redactor) ID(s string) string {
	for _, ru := range rules {
		if ru.kind != RedactPhone && ru.kind != RedactLongNumber {
			s = r.apply(ru, s)
		}
	}
	return s
}

// JSON scrubs a JSON value: the values of sensitive keys whole, every string
// with Text, every number with nine or more digits. Invalid JSON is scrubbed
// as text and returned as a JSON string.
func (r *Redactor) JSON(raw json.RawMessage) json.RawMessage {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		out, _ := json.Marshal(r.Text(string(raw)))
		return out
	}
	out, err := json.Marshal(r.walk(v))
	if err != nil {
		out, _ = json.Marshal(r.Text(string(raw)))
	}
	return out
}

// Session scrubs every text a draft can write from s, in place.
func (r *Redactor) Session(s *Session) {
	s.ID = r.ID(s.ID)
	s.Name = r.Text(s.Name)
	for i := range s.Turns {
		s.Turns[i].Text = r.Text(s.Turns[i].Text)
	}
	for i := range s.ToolCalls {
		c := &s.ToolCalls[i]
		c.ID = r.ID(c.ID)
		c.Name = r.Text(c.Name)
		c.Arguments = r.JSON(c.Arguments)
		c.Result = r.Text(c.Result)
		c.Error = r.Text(c.Error)
	}
	s.FinalAnswer = r.Text(s.FinalAnswer)
	if w := s.Workspace; w != nil {
		w.Cwd = r.Text(w.Cwd)
		w.Branch = r.Text(w.Branch)
		w.Commit = r.ID(w.Commit)
		w.RepoURL = r.Text(w.RepoURL)
	}
	for i := range s.Outcome.Hints {
		s.Outcome.Hints[i] = r.Text(s.Outcome.Hints[i])
	}
}

func (r *Redactor) apply(ru rule, s string) string {
	return ru.re.ReplaceAllStringFunc(s, func(m string) string {
		if !ru.keep {
			r.counts[ru.kind]++
			return fmt.Sprintf(redactionTemplate, ru.kind)
		}
		sub := ru.re.FindStringSubmatch(m)
		if len(sub) < 4 {
			r.counts[ru.kind]++
			return fmt.Sprintf(redactionTemplate, ru.kind)
		}
		if strings.Contains(sub[3], "[REDACTED") {
			return m
		}
		r.counts[ru.kind]++
		val := fmt.Sprintf(redactionTemplate, ru.kind)
		if strings.HasPrefix(sub[3], `"`) {
			val = `"` + val + `"`
		} else if strings.HasPrefix(sub[3], `'`) {
			val = `'` + val + `'`
		}
		return sub[1] + sub[2] + val
	})
}

func (r *Redactor) walk(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			nk := r.Text(k)
			if sensitiveKey.MatchString(k) && val != nil && val != "" {
				if _, nested := val.(map[string]any); !nested {
					r.counts[keyKind(k)]++
					out[nk] = fmt.Sprintf(redactionTemplate, keyKind(k))
					continue
				}
			}
			out[nk] = r.walk(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = r.walk(val)
		}
		return out
	case string:
		return r.Text(x)
	case json.Number:
		digits := strings.Map(func(c rune) rune {
			if c >= '0' && c <= '9' {
				return c
			}
			return -1
		}, x.String())
		if len(digits) >= 9 && !strings.ContainsAny(x.String(), ".eE") {
			r.counts[RedactLongNumber]++
			return fmt.Sprintf(redactionTemplate, RedactLongNumber)
		}
		return x
	}
	return v
}

func keyKind(k string) string {
	switch strings.ToLower(k) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key", "apikey", "x-auth-token":
		return RedactHeader
	}
	return RedactSecret
}
