package livescore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// Config is livescore/config.yaml.
type Config struct {
	Source     Source          `yaml:"source"`
	Versions   []Version       `yaml:"versions"`
	Compare    *CompareConfig  `yaml:"compare,omitempty"`
	Sessions   SessionRules    `yaml:"sessions,omitempty"`
	Sampling   Sampling        `yaml:"sampling,omitempty"`
	Retention  Retention       `yaml:"retention,omitempty"`
	Thresholds []Threshold     `yaml:"thresholds,omitempty"`
	Alerts     []alerts.Config `yaml:"alerts,omitempty"`
	Inbox      InboxPush       `yaml:"inbox,omitempty"`
	Judge      *JudgeConfig    `yaml:"judge,omitempty"`
	Schedule   string          `yaml:"schedule,omitempty"` // cron of the scheduled pull (job livescore-pull)
}

// Source is the gateway to read. The management credential is named by
// environment variable only; it is sent as Bifrost's setup token header on
// GET requests to the log API and nowhere else.
type Source struct {
	URL      string              `yaml:"url,omitempty"`       // default: the bench config's gateway.url
	UIURL    string              `yaml:"ui_url,omitempty"`    // links to the Bifrost logs page; default: url
	TokenEnv string              `yaml:"token_env,omitempty"` // default: the bench config's gateway.admin_token_env
	Window   traceinbox.Duration `yaml:"window,omitempty"`    // first pull lookback (default 24h)
	Overlap  traceinbox.Duration `yaml:"overlap,omitempty"`   // re-read before the cursor for late rows (default 15m)
}

// Version is one live agent version and the selector that finds its
// traffic. Every field set must match (and); the values of one field are
// alternatives (or). At least one field is required.
type Version struct {
	Name        string     `yaml:"name"`
	VirtualKeys []string   `yaml:"virtual_keys,omitempty"` // Bifrost virtual key ids
	Teams       []string   `yaml:"teams,omitempty"`        // team ids
	Customers   []string   `yaml:"customers,omitempty"`    // customer ids
	Models      []string   `yaml:"models,omitempty"`       // model or provider/model
	Tag         *HeaderTag `yaml:"tag,omitempty"`          // a request header the agent sends, logged as metadata
}

// HeaderTag selects rows by a header the agent sends on every request. With
// header x-bf-lh-<name> Bifrost logs the value as metadata key <name> with
// no gateway setting; any other header must be listed in Bifrost's
// client.logging_headers and is logged under its own name.
type HeaderTag struct {
	Header string `yaml:"header"`
	Value  string `yaml:"value"`
}

// MetadataKey is the key Bifrost logs the header under.
func (t *HeaderTag) MetadataKey() string {
	h := strings.ToLower(strings.TrimSpace(t.Header))
	if k, ok := strings.CutPrefix(h, "x-bf-lh-"); ok {
		return k
	}
	return h
}

// CompareConfig names the two versions a comparison tests: Baseline is the
// version in production, Candidate the new one.
type CompareConfig struct {
	Baseline  string   `yaml:"baseline"`
	Candidate string   `yaml:"candidate"`
	Alpha     float64  `yaml:"alpha,omitempty"`     // false-alarm rate over every signal and every look, default 0.05
	MinPairs  int      `yaml:"min_pairs,omitempty"` // pairs before a signal may decide, default 30
	Signals   []string `yaml:"signals,omitempty"`   // default: every comparable signal
}

// SessionRules say how rows become sessions.
type SessionRules struct {
	Gap traceinbox.Duration `yaml:"gap,omitempty"` // idle time that ends a session (default 10m)
}

// Sampling says which sessions are scored. Rate is the share of sessions
// (0 to 1, default 1), chosen by a hash of the session key, so a session is
// either scored whole or not at all, and the same session is chosen on every
// pull.
type Sampling struct {
	Rate *float64 `yaml:"rate,omitempty"`
}

// Retention bounds what is kept: scored sessions older than Days are
// dropped at each pull.
type Retention struct {
	Days int `yaml:"days,omitempty"` // default 30, at most 400
}

