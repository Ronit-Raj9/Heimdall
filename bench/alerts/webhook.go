// Package alerts delivers webhook alerts about scheduled experiments: a
// regressed verdict, a release policy block, an invalid experiment, runs that
// ended in infrastructure errors, and a schedule that could not run. The
// receiver URL and the signing secret always come from environment
// variables named in the config, never from the config itself, so no secret
// is ever written to a file. Delivery is retried with backoff and every
// attempt is logged without the URL or the secret.
package alerts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Event names an alert can subscribe to.
const (
	EventVerdictRegressed  = "verdict_regressed"
	EventPolicyBlock       = "policy_block"
	EventExperimentInvalid = "experiment_invalid"
	EventRunErrors         = "run_errors"
	EventScheduleFailed    = "schedule_failed"
	// W5 drift canary (docs/drift-canary.md): a confirmed drift against the
	// baseline, and a served model that changed under the same alias.
	EventCanaryDrifted      = "canary_drifted"
	EventCanaryModelChanged = "canary_model_changed"
)

// Events lists every event, in the order the docs describe them.
var Events = []string{EventVerdictRegressed, EventPolicyBlock, EventExperimentInvalid, EventRunErrors, EventScheduleFailed,
	EventCanaryDrifted, EventCanaryModelChanged} // W5 drift canary

// X6 live scoring (docs/live-scoring.md): a live signal crossed its
// threshold, and a live comparison decided the candidate worse. The numbers
// are in Message and Changes.
const (
	EventLiveThreshold = "live_signal_threshold"
	EventLiveWorse     = "live_comparison_worse"
)

func init() { Events = append(Events, EventLiveThreshold, EventLiveWorse) } // X6 live scoring

// Payload formats.
const (
	FormatJSON  = "json"  // the Payload object
	FormatSlack = "slack" // {"text": "..."}, accepted by Slack incoming webhooks and compatible receivers
)

// Headers set on every delivery.
const (
	HeaderEvent     = "X-Heimdall-Event"
	HeaderSignature = "X-Heimdall-Signature" // "sha256=<hex HMAC-SHA256 of the body>", only with a secret
	HeaderDelivery  = "X-Heimdall-Delivery"  // attempt number, 1-based
)

// Delivery defaults.
const (
	DefaultTimeout = 10 * time.Second
	MaxRetries     = 3 // retries after the first attempt
	DefaultBackoff = time.Second
)

// envName is what a variable name may look like. A value with "://" or any
// other character is refused, so a URL pasted into url_env never lands in a
// committed file looking like configuration.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Config is one webhook receiver. URLEnv and SecretEnv name environment
// variables; their values are read at send time and never stored or logged.
type Config struct {
	URLEnv    string   `json:"url_env" yaml:"url_env"`                           // variable holding the receiver URL
	SecretEnv string   `json:"secret_env,omitempty" yaml:"secret_env,omitempty"` // variable holding the HMAC secret; empty sends unsigned
	Events    []string `json:"events,omitempty" yaml:"events,omitempty"`         // empty subscribes to every event
	Format    string   `json:"format,omitempty" yaml:"format,omitempty"`         // json (default) or slack
	TimeoutS  int      `json:"timeout_s,omitempty" yaml:"timeout_s,omitempty"`   // per attempt; 0 is DefaultTimeout
}

// Validate checks the config without reading any environment variable.
func (c Config) Validate() error {
	if c.URLEnv == "" {
		return errors.New("alert url_env is empty: name the environment variable that holds the webhook URL")
	}
	if !envName.MatchString(c.URLEnv) {
		return fmt.Errorf("alert url_env %q is not an environment variable name; put the URL in the environment, never in the file", redact(c.URLEnv))
	}
	if c.SecretEnv != "" && !envName.MatchString(c.SecretEnv) {
		return fmt.Errorf("alert secret_env %q is not an environment variable name; put the secret in the environment, never in the file", redact(c.SecretEnv))
	}
	for _, e := range c.Events {
		if !slices.Contains(Events, e) {
			return fmt.Errorf("alert event %q is not one of %s", e, strings.Join(Events, ", "))
		}
	}
	switch c.Format {
	case "", FormatJSON, FormatSlack:
	default:
		return fmt.Errorf("alert format %q is not json or slack", c.Format)
	}
	if c.TimeoutS < 0 {
		return fmt.Errorf("alert timeout_s %d is negative", c.TimeoutS)
	}
	return nil
}

