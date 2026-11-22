package agentinfer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentFilePath is where a repository commits its agent, relative to the
// repository root. The CLI (heimdall agent file, heimdall connect) and the
// repository import read it.
const AgentFilePath = ".heimdall/agent.yaml"

// AgentFileVersion is the format version this package reads.
const AgentFileVersion = 1

// MaxAgentFileBytes caps an agent file.
const MaxAgentFileBytes = 64 << 10

// AgentFile is the committed .heimdall/agent.yaml. It says how to reach
// the agent through one of the connect templates, or holds a full agent
// config. It names secrets (Settings > Secrets) and never holds one.
//
//	version: 1
//	id: support-bot
//	name: Support bot
//	framework: langgraph
//	model: {provider: openai, model: gpt-5.4-mini}
//	connect:
//	  template: http-url
//	  url: https://support.example.com/chat
//	  auth_secret: SUPPORT_BOT_TOKEN
//	capabilities: [tools, sessions]
//	tools: [lookup_order, refund]
//	evaluate: {benchmarks: [support-basics]}
type AgentFile struct {
	Version      int            `yaml:"version" json:"version"`
	ID           string         `yaml:"id" json:"id"`
	Name         string         `yaml:"name,omitempty" json:"name,omitempty"`
	Framework    string         `yaml:"framework,omitempty" json:"framework,omitempty"`
	Preset       string         `yaml:"preset,omitempty" json:"preset,omitempty"`
	Model        *ModelRef      `yaml:"model,omitempty" json:"model,omitempty"`
	Connect      map[string]any `yaml:"connect,omitempty" json:"connect,omitempty"`
	Config       map[string]any `yaml:"config,omitempty" json:"config,omitempty"` // a full agent config instead of connect
	Capabilities []string       `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Tools        []string       `yaml:"tools,omitempty" json:"tools,omitempty"` // what the agent has; informational
	Notes        string         `yaml:"notes,omitempty" json:"notes,omitempty"`
	Evaluate     *FileEvaluate  `yaml:"evaluate,omitempty" json:"evaluate,omitempty"`
}

// FileEvaluate is what the CLI runs for this agent by default.
type FileEvaluate struct {
	Benchmarks []string `yaml:"benchmarks,omitempty" json:"benchmarks,omitempty"`
	Preset     string   `yaml:"preset,omitempty" json:"preset,omitempty"` // first_look | full
}

// FromTemplate is the body of POST /agents/from-template
// (handlers.AgentFromTemplateRequest).
type FromTemplate struct {
	Template string            `json:"template"`
	ID       string            `json:"id"`
	Model    *ModelRef         `json:"model,omitempty"`
	Notes    string            `json:"notes,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	DryRun   bool              `json:"dry_run,omitempty"`
}

