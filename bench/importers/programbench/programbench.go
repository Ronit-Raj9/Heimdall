package programbench

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Name is the import format name: `bifrost-bench import programbench`.
const Name = "programbench"

// Settings read from repeated -set key=value.
const (
	SetTests           = "tests"            // the Hugging Face test-blob snapshot; default <from>/tests-hf
	SetRepo            = "repo"             // the upstream clone; default <from>/ProgramBench
	SetImageTag        = "image_tag"        // task image tag; default task_cleanroom_v6 (upstream inference and eval tag)
	SetAgentTimeout    = "agent_timeout"    // agent wall-time cap in seconds; default 7200
	SetVerifierTimeout = "verifier_timeout" // verifier cap in seconds; default 3600
	SetRevision        = "revision"         // upstream commit; default the clone's HEAD
	SetTestsRevision   = "tests_revision"   // Hugging Face revision of the test blobs, recorded in IMPORT notes
)

const (
	upstreamRepo     = "https://github.com/facebookresearch/ProgramBench"
	testsRepo        = "https://huggingface.co/datasets/programbench/ProgramBench-Tests"
	dockerOrg        = "programbench"
	defaultImageTag  = "task_cleanroom_v6"
	defaultAgentS    = 7200
	defaultVerifierS = 3600
	// fixtureTask is upstream's own test fixture (testorg/calculator), not
	// a benchmark task: it has no image or test blobs.
	fixtureTask = "testorg__calculator.abc1234"
)

var (
	//go:embed scripts/test.sh
	testScript string
	//go:embed scripts/grade.py
	gradeScript string
	//go:embed scripts/solve.sh
	solveScript string
	//go:embed scripts/instruction.md.tmpl
	instructionTmpl string
)

func init() {
	importers.Register(&importer{})
}

// importer converts ProgramBench task folders plus their hidden test blobs
// into Harbor-format task folders and imports them with the harbor importer
// (-set image=prebuilt): each task runs in upstream's own image and is graded
// by a converted copy of upstream's eval pipeline in a snapshot of the agent's
// container.
type importer struct{}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"programbench@" + defaultImageTag + " (200 tasks)"},
		Source:     "git clone " + upstreamRepo + " ProgramBench; hf download programbench/ProgramBench-Tests --repo-type dataset --local-dir tests-hf",
		License:    "MIT (benchmark code and tests); each program keeps its own licence (ATTRIBUTION.md per task)",
		Track:      schemas.TrackTerminal,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"linux/amd64 task images from Docker Hub (programbench/<task>:" + defaultImageTag + ", about 0.9 GB compressed each)",
			"snapshot verifier mode (compile.sh runs and the hidden tests run in a copy of the agent's container)",
		},
		Summary: "rebuild a whole program from its binary and docs; hidden pytest branches score the rebuilt executable",
	}
}

type settings struct {
	tests, repo, imageTag, revision, testsRevision string
	agentS, verifierS                              int
}

func readSettings(from string, extra map[string]string) (settings, error) {
	cfg := settings{
		tests:     filepath.Join(from, "tests-hf"),
		repo:      filepath.Join(from, "ProgramBench"),
		imageTag:  defaultImageTag,
		agentS:    defaultAgentS,
		verifierS: defaultVerifierS,
	}
	for k, v := range extra {
		switch k {
		case SetTests:
			cfg.tests = v
		case SetRepo:
			cfg.repo = v
		case SetImageTag:
			cfg.imageTag = v
		case SetRevision:
			cfg.revision = v
		case SetTestsRevision:
			cfg.testsRevision = v
		case SetAgentTimeout, SetVerifierTimeout:
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return cfg, fmt.Errorf("programbench: -set %s=%s: want a positive number of seconds", k, v)
			}
			if k == SetAgentTimeout {
				cfg.agentS = n
			} else {
				cfg.verifierS = n
			}
		default:
			return cfg, fmt.Errorf("programbench: unknown setting %q (known: tests, repo, image_tag, agent_timeout, verifier_timeout, revision, tests_revision)", k)
		}
	}
	return cfg, nil
}

