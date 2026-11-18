package traceinbox

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Files under the inbox folder.
const (
	Dir          = "inbox"        // under the project root
	SourcesFile  = "sources.yaml" // the sources configuration
	ItemsFile    = "items.jsonl"  // one item per line
	CursorsFile  = "cursors.json" // per source: how far the last pull got
	LogFile      = "log.jsonl"    // pulls and state changes, appended
	ItemsDir     = "items"        // one folder per item
	SessionFile  = "session.json" // the redacted session of the first occurrence
	PreviewFile  = "preview.md"   // the redacted transcript preview
	lockFileName = ".lock"
)

// Defaults.
const (
	DefaultWindow  = 24 * time.Hour   // how far back the first pull of a source looks
	DefaultOverlap = 15 * time.Minute // how far before the cursor a pull starts again, for late data
	DefaultRate    = 60               // webhook pushes per minute
	DefaultMaxBody = 1 << 20          // webhook body bytes
	MaxWebhookBody = 8 << 20          // the largest webhook body a config may allow
)

var sourceName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// envName matches an environment variable name. Secrets are only ever named
// by variable, never written in the file.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// Config is inbox/sources.yaml.
type Config struct {
	Sources []Source `yaml:"sources"`
	// Schedule is the cron of the scheduled pull, run by the schedule daemon
	// as a job named inbox-pull (docs/failure-inbox.md). Empty: no scheduled
	// pull.
	Schedule string `yaml:"schedule,omitempty"`
}

