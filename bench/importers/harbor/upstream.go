package harbor

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Task layouts the importer reads.
const (
	// LayoutHarbor is the Harbor task folder: task.toml, instruction.md,
	// environment/, solution/solve.sh and tests/test.sh. Harbor uploads the
	// tests to /tests and the solution to /solution.
	LayoutHarbor = "harbor"
	// LayoutTaskMD is the BenchFlow task.md package SkillsBench publishes from
	// version 1.1 on: task.md (YAML front matter, then the instruction),
	// environment/, oracle/solve.sh and verifier/test.sh, uploaded to /oracle
	// and /verifier.
	LayoutTaskMD = "task_md"
)

// upstreamTask is one upstream task folder, read but not yet converted.
type upstreamTask struct {
	ID     string // folder name
	Dir    string // absolute task folder
	Layout string // LayoutHarbor or LayoutTaskMD

	Name        string // [task].name, e.g. terminal-bench/chess-best-move
	Description string
	Instruction string // the instruction text
	Difficulty  string
	Category    string
	Tags        []string

	AgentTimeoutS    float64
	VerifierTimeoutS float64

	// Long-Horizon Terminal-Bench (LHTB) additions to [agent] and
	// [environment]: its modified Harbor resumes the agent after a failed
	// verifier pass until the timeout (continue_until_timeout), runs the
	// agent as a named user, and starts daemons from a healthcheck command
	// before the agent's first turn.
	ContinueUntilTimeout bool
	AgentUser            string
	Healthcheck          string

	DockerImage   string // prebuilt image upstream also publishes
	Workdir       string // workdir override from the task file (task.md sandbox.workdir)
	GPUs          int64
	AllowInternet bool
	MCPServers    int
	Compose       bool // environment/ holds a compose file: a multi-container task
	// SeparateVerifier is set when the task grades in its own container
	// ([verifier] environment_mode = "separate", a [verifier.environment]
	// table, or [[verifier.collect]] hooks that carry the agent's work there).
	SeparateVerifier bool

	EnvEnv      map[string]string // [environment.env]: set in the task image
	VerifierEnv map[string]string // [verifier.env]: set for test.sh
	SolutionEnv map[string]string // [solution.env] or oracle.env: set for solve.sh

	EnvDir        string // absolute environment folder
	SolutionDir   string // absolute solution (or oracle) folder
	TestsDir      string // absolute tests (or verifier) folder
	TestsMount    string // where test.sh expects its folder: /tests or /verifier
	SolutionMount string // where solve.sh expects its folder: /solution or /oracle
}

// detectLayout says which layout dir holds, or "" when it is no task folder.
func detectLayout(dir string) string {
	switch {
	case isFile(filepath.Join(dir, "task.toml")):
		return LayoutHarbor
	case isFile(filepath.Join(dir, "task.md")):
		return LayoutTaskMD
	}
	return ""
}