// Threshold alerts when a signal's mean over the window crosses a level. It
// is a monitoring rule, not a statistical test: it alerts on the observed
// mean of at least MinSessions sessions.
type Threshold struct {
	Signal      string              `yaml:"signal"`
	Version     string              `yaml:"version,omitempty"` // empty: every version
	Above       *float64            `yaml:"above,omitempty"`
	Below       *float64            `yaml:"below,omitempty"`
	Window      traceinbox.Duration `yaml:"window,omitempty"`       // default 24h
	MinSessions int                 `yaml:"min_sessions,omitempty"` // default 20
}

// InboxPush sends the worst failing sessions to the failure inbox.
type InboxPush struct {
	Disabled   bool   `yaml:"disabled,omitempty"`
	MaxPerPull int    `yaml:"max_per_pull,omitempty"` // failure signatures per pull, default 5
	Suite      string `yaml:"suite,omitempty"`        // suggested suite for the drafts
}

// JudgeConfig is the optional judge, run on a sample of sessions through an
// OpenAI-compatible chat endpoint (the gateway itself, with its own virtual
// key). Its score is reported; it decides nothing unless a calibration
// record of the bench's judge calibration (docs/judge-calibration.md) covers
// the exact model and prompt.
type JudgeConfig struct {
	URL         string  `yaml:"url"`                   // base URL of the chat completions API, e.g. http://127.0.0.1:18080/v1
	KeyEnv      string  `yaml:"key_env"`               // variable holding the judge's key (a virtual key with a budget)
	Model       string  `yaml:"model"`                 // e.g. openai/gpt-5.4-mini
	PromptFile  string  `yaml:"prompt_file,omitempty"` // relative to the project root; default: the built-in prompt
	SampleRate  float64 `yaml:"sample_rate"`           // share of scored sessions judged, 0 to 1
	MaxPerPull  int     `yaml:"max_per_pull,omitempty"`
	Calibration string  `yaml:"calibration,omitempty"` // a judged suite folder whose calibration records may cover this judge
}

var (
	versionName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	envName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	headerName  = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)
)

// LoadConfig reads livescore/config.yaml strictly: an unknown key is an
// error, so a misspelled selector never passes as no selector.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("read live scoring config %s: %w", path, err)
	}
	return ParseConfig(data, path)
}

// ParseConfig parses and validates a config's bytes; name labels errors.
func ParseConfig(data []byte, name string) (*Config, error) {
	c := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse live scoring config %s: %w", name, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("live scoring config %s: %w", name, err)
	}
	return c, nil
}

