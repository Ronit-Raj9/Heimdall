package quickstart

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Dir is the project folder init writes inside a repository.
const Dir = ".heimdall"

// RunsIgnore is the line init adds to the repository's .gitignore.
const RunsIgnore = ".heimdall/runs/"

// HelperImage is the trusted helper image every project builds; the
// python-pytest suite reuses it as its toolchain image (same Dockerfile).
const HelperImage = "bench/coding-core-base:1"

//go:embed agents
var agentFiles embed.FS

// ProjectFile is .heimdall/heimdall.yaml: what init detected, so later
// commands (tasks mine, compare, ci install) know the suite and the test
// command without asking again.
type ProjectFile struct {
	Version   int          `yaml:"version"`
	Repo      string       `yaml:"repo"`  // repository name
	Suite     string       `yaml:"suite"` // suite id under benchmarks/
	Tests     TestSetup    `yaml:"tests"`
	Agents    []AgentCLI   `yaml:"agents"`
	Docker    DockerStatus `yaml:"docker"`
	Ports     []PortStatus `yaml:"ports"`
	CreatedBy string       `yaml:"created_by"`
}

// ProjectOptions configures Project.
type ProjectOptions struct {
	Detection Detection
	Name      string // repository name; the folder name by default
	Suite     string // suite id; derived from Name by default
	Image     string // toolchain image for the suite; empty picks per template
}

// Project returns every file of .heimdall/ (paths relative to it): the
// scaffold project (deploy/, scripted agents, policies, README), the suite
// from the detected template, one agent config per detected CLI with its
// image Dockerfile, deploy/.env with fresh random tokens, and heimdall.yaml.
func Project(opts ProjectOptions) ([]scaffold.File, *ProjectFile, error) {
	name := opts.Name
	if name == "" {
		name = filepath.Base(opts.Detection.Root)
	}
	suiteID := opts.Suite
	if suiteID == "" {
		suiteID = SuiteID(name)
	}
	if !scaffold.ValidID(suiteID) {
		return nil, nil, fmt.Errorf("suite id %q is not valid", suiteID)
	}
	files, err := scaffold.Project(scaffold.ProjectOptions{Name: name})
	if err != nil {
		return nil, nil, err
	}
	for i, f := range files {
		if f.Path == "experiments/smoke.yaml" {
			files[i].Data = bytes.Replace(f.Data, []byte("suite: my-suite"), []byte("suite: "+suiteID), 1)
		}
	}
	tests := opts.Detection.Tests
	image := opts.Image
	if image == "" && tests.Template == scaffold.TemplatePythonPytest {
		image = HelperImage
	}
	suite, err := scaffold.Suite(scaffold.SuiteOptions{ID: suiteID, Template: tests.Template, Image: image})
	if err != nil {
		return nil, nil, err
	}
	for _, f := range suite {
		f.Path = "benchmarks/" + suiteID + "/" + f.Path
		if f.Path == "benchmarks/"+suiteID+"/suite.yaml" {
			f.Data = append([]byte(suiteHeader(tests)), f.Data...)
		}
		files = append(files, f)
	}
	agents, err := agentConfigs(opts.Detection.Agents)
	if err != nil {
		return nil, nil, err
	}
	files = append(files, agents...)
	env, err := envFile(files)
	if err != nil {
		return nil, nil, err
	}
	files = append(files, env)
	pf := &ProjectFile{Version: 1, Repo: name, Suite: suiteID, Tests: tests, Agents: opts.Detection.Agents,
		Docker: opts.Detection.Docker, Ports: opts.Detection.Ports, CreatedBy: "heimdall init"}
	raw, err := yaml.Marshal(pf)
	if err != nil {
		return nil, nil, err
	}
	files = append(files, scaffold.File{Path: "heimdall.yaml", Mode: 0o644,
		Data: append([]byte("# Written by `heimdall init`: what it detected in this repository. tasks mine,\n# compare and ci install read it. Edit tests.command if the guess is wrong.\n"), raw...)})
	return files, pf, nil
}

// suiteHeader is the comment init puts above suite.yaml.
func suiteHeader(t TestSetup) string {
	cmd := t.Command
	if cmd == "" {
		cmd = "(none found; set tests.command in .heimdall/heimdall.yaml)"
	}
	return fmt.Sprintf("# Repository test command (heimdall init, %s confidence): %s\n# `heimdall tasks mine` cuts tasks from fix commits into this suite.\n", t.Confidence, cmd)
}

var nonID = regexp.MustCompile(`[^a-z0-9._-]+`)

// SuiteID turns a repository name into a suite id.
func SuiteID(name string) string {
	id := strings.Trim(nonID.ReplaceAllString(strings.ToLower(name), "-"), "-._")
	if id == "" {
		return "repo"
	}
	return id
}

