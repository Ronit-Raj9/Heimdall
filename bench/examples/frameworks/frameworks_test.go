// Package frameworks holds the framework example agents (LangGraph, OpenAI
// Agents SDK, CrewAI, Pydantic AI, LlamaIndex, Vercel AI SDK and a
// no-framework loop on the OpenAI SDK), one folder each with the agent, its
// pinned dependencies and its Dockerfile; their configs are agents/fw-*.yaml.
// It has no Go code: these tests keep the folders, the configs, the matrix
// experiment and docs/harness-matrix.md from drifting apart.
package frameworks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// benchRoot is the bench project root, relative to this package.
const benchRoot = "../.."

// example is one folder under examples/frameworks and the config that runs it.
type example struct {
	dir    string // folder name, e.g. "langgraph"
	id     string // agent config id, "fw-" + dir
	pins   map[string]string
	config schemas.AgentConfig
}

// pinRe is one exact pin in a requirements file: name==version, extras allowed.
var pinRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+)(\[[^\]]*\])?==([^\s;#]+)`)

// exactVersionRe is an exact npm version: no range operators.
var exactVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func loadExamples(t *testing.T) []example {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []example
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ex := example{dir: e.Name(), id: "fw-" + e.Name(), pins: map[string]string{}}
		path := filepath.Join(benchRoot, "agents", ex.id+".yaml")
		if err := suite.DecodeYAMLFile(path, path, suite.SchemaAgent, &ex.config); err != nil {
			t.Fatalf("examples/frameworks/%s has no valid agent config: %v", ex.dir, err)
		}
		ex.pins = readPins(t, ex.dir)
		out = append(out, ex)
	}
	if len(out) < 7 {
		t.Fatalf("%d framework examples, want at least 7", len(out))
	}
	return out
}

// readPins reads the top-level pins: requirements.in for Python, package.json
// for Node. Every pin must be exact.
func readPins(t *testing.T, dir string) map[string]string {
	t.Helper()
	pins := map[string]string{}
	if raw, err := os.ReadFile(filepath.Join(dir, "requirements.in")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			m := pinRe.FindStringSubmatch(line)
			if m == nil {
				t.Errorf("%s/requirements.in: %q is not an exact pin (name==version)", dir, line)
				continue
			}
			pins[strings.ToLower(m[1])] = m[3]
		}
		return pins
	}
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatalf("%s: neither requirements.in nor package.json", dir)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("%s/package.json: %v", dir, err)
	}
	for name, v := range pkg.Dependencies {
		if !exactVersionRe.MatchString(v) {
			t.Errorf("%s/package.json: %s %q is not an exact version", dir, name, v)
		}
		pins[name] = v
	}
	return pins
}

// TestFrameworkExamplesPinned: every example has an agent config that
// validates against the agent schema, builds its own pinned image on the
// suite base, and installs only locked, exact versions.
func TestFrameworkExamplesPinned(t *testing.T) {
	for _, ex := range loadExamples(t) {
		t.Run(ex.dir, func(t *testing.T) {
			c := ex.config
			if c.ID != ex.id || c.Adapter != "command" {
				t.Errorf("id %q adapter %q, want %q command", c.ID, c.Adapter, ex.id)
			}
			name, tag, ok := strings.Cut(c.Image, ":")
			if !ok || tag == "" || tag == "latest" || name != "bench/agent-"+ex.id {
				t.Errorf("image %q: want bench/agent-%s:<pinned tag>", c.Image, ex.id)
			}
			if c.Build == nil || c.Build.Tag != c.Image || c.Build.BaseArg != "BASE_IMAGE" ||
				c.Build.Dockerfile != "examples/frameworks/"+ex.dir+"/Dockerfile" {
				t.Fatalf("build %+v: want tag = image, base_arg BASE_IMAGE, the example's Dockerfile", c.Build)
			}
			docker, err := os.ReadFile(filepath.Join(ex.dir, "Dockerfile"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"ARG BASE_IMAGE=", "FROM ${BASE_IMAGE}", "USER 1000", "/usr/local/bin/fw-agent"} {
				if !strings.Contains(string(docker), want) {
					t.Errorf("Dockerfile lacks %q", want)
				}
			}
			if len(ex.pins) == 0 {
				t.Error("no pinned dependency")
			}
			if _, err := os.Stat(filepath.Join(ex.dir, "requirements.in")); err == nil {
				checkLock(t, ex.dir)
			} else if _, err := os.Stat(filepath.Join(ex.dir, "package-lock.json")); err != nil {
				t.Errorf("no package-lock.json: %v", err)
			}
			// The agent runs unchanged: the YAML points the standard variables at the gateway.
			if c.Env["OPENAI_BASE_URL"] != "{{gateway.url}}/openai/v1" || c.Env["OPENAI_API_KEY"] != "{{run.key}}" {
				t.Errorf("env OPENAI_BASE_URL %q OPENAI_API_KEY %q", c.Env["OPENAI_BASE_URL"], c.Env["OPENAI_API_KEY"])
			}
			if c.Env["AGENT_MAX_STEPS"] == "" || c.Env["AGENT_MODEL"] != "{{model}}" || c.Env["AGENT_SESSION_ID"] != "{{run.session}}" {
				t.Errorf("env AGENT_MAX_STEPS %q AGENT_MODEL %q AGENT_SESSION_ID %q", c.Env["AGENT_MAX_STEPS"], c.Env["AGENT_MODEL"], c.Env["AGENT_SESSION_ID"])
			}
			for _, a := range c.Argv {
				if strings.Contains(a, "{{run.key}}") {
					t.Error("run key in argv")
				}
			}
			if c.Gateway.Provider != "openai" || c.Gateway.MaxRetries != 0 || c.Model == "" {
				t.Errorf("gateway %+v model %q", c.Gateway, c.Model)
			}
		})
	}
}

// checkLock: requirements.txt is the compiled lock of requirements.in, every
// line an exact pin, and it contains each top-level pin.
func checkLock(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "requirements.txt"))
	if err != nil {
		t.Fatalf("no requirements.txt lock: %v", err)
	}
	locked := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := pinRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("requirements.txt: %q is not an exact pin", line)
			continue
		}
		locked[strings.ToLower(m[1])] = m[3]
	}
	for name, v := range readPins(t, dir) {
		if locked[name] != v {
			t.Errorf("requirements.txt has %s %q, requirements.in pins %q: recompile the lock", name, locked[name], v)
		}
	}
}