// Validate checks the whole config. Every refusal names the field.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	placeholders := 0
	if c.Source.URL != "" {
		if err := checkHTTPURL(c.Source.URL); err != nil {
			add("source.url: %v", err)
		}
	}
	if c.Source.UIURL != "" {
		if err := checkHTTPURL(c.Source.UIURL); err != nil {
			add("source.ui_url: %v", err)
		}
	}
	if c.Source.TokenEnv != "" && !envName.MatchString(c.Source.TokenEnv) {
		add("source.token_env %q is not an environment variable name; the credential is named by variable, never written here", redactValue(c.Source.TokenEnv))
	}
	if len(c.Versions) == 0 {
		add("versions: name at least one version and its selector")
	}
	seen := map[string]bool{}
	for i := range c.Versions {
		v := &c.Versions[i]
		if !versionName.MatchString(v.Name) {
			add("versions[%d]: name %q must be lowercase letters, digits, '.', '-' or '_', at most 64 characters", i, v.Name)
			continue
		}
		if seen[v.Name] {
			add("versions: %s appears more than once", v.Name)
		}
		seen[v.Name] = true
		if len(v.VirtualKeys) == 0 && len(v.Teams) == 0 && len(v.Customers) == 0 && len(v.Models) == 0 && v.Tag == nil {
			add("version %s: no selector; set virtual_keys, teams, customers, models or tag (a version that matches every row compares nothing)", v.Name)
		}
		for field, list := range map[string][]string{"virtual_keys": v.VirtualKeys, "teams": v.Teams, "customers": v.Customers, "models": v.Models} {
			for _, s := range list {
				switch {
				case strings.TrimSpace(s) == "" || strings.ContainsAny(s, ",\n"):
					add("version %s: %s value %q is empty or holds a comma", v.Name, field, s)
				case strings.Contains(s, "EDIT ME"):
					placeholders++
					add("version %s: %s is still the template's EDIT ME placeholder", v.Name, field)
				}
			}
		}
		if t := v.Tag; t != nil {
			if !headerName.MatchString(t.Header) {
				add("version %s: tag.header %q is not a header name", v.Name, t.Header)
			} else if t.MetadataKey() == "" {
				add("version %s: tag.header %q names no metadata key", v.Name, t.Header)
			}
			if strings.TrimSpace(t.Value) == "" {
				add("version %s: tag.value is empty", v.Name)
			}
		}
	}
	if cc := c.Compare; cc != nil {
		if !seen[cc.Baseline] {
			add("compare.baseline %q is not a configured version", cc.Baseline)
		}
		if !seen[cc.Candidate] {
			add("compare.candidate %q is not a configured version", cc.Candidate)
		}
		if cc.Baseline != "" && cc.Baseline == cc.Candidate {
			add("compare: baseline and candidate are both %s", cc.Baseline)
		}
		if cc.Alpha != 0 && (cc.Alpha <= 0 || cc.Alpha > 0.2) {
			add("compare.alpha %v: want more than 0 and at most 0.2", cc.Alpha)
		}
		if cc.MinPairs < 0 || cc.MinPairs > 100000 {
			add("compare.min_pairs %d: want 0 to 100000 (0 is the default %d)", cc.MinPairs, DefaultMinPairs)
		}
		for _, s := range cc.Signals {
			if _, ok := signalByName(s); !ok {
				add("compare.signals: %q is not a comparable signal (%s)", s, strings.Join(SignalNames(), ", "))
			}
		}
	}
	if r := c.Sampling.Rate; r != nil && (*r <= 0 || *r > 1) {
		add("sampling.rate %v: want more than 0 and at most 1", *r)
	}
	if c.Retention.Days < 0 || c.Retention.Days > 400 {
		add("retention.days %d: want 1 to 400 (0 is the default 30)", c.Retention.Days)
	}
	if g := time.Duration(c.Sessions.Gap); g != 0 && (g < 30*time.Second || g > 24*time.Hour) {
		add("sessions.gap %s: want 30s to 24h", g)
	}
	for i, t := range c.Thresholds {
		d, ok := signalByName(t.Signal)
		if !ok {
			add("thresholds[%d]: signal %q is not a signal (%s)", i, t.Signal, strings.Join(SignalNames(), ", "))
			continue
		}
		if (t.Above == nil) == (t.Below == nil) {
			add("thresholds[%d] (%s): set exactly one of above and below", i, t.Signal)
		}
		if t.Version != "" && !seen[t.Version] {
			add("thresholds[%d]: version %q is not configured", i, t.Version)
		}
		if t.MinSessions < 0 {
			add("thresholds[%d]: min_sessions is negative", i)
		}
		if d.Kind == KindBounded {
			for _, v := range []*float64{t.Above, t.Below} {
				if v != nil && (*v < 0 || *v > 1) {
					add("thresholds[%d] (%s): a rate threshold is between 0 and 1, got %v", i, t.Signal, *v)
				}
			}
		}
	}
	for i, a := range c.Alerts {
		if err := a.Validate(); err != nil {
			add("alerts[%d]: %v", i, err)
		}
	}
	if c.Inbox.MaxPerPull < 0 || c.Inbox.MaxPerPull > MaxInboxPerPull {
		add("inbox.max_per_pull %d: want 1 to %d", c.Inbox.MaxPerPull, MaxInboxPerPull)
	}
	if j := c.Judge; j != nil {
		if err := checkHTTPURL(j.URL); err != nil {
			add("judge.url: %v", err)
		}
		if !envName.MatchString(j.KeyEnv) {
			add("judge.key_env %q is not an environment variable name", redactValue(j.KeyEnv))
		}
		if j.Model == "" {
			add("judge.model is required")
		}
		if j.SampleRate <= 0 || j.SampleRate > 1 {
			add("judge.sample_rate %v: want more than 0 and at most 1", j.SampleRate)
		}
		if j.MaxPerPull < 0 || j.MaxPerPull > 1000 {
			add("judge.max_per_pull %d: want 1 to 1000", j.MaxPerPull)
		}
	}
	if len(errs) > 0 && placeholders == len(errs) {
		return fmt.Errorf("%w: %w", ErrUnedited, errors.Join(errs...))
	}
	return errors.Join(errs...)
}

