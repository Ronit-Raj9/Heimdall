package remotemap

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

var (
	headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	queryNameRe  = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,64}$`)
	methods      = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	// reservedHeaders are set by Heimdall and the auth block, never by a
	// mapping.
	reservedHeaders = map[string]bool{"host": true, "content-length": true, "x-heimdall-signature": true,
		"x-heimdall-timestamp": true, "x-amz-date": true, "x-amz-security-token": true, "transfer-encoding": true, "connection": true}
)

// MaxHeaders caps the headers (and the query parameters) of a mapping.
const MaxHeaders = 32

// Mapped reports whether a remote agent uses a mapping or an auth block, so
// its requests go through this package instead of Heimdall's own shape.
func Mapped(r *schemas.RemoteAgent) bool {
	return r != nil && (r.Mapping != nil || r.Auth != nil)
}

// Check refuses a mapping or auth block that cannot run: unknown
// placeholders, invalid JSON templates or paths, headers Heimdall owns,
// steps on another host. It is pure.
func Check(r *schemas.RemoteAgent) error {
	if r == nil {
		return nil
	}
	var errs []error
	if err := CheckAuth(r.Auth); err != nil {
		errs = append(errs, err)
	}
	m := r.Mapping
	if m == nil {
		return errors.Join(errs...)
	}
	base, err := url.Parse(r.URL)
	if err != nil || base.Hostname() == "" {
		return errors.Join(append(errs, fmt.Errorf("remote url %q is not an absolute URL", r.URL))...)
	}
	if strings.Contains(base.Host, "{{") {
		errs = append(errs, errors.New("remote url: the host cannot hold a placeholder"))
	}
	if err := CheckPlaceholders(r.URL, false); err != nil {
		errs = append(errs, fmt.Errorf("remote url: %v", err))
	}
	if m.Method != "" && !methods[strings.ToUpper(m.Method)] {
		errs = append(errs, fmt.Errorf("mapping method %q: want GET, POST, PUT, PATCH or DELETE", m.Method))
	}
	errs = append(errs, checkHeaders("mapping headers", m.Headers), checkQuery(m.Query), checkBody("mapping body", m.Body))
	for _, p := range []struct{ label, path string }{{"answer_path", m.AnswerPath}, {"stream_path", m.StreamPath}} {
		if p.path != "" {
			if _, err := ParsePath(p.path); err != nil {
				errs = append(errs, fmt.Errorf("mapping %s: %v", p.label, err))
			}
		}
	}
	switch m.Stream {
	case "", schemas.RemoteStreamSSE, schemas.RemoteStreamAWSEventStream:
	default:
		errs = append(errs, fmt.Errorf("mapping stream %q: want sse or aws_eventstream", m.Stream))
	}
	switch m.StreamDecode {
	case "", "base64":
	default:
		errs = append(errs, fmt.Errorf("mapping stream_decode %q: want base64", m.StreamDecode))
	}
	if m.Stream != "" && m.Poll != nil {
		errs = append(errs, errors.New("mapping: a streamed answer cannot also be polled"))
	}
	if m.Stream == "" && (m.StreamPath != "" || m.StreamDecode != "") {
		errs = append(errs, errors.New("mapping stream_path and stream_decode need stream"))
	}
	for _, s := range []struct {
		label string
		step  *schemas.RemoteStep
	}{{"start", m.Start}, {"end", m.End}} {
		if s.step == nil {
			continue
		}
		if s.step.Method != "" && !methods[strings.ToUpper(s.step.Method)] {
			errs = append(errs, fmt.Errorf("mapping %s method %q: want GET, POST, PUT, PATCH or DELETE", s.label, s.step.Method))
		}
		errs = append(errs, checkStepURL("mapping "+s.label, s.step.URL, base), checkHeaders("mapping "+s.label+" headers", s.step.Headers),
			checkBody("mapping "+s.label+" body", s.step.Body))
		if s.step.IDPath != "" {
			if _, err := ParsePath(s.step.IDPath); err != nil {
				errs = append(errs, fmt.Errorf("mapping %s id_path: %v", s.label, err))
			}
		}
	}
	if m.Start == nil && usesStartID(m) {
		errs = append(errs, errors.New("mapping uses {{start.id}} but has no start step"))
	}
	if p := m.Poll; p != nil {
		errs = append(errs, checkStepURL("mapping poll", p.URL, base))
		if p.AnswerPath == "" {
			errs = append(errs, errors.New("mapping poll needs answer_path"))
		} else if _, err := ParsePath(p.AnswerPath); err != nil {
			errs = append(errs, fmt.Errorf("mapping poll answer_path: %v", err))
		}
		if p.IntervalS < 0 || p.IntervalS > 30 {
			errs = append(errs, fmt.Errorf("mapping poll interval_s %d: want 1 to 30", p.IntervalS))
		}
		if p.ReplyIDPath != "" {
			if _, err := ParsePath(p.ReplyIDPath); err != nil {
				errs = append(errs, fmt.Errorf("mapping poll reply_id_path: %v", err))
			}
		}
	}
	return errors.Join(errs...)
}

func usesStartID(m *schemas.RemoteMapping) bool {
	s := m.Body
	if m.Poll != nil {
		s += m.Poll.URL
	}
	if m.End != nil {
		s += m.End.URL + m.End.Body
	}
	return strings.Contains(s, "start.id")
}

func checkHeaders(label string, h map[string]string) error {
	if len(h) > MaxHeaders {
		return fmt.Errorf("%s: at most %d", label, MaxHeaders)
	}
	var errs []error
	for _, k := range sortedKeys(h) {
		v := h[k]
		if !headerNameRe.MatchString(k) {
			errs = append(errs, fmt.Errorf("%s: %q is not a header name", label, k))
			continue
		}
		if reservedHeaders[strings.ToLower(k)] {
			errs = append(errs, fmt.Errorf("%s: %s is set by Heimdall", label, k))
		}
		if strings.ContainsAny(v, "\r\n\x00") || len(v) > 4096 {
			errs = append(errs, fmt.Errorf("%s: the value of %s holds a line break or is over 4096 characters", label, k))
		}
		if err := CheckPlaceholders(v, true); err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %v", label, k, err))
		}
	}
	return errors.Join(errs...)
}

func checkQuery(q map[string]string) error {
	if len(q) > MaxHeaders {
		return fmt.Errorf("mapping query: at most %d parameters", MaxHeaders)
	}
	var errs []error
	for _, k := range sortedKeys(q) {
		if !queryNameRe.MatchString(k) {
			errs = append(errs, fmt.Errorf("mapping query: %q is not a parameter name", k))
		}
		if err := CheckPlaceholders(q[k], true); err != nil {
			errs = append(errs, fmt.Errorf("mapping query %s: %v", k, err))
		}
	}
	return errors.Join(errs...)
}

func checkBody(label, body string) error {
	if body == "" {
		return nil
	}
	if len(body) > 64<<10 {
		return fmt.Errorf("%s is over 64 KiB", label)
	}
	if err := CheckPlaceholders(body, false); err != nil {
		return fmt.Errorf("%s: %v", label, err)
	}
	if _, err := RenderBody(body, sampleVars()); err != nil {
		return fmt.Errorf("%s: %v", label, err)
	}
	return nil
}

// checkStepURL refuses a step URL that leaves the agent URL's host.
func checkStepURL(label, raw string, base *url.URL) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%s needs url", label)
	}
	if err := CheckPlaceholders(raw, false); err != nil {
		return fmt.Errorf("%s url: %v", label, err)
	}
	u, err := RenderURL(raw, base, sampleVars())
	if err != nil {
		return fmt.Errorf("%s url: %v", label, err)
	}
	if !SameOrigin(base, u) {
		return fmt.Errorf("%s url %q is not on the agent URL's host %s: every request goes to that host only", label, raw, base.Host)
	}
	return nil
}

// SameOrigin reports whether a and b have the same scheme, host and port.
func SameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && portOf(a) == portOf(b)
}

func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// EgressTargets are the host:port destinations a run needs: the agent
// URL's, then the token endpoint's when the auth block fetches tokens from
// another host.
func EgressTargets(r *schemas.RemoteAgent) ([]string, error) {
	u, err := url.Parse(r.URL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("remote url %q is not an absolute URL", r.URL)
	}
	out := []string{net.JoinHostPort(u.Hostname(), portOf(u))}
	if t := TokenURL(r.Auth); t != "" {
		tu, err := url.Parse(t)
		if err != nil || tu.Hostname() == "" {
			return nil, fmt.Errorf("remote.auth token URL %q is not an absolute URL", t)
		}
		if hp := net.JoinHostPort(tu.Hostname(), portOf(tu)); hp != out[0] {
			out = append(out, hp)
		}
	}
	return out, nil
}

// sampleVars are the values checks and previews render with.
func sampleVars() *Vars {
	return &Vars{Instruction: SampleInstruction, SessionID: "heimdall-sample", SessionIndex: 1, Output: "answer",
		StartID: "start-id", ReplyID: "reply-id", NewID: func() string { return "00000000000000000000000000000000" }}
}

// SampleInstruction is the task a preview renders.
const SampleInstruction = "This is a connection test from Heimdall. Reply with the single word READY."
