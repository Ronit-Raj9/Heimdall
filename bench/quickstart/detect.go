// Package quickstart is the first-workflow layer of Heimdall
// (docs/quickstart.md): `heimdall init` inside a repository detects the test
// command, the agent CLIs on PATH, Docker and the stack's ports, and writes a
// .heimdall/ project; `heimdall compare` turns two refs, two configs or two
// models into one experiment file. Nothing here calls a model, writes to the
// user's repository outside .heimdall/ and .gitignore, or runs the user's
// tests.
package quickstart

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Confidence of a detected test command.
const (
	ConfidenceHigh   = "high"   // a file names the command (scripts.test, go.mod, Cargo.toml, a pytest setting)
	ConfidenceMedium = "medium" // inferred from layout (tests/test_*.py, a Makefile test target)
	ConfidenceLow    = "low"    // nothing found; a guess the user must confirm
	ConfidenceGiven  = "given"  // -test-cmd
)

// TestSetup is what init found about how the repository runs its tests.
type TestSetup struct {
	Language   string   `json:"language" yaml:"language"`     // python, node, go, rust, ruby, java, make or unknown
	Command    string   `json:"command" yaml:"command"`       // the repository's own test command
	Confidence string   `json:"confidence" yaml:"confidence"` // high, medium, low or given
	Evidence   []string `json:"evidence" yaml:"evidence"`     // the files that decided it
	Template   string   `json:"template" yaml:"template"`     // suite template the tasks follow
	Also       []string `json:"also,omitempty" yaml:"also,omitempty"`
}

// AgentCLI is one coding-agent CLI found on PATH.
type AgentCLI struct {
	Name    string `json:"name" yaml:"name"`       // claude, codex, opencode or gemini
	Path    string `json:"path" yaml:"path"`       // where it was found
	Version string `json:"version" yaml:"version"` // first line of `<cli> --version`, or "unknown"
	Config  string `json:"config" yaml:"config"`   // the bench agent config written for it (agents/<id>.yaml)
	Image   string `json:"image" yaml:"image"`     // the container image that config runs
}

// DockerStatus is whether Docker and Compose answer.
type DockerStatus struct {
	Available      bool   `json:"available" yaml:"available"`
	Version        string `json:"version,omitempty" yaml:"version,omitempty"`
	Compose        bool   `json:"compose" yaml:"compose"`
	ComposeVersion string `json:"compose_version,omitempty" yaml:"compose_version,omitempty"`
	Note           string `json:"note,omitempty" yaml:"note,omitempty"`
}

// PortStatus is one loopback port the stack publishes.
type PortStatus struct {
	Name string `json:"name" yaml:"name"`
	Port int    `json:"port" yaml:"port"`
	Free bool   `json:"free" yaml:"free"`
	Note string `json:"note,omitempty" yaml:"note,omitempty"`
}

// Detection is everything `init -detect` reports and records in
// .heimdall/heimdall.yaml.
type Detection struct {
	Root   string       `json:"root" yaml:"root"`
	Tests  TestSetup    `json:"tests" yaml:"tests"`
	Agents []AgentCLI   `json:"agents" yaml:"agents"`
	Docker DockerStatus `json:"docker" yaml:"docker"`
	Ports  []PortStatus `json:"ports" yaml:"ports"`
}

// Probe is how detection reaches the machine; tests replace it.
type Probe struct {
	LookPath func(string) (string, error)
	// Output runs a local command with a short timeout and returns its
	// combined output. It is only ever given version and info commands.
	Output   func(ctx context.Context, name string, args ...string) (string, error)
	PortFree func(port int) bool
}

// SystemProbe is the real machine.
func SystemProbe() Probe {
	return Probe{
		LookPath: exec.LookPath,
		Output: func(ctx context.Context, name string, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env = append(os.Environ(), "NO_COLOR=1", "CI=1")
			out, err := cmd.CombinedOutput()
			return string(out), err
		},
		PortFree: func(port int) bool {
			l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
			if err != nil {
				return false
			}
			_ = l.Close()
			return true
		},
	}
}

// agentCLIs maps each CLI to the bench agent config that runs it.
var agentCLIs = []struct{ cli, config, image string }{
	{"claude", "claude-code", "bench/agent-claude-code:1"},
	{"codex", "codex", "bench/agent-codex:1"},
	{"opencode", "opencode", "bench/agent-opencode:1"},
	{"gemini", "gemini-cli", "bench/agent-gemini-cli:1"},
}

// StackPorts are the loopback ports the project's compose file publishes,
// and the bench server's.
var StackPorts = []PortStatus{
	{Name: "gateway (Bifrost)", Port: 18080},
	{Name: "fault proxy control", Port: 19101},
	{Name: "bench server (heimdall serve)", Port: 8090},
}