// Wants reports whether the receiver subscribes to event.
func (c Config) Wants(event string) bool {
	return len(c.Events) == 0 || slices.Contains(c.Events, event)
}

// Payload is the JSON body of a json-format alert.
type Payload struct {
	Event      string    `json:"event"`
	Schedule   string    `json:"schedule,omitempty"`
	Experiment string    `json:"experiment,omitempty"`
	Decision   string    `json:"decision,omitempty"` // verdict decision
	Action     string    `json:"action,omitempty"`   // verdict action
	Policy     string    `json:"policy,omitempty"`   // release policy decision: allow, warn or block
	Link       string    `json:"link,omitempty"`     // path of the verdict page on the bench UI, e.g. /verdict?exp=e_x
	Summary    Summary   `json:"summary"`
	Message    string    `json:"message"` // one line in plain words; the text of a slack-format alert
	SentAt     time.Time `json:"sent_at"`
	// W5 drift canary: the canary and what moved against its baseline, one
	// line each (docs/drift-canary.md); empty for every other alert.
	Canary  string   `json:"canary,omitempty"`
	Changes []string `json:"changes,omitempty"`
}

// Summary carries the numbers behind an alert.
type Summary struct {
	Runs          int     `json:"runs"`                      // sealed counted runs of the experiment
	RunErrors     int     `json:"run_errors"`                // runs that ended infra_error or aborted_infra
	CostUSD       float64 `json:"cost_usd"`                  // spend of this scheduled run
	SpentTodayUSD float64 `json:"spent_today_usd"`           // the schedule's spend today, this run included
	MaxUSDPerDay  float64 `json:"max_usd_per_day,omitempty"` // the schedule's daily cap
}

// LinkPath is the UI path of an experiment's verdict page.
func LinkPath(expID string) string {
	if expID == "" {
		return ""
	}
	return "/verdict?exp=" + url.QueryEscape(expID)
}

// Delivery is one line of the delivery log. It names the variable that held
// the URL, never the URL.
type Delivery struct {
	At         time.Time `json:"at"`
	Event      string    `json:"event"`
	Schedule   string    `json:"schedule,omitempty"`
	Experiment string    `json:"experiment,omitempty"`
	Target     string    `json:"target"` // the url_env variable name
	Attempt    int       `json:"attempt"`
	Status     int       `json:"status,omitempty"` // HTTP status; 0 when no response came back
	Delivered  bool      `json:"delivered"`
	Error      string    `json:"error,omitempty"`
}

// Sender delivers alerts. The zero value is not ready; use NewSender.
type Sender struct {
	logPath string
	client  *http.Client
	getenv  func(string) string
	sleep   func(context.Context, time.Duration) error
	now     func() time.Time
	backoff time.Duration
	mu      sync.Mutex // serializes delivery log appends
}

// SenderOption configures a Sender, mainly for tests.
type SenderOption func(*Sender)

// WithGetenv replaces os.Getenv.
func WithGetenv(f func(string) string) SenderOption { return func(s *Sender) { s.getenv = f } }

// WithSleep replaces the backoff wait.
func WithSleep(f func(context.Context, time.Duration) error) SenderOption {
	return func(s *Sender) { s.sleep = f }
}

// WithClock replaces time.Now.
func WithClock(f func() time.Time) SenderOption { return func(s *Sender) { s.now = f } }

// WithClient replaces the HTTP client. Its timeout is ignored; each attempt
// carries the config's timeout on its context.
func WithClient(c *http.Client) SenderOption { return func(s *Sender) { s.client = c } }

