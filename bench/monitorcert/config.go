package monitorcert

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// Config defaults and limits.
const (
	defaultTimeoutS  = 30
	maxTimeoutS      = 600
	defaultCPUs      = 1.0
	defaultMemoryMB  = 256
	defaultPidsLimit = 64
	maxScriptBytes   = 64 << 10
	scriptToken      = "{{script}}"
)

var monitorNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Resolved is a validated monitor config with its defaults applied, the
// contents of its script (if any) and the hash a certificate records.
type Resolved struct {
	Config schemas.MonitorConfig
	Script string // contents of Config.Script; replaces {{script}} in argv
	Hash   string // "sha256:" + hex over the canonical config and the script
}

// LoadConfig reads a monitor config from YAML or JSON (JSON is YAML), with
// unknown fields refused, applies defaults and validates it. A script path
// is resolved against the config file's folder.
func LoadConfig(path string) (*Resolved, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read monitor config %s: %w", path, err)
	}
	var cfg schemas.MonitorConfig
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse monitor config %s: %w", path, err)
	}
	script := ""
	if cfg.Script != "" {
		p := cfg.Script
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(path), p)
		}
		sb, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("monitor config %s: read script: %w", path, err)
		}
		if len(sb) > maxScriptBytes {
			return nil, fmt.Errorf("monitor config %s: script %s is %d bytes, over %d", path, cfg.Script, len(sb), maxScriptBytes)
		}
		script = string(sb)
	}
	r, err := Resolve(cfg, script)
	if err != nil {
		return nil, fmt.Errorf("monitor config %s: %w", path, err)
	}
	return r, nil
}

// Resolve applies defaults to cfg, validates it and hashes it with the
// script contents.
func Resolve(cfg schemas.MonitorConfig, script string) (*Resolved, error) {
	applyDefaults(&cfg)
	if err := validate(&cfg, script); err != nil {
		return nil, err
	}
	h, err := utils.HashJSON(struct {
		Config       schemas.MonitorConfig `json:"config"`
		ScriptSHA256 string                `json:"script_sha256,omitempty"`
	}{cfg, scriptHash(script)})
	if err != nil {
		return nil, fmt.Errorf("hash monitor config: %w", err)
	}
	return &Resolved{Config: cfg, Script: script, Hash: "sha256:" + h}, nil
}

// Argv is the command monitor's argv with {{script}} replaced.
func (r *Resolved) Argv() []string {
	out := make([]string, len(r.Config.Argv))
	for i, a := range r.Config.Argv {
		out[i] = strings.ReplaceAll(a, scriptToken, r.Script)
	}
	return out
}

// Views are the transcript views to certify, one certificate each: the
// config's views, or [actions] when it names none.
func (r *Resolved) Views() []string {
	if len(r.Config.Views) == 0 {
		return []string{schemas.MonitorViewActions}
	}
	return append([]string(nil), r.Config.Views...)
}

// Network is the network setting a certificate records for the monitor.
func (r *Resolved) Network() string {
	if r.Config.Kind == schemas.MonitorKindBuiltin {
		return schemas.MonitorNetworkNone
	}
	return r.Config.Network
}

func applyDefaults(c *schemas.MonitorConfig) {
	if len(c.Views) == 1 && c.Views[0] == schemas.MonitorViewActions {
		c.Views = nil // the default, written out: hashes like a config that names no view
	}
	if c.Mode == "" {
		c.Mode = schemas.MonitorModeTranscript
	}
	if c.TimeoutS == 0 {
		c.TimeoutS = defaultTimeoutS
	}
	switch c.Kind {
	case schemas.MonitorKindCommand:
		if c.Network == "" {
			c.Network = schemas.MonitorNetworkNone
		}
		if c.Resources.CPUs == 0 {
			c.Resources.CPUs = defaultCPUs
		}
		if c.Resources.MemoryMB == 0 {
			c.Resources.MemoryMB = defaultMemoryMB
		}
		if c.Resources.PidsLimit == 0 {
			c.Resources.PidsLimit = defaultPidsLimit
		}
	case schemas.MonitorKindHTTP:
		if c.Network == "" {
			c.Network = schemas.MonitorNetworkOperator
		}
	case schemas.MonitorKindBuiltin:
		if c.Builtin == BuiltinKeyword && c.BlockAt == 0 {
			c.BlockAt = 1
		}
	}
}