// Detect inspects the repository at root and the machine. testCmd, when not
// empty, overrides the detected test command (its template is still taken
// from the detected language).
func Detect(ctx context.Context, root, testCmd string, p Probe) Detection {
	d := Detection{Root: root, Tests: DetectTests(root)}
	if strings.TrimSpace(testCmd) != "" {
		d.Tests.Command = strings.TrimSpace(testCmd)
		d.Tests.Confidence = ConfidenceGiven
		d.Tests.Evidence = append([]string{"-test-cmd"}, d.Tests.Evidence...)
	}
	for _, a := range agentCLIs {
		path, err := p.LookPath(a.cli)
		if err != nil {
			continue
		}
		version := "unknown"
		if out, err := p.Output(ctx, path, "--version"); err == nil {
			if line := firstLine(out); line != "" {
				version = line
			}
		}
		d.Agents = append(d.Agents, AgentCLI{Name: a.cli, Path: path, Version: version, Config: "agents/" + a.config + ".yaml", Image: a.image})
	}
	d.Docker = detectDocker(ctx, p)
	for _, port := range StackPorts {
		port.Free = p.PortFree(port.Port)
		if !port.Free {
			port.Note = "a Heimdall stack may already be running there (then skip `docker compose up`); otherwise change the port in .heimdall/deploy/bench.yaml and docker-compose.yml"
		}
		d.Ports = append(d.Ports, port)
	}
	return d
}

func detectDocker(ctx context.Context, p Probe) DockerStatus {
	var s DockerStatus
	path, err := p.LookPath("docker")
	if err != nil {
		s.Note = "docker is not on PATH; tasks are proved and agents run in containers, so install Docker first"
		return s
	}
	out, err := p.Output(ctx, path, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		s.Note = "docker is installed but the daemon did not answer: " + firstLine(out)
		return s
	}
	s.Available, s.Version = true, firstLine(out)
	if out, err := p.Output(ctx, path, "compose", "version", "--short"); err == nil {
		s.Compose, s.ComposeVersion = true, firstLine(out)
	} else {
		s.Note = "docker compose (v2 plugin) did not answer; the gateway stack needs it"
	}
	return s
}

// npmDefaultTest is the test script `npm init` writes; it is not a test command.
var npmDefaultTest = regexp.MustCompile(`no test specified`)

// DetectTests reads the repository's build files (working tree, read only) and
// names its test command. The first ecosystem that matches wins; others are
// listed in Also. With nothing found it guesses generic-reward with low
// confidence.
func DetectTests(root string) TestSetup {
	var found []TestSetup
	for _, det := range []func(string) (TestSetup, bool){detectNode, detectPython, detectGo, detectRust, detectRuby, detectJava, detectMake} {
		if s, ok := det(root); ok {
			found = append(found, s)
		}
	}
	if len(found) == 0 {
		return TestSetup{Language: "unknown", Command: "", Confidence: ConfidenceLow, Template: scaffold.TemplateGenericReward,
			Evidence: []string{"no package.json, pyproject.toml, go.mod, Cargo.toml, Gemfile, pom.xml, build.gradle or Makefile test target"}}
	}
	// A high-confidence ecosystem beats a medium one found earlier.
	best := 0
	for i, s := range found {
		if s.Confidence == ConfidenceHigh && found[best].Confidence != ConfidenceHigh {
			best = i
		}
	}
	out := found[best]
	for i, s := range found {
		if i != best {
			out.Also = append(out.Also, fmt.Sprintf("%s: %s (%s)", s.Language, s.Command, s.Confidence))
		}
	}
	return out
}

func detectNode(root string) (TestSetup, bool) {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return TestSetup{}, false
	}
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		DevDependencies map[string]string `json:"devDependencies"`
		Dependencies    map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return TestSetup{Language: "node", Command: "npm test", Confidence: ConfidenceLow, Template: scaffold.TemplateNodeTest,
			Evidence: []string{"package.json (does not parse: " + err.Error() + ")"}}, true
	}
	runner := "npm"
	switch {
	case exists(root, "pnpm-lock.yaml"):
		runner = "pnpm"
	case exists(root, "yarn.lock"):
		runner = "yarn"
	case exists(root, "bun.lockb"), exists(root, "bun.lock"):
		runner = "bun run"
	}
	script := strings.TrimSpace(pkg.Scripts["test"])
	s := TestSetup{Language: "node", Template: scaffold.TemplateNodeTest}
	switch {
	case script != "" && !npmDefaultTest.MatchString(script):
		s.Command, s.Confidence = runner+" test", ConfidenceHigh
		s.Evidence = []string{fmt.Sprintf("package.json scripts.test: %q", script)}
		// node:test runs hidden files directly; another framework needs its
		// own runner and node_modules in the image, so the reward template
		// runs the repository's command instead.
		if !strings.Contains(script, "node --test") && !strings.Contains(script, "node:test") {
			s.Template = scaffold.TemplateGenericReward
		}
	case exists(root, "test") || exists(root, "tests") || exists(root, "__tests__"):
		s.Command, s.Confidence = "node --test", ConfidenceMedium
		s.Evidence = []string{"package.json without a test script; a test folder exists"}
	default:
		s.Command, s.Confidence = "node --test", ConfidenceLow
		s.Evidence = []string{"package.json without a test script"}
	}
	return s, true
}