// NewSender returns a sender that appends every attempt to logPath (JSON
// lines). An empty logPath keeps no log.
func NewSender(logPath string, opts ...SenderOption) *Sender {
	s := &Sender{
		logPath: logPath,
		client:  &http.Client{CheckRedirect: noRedirect},
		getenv:  os.Getenv,
		sleep:   sleepCtx,
		now:     time.Now,
		backoff: DefaultBackoff,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Send delivers p to every receiver in cfgs that subscribes to p.Event, one
// after another, and returns every failure joined. A failure to deliver
// never stops the next receiver.
func (s *Sender) Send(ctx context.Context, cfgs []Config, p Payload) error {
	if p.SentAt.IsZero() {
		p.SentAt = s.now().UTC()
	}
	var errs []error
	for _, c := range cfgs {
		if !c.Wants(p.Event) {
			continue
		}
		if err := s.deliver(ctx, c, p); err != nil {
			errs = append(errs, fmt.Errorf("alert %s to %s: %w", p.Event, c.URLEnv, err))
		}
	}
	return errors.Join(errs...)
}

// deliver sends p to one receiver: the first attempt and up to MaxRetries
// retries, waiting backoff, 2*backoff, 4*backoff between them. A network
// error, a 429 or a 5xx is retried; any other status is final. A missing URL
// or secret, or a URL that is not allowed, fails before anything is sent.
func (s *Sender) deliver(ctx context.Context, c Config, p Payload) error {
	rec := Delivery{Event: p.Event, Schedule: p.Schedule, Experiment: p.Experiment, Target: c.URLEnv}
	target, body, headers, err := s.prepare(c, p)
	if err != nil {
		rec.At, rec.Error = s.now().UTC(), err.Error()
		return errors.Join(err, s.log(rec))
	}
	timeout := DefaultTimeout
	if c.TimeoutS > 0 {
		timeout = time.Duration(c.TimeoutS) * time.Second
	}
	wait := s.backoff
	var last, logErr error
	for attempt := 1; attempt <= MaxRetries+1; attempt++ {
		if attempt > 1 {
			if err := s.sleep(ctx, wait); err != nil {
				return errors.Join(last, err, logErr)
			}
			wait *= 2
		}
		status, retry, err := s.post(ctx, target, body, headers, attempt, timeout)
		rec.At, rec.Attempt, rec.Status = s.now().UTC(), attempt, status
		rec.Delivered, rec.Error = err == nil, ""
		if err != nil {
			rec.Error = err.Error()
		}
		logErr = errors.Join(logErr, s.log(rec))
		if err == nil {
			return logErr
		}
		last = err
		if !retry {
			return errors.Join(err, logErr)
		}
	}
	return errors.Join(fmt.Errorf("gave up after %d attempts: %w", MaxRetries+1, last), logErr)
}

// prepare resolves the URL and secret from the environment and builds the
// body and headers.
func (s *Sender) prepare(c Config, p Payload) (string, []byte, http.Header, error) {
	if err := c.Validate(); err != nil {
		return "", nil, nil, err
	}
	raw := strings.TrimSpace(s.getenv(c.URLEnv))
	if raw == "" {
		return "", nil, nil, fmt.Errorf("environment variable %s is not set", c.URLEnv)
	}
	if err := CheckURL(raw); err != nil {
		return "", nil, nil, fmt.Errorf("URL in %s: %w", c.URLEnv, err)
	}
	var body []byte
	var err error
	if c.Format == FormatSlack {
		body, err = json.Marshal(map[string]string{"text": SlackText(p)})
	} else {
		body, err = json.Marshal(p)
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("encode alert: %w", err)
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "heimdall-alerts")
	h.Set(HeaderEvent, p.Event)
	if c.SecretEnv != "" {
		secret := s.getenv(c.SecretEnv)
		if secret == "" {
			// Never fall back to unsigned: the receiver expects a signature.
			return "", nil, nil, fmt.Errorf("environment variable %s (secret_env) is not set; not sending unsigned", c.SecretEnv)
		}
		h.Set(HeaderSignature, Sign([]byte(secret), body))
	}
	return raw, body, h, nil
}

// post makes one attempt. It reports the status, whether the failure may be
// retried, and an error that never contains the URL.
func (s *Sender) post(ctx context.Context, target string, body []byte, headers http.Header, attempt int, timeout time.Duration) (int, bool, error) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, false, errors.New("build request: invalid URL")
	}
	req.Header = headers.Clone()
	req.Header.Set(HeaderDelivery, fmt.Sprint(attempt))
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, false, ctx.Err()
		}
		return 0, true, scrub(err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if err := resp.Body.Close(); err != nil {
		return resp.StatusCode, true, errors.New("read response: " + scrub(err).Error())
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return resp.StatusCode, false, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return resp.StatusCode, true, fmt.Errorf("receiver answered %d", resp.StatusCode)
	default:
		return resp.StatusCode, false, fmt.Errorf("receiver answered %d", resp.StatusCode)
	}
}