var (
	fileIDRe       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	templateIDRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	paramNameRe    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	secretNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	secretValueRes = []*regexp.Regexp{
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`),
		regexp.MustCompile(`\b(hmi|hma|hmr)_[A-Za-z0-9_-]{16,}`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`(?i)^bearer\s+\S{12,}`),
		regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	}
)

// secretParams are parameter names that would hold a secret value.
var secretParams = map[string]bool{"token": true, "api_key": true, "apikey": true, "password": true, "secret": true,
	"authorization": true, "bearer": true, "key": true, "access_token": true, "client_secret": true}

// ErrNoAgentFile is returned by LoadAgentFile when the file is absent.
var ErrNoAgentFile = errors.New("no " + AgentFilePath)

// ParseAgentFile decodes and checks an agent file.
func ParseAgentFile(b []byte) (*AgentFile, error) {
	if len(b) > MaxAgentFileBytes {
		return nil, fmt.Errorf("%s is larger than %d KiB", AgentFilePath, MaxAgentFileBytes>>10)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var f AgentFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", AgentFilePath, err)
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// LoadAgentFile reads root/.heimdall/agent.yaml (or agent.yml).
func LoadAgentFile(root string) (*AgentFile, string, error) {
	for _, rel := range []string{AgentFilePath, strings.TrimSuffix(AgentFilePath, ".yaml") + ".yml"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, p, err
		}
		f, err := ParseAgentFile(b)
		return f, p, err
	}
	return nil, filepath.Join(root, filepath.FromSlash(AgentFilePath)), ErrNoAgentFile
}

// Validate checks the file; every error names the field.
func (f *AgentFile) Validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if f.Version == 0 {
		f.Version = AgentFileVersion
	}
	if f.Version != AgentFileVersion {
		add("version: %d is not supported (this server reads version %d)", f.Version, AgentFileVersion)
	}
	if !fileIDRe.MatchString(f.ID) {
		add("id: %q must be lowercase letters, digits, '.', '_' or '-', up to 63 characters", f.ID)
	}
	switch {
	case len(f.Connect) == 0 && len(f.Config) == 0:
		add("connect: say how to reach the agent (connect.template and its fields), or give a full agent config under config")
	case len(f.Connect) > 0 && len(f.Config) > 0:
		add("connect and config: give one of them, not both")
	}
	if len(f.Connect) > 0 {
		t, _ := f.Connect["template"].(string)
		if !templateIDRe.MatchString(t) {
			add("connect.template: %q is not a template id (http-url, openai-endpoint, a2a-card, wrap-app, docker-image)", t)
		}
		keys := make([]string, 0, len(f.Connect))
		for k := range f.Connect {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "template" {
				continue
			}
			v, err := paramString(f.Connect[k])
			switch {
			case !paramNameRe.MatchString(k):
				add("connect.%s: not a field name", k)
			case err != nil:
				add("connect.%s: %v", k, err)
			case secretParams[k]:
				add("connect.%s: %s names secrets and never holds them; put the value in Settings > Secrets and give its name, for example auth_secret: MY_AGENT_TOKEN", k, AgentFilePath)
			case strings.HasSuffix(k, "_secret") && v != "" && !secretNameRe.MatchString(v):
				add("connect.%s: %q is not a secret name (letters, digits and _)", k, v)
			case looksSecret(v):
				add("connect.%s: the value looks like a key or token; %s holds secret names only", k, AgentFilePath)
			}
		}
	}
	if len(f.Config) > 0 {
		b, _ := yaml.Marshal(f.Config)
		if looksSecret(string(b)) {
			add("config: a value looks like a key or token; %s holds secret names only", AgentFilePath)
		}
		if id, ok := f.Config["id"].(string); ok && id != "" && id != f.ID {
			add("config.id: %q differs from id %q", id, f.ID)
		}
	}
	if f.Model != nil && strings.ContainsAny(f.Model.Model, " \t\n\"'{}") {
		add("model.model: %q has characters a model id cannot hold", f.Model.Model)
	}
	if f.Evaluate != nil && f.Evaluate.Preset != "" && f.Evaluate.Preset != "first_look" && f.Evaluate.Preset != "full" {
		add("evaluate.preset: %q is not first_look or full", f.Evaluate.Preset)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", AgentFilePath, strings.Join(errs, "; "))
	}
	return nil
}

func looksSecret(v string) bool {
	for _, re := range secretValueRes {
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

// paramString turns a YAML scalar or list into a template parameter value:
// lists are comma-joined, maps (env) become KEY=VALUE lines.
func paramString(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			s, err := paramString(e)
			if err != nil {
				return "", err
			}
			if strings.Contains(s, ",") {
				return "", fmt.Errorf("list items cannot hold commas")
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ", "), nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, k := range keys {
			s, err := paramString(x[k])
			if err != nil {
				return "", err
			}
			if strings.ContainsAny(s, "\n") {
				return "", fmt.Errorf("%s: a value cannot span lines", k)
			}
			lines = append(lines, k+"="+s)
		}
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("unsupported value %T", v)
}

// ToRequest is the POST /agents/from-template body the connect block
// describes. It fails for a file that holds a full config (use ConfigYAML).
func (f *AgentFile) ToRequest() (FromTemplate, error) {
	if len(f.Connect) == 0 {
		return FromTemplate{}, fmt.Errorf("%s holds a full agent config, not a connect block", AgentFilePath)
	}
	t, _ := f.Connect["template"].(string)
	req := FromTemplate{Template: t, ID: f.ID, Notes: f.Notes, Params: map[string]string{}}
	if f.Model != nil && (f.Model.Provider != "" || f.Model.Model != "") {
		m := *f.Model
		req.Model = &m
	}
	for k, v := range f.Connect {
		if k == "template" {
			continue
		}
		s, err := paramString(v)
		if err != nil {
			return FromTemplate{}, fmt.Errorf("connect.%s: %w", k, err)
		}
		req.Params[k] = s
	}
	if len(req.Params) == 0 {
		req.Params = nil
	}
	return req, nil
}

// ConfigYAML is the agent config a file with a config block holds, as the
// YAML PUT /agents/{id}/config takes. The id is set from the file.
func (f *AgentFile) ConfigYAML() (string, error) {
	if len(f.Config) == 0 {
		return "", fmt.Errorf("%s has no config block", AgentFilePath)
	}
	c := make(map[string]any, len(f.Config)+1)
	for k, v := range f.Config {
		c[k] = v
	}
	c["id"] = f.ID
	b, err := yaml.Marshal(c)
	return string(b), err
}

// FileFromDraft turns a draft into an agent file. Fields the traces did not
// show are left for the person, with the draft's questions as comments.
func FileFromDraft(d Draft) *AgentFile {
	f := &AgentFile{Version: AgentFileVersion, ID: d.ID, Name: d.Name, Framework: d.Framework, Preset: d.Preset,
		Capabilities: d.Capabilities}
	if f.ID == "" {
		f.ID = "traced-agent"
	}
	if d.Model != nil {
		m := *d.Model
		f.Model = &m
	}
	t := d.Template
	if t == "" {
		t = TemplateHTTP
	}
	f.Connect = map[string]any{"template": t}
	for k, v := range d.Params {
		f.Connect[k] = v
	}
	if t == TemplateHTTP {
		if _, ok := f.Connect["url"]; !ok {
			f.Connect["url"] = ""
		}
	}
	names := append([]string(nil), d.ToolsDeclared...)
	for _, tl := range d.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	f.Tools = dedupe(names)
	return f
}

func dedupe(xs []string) []string {
	var out []string
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// Marshal renders the file as YAML with a header comment; questions (from
// a draft) are added as comments the person answers.
func (f *AgentFile) Marshal(questions ...string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# Heimdall agent file (" + AgentFilePath + "). Commit it; heimdall agent file push creates or updates the agent.\n")
	buf.WriteString("# Secrets are named here (Settings > Secrets), never written here.\n")
	for _, q := range questions {
		buf.WriteString("# TODO: " + strings.ReplaceAll(q, "\n", " ") + "\n")
	}
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