func detectPython(root string) (TestSetup, bool) {
	s := TestSetup{Language: "python", Command: "python -m pytest", Template: scaffold.TemplatePythonPytest}
	if b, err := os.ReadFile(filepath.Join(root, "pyproject.toml")); err == nil {
		switch {
		case bytes.Contains(b, []byte("[tool.pytest")):
			s.Confidence, s.Evidence = ConfidenceHigh, []string{"pyproject.toml [tool.pytest.ini_options]"}
		case bytes.Contains(b, []byte("pytest")):
			s.Confidence, s.Evidence = ConfidenceHigh, []string{"pyproject.toml names pytest"}
		default:
			s.Confidence, s.Evidence = ConfidenceMedium, []string{"pyproject.toml (no pytest setting)"}
		}
		return s, true
	}
	for _, f := range []string{"pytest.ini", "conftest.py"} {
		if exists(root, f) {
			s.Confidence, s.Evidence = ConfidenceHigh, []string{f}
			return s, true
		}
	}
	for _, f := range []string{"setup.cfg", "tox.ini"} {
		if b, err := os.ReadFile(filepath.Join(root, f)); err == nil && bytes.Contains(b, []byte("pytest")) {
			s.Confidence, s.Evidence = ConfidenceHigh, []string{f + " names pytest"}
			return s, true
		}
	}
	if hasPythonTests(root) {
		s.Confidence, s.Evidence = ConfidenceMedium, []string{"test_*.py files under tests/ or the root"}
		return s, true
	}
	if exists(root, "setup.py") || exists(root, "requirements.txt") {
		s.Confidence, s.Evidence = ConfidenceLow, []string{"setup.py or requirements.txt, no tests found"}
		return s, true
	}
	return TestSetup{}, false
}

func hasPythonTests(root string) bool {
	for _, dir := range []string{root, filepath.Join(root, "tests"), filepath.Join(root, "test")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() && strings.HasSuffix(n, ".py") && (strings.HasPrefix(n, "test_") || strings.HasSuffix(n, "_test.py")) {
				return true
			}
		}
	}
	return false
}

func detectGo(root string) (TestSetup, bool) {
	if !exists(root, "go.mod") {
		return TestSetup{}, false
	}
	return TestSetup{Language: "go", Command: "go test ./...", Confidence: ConfidenceHigh, Template: scaffold.TemplateGoTest,
		Evidence: []string{"go.mod"}}, true
}

func detectRust(root string) (TestSetup, bool) {
	if !exists(root, "Cargo.toml") {
		return TestSetup{}, false
	}
	return TestSetup{Language: "rust", Command: "cargo test", Confidence: ConfidenceHigh, Template: scaffold.TemplateGenericReward,
		Evidence: []string{"Cargo.toml"}}, true
}

func detectRuby(root string) (TestSetup, bool) {
	b, err := os.ReadFile(filepath.Join(root, "Gemfile"))
	if err != nil {
		return TestSetup{}, false
	}
	s := TestSetup{Language: "ruby", Template: scaffold.TemplateGenericReward}
	switch {
	case bytes.Contains(b, []byte("rspec")) || exists(root, ".rspec") || exists(root, "spec"):
		s.Command, s.Confidence, s.Evidence = "bundle exec rspec", ConfidenceHigh, []string{"Gemfile with rspec (or .rspec, spec/)"}
	case exists(root, "Rakefile"):
		s.Command, s.Confidence, s.Evidence = "bundle exec rake test", ConfidenceMedium, []string{"Gemfile and Rakefile"}
	default:
		s.Command, s.Confidence, s.Evidence = "bundle exec rake test", ConfidenceLow, []string{"Gemfile"}
	}
	return s, true
}

func detectJava(root string) (TestSetup, bool) {
	s := TestSetup{Language: "java", Template: scaffold.TemplateGenericReward, Confidence: ConfidenceHigh}
	switch {
	case exists(root, "pom.xml"):
		s.Command, s.Evidence = "mvn -q test", []string{"pom.xml"}
		if exists(root, "mvnw") {
			s.Command = "./mvnw -q test"
		}
	case exists(root, "build.gradle"), exists(root, "build.gradle.kts"):
		s.Command, s.Evidence = "gradle test", []string{"build.gradle"}
		if exists(root, "gradlew") {
			s.Command = "./gradlew test"
		}
	default:
		return TestSetup{}, false
	}
	return s, true
}

var makeTestRE = regexp.MustCompile(`^test\s*:`)

func detectMake(root string) (TestSetup, bool) {
	f, err := os.Open(filepath.Join(root, "Makefile"))
	if err != nil {
		return TestSetup{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if makeTestRE.MatchString(sc.Text()) {
			return TestSetup{Language: "make", Command: "make test", Confidence: ConfidenceMedium, Template: scaffold.TemplateGenericReward,
				Evidence: []string{"Makefile test target"}}, true
		}
	}
	return TestSetup{}, false
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, rel))
	return err == nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