// readTask reads the task folder dir in its layout. An error names what is
// wrong; the caller turns it into a skip.
func readTask(dir string) (*upstreamTask, error) {
	t := &upstreamTask{ID: filepath.Base(dir), Dir: dir, EnvDir: filepath.Join(dir, "environment")}
	switch t.Layout = detectLayout(dir); t.Layout {
	case LayoutHarbor:
		if err := t.readHarbor(); err != nil {
			return nil, err
		}
	case LayoutTaskMD:
		if err := t.readTaskMD(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("neither task.toml nor task.md")
	}
	// Terminal-Bench's custom_docker_compose flag is not used: tasks whose
	// compose settings moved into the Dockerfile keep the flag but no file.
	t.Compose = anyFile(t.EnvDir, "docker-compose.yaml", "docker-compose.yml", "compose.yaml", "compose.yml")
	return t, nil
}

// readHarbor reads task.toml and instruction.md.
func (t *upstreamTask) readHarbor() error {
	b, err := os.ReadFile(filepath.Join(t.Dir, "task.toml"))
	if err != nil {
		return err
	}
	doc, err := parseTOML(string(b))
	if err != nil {
		return err
	}
	instr, err := os.ReadFile(filepath.Join(t.Dir, "instruction.md"))
	if err != nil {
		return fmt.Errorf("instruction.md: %w", err)
	}
	t.Instruction = string(instr)
	t.SolutionDir = filepath.Join(t.Dir, "solution")
	t.TestsDir = filepath.Join(t.Dir, "tests")
	t.TestsMount, t.SolutionMount = "/tests", "/solution"

	task := tomlTable(doc, "task")
	meta := tomlTable(doc, "metadata")
	env := tomlTable(doc, "environment")
	t.Name = tomlString(task, "name")
	t.Description = tomlString(task, "description")
	t.Difficulty = tomlString(meta, "difficulty")
	t.Category = tomlString(meta, "category")
	t.Tags = dedupe(append(tomlStrings(meta, "tags"), tomlStrings(task, "keywords")...))
	verifier := tomlTable(doc, "verifier")
	agent := tomlTable(doc, "agent")
	t.AgentTimeoutS = tomlNumber(agent, "timeout_sec")
	t.VerifierTimeoutS = tomlNumber(verifier, "timeout_sec")
	t.ContinueUntilTimeout = tomlBool(agent, "continue_until_timeout")
	t.AgentUser = tomlString(agent, "user")
	t.Healthcheck = tomlString(tomlTable(env, "healthcheck"), "command")
	_, hasEnv := verifier["environment"].(map[string]any)
	t.SeparateVerifier = tomlString(verifier, "environment_mode") == "separate" || hasEnv || len(tomlList(verifier, "collect")) > 0
	t.DockerImage = tomlString(env, "docker_image")
	t.GPUs = int64(tomlNumber(env, "gpus"))
	t.AllowInternet = tomlBool(env, "allow_internet")
	t.MCPServers = len(tomlList(env, "mcp_servers"))
	if t.EnvEnv, err = tomlEnv(env, "environment.env"); err != nil {
		return err
	}
	if t.VerifierEnv, err = tomlEnv(tomlTable(doc, "verifier"), "verifier.env"); err != nil {
		return err
	}
	if t.SolutionEnv, err = tomlEnv(tomlTable(doc, "solution"), "solution.env"); err != nil {
		return err
	}
	return nil
}

// taskMDFront is the part of task.md's front matter the importer reads.
type taskMDFront struct {
	Metadata struct {
		Difficulty string   `yaml:"difficulty"`
		Category   string   `yaml:"category"`
		Tags       []string `yaml:"tags"`
	} `yaml:"metadata"`
	Verifier struct {
		TimeoutS float64           `yaml:"timeout_sec"`
		Env      map[string]string `yaml:"env"`
	} `yaml:"verifier"`
	Agent struct {
		TimeoutS float64 `yaml:"timeout_sec"`
	} `yaml:"agent"`
	Oracle struct {
		Env map[string]string `yaml:"env"`
	} `yaml:"oracle"`
	Sandbox struct {
		NetworkMode string `yaml:"network_mode"`
		GPUs        int64  `yaml:"gpus"`
		Workdir     string `yaml:"workdir"`
	} `yaml:"sandbox"`
	Environment map[string]string `yaml:"environment"`
}

// readTaskMD reads task.md: YAML front matter between --- lines, then the
// instruction.
func (t *upstreamTask) readTaskMD() error {
	b, err := os.ReadFile(filepath.Join(t.Dir, "task.md"))
	if err != nil {
		return err
	}
	front, body, err := splitFrontMatter(b)
	if err != nil {
		return err
	}
	var fm taskMDFront
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return fmt.Errorf("task.md front matter: %w", err)
	}
	t.Instruction = strings.TrimLeft(string(body), "\n")
	t.SolutionDir = filepath.Join(t.Dir, "oracle")
	t.TestsDir = filepath.Join(t.Dir, "verifier")
	t.TestsMount, t.SolutionMount = "/verifier", "/oracle"
	t.Difficulty = fm.Metadata.Difficulty
	t.Category = fm.Metadata.Category
	t.Tags = dedupe(fm.Metadata.Tags)
	t.AgentTimeoutS = fm.Agent.TimeoutS
	t.VerifierTimeoutS = fm.Verifier.TimeoutS
	t.GPUs = fm.Sandbox.GPUs
	t.Workdir = fm.Sandbox.Workdir
	t.AllowInternet = fm.Sandbox.NetworkMode != "" && fm.Sandbox.NetworkMode != "no-network" && fm.Sandbox.NetworkMode != "none"
	t.EnvEnv = fm.Environment
	t.VerifierEnv = fm.Verifier.Env
	t.SolutionEnv = fm.Oracle.Env
	return nil
}

// splitFrontMatter splits "---\n<yaml>\n---\n<body>".
func splitFrontMatter(b []byte) ([]byte, []byte, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !bytes.HasPrefix(b, []byte("---\n")) && !bytes.HasPrefix(b, []byte("---\r\n")) {
		return nil, nil, fmt.Errorf("task.md: no front matter")
	}
	rest := b[bytes.IndexByte(b, '\n')+1:]
	for i := 0; i < len(rest); {
		j := bytes.IndexByte(rest[i:], '\n')
		line := rest[i:]
		if j >= 0 {
			line = rest[i : i+j]
		}
		if strings.TrimRight(string(line), "\r") == "---" {
			body := []byte{}
			if j >= 0 {
				body = rest[i+j+1:]
			}
			return rest[:i], body, nil
		}
		if j < 0 {
			break
		}
		i += j + 1
	}
	return nil, nil, fmt.Errorf("task.md: front matter is not closed")
}

// tomlTable returns doc[key] when it is a table, else an empty one.
func tomlTable(doc map[string]any, key string) map[string]any {
	if t, ok := doc[key].(map[string]any); ok {
		return t
	}
	return map[string]any{}
}

func tomlString(t map[string]any, key string) string {
	s, _ := t[key].(string)
	return s
}

func tomlBool(t map[string]any, key string) bool {
	b, _ := t[key].(bool)
	return b
}

func tomlList(t map[string]any, key string) []any {
	l, _ := t[key].([]any)
	return l
}

// tomlNumber returns an integer or float value as float64, 0 when absent.
func tomlNumber(t map[string]any, key string) float64 {
	switch v := t[key].(type) {
	case int64:
		return float64(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0
		}
		return v
	}
	return 0
}

func tomlStrings(t map[string]any, key string) []string {
	var out []string
	for _, v := range tomlList(t, key) {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// tomlEnv reads the env table under t as strings. A value that is not a
// string, number or boolean is an error.
func tomlEnv(t map[string]any, name string) (map[string]string, error) {
	raw, ok := t["env"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch x := v.(type) {
		case string:
			out[k] = x
		case int64, float64, bool:
			out[k] = fmt.Sprint(x)
		default:
			return nil, fmt.Errorf("[%s] %s: not a string", name, k)
		}
	}
	return out, nil
}

// dedupe drops empty and repeated strings, keeping the first order.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sortedEnvKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func anyFile(dir string, names ...string) bool {
	for _, n := range names {
		if isFile(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}
