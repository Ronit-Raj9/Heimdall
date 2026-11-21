package benchcatalog

import (
	"os"
	"path/filepath"
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
	if hosted {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(DevEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
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