// TestHarnessMatrixCoversFrameworks: docs/harness-matrix.md has a row for
// every agent config in agents/ and, for each framework example, a row that
// names the exact pinned versions. A version bump without a doc update fails.
func TestHarnessMatrixCoversFrameworks(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(benchRoot, "docs", "harness-matrix.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{} // config id -> its table row
	for _, line := range strings.Split(string(doc), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cell := strings.SplitN(line, "|", 3)[1]
		for _, id := range regexp.MustCompile("`([a-z0-9-]+)`").FindAllStringSubmatch(cell, -1) {
			rows[id[1]] += line
		}
	}
	configs, err := filepath.Glob(filepath.Join(benchRoot, "agents", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range configs {
		id := strings.TrimSuffix(filepath.Base(p), ".yaml")
		if rows[id] == "" {
			t.Errorf("docs/harness-matrix.md has no table row for agents/%s.yaml", id)
		}
	}
	for _, ex := range loadExamples(t) {
		row := rows[ex.id]
		names := make([]string, 0, len(ex.pins))
		for name := range ex.pins {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if name == "zod" || strings.HasPrefix(name, "opentelemetry-") {
				continue // helpers, not the framework
			}
			if !strings.Contains(row, ex.pins[name]) {
				t.Errorf("harness-matrix row for %s does not name %s %s", ex.id, name, ex.pins[name])
			}
		}
	}
}

// TestMatrixExperimentListsEveryFramework: experiments/matrix-frameworks.yaml
// validates, expands to one arm per framework agent plus Codex, all on the
// same model; the smoke runs every framework arm on the scripted provider.
func TestMatrixExperimentListsEveryFramework(t *testing.T) {
	exs := loadExamples(t)
	var e schemas.Experiment
	path := filepath.Join(benchRoot, "experiments", "matrix-frameworks.yaml")
	if err := suite.DecodeYAMLFile(path, path, suite.SchemaExperiment, &e); err != nil {
		t.Fatal(err)
	}
	expanded, err := engine.ExpandMatrix(&e)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Matrix.Models) != 1 {
		t.Errorf("matrix models %v: want one model for every agent", e.Matrix.Models)
	}
	agents := map[string]bool{}
	for _, a := range expanded.Arms {
		agents[a.Agent] = true
	}
	if !agents["agents/codex.yaml"] {
		t.Error("matrix lacks the Codex reference arm")
	}
	for _, ex := range exs {
		if !agents["agents/"+ex.id+".yaml"] {
			t.Errorf("matrix lacks agents/%s.yaml", ex.id)
		}
	}

	var smoke schemas.Experiment
	path = filepath.Join(benchRoot, "experiments", "matrix-frameworks-smoke.yaml")
	if err := suite.DecodeYAMLFile(path, path, suite.SchemaExperiment, &smoke); err != nil {
		t.Fatal(err)
	}
	if !smoke.Smoke || smoke.Budget.TotalUSD != 0 {
		t.Errorf("smoke %v budget %v: want smoke: true and total_usd 0", smoke.Smoke, smoke.Budget.TotalUSD)
	}
	inSmoke := map[string]bool{}
	for id, arm := range smoke.Arms {
		raw, err := os.ReadFile(filepath.Join(benchRoot, arm.Agent))
		if err != nil {
			t.Fatalf("arm %s: %v", id, err)
		}
		cfg, err := engine.ResolveAgentConfig(raw, arm.Set)
		if err != nil {
			t.Fatalf("arm %s: %v", id, err)
		}
		// $0 only while the provider is one the proxy answers from its scripted model.
		if cfg.Gateway.Provider != "anthropic" || cfg.Env["AGENT_MODEL"] != "anthropic/{{model}}" {
			t.Errorf("arm %s: provider %q AGENT_MODEL %q, want anthropic", id, cfg.Gateway.Provider, cfg.Env["AGENT_MODEL"])
		}
		inSmoke[cfg.ID] = true
	}
	for _, ex := range exs {
		if !inSmoke[ex.id] {
			t.Errorf("smoke lacks %s", ex.id)
		}
	}
}
