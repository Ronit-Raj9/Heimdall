package benchcatalog

import (
	"os"
	"path/filepath"
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
