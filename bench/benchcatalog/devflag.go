package benchcatalog

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// DevEnv is the environment variable that turns on developer content: the
// Acme practice example projects in the catalog and the scripted control
// agents (oracle, noop, ...) in agent lists. Tests and local development
// set it; a production install never does.
const DevEnv = "HEIMDALL_DEV"

// DevContent reports whether practice examples and scripted controls are
// offered to users. A hosted server never offers them, whatever the flag:
// a hosted org holds its own agents and benchmarks only.
func DevContent(hosted bool) bool {
	return !hosted && DevFlag()
}

// DevFlag reports whether HEIMDALL_DEV is set, hosted or not. It keeps the
// practice suite (PracticeSuites) available to hosted tests and development
// servers; a production install never sets it.
func DevFlag() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(DevEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// PracticeSuites are the bundled practice suites (coding-core): local
// starters and test fixtures, never seeded into or listed for a hosted org
// unless DevFlag is on.
var PracticeSuites = []string{"coding-core"}

// HidePracticeSuite reports whether a hosted org must not see suite id.
func HidePracticeSuite(hosted bool, id string) bool {
	return hosted && !DevFlag() && slices.Contains(PracticeSuites, id)
}

// ExampleSuiteIDs are the suite ids the Acme practice example projects
// under root ship (examples/acme-*/benchmarks/<id>). An org that added an
// example before examples left hosted orgs still holds these suites; the
// suite list hides them when DevContent is off.
func ExampleSuiteIDs(root string) map[string]bool {
	out := map[string]bool{}
	if root == "" {
		return out
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "examples", "acme-*", "benchmarks", "*", "suite.yaml"))
	for _, d := range dirs {
		out[filepath.Base(filepath.Dir(d))] = true
	}
	return out
}

// ExampleAgents recognizes agent files that came from the Acme practice
// example projects (examples/acme-*/agents/*.yaml): an id an example declares
// with the "acme-" prefix (acme-agent, acme-solo, acme-team), or a file
// byte-identical to an example agent file. A file identical to a built-in
// agent template (agents/*.yaml) is never matched, so a real agent made from
// a template stays. An org that added an example before examples left
// hosted orgs still holds these agents; the agent list hides them when
// DevContent is off, as ExampleSuiteIDs does for suites.
type ExampleAgents struct {
	ids    map[string]bool
	hashes map[[sha256.Size]byte]bool
}

var exampleAgentID = regexp.MustCompile(`(?m)^id:[ \t]*["']?([A-Za-z0-9._-]+)`)

// NewExampleAgents reads the example agent files under root. An empty or
// missing root matches nothing.
func NewExampleAgents(root string) *ExampleAgents {
	m := &ExampleAgents{ids: map[string]bool{}, hashes: map[[sha256.Size]byte]bool{}}
	if root == "" {
		return m
	}
	files, _ := filepath.Glob(filepath.Join(root, "examples", "acme-*", "agents", "*.yaml"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		m.hashes[sha256.Sum256(data)] = true
		if id := exampleAgentID.FindSubmatch(data); id != nil && strings.HasPrefix(string(id[1]), "acme-") {
			m.ids[string(id[1])] = true
		}
	}
	templates, _ := filepath.Glob(filepath.Join(root, "agents", "*.yaml"))
	for _, f := range templates {
		if data, err := os.ReadFile(f); err == nil {
			delete(m.hashes, sha256.Sum256(data))
		}
	}
	return m
}

// Match reports whether an agent with id and file content came from an
// example (content may be nil when it could not be read).
func (m *ExampleAgents) Match(id string, content []byte) bool {
	if m == nil {
		return false
	}
	if m.ids[id] {
		return true
	}
	return content != nil && m.hashes[sha256.Sum256(content)]
}
