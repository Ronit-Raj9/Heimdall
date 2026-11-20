package quickstart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/scaffold"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectTestsPerEcosystem(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		lang, cmd  string
		confidence string
		template   string
	}{
		{"node test script node:test", map[string]string{"package.json": `{"scripts":{"test":"node --test test/"}}`},
			"node", "npm test", ConfidenceHigh, scaffold.TemplateNodeTest},
		{"node jest via pnpm", map[string]string{"package.json": `{"scripts":{"test":"jest"}}`, "pnpm-lock.yaml": ""},
			"node", "pnpm test", ConfidenceHigh, scaffold.TemplateGenericReward},
		{"node yarn vitest", map[string]string{"package.json": `{"scripts":{"test":"vitest run"}}`, "yarn.lock": ""},
			"node", "yarn test", ConfidenceHigh, scaffold.TemplateGenericReward},
		{"node default npm script", map[string]string{"package.json": `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`, "test/a.test.js": ""},
			"node", "node --test", ConfidenceMedium, scaffold.TemplateNodeTest},
		{"node no tests", map[string]string{"package.json": `{"name":"x"}`},
			"node", "node --test", ConfidenceLow, scaffold.TemplateNodeTest},
		{"python pyproject pytest", map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\ntestpaths=['tests']\n"},
			"python", "python -m pytest", ConfidenceHigh, scaffold.TemplatePythonPytest},
		{"python pytest.ini", map[string]string{"pytest.ini": "[pytest]\n"},
			"python", "python -m pytest", ConfidenceHigh, scaffold.TemplatePythonPytest},
		{"python setup.cfg", map[string]string{"setup.cfg": "[tool:pytest]\n"},
			"python", "python -m pytest", ConfidenceHigh, scaffold.TemplatePythonPytest},
		{"python layout only", map[string]string{"tests/test_x.py": "def test_x(): pass\n"},
			"python", "python -m pytest", ConfidenceMedium, scaffold.TemplatePythonPytest},
		{"python pyproject without pytest", map[string]string{"pyproject.toml": "[project]\nname='x'\n"},
			"python", "python -m pytest", ConfidenceMedium, scaffold.TemplatePythonPytest},
		{"go", map[string]string{"go.mod": "module x\n"},
			"go", "go test ./...", ConfidenceHigh, scaffold.TemplateGoTest},
		{"rust", map[string]string{"Cargo.toml": "[package]\n"},
			"rust", "cargo test", ConfidenceHigh, scaffold.TemplateGenericReward},
		{"ruby rspec", map[string]string{"Gemfile": "gem 'rspec'\n"},
			"ruby", "bundle exec rspec", ConfidenceHigh, scaffold.TemplateGenericReward},
		{"ruby rake", map[string]string{"Gemfile": "gem 'minitest'\n", "Rakefile": ""},
			"ruby", "bundle exec rake test", ConfidenceMedium, scaffold.TemplateGenericReward},
		{"maven wrapper", map[string]string{"pom.xml": "<project/>", "mvnw": ""},
			"java", "./mvnw -q test", ConfidenceHigh, scaffold.TemplateGenericReward},
		{"gradle", map[string]string{"build.gradle.kts": ""},
			"java", "gradle test", ConfidenceHigh, scaffold.TemplateGenericReward},
		{"make", map[string]string{"Makefile": "build:\n\tcc x.c\n\ntest: build\n\t./run-tests\n"},
			"make", "make test", ConfidenceMedium, scaffold.TemplateGenericReward},
		{"nothing", map[string]string{"README.md": "hi\n"},
			"unknown", "", ConfidenceLow, scaffold.TemplateGenericReward},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectTests(writeTree(t, tc.files))
			if got.Language != tc.lang || got.Command != tc.cmd || got.Confidence != tc.confidence || got.Template != tc.template {
				t.Errorf("got %s %q %s %s, want %s %q %s %s", got.Language, got.Command, got.Confidence, got.Template,
					tc.lang, tc.cmd, tc.confidence, tc.template)
			}
			if len(got.Evidence) == 0 {
				t.Error("no evidence")
			}
		})
	}
}

func TestDetectTestsHighBeatsMedium(t *testing.T) {
	// A Node package.json with no test script next to go.mod: Go is certain.
	got := DetectTests(writeTree(t, map[string]string{"package.json": `{}`, "go.mod": "module x\n", "Makefile": "test:\n\tgo test\n"}))
	if got.Language != "go" || len(got.Also) != 2 {
		t.Fatalf("got %+v", got)
	}
}

// fakeProbe answers like a machine with claude and codex installed, Docker
// up, and port 18080 taken.
func fakeProbe() Probe {
	return Probe{
		LookPath: func(name string) (string, error) {
			switch name {
			case "claude", "codex", "docker":
				return "/usr/local/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Output: func(_ context.Context, name string, args ...string) (string, error) {
			switch {
			case strings.HasSuffix(name, "claude"):
				return "2.1.0 (Claude Code)\n", nil
			case strings.HasSuffix(name, "codex"):
				return "", errors.New("exit 1")
			case slices.Contains(args, "compose"):
				return "v2.40.0\n", nil
			case strings.HasSuffix(name, "docker"):
				return "28.1.1\n", nil
			}
			return "", errors.New("unexpected " + name)
		},
		PortFree: func(port int) bool { return port != 18080 },
	}
}

func TestDetectMachine(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": "module x\n"})
	d := Detect(context.Background(), root, "  make check  ", fakeProbe())
	if d.Tests.Command != "make check" || d.Tests.Confidence != ConfidenceGiven || d.Tests.Template != scaffold.TemplateGoTest {
		t.Errorf("tests = %+v", d.Tests)
	}
	if len(d.Agents) != 2 || d.Agents[0].Name != "claude" || d.Agents[0].Version != "2.1.0 (Claude Code)" || d.Agents[0].Config != "agents/claude-code.yaml" ||
		d.Agents[1].Version != "unknown" {
		t.Errorf("agents = %+v", d.Agents)
	}
	if !d.Docker.Available || !d.Docker.Compose || d.Docker.Version != "28.1.1" || d.Docker.ComposeVersion != "v2.40.0" {
		t.Errorf("docker = %+v", d.Docker)
	}
	if len(d.Ports) != 3 || d.Ports[0].Free || d.Ports[0].Note == "" || !d.Ports[1].Free {
		t.Errorf("ports = %+v", d.Ports)
	}
	none := fakeProbe()
	none.LookPath = func(string) (string, error) { return "", errors.New("no") }
	d = Detect(context.Background(), root, "", none)
	if d.Docker.Available || d.Docker.Note == "" || len(d.Agents) != 0 {
		t.Errorf("empty machine: %+v %+v", d.Docker, d.Agents)
	}
}

// A -test-cmd override on a standard template says it does not grade
// (audit 5-B-15).
func TestTestCommandScope(t *testing.T) {
	given := TestSetup{Command: "python -m pytest -m audit_selected", Confidence: ConfidenceGiven, Template: scaffold.TemplatePythonPytest}
	if note := TestCommandScope(given); !strings.Contains(note, "verifier.command") {
		t.Fatalf("note %q", note)
	}
	if !strings.Contains(suiteHeader(given), "verifier.command") {
		t.Fatal("suite header lacks the scope note")
	}
	given.Template = scaffold.TemplateGenericReward
	if TestCommandScope(given) != "" {
		t.Fatal("generic-reward runs the command")
	}
	detected := TestSetup{Command: "python -m pytest", Template: scaffold.TemplatePythonPytest}
	if TestCommandScope(detected) != "" {
		t.Fatal("note without an override")
	}
}