// upstreamTask is task.yaml.
type upstreamTask struct {
	Repository      string   `yaml:"repository"`
	Commit          string   `yaml:"commit"`
	Language        string   `yaml:"language"`
	Difficulty      string   `yaml:"difficulty"`
	EvalCleanHashes []string `yaml:"eval_clean_hashes"`
}

// testsFile is tests.json.
type testsFile struct {
	Branches map[string]struct {
		Ignored bool     `json:"ignored"`
		Tests   []string `json:"tests"`
	} `json:"branches"`
}

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	cfg, err := readSettings(opts.From, opts.Extra)
	if err != nil {
		return nil, err
	}
	tasksDir := filepath.Join(cfg.repo, "src", "programbench", "data", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, fmt.Errorf("programbench: %w (want -from holding ProgramBench/ and tests-hf/, or -set repo= and tests=)", err)
	}
	harbor, ok := importers.Get("harbor")
	if !ok {
		return nil, errors.New("programbench: the harbor importer is not linked")
	}
	stage, err := os.MkdirTemp("", "programbench-stage-")
	if err != nil {
		return nil, fmt.Errorf("programbench: %w", err)
	}
	defer os.RemoveAll(stage)

	want := map[string]bool{}
	for _, id := range opts.IDs {
		want[id] = true
	}
	var skipped []importers.Skip
	staged := 0
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := e.Name()
		if !e.IsDir() || name == fixtureTask {
			continue
		}
		if len(want) > 0 && !want[name] && !want[strings.ToLower(name)] {
			continue
		}
		if opts.Limit > 0 && staged >= opts.Limit {
			break
		}
		reason, err := stageTask(filepath.Join(tasksDir, name), filepath.Join(cfg.tests, name), filepath.Join(stage, name), cfg)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			skipped = append(skipped, importers.Skip{ID: name, Reason: reason})
			continue
		}
		staged++
	}
	if staged == 0 {
		return &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir(), Skipped: skipped}, errors.New("programbench: no task was staged; see the skipped list")
	}
	rev := cfg.revision
	if rev == "" {
		rev = headRevision(cfg.repo)
	}
	hopts := opts
	hopts.From = stage
	hopts.IDs = nil
	hopts.Limit = 0
	hopts.Extra = map[string]string{
		"image":     "prebuilt",
		"benchmark": "programbench@" + cfg.imageTag,
		"source":    upstreamRepo,
		"license":   "MIT",
		"track":     schemas.TrackTerminal,
	}
	if rev != "" {
		hopts.Extra["revision"] = rev
	}
	res, err := harbor.Import(ctx, hopts)
	if res != nil {
		res.Skipped = append(skipped, res.Skipped...)
		if cfg.testsRevision != "" {
			res.Notes = append(res.Notes, "hidden tests: "+testsRepo+" at "+cfg.testsRevision)
		}
	}
	return res, err
}