// agentConfigs copies the bench's agent config of each detected CLI, with a
// header saying where the CLI was found and how to build its image.
func agentConfigs(agents []AgentCLI) ([]scaffold.File, error) {
	var out []scaffold.File
	for _, a := range agents {
		id := strings.TrimSuffix(path.Base(a.Config), ".yaml")
		raw, err := agentFiles.ReadFile("agents/" + id + ".yaml")
		if err != nil {
			return nil, fmt.Errorf("agent config for %s: %w", a.Name, err)
		}
		docker, err := agentFiles.ReadFile("agents/images/" + id + "/Dockerfile")
		if err != nil {
			return nil, fmt.Errorf("agent image for %s: %w", a.Name, err)
		}
		head := fmt.Sprintf("# Written by `heimdall init`: %s %s was found at %s on this machine.\n"+
			"# The run uses the CLI inside the container image below, not the host's copy;\n"+
			"# build it once from the project folder (no model call):\n"+
			"#   docker build -t %s agents/images/%s\n"+
			"# Pin the CLI version in agents/images/%s/Dockerfile to match the host's when you\n"+
			"# want the benchmark to run what you run.\n", a.Name, a.Version, a.Path, a.Image, id, id)
		out = append(out,
			scaffold.File{Path: "agents/" + id + ".yaml", Data: append([]byte(head), raw...), Mode: 0o644},
			scaffold.File{Path: "agents/images/" + id + "/Dockerfile", Data: docker, Mode: 0o644})
	}
	return out, nil
}

// envFile writes deploy/.env from the template with fresh random tokens, so
// the stack starts without a manual step. Provider keys stay empty: the fault
// proxy answers from its scripted model until one is set.
func envFile(files []scaffold.File) (scaffold.File, error) {
	var tmpl []byte
	for _, f := range files {
		if f.Path == "deploy/.env.example" {
			tmpl = f.Data
		}
	}
	if tmpl == nil {
		return scaffold.File{}, errors.New("project template has no deploy/.env.example")
	}
	out := string(tmpl)
	for _, key := range []string{"BIFROST_SETUP_TOKEN", "BIFROST_PG_PASSWORD", "BENCH_FAULTPROXY_TOKEN"} {
		tok, err := randomHex(24)
		if err != nil {
			return scaffold.File{}, err
		}
		re := regexp.MustCompile(`(?m)^` + key + `=.*$`)
		out = re.ReplaceAllString(out, key+"="+tok)
	}
	return scaffold.File{Path: "deploy/.env", Data: []byte(out), Mode: 0o600}, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// WriteProject writes files under dir. Without force an existing file is
// left alone and reported as kept (so a second init never replaces a
// .env or an edited suite); with force the project's own files are replaced.
func WriteProject(dir string, files []scaffold.File, force bool) (written, kept []string, err error) {
	for _, f := range files {
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if _, err := os.Lstat(dst); err == nil && (!force || f.Path == "deploy/.env") {
			kept = append(kept, f.Path)
			continue
		}
		if err := scaffold.WriteFiles(dir, []scaffold.File{f}, true); err != nil {
			return written, kept, err
		}
		written = append(written, f.Path)
	}
	return written, kept, nil
}

// EnsureIgnored adds line to the .gitignore at the repository root unless a
// line already ignores it. It reports whether the file changed.
func EnsureIgnored(repoRoot, line string) (bool, error) {
	p := filepath.Join(repoRoot, ".gitignore")
	raw, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	want := strings.TrimSuffix(strings.TrimPrefix(line, "/"), "/")
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if strings.TrimSuffix(strings.TrimPrefix(l, "/"), "/") == want || l == ".heimdall" || l == ".heimdall/" || l == "/.heimdall/" {
			return false, nil
		}
	}
	out := raw
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, []byte("# Heimdall sealed runs (heimdall init)\n"+line+"\n")...)
	return true, os.WriteFile(p, out, 0o644)
}

// LoadProjectFile reads .heimdall/heimdall.yaml from a project root.
func LoadProjectFile(projectRoot string) (*ProjectFile, error) {
	raw, err := os.ReadFile(filepath.Join(projectRoot, "heimdall.yaml"))
	if err != nil {
		return nil, err
	}
	var pf ProjectFile
	if err := yaml.Unmarshal(raw, &pf); err != nil {
		return nil, fmt.Errorf("heimdall.yaml: %w", err)
	}
	return &pf, nil
}

// FindProject returns the .heimdall folder holding deploy/bench.yaml in dir
// or its nearest parent that has one, or "".
func FindProject(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		cand := filepath.Join(abs, Dir)
		if _, err := os.Stat(filepath.Join(cand, "deploy", "bench.yaml")); err == nil {
			return cand
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}