// Source is one place flagged sessions come from. Fields apply per kind; a
// field another kind uses is an error, so a misplaced filter never passes
// silently.
type Source struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`

	// Langfuse and LangSmith: the API host.
	Host string `yaml:"host,omitempty"`
	// Langfuse: basic auth from these variables.
	PublicKeyEnv string `yaml:"public_key_env,omitempty"`
	SecretKeyEnv string `yaml:"secret_key_env,omitempty"`
	// LangSmith: X-API-Key from this variable; Bifrost: the management token
	// (default: the bench config's gateway.admin_token_env); webhook: the
	// push token.
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
	TokenEnv  string `yaml:"token_env,omitempty"`
	// Project: Langfuse project id (links only); LangSmith project id.
	Project     string `yaml:"project,omitempty"`
	Workspace   string `yaml:"workspace,omitempty"`   // LangSmith X-Tenant-Id
	Environment string `yaml:"environment,omitempty"` // Langfuse environment filter

	// Bifrost: the gateway (default: the bench config's gateway.url) and the
	// UI base for links (default: the gateway URL).
	URL         string   `yaml:"url,omitempty"`
	UIURL       string   `yaml:"ui_url,omitempty"`
	VirtualKeys []string `yaml:"virtual_keys,omitempty"`

	// OTLP folder.
	Path    string `yaml:"path,omitempty"`
	Pattern string `yaml:"pattern,omitempty"`

	// Webhook.
	RatePerMinute int   `yaml:"rate_per_minute,omitempty"`
	MaxBytes      int64 `yaml:"max_bytes,omitempty"`

	// Link is the URL template back to the original trace: {host},
	// {project}, {trace_id}, {url}, {ui_url} are replaced (URL-escaped).
	Link string `yaml:"link,omitempty"`
	// Suite suggested for items from this source.
	Suite string `yaml:"suite,omitempty"`
	// MaxPerPull caps the sessions one pull fetches (default 200).
	MaxPerPull int     `yaml:"max_per_pull,omitempty"`
	Filters    Filters `yaml:"filters,omitempty"`
}

// Filters say which sessions are flagged. Each source reads the ones its
// kind supports.
type Filters struct {
	Scores     []ScoreFilter   `yaml:"scores,omitempty"`      // langfuse: score name below a value or in a set
	Feedback   *FeedbackFilter `yaml:"feedback,omitempty"`    // langfuse, langsmith: negative user feedback
	LevelError bool            `yaml:"level_error,omitempty"` // langfuse: an observation at level ERROR
	Tags       []string        `yaml:"tags,omitempty"`        // langfuse: traces carrying any of these tags
	Errors     bool            `yaml:"errors,omitempty"`      // langsmith, bifrost, otlp_dir: errored runs, requests or spans
	MinRetries int             `yaml:"min_retries,omitempty"` // bifrost: requests retried at least this often
	Fallbacks  bool            `yaml:"fallbacks,omitempty"`   // bifrost: requests served by a fallback
	Budget     bool            `yaml:"budget,omitempty"`      // bifrost: requests refused by a budget
	Window     Duration        `yaml:"window,omitempty"`      // first pull lookback (default 24h)
	Overlap    Duration        `yaml:"overlap,omitempty"`     // re-read before the cursor (default 15m)
}

// ScoreFilter flags a session with a score of this name below Below, or
// equal to one of Values (compared as text).
type ScoreFilter struct {
	Name   string   `yaml:"name"`
	Below  *float64 `yaml:"below,omitempty"`
	Values []string `yaml:"values,omitempty"`
}

// FeedbackFilter flags negative user feedback: a Langfuse score or a
// LangSmith feedback key named Name, below Below or equal to one of
// Negative.
type FeedbackFilter struct {
	Name     string   `yaml:"name"`
	Below    *float64 `yaml:"below,omitempty"`
	Negative []string `yaml:"negative,omitempty"`
}

// Duration is a YAML duration: Go syntax ("90m", "24h") or whole days ("7d").
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseDuration(n.Value)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// ParseDuration reads "7d" or a Go duration.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 || n > 3650 {
			return 0, fmt.Errorf("duration %q: want whole days like 7d", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	v, err := time.ParseDuration(s)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("duration %q: want 90m, 24h or 7d", s)
	}
	return v, nil
}

// LoadConfig reads inbox/sources.yaml strictly: an unknown key is an error,
// so a misspelled filter never passes as no filter. A missing file is an
// empty configuration: nothing to pull, and the webhook is off.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read inbox sources %s: %w", path, err)
	}
	c := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse inbox sources %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("inbox sources %s: %w", path, err)
	}
	return c, nil
}

// Find returns the source named name.
func (c *Config) Find(name string) (*Source, bool) {
	for i := range c.Sources {
		if c.Sources[i].Name == name {
			return &c.Sources[i], true
		}
	}
	return nil, false
}

// Webhook returns the webhook source, if one is configured.
func (c *Config) Webhook() (*Source, bool) {
	for i := range c.Sources {
		if c.Sources[i].Kind == KindWebhook {
			return &c.Sources[i], true
		}
	}
	return nil, false
}

// Validate checks every source: names unique and well formed, the fields of
// its kind present, no field of another kind, secrets named by variable.
func (c *Config) Validate() error {
	var errs []error
	seen := map[string]bool{}
	webhooks := 0
	for i := range c.Sources {
		s := &c.Sources[i]
		if seen[s.Name] {
			errs = append(errs, fmt.Errorf("source %q appears more than once", s.Name))
		}
		seen[s.Name] = true
		if s.Kind == KindWebhook {
			webhooks++
		}
		if err := s.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	if webhooks > 1 {
		errs = append(errs, errors.New("more than one webhook source; the ingest endpoint has one token"))
	}
	return errors.Join(errs...)
}

// Validate checks one source (see Config.Validate).
func (s *Source) Validate() error {
	wrap := func(format string, args ...any) error {
		return fmt.Errorf("source %s: "+format, append([]any{s.Name}, args...)...)
	}
	if !sourceName.MatchString(s.Name) {
		return fmt.Errorf("source %q: name must be lowercase letters, digits, - or _, at most 64 characters", s.Name)
	}
	for field, v := range map[string]string{"public_key_env": s.PublicKeyEnv, "secret_key_env": s.SecretKeyEnv, "api_key_env": s.APIKeyEnv, "token_env": s.TokenEnv} {
		if v != "" && !envName.MatchString(v) {
			return wrap("%s %q is not an environment variable name; secrets are named by variable, never written here", field, v)
		}
	}
	if s.MaxPerPull < 0 || s.MaxPerPull > 5000 {
		return wrap("max_per_pull %d: want 1 to 5000", s.MaxPerPull)
	}
	used := s.usedFields()
	allowed := map[string][]string{
		KindLangfuse:  {"host", "public_key_env", "secret_key_env", "project", "environment", "scores", "feedback", "level_error", "tags"},
		KindLangSmith: {"host", "api_key_env", "project", "workspace", "feedback", "errors"},
		KindBifrost:   {"url", "ui_url", "token_env", "virtual_keys", "errors", "min_retries", "fallbacks", "budget"},
		KindOTLPDir:   {"path", "pattern", "errors"},
		KindWebhook:   {"token_env", "rate_per_minute", "max_bytes"},
	}
	ok, known := allowed[s.Kind]
	if !known {
		return wrap("kind %q: want langfuse, langsmith, bifrost, otlp_dir or webhook", s.Kind)
	}
	for _, f := range used {
		if !slices.Contains(ok, f) {
			return wrap("%s does not apply to a %s source", f, s.Kind)
		}
	}
	for _, h := range []string{s.Host, s.URL, s.UIURL} {
		if h == "" {
			continue
		}
		u, err := url.Parse(h)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return wrap("%q is not an http(s) URL", h)
		}
		if u.User != nil {
			return wrap("%q carries credentials; name them by environment variable instead", h)
		}
	}
	for _, f := range s.Filters.Scores {
		if f.Name == "" || (f.Below == nil && len(f.Values) == 0) {
			return wrap("a score filter needs a name and below or values")
		}
	}
	if fb := s.Filters.Feedback; fb != nil && (fb.Name == "" || (fb.Below == nil && len(fb.Negative) == 0)) {
		return wrap("feedback needs a name and below or negative")
	}
	switch s.Kind {
	case KindLangfuse:
		if s.Host == "" || s.PublicKeyEnv == "" || s.SecretKeyEnv == "" {
			return wrap("a langfuse source needs host, public_key_env and secret_key_env")
		}
		if len(s.Filters.Scores) == 0 && s.Filters.Feedback == nil && !s.Filters.LevelError && len(s.Filters.Tags) == 0 {
			return wrap("no filter: set scores, feedback, level_error or tags, or every trace would be pulled")
		}
	case KindLangSmith:
		if s.Host == "" || s.APIKeyEnv == "" || s.Project == "" {
			return wrap("a langsmith source needs host, api_key_env and project (the project id)")
		}
		if s.Filters.Feedback == nil && !s.Filters.Errors {
			return wrap("no filter: set feedback or errors")
		}
		if fb := s.Filters.Feedback; fb != nil && fb.Below == nil {
			return wrap("langsmith feedback needs below (feedback scores are numeric)")
		}
	case KindBifrost:
		if !s.Filters.Errors && s.Filters.MinRetries <= 0 && !s.Filters.Fallbacks && !s.Filters.Budget {
			return wrap("no filter: set errors, min_retries, fallbacks or budget")
		}
	case KindOTLPDir:
		if s.Path == "" {
			return wrap("an otlp_dir source needs path")
		}
		if s.Pattern != "" {
			if _, err := matchPattern(s.Pattern, "x"); err != nil {
				return wrap("pattern %q: %v", s.Pattern, err)
			}
		}
	case KindWebhook:
		if s.TokenEnv == "" {
			return wrap("a webhook source needs token_env")
		}
		if s.RatePerMinute < 0 || s.RatePerMinute > 6000 {
			return wrap("rate_per_minute %d: want 1 to 6000", s.RatePerMinute)
		}
		if s.MaxBytes < 0 || s.MaxBytes > MaxWebhookBody {
			return wrap("max_bytes %d: want at most %d", s.MaxBytes, MaxWebhookBody)
		}
	}
	return nil
}

// usedFields names the kind-specific fields a source sets.
func (s *Source) usedFields() []string {
	var out []string
	add := func(name string, set bool) {
		if set {
			out = append(out, name)
		}
	}
	add("host", s.Host != "")
	add("public_key_env", s.PublicKeyEnv != "")
	add("secret_key_env", s.SecretKeyEnv != "")
	add("api_key_env", s.APIKeyEnv != "")
	add("token_env", s.TokenEnv != "")
	add("project", s.Project != "")
	add("workspace", s.Workspace != "")
	add("environment", s.Environment != "")
	add("url", s.URL != "")
	add("ui_url", s.UIURL != "")
	add("virtual_keys", len(s.VirtualKeys) > 0)
	add("path", s.Path != "")
	add("pattern", s.Pattern != "")
	add("rate_per_minute", s.RatePerMinute != 0)
	add("max_bytes", s.MaxBytes != 0)
	f := s.Filters
	add("scores", len(f.Scores) > 0)
	add("feedback", f.Feedback != nil)
	add("level_error", f.LevelError)
	add("tags", len(f.Tags) > 0)
	add("errors", f.Errors)
	add("min_retries", f.MinRetries != 0)
	add("fallbacks", f.Fallbacks)
	add("budget", f.Budget)
	return out
}

// maxPerPull is the source's cap.
func (s *Source) maxPerPull() int {
	if s.MaxPerPull > 0 {
		return s.MaxPerPull
	}
	return DefaultMaxPerPull
}

// window is the first pull's lookback.
func (s *Source) window() time.Duration {
	if s.Filters.Window > 0 {
		return time.Duration(s.Filters.Window)
	}
	return DefaultWindow
}

// overlap is how far before the cursor a pull reads again.
func (s *Source) overlap() time.Duration {
	if s.Filters.Overlap > 0 {
		return time.Duration(s.Filters.Overlap)
	}
	return DefaultOverlap
}

// LinkFor renders the source's link template for one trace. Every value is
// path-escaped; an empty template gives "".
func (s *Source) LinkFor(traceID string, extra map[string]string) string {
	tmpl := s.Link
	if tmpl == "" {
		tmpl = defaultLink(s)
	}
	if tmpl == "" {
		return ""
	}
	vals := map[string]string{"{trace_id}": url.PathEscape(traceID), "{project}": url.PathEscape(s.Project),
		"{host}": strings.TrimRight(s.Host, "/"), "{url}": strings.TrimRight(s.URL, "/"), "{ui_url}": strings.TrimRight(firstNonEmpty(s.UIURL, s.URL), "/")}
	for k, v := range extra {
		vals["{"+k+"}"] = url.QueryEscape(v)
	}
	out := tmpl
	for k, v := range vals {
		out = strings.ReplaceAll(out, k, v)
	}
	if u, err := url.Parse(out); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return out
}

// defaultLink is each kind's link when the source sets none. Langfuse's is
// the UI's trace page (from the Langfuse UI, not its API reference);
// Bifrost's is the logs page with the row selected (ui/app/workspace/logs in
// this repository). LangSmith's UI path needs the organization, so it has
// no default.
func defaultLink(s *Source) string {
	switch s.Kind {
	case KindLangfuse:
		if s.Project == "" {
			return ""
		}
		return "{host}/project/{project}/traces/{trace_id}"
	case KindBifrost:
		return "{ui_url}/workspace/logs?selected_log={row_id}"
	}
	return ""
}
