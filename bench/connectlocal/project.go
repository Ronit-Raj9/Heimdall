package connectlocal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProjectPath is the committed agent file, relative to the repository root.
const ProjectPath = ".heimdall/agent.yaml"

// ProjectFile is .heimdall/agent.yaml: which Heimdall agent this repository
// is, and how `heimdall serve` serves it. It never holds a secret: headers
// name environment variables (${MY_TOKEN}).
type ProjectFile struct {
	Version int    `yaml:"version"`
	Server  string `yaml:"server,omitempty"`
	Agent   string `yaml:"agent"`
	// Framework is what connect detected (langgraph, http, ...).
	Framework string     `yaml:"framework,omitempty"`
	Serve     *ServeSpec `yaml:"serve,omitempty"`
	// Config is the agent config as registered in Heimdall.
	Config string `yaml:"config,omitempty"`
}

// ServeSpec is how `heimdall serve` with no target serves this agent.
type ServeSpec struct {
	// Target is file.py:object, module:object or an http(s) URL (relay).
	Target      string   `yaml:"target"`
	Concurrency int      `yaml:"concurrency,omitempty"`
	Map         *Mapping `yaml:"map,omitempty"`
}

// ReadProject reads .heimdall/agent.yaml under root, or the file itself.
func ReadProject(root string) (*ProjectFile, string, error) {
	p := root
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		p = filepath.Join(root, ProjectPath)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, p, err
	}
	var pf ProjectFile
	if err := yaml.Unmarshal(b, &pf); err != nil {
		return nil, p, fmt.Errorf("%s: %v", p, err)
	}
	if pf.Agent == "" {
		return nil, p, fmt.Errorf("%s names no agent", p)
	}
	if pf.Serve != nil && pf.Serve.Map != nil {
		if err := pf.Serve.Map.Validate(); err != nil {
			return nil, p, fmt.Errorf("%s serve.map: %v", p, err)
		}
	}
	return &pf, p, nil
}

// secretValueRE finds header values that look like a literal credential.
var secretValueRE = regexp.MustCompile(`(?i)^(bearer|basic|token)\s+[A-Za-z0-9._~+/=-]{12,}$|^[A-Za-z0-9._~+/=-]{24,}$`)

// WriteProject writes .heimdall/agent.yaml under root (0644: it is meant to
// be committed). It refuses a header holding a literal credential.
func WriteProject(root string, pf *ProjectFile) (string, error) {
	if pf.Agent == "" {
		return "", errors.New("project file needs the agent name")
	}
	if pf.Version == 0 {
		pf.Version = 1
	}
	if pf.Serve != nil && pf.Serve.Map != nil {
		for k, v := range pf.Serve.Map.Headers {
			if !strings.Contains(v, "${") && secretValueRE.MatchString(strings.TrimSpace(v)) {
				return "", fmt.Errorf("header %s holds a literal credential; use ${ENV_VAR} so the file can be committed", k)
			}
		}
		pf.Serve.Map.BodyText = string(pf.Serve.Map.Body)
	}
	b, err := yaml.Marshal(pf)
	if err != nil {
		return "", err
	}
	head := "# Heimdall agent for this repository (written by `heimdall connect`; commit it).\n" +
		"# `heimdall serve` with no arguments serves it. Secrets stay in the environment.\n"
	p := filepath.Join(root, ProjectPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, append([]byte(head), b...), 0o644)
}

// SecretEnvName is the environment variable a header's value moves to:
// Authorization becomes HEIMDALL_AGENT_AUTHORIZATION.
func SecretEnvName(header string) string {
	var b strings.Builder
	b.WriteString("HEIMDALL_AGENT_")
	for _, r := range strings.ToUpper(header) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// MoveSecretsToEnv replaces credential header values with ${VAR}
// references and returns the variables to export, with their values, so
// the caller can tell the user once and never write them to a file.
func MoveSecretsToEnv(headers map[string]string, names []string) map[string]string {
	out := map[string]string{}
	for _, k := range names {
		v, ok := headers[k]
		if !ok || strings.Contains(v, "${") {
			continue
		}
		env := SecretEnvName(k)
		out[env] = v
		headers[k] = "${" + env + "}"
	}
	return out
}