// stageTask writes one Harbor-format task folder under dst. A non-empty
// reason skips the task.
func stageTask(src, blobs, dst string, cfg settings) (string, error) {
	var t upstreamTask
	b, err := os.ReadFile(filepath.Join(src, "task.yaml"))
	if err != nil {
		return "no task.yaml", nil
	}
	if err := yaml.Unmarshal(b, &t); err != nil {
		return "task.yaml does not parse: " + err.Error(), nil
	}
	tj, err := os.ReadFile(filepath.Join(src, "tests.json"))
	if err != nil {
		return "no tests.json", nil
	}
	var tf testsFile
	if err := json.Unmarshal(tj, &tf); err != nil {
		return "tests.json does not parse: " + err.Error(), nil
	}
	var active []string
	for br, info := range tf.Branches {
		if !info.Ignored && len(info.Tests) > 0 {
			active = append(active, br)
		}
	}
	sort.Strings(active)
	if len(active) == 0 {
		return "tests.json has no active test branch", nil
	}
	for _, br := range active {
		if _, err := os.Stat(filepath.Join(blobs, "tests", br+".tar.gz")); err != nil {
			return "hidden test archive " + br + ".tar.gz missing from the test-blob snapshot", nil
		}
	}
	name := filepath.Base(src)
	image := fmt.Sprintf("%s/%s:%s", dockerOrg, strings.ReplaceAll(name, "__", "_1776_"), cfg.imageTag)
	for _, d := range []string{"environment", "solution", "tests/branches"} {
		if err := os.MkdirAll(filepath.Join(dst, d), 0o755); err != nil {
			return "", err
		}
	}
	diff := t.Difficulty
	if diff == "" {
		diff = "unknown"
	}
	toml := fmt.Sprintf(`schema_version = "1.1"

[task]
name = "programbench/%s"
description = "Rebuild %s from its binary and documentation."
keywords = ["programbench", "reverse-engineering", %q]

[metadata]
difficulty = %q
category = "program-rebuild"
tags = ["programbench", %q]

[verifier]
timeout_sec = %d.0

[agent]
timeout_sec = %d.0

[environment]
docker_image = %q
allow_internet = false
gpus = 0
`, name, t.Repository, t.Language, diff, t.Language, cfg.verifierS, cfg.agentS, image)
	instr := strings.NewReplacer("{{REPO}}", t.Repository, "{{LANG}}", language(t.Language)).Replace(instructionTmpl)
	files := map[string]string{
		"task.toml":              toml,
		"instruction.md":         instr,
		"environment/Dockerfile": "FROM " + image + "\nWORKDIR /workspace\n",
		"solution/solve.sh":      solveScript,
		"tests/test.sh":          testScript,
		"tests/grade.py":         gradeScript,
		"tests/tests.json":       string(tj),
		"tests/clean_hashes.txt": hashLines(t.EvalCleanHashes),
		"tests/UPSTREAM.md": fmt.Sprintf("ProgramBench task %s: %s at %s (%s). Hidden test branches from %s; %d active branches.\n",
			name, t.Repository, t.Commit, t.Language, testsRepo, len(active)),
	}
	for rel, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dst, rel), []byte(body), mode); err != nil {
			return "", err
		}
	}
	for _, extra := range []string{"ATTRIBUTION.md", "LICENSE"} {
		if _, err := os.Stat(filepath.Join(blobs, extra)); err == nil {
			if err := linkOrCopy(filepath.Join(blobs, extra), filepath.Join(dst, "tests", extra)); err != nil {
				return "", err
			}
		}
	}
	for _, br := range active {
		if err := linkOrCopy(filepath.Join(blobs, "tests", br+".tar.gz"), filepath.Join(dst, "tests", "branches", br+".tar.gz")); err != nil {
			return "", err
		}
	}
	return "", nil
}

func language(code string) string {
	switch code {
	case "rs":
		return "Rust"
	case "go":
		return "Go"
	case "c":
		return "C"
	case "cpp":
		return "C++"
	case "hs":
		return "Haskell"
	case "java":
		return "Java"
	case "bash":
		return "Bash"
	}
	return code
}

// linkOrCopy hard-links src to dst (the staging folder is thrown away), or
// copies it across file systems.
func linkOrCopy(src, dst string) error {
	real, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	if os.Link(real, dst) == nil {
		return nil
	}
	in, err := os.Open(real)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// headRevision reads the clone's HEAD commit without running git.
func headRevision(repo string) string {
	b, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "ref: ") {
		return s
	}
	ref := strings.TrimPrefix(s, "ref: ")
	if b, err := os.ReadFile(filepath.Join(repo, ".git", filepath.FromSlash(ref))); err == nil {
		return strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(repo, ".git", "packed-refs")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[1] == ref {
				return f[0]
			}
		}
	}
	return ""
}

// hashLines is one sha256 per line, or an empty file when there are none.
func hashLines(hs []string) string {
	if len(hs) == 0 {
		return ""
	}
	return strings.Join(hs, "\n") + "\n"
}