// validate checks a config after defaults. Network: a command monitor runs
// with --network none and nothing else, because the bench has no way to
// enforce an allowlist on a monitor container; an http monitor is a remote
// endpoint the bench does not sandbox, recorded as operator-managed.
func validate(c *schemas.MonitorConfig, script string) error {
	var errs []error
	if !monitorNameRE.MatchString(c.Name) {
		errs = append(errs, fmt.Errorf("name %q: 1 to 64 letters, digits, '.', '_' or '-'", c.Name))
	}
	if c.Mode != schemas.MonitorModeTranscript && c.Mode != schemas.MonitorModeAction {
		errs = append(errs, fmt.Errorf("mode %q: want transcript or action", c.Mode))
	}
	if c.TimeoutS < 1 || c.TimeoutS > maxTimeoutS {
		errs = append(errs, fmt.Errorf("timeout_s %d: want 1 to %d", c.TimeoutS, maxTimeoutS))
	}
	seenView := map[string]bool{}
	for _, v := range c.Views {
		switch v {
		case schemas.MonitorViewActions, schemas.MonitorViewCoT, schemas.MonitorViewHybrid:
		default:
			errs = append(errs, fmt.Errorf("view %q: want actions, cot or hybrid", v))
		}
		if seenView[v] {
			errs = append(errs, fmt.Errorf("view %q is listed twice", v))
		}
		seenView[v] = true
	}
	switch c.Kind {
	case schemas.MonitorKindCommand:
		errs = append(errs, validateCommand(c, script)...)
	case schemas.MonitorKindHTTP:
		errs = append(errs, validateHTTP(c, script)...)
	case schemas.MonitorKindBuiltin:
		errs = append(errs, validateBuiltin(c, script)...)
	default:
		errs = append(errs, fmt.Errorf("kind %q: want command, http or builtin", c.Kind))
	}
	return errors.Join(errs...)
}

func validateCommand(c *schemas.MonitorConfig, script string) []error {
	var errs []error
	if strings.TrimSpace(c.Image) == "" {
		errs = append(errs, errors.New("image is required for a command monitor"))
	}
	if len(c.Argv) == 0 {
		errs = append(errs, errors.New("argv is required for a command monitor"))
	}
	if c.Network != schemas.MonitorNetworkNone {
		errs = append(errs, fmt.Errorf("network %q: a command monitor runs with network none only; the bench cannot enforce an allowlist on a monitor container", c.Network))
	}
	if c.URL != "" || c.Builtin != "" || len(c.Keywords) > 0 || len(c.Patterns) > 0 {
		errs = append(errs, errors.New("url, builtin, keywords and patterns are not used by a command monitor"))
	}
	hasToken := slices.ContainsFunc(c.Argv, func(a string) bool { return strings.Contains(a, scriptToken) })
	switch {
	case c.Script != "" && !hasToken:
		errs = append(errs, errors.New("script is set but no argv element holds {{script}}"))
	case c.Script == "" && hasToken:
		errs = append(errs, errors.New("argv holds {{script}} but script is not set"))
	case c.Script != "" && strings.ContainsRune(script, 0):
		errs = append(errs, errors.New("script holds a NUL byte"))
	}
	if c.Resources.CPUs < 0 || c.Resources.MemoryMB < 0 || c.Resources.PidsLimit < 0 {
		errs = append(errs, errors.New("resources must not be negative"))
	}
	return errs
}

func validateHTTP(c *schemas.MonitorConfig, script string) []error {
	var errs []error
	u, err := url.Parse(c.URL)
	if c.URL == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("url %q: want an http or https URL", c.URL))
	}
	if c.Network != schemas.MonitorNetworkOperator {
		errs = append(errs, fmt.Errorf("network %q: an http monitor is a remote endpoint the bench does not sandbox; it is recorded as operator-managed", c.Network))
	}
	if c.Image != "" || len(c.Argv) > 0 || c.Script != "" || script != "" {
		errs = append(errs, errors.New("image, argv and script are not used by an http monitor; image-backed http monitors are not supported"))
	}
	if c.Builtin != "" || len(c.Keywords) > 0 || len(c.Patterns) > 0 {
		errs = append(errs, errors.New("builtin, keywords and patterns are not used by an http monitor"))
	}
	return errs
}

func validateBuiltin(c *schemas.MonitorConfig, script string) []error {
	var errs []error
	if !slices.Contains(Builtin(), c.Builtin) {
		errs = append(errs, fmt.Errorf("builtin %q: want one of %s", c.Builtin, strings.Join(Builtin(), ", ")))
	}
	if c.Image != "" || len(c.Argv) > 0 || c.Script != "" || script != "" || c.URL != "" {
		errs = append(errs, errors.New("image, argv, script and url are not used by a builtin monitor"))
	}
	if c.Network != "" && c.Network != schemas.MonitorNetworkNone {
		errs = append(errs, fmt.Errorf("network %q: a builtin monitor has no network", c.Network))
	}
	if c.Builtin == BuiltinKeyword {
		if len(c.Keywords) == 0 && len(c.Patterns) == 0 {
			errs = append(errs, errors.New("builtin keyword needs keywords or patterns"))
		}
		for _, p := range c.Patterns {
			if _, err := regexp.Compile(p); err != nil {
				errs = append(errs, fmt.Errorf("pattern %q: %w", p, err))
			}
		}
		if c.BlockAt < 1 {
			errs = append(errs, fmt.Errorf("block_at %d: want 1 or more", c.BlockAt))
		}
	} else if len(c.Keywords) > 0 || len(c.Patterns) > 0 {
		errs = append(errs, fmt.Errorf("keywords and patterns are used only by builtin %s", BuiltinKeyword))
	}
	return errs
}

func scriptHash(script string) string {
	if script == "" {
		return ""
	}
	return utils.SHA256Bytes([]byte(script))
}