// log appends one delivery record. A log that cannot be written is
// reported to the caller beside the delivery result, never instead of it.
func (s *Sender) log(d Delivery) error {
	if s.logPath == "" {
		return nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode delivery log: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.logPath), 0o755); err != nil {
		return fmt.Errorf("delivery log: %w", err)
	}
	f, err := os.OpenFile(s.logPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("delivery log: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return errors.Join(fmt.Errorf("delivery log: %w", err), f.Close())
	}
	return f.Close()
}

// Sign returns the signature header value for body: "sha256=" and the hex
// HMAC-SHA256 of body under secret.
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Verify reports whether header is the signature of body under secret, in
// constant time. Receivers written in Go can use it as is.
func Verify(secret, body []byte, header string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(header))
}

// CheckURL allows https to any host and plain http only to a loopback host
// (localhost, 127.0.0.0/8, ::1), so an alert is never sent in clear text over
// a network. User info in the URL is refused: a secret goes in secret_env.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("not a valid URL")
	}
	if u.User != nil {
		return errors.New("user info in the URL is not allowed")
	}
	if u.Host == "" {
		return errors.New("URL has no host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return errors.New("plain http is only allowed to a loopback host; use https")
	default:
		return fmt.Errorf("scheme %q is not http or https", u.Scheme)
	}
}

// SlackText is the one-line text of a slack-format alert.
func SlackText(p Payload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[heimdall] %s", p.Event)
	if p.Schedule != "" {
		fmt.Fprintf(&b, " schedule %s", p.Schedule)
	}
	if p.Experiment != "" {
		fmt.Fprintf(&b, " experiment %s", p.Experiment)
	}
	if p.Decision != "" {
		fmt.Fprintf(&b, ": verdict %s", p.Decision)
		if p.Action != "" {
			fmt.Fprintf(&b, " (%s)", p.Action)
		}
	}
	if p.Policy != "" {
		fmt.Fprintf(&b, ", policy %s", p.Policy)
	}
	fmt.Fprintf(&b, ", %d runs, %d run errors, cost %.4f USD", p.Summary.Runs, p.Summary.RunErrors, p.Summary.CostUSD)
	if p.Message != "" {
		fmt.Fprintf(&b, ". %s", p.Message)
	}
	if p.Link != "" {
		fmt.Fprintf(&b, " %s", p.Link)
	}
	return b.String()
}

// isLoopback reports whether host is localhost or a loopback address.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// scrub turns a client error into one without the URL: *url.Error carries
// the full URL, so only its cause is kept, and a dial error keeps only the
// system call failure, not the address.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return errors.New("request timed out")
		}
		err = ue.Err
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		return fmt.Errorf("%s failed: %v", oe.Op, oe.Err)
	}
	var de *net.DNSError
	if errors.As(err, &de) {
		return errors.New("host name lookup failed")
	}
	return errors.New("request failed: " + strings.SplitN(err.Error(), "\"", 2)[0])
}

// redact shortens a value that may be a secret pasted into a name field.
func redact(v string) string {
	if len(v) > 8 {
		return v[:4] + "..."
	}
	return v
}

// noRedirect refuses redirects, so a receiver cannot bounce an alert (and its
// signature) to another host or to plain http.
func noRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