// ErrUnedited marks settings whose only problems are the template's EDIT ME
// placeholders: the file was written and never edited, which is "not set up
// yet", not a broken file.
var ErrUnedited = errors.New("live scoring settings still hold placeholders")

// Find returns the version named name.
func (c *Config) Find(name string) (*Version, bool) {
	for i := range c.Versions {
		if c.Versions[i].Name == name {
			return &c.Versions[i], true
		}
	}
	return nil, false
}

// gap is the idle time that ends a session.
func (c *Config) gap() time.Duration {
	if g := time.Duration(c.Sessions.Gap); g > 0 {
		return g
	}
	return DefaultGap
}

// sampleRate is the share of sessions scored.
func (c *Config) sampleRate() float64 {
	if c.Sampling.Rate != nil {
		return *c.Sampling.Rate
	}
	return 1
}

// retention is how long scored sessions are kept.
func (c *Config) retention() time.Duration {
	if c.Retention.Days > 0 {
		return time.Duration(c.Retention.Days) * 24 * time.Hour
	}
	return DefaultRetention
}

func (c *Config) window() time.Duration {
	if w := time.Duration(c.Source.Window); w > 0 {
		return w
	}
	return DefaultWindow
}

func (c *Config) overlap() time.Duration {
	if o := time.Duration(c.Source.Overlap); o > 0 {
		return o
	}
	return DefaultOverlap
}

func (c *Config) inboxMax() int {
	if c.Inbox.MaxPerPull > 0 {
		return c.Inbox.MaxPerPull
	}
	return DefaultInboxPush
}

// compareSettings is the comparison with its defaults filled, or nil.
func (c *Config) compareSettings() *CompareConfig {
	if c.Compare == nil {
		return nil
	}
	cc := *c.Compare
	if cc.Alpha == 0 {
		cc.Alpha = DefaultAlpha
	}
	if cc.MinPairs == 0 {
		cc.MinPairs = DefaultMinPairs
	}
	if len(cc.Signals) == 0 {
		cc.Signals = ComparedByDefault()
	}
	cc.Signals = slices.Clone(cc.Signals)
	return &cc
}

// settingsHash identifies a comparison's frozen settings: baseline,
// candidate, alpha, minimum pairs, signals and the versions' selectors. A
// comparison whose settings change must be restarted, because continuing
// it would test something other than what it started testing.
func (c *Config) settingsHash() string {
	cc := c.compareSettings()
	if cc == nil {
		return ""
	}
	base, _ := c.Find(cc.Baseline)
	cand, _ := c.Find(cc.Candidate)
	b, _ := json.Marshal(struct {
		C    *CompareConfig
		A, B *Version
		Gap  time.Duration
		Rate float64
	}{cc, base, cand, c.gap(), c.sampleRate()})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func checkHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL", redactValue(raw))
	}
	if u.User != nil {
		return errors.New("the URL carries credentials; name them by environment variable instead")
	}
	return nil
}

// redactValue shortens a value that may be a secret pasted into a name
// field.
func redactValue(v string) string {
	if len(v) > 8 {
		return v[:4] + "..."
	}
	return v
}
