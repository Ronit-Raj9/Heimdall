package quickstart

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/scaffold"
)

func testDetection(root string) Detection {
	return Detection{
		Root:   root,
		Tests:  TestSetup{Language: "python", Command: "python -m pytest", Confidence: ConfidenceHigh, Template: scaffold.TemplatePythonPytest, Evidence: []string{"pytest.ini"}},
		Agents: []AgentCLI{{Name: "claude", Path: "/bin/claude", Version: "2.1.0", Config: "agents/claude-code.yaml", Image: "bench/agent-claude-code:1"}},
		Docker: DockerStatus{Available: true, Version: "28", Compose: true},
	}
}

func filesByPath(files []scaffold.File) map[string]scaffold.File {
	out := map[string]scaffold.File{}
	for _, f := range files {
		out[f.Path] = f
	}
	return out
}

func TestProject(t *testing.T) {
	files, pf, err := Project(ProjectOptions{Detection: testDetection("/src/My Lib")})
	if err != nil {
		t.Fatal(err)
	}
	if pf.Suite != "my-lib" || pf.Repo != "My Lib" || pf.Tests.Command != "python -m pytest" {
		t.Errorf("project file = %+v", pf)
	}
	byPath := filesByPath(files)
	for _, want := range []string{"deploy/bench.yaml", "deploy/docker-compose.yml", "deploy/.env.example", "deploy/.env", "policies/default.yaml",
		"agents/oracle.yaml", "agents/noop.yaml", "agents/toy.yaml", "agents/claude-code.yaml", "agents/images/claude-code/Dockerfile",
		"benchmarks/my-lib/suite.yaml", "experiments/smoke.yaml", "heimdall.yaml", ".gitignore", "README.md"} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	if _, ok := byPath["benchmarks/my-lib/base/Dockerfile"]; ok {
		t.Error("python suite should reuse the helper image, not build base/Dockerfile")
	}
	suite := string(byPath["benchmarks/my-lib/suite.yaml"].Data)
	if !strings.Contains(suite, "python -m pytest") || !strings.Contains(suite, "tag: "+HelperImage) || scaffold.DetectTemplate([]byte(suite)) != scaffold.TemplatePythonPytest {
		t.Errorf("suite.yaml:\n%s", suite)
	}
	if !bytes.Contains(byPath["experiments/smoke.yaml"].Data, []byte("suite: my-lib")) {
		t.Error("smoke.yaml does not name the suite")
	}
	agent := string(byPath["agents/claude-code.yaml"].Data)
	if !strings.Contains(agent, "claude 2.1.0 was found at /bin/claude") || !strings.Contains(agent, "docker build -t bench/agent-claude-code:1 agents/images/claude-code") {
		t.Errorf("agent header:\n%s", agent[:300])
	}
	env := byPath["deploy/.env"]
	if env.Mode != 0o600 || strings.Contains(string(env.Data), "replace-with-a-random") {
		t.Errorf(".env mode %o or placeholder left:\n%s", env.Mode, env.Data)
	}
	tok := regexp.MustCompile(`(?m)^BENCH_FAULTPROXY_TOKEN=([0-9a-f]{48})$`)
	if !tok.Match(env.Data) {
		t.Error(".env has no fresh fault proxy token")
	}
	again, _, _ := Project(ProjectOptions{Detection: testDetection("/src/My Lib")})
	if bytes.Equal(filesByPath(again)["deploy/.env"].Data, env.Data) {
		t.Error("two inits wrote the same tokens")
	}
	// Other templates build their own base image.
	d := testDetection("/src/x")
	d.Tests.Template = scaffold.TemplateGenericReward
	files, _, err = Project(ProjectOptions{Detection: d})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := filesByPath(files)["benchmarks/x/base/Dockerfile"]; !ok {
		t.Error("generic-reward suite has no base/Dockerfile")
	}
}

func TestWriteProjectKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	files, _, err := Project(ProjectOptions{Detection: testDetection("/src/x")})
	if err != nil {
		t.Fatal(err)
	}
	written, kept, err := WriteProject(dir, files, false)
	if err != nil || len(written) != len(files) || len(kept) != 0 {
		t.Fatalf("first write: %d written, %d kept, %v", len(written), len(kept), err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "deploy", ".env")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf(".env mode: %v %v", fi, err)
	}
	envBefore, _ := os.ReadFile(filepath.Join(dir, "deploy", ".env"))
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	files2, _, _ := Project(ProjectOptions{Detection: testDetection("/src/x")})
	_, kept, err = WriteProject(dir, files2, false)
	if err != nil || len(kept) != len(files2) {
		t.Fatalf("second write kept %d of %d: %v", len(kept), len(files2), err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(b) != "mine" {
		t.Error("second init replaced an edited file")
	}
	if _, _, err := WriteProject(dir, files2, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(b) == "mine" {
		t.Error("-force did not replace README.md")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "deploy", ".env")); !bytes.Equal(b, envBefore) {
		t.Error("-force replaced deploy/.env")
	}
	if FindProject(filepath.Join(dir, "..")) != "" {
		t.Error("found a project where none is")
	}
}

func TestEnsureIgnored(t *testing.T) {
	dir := t.TempDir()
	changed, err := EnsureIgnored(dir, RunsIgnore)
	if err != nil || !changed {
		t.Fatalf("first: %v %v", changed, err)
	}
	changed, err = EnsureIgnored(dir, RunsIgnore)
	if err != nil || changed {
		t.Fatalf("second: %v %v", changed, err)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, ".gitignore"), []byte("node_modules\n/.heimdall/"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, _ := EnsureIgnored(other, RunsIgnore); changed {
		t.Error("a .gitignore that ignores all of .heimdall was edited")
	}
}

// TestEmbeddedAgentsMatchBench keeps the copies init writes equal to the
// bench's own agent configs and image Dockerfiles.
func TestEmbeddedAgentsMatchBench(t *testing.T) {
	for _, a := range agentCLIs {
		for _, rel := range []string{a.config + ".yaml", "images/" + a.config + "/Dockerfile"} {
			want, err := os.ReadFile(filepath.Join("..", "agents", filepath.FromSlash(rel)))
			if err != nil {
				t.Fatal(err)
			}
			got, err := agentFiles.ReadFile("agents/" + rel)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("quickstart/agents/%s differs from bench/agents/%s; copy it again", rel, rel)
			}
		}
	}
}

func TestSuiteID(t *testing.T) {
	for in, want := range map[string]string{"My Lib": "my-lib", "minitext": "minitext", "__": "repo", "a.b_c": "a.b_c"} {
		if got := SuiteID(in); got != want || !scaffold.ValidID(got) {
			t.Errorf("SuiteID(%q) = %q, want %q", in, got, want)
		}
	}
}
