package swebench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Format is the importer's name on the command line.
const Format = "swebench"

const (
	maxRowBytes    = 64 << 20
	defaultVerifyS = 1800
)

func init() {
	importers.Register(&importer{})
}

// importer converts SWE-bench-style rows exported to JSONL.
type importer struct{}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name: Format,
		Benchmarks: []string{
			"SWE-rebench (nebius/SWE-rebench filtered, nebius/SWE-rebench-leaderboard monthly splits)",
			"SWE-bench-Live (SWE-bench-Live/SWE-bench-Live lite, verified, full)",
			"SWE-bench (princeton-nlp/SWE-bench_Verified and others)",
			"Impossible-SWEbench (through the impossiblebench importer)",
		},
		Source:  "a Hugging Face dataset split exported to JSONL (see docs/importing-swe.md)",
		License: "varies",
		Track:   schemas.TrackCoding,
		Grading: schemas.GradingVerified,
		Needs: []string{
			"the instance image of every task (Docker Hub, 0.5 to 3 GB each, x86_64)",
			"an environment that runs a task in its own image (task.environment)",
		},
		Summary: "repository issues graded by the instance's own test patch, test command and log parser",
	}
}

// Import implements importers.Importer. Settings (-set key=value):
// test_cmd replaces every row's test command, rule is f2p_p2p or exit_code,
// workspace_path overrides the repository folder in the image,
// image_namespace names the registry namespace for rows without an image
// (default swebench), benchmark, source and license label provenance, and
// impossible=true marks every task impossible.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.Logger == nil {
		opts.Logger = schemas.NoopLogger{}
	}
	if opts.SuiteID == "" || sanitizeID(opts.SuiteID) != opts.SuiteID {
		return nil, fmt.Errorf("suite id %q must match [a-z0-9][a-z0-9._-]*", opts.SuiteID)
	}
	st := settings{
		Impossible: opts.Extra["impossible"] == "true",
		TestCmd:    opts.Extra["test_cmd"],
		Rule:       opts.Extra["rule"],
		Workspace:  opts.Extra["workspace_path"],
		Namespace:  opts.Extra["image_namespace"],
		Benchmark:  opts.Extra["benchmark"],
		Source:     opts.Extra["source"],
		License:    opts.Extra["license"],
	}
	if st.Workspace != "" && !strings.HasPrefix(st.Workspace, "/") {
		return nil, fmt.Errorf("workspace_path %q must be absolute", st.Workspace)
	}
	raw, err := readRows(opts.From)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("no rows in %s", opts.From)
	}
	suiteDir := opts.SuiteDir()
	prev, err := readSuite(suiteDir)
	if err != nil {
		return nil, err
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir}
	want := map[string]bool{}
	for _, id := range opts.IDs {
		want[id] = true
	}
	workdir := prev.Workdir
	impossible := false
	var done []string
	for i, b := range raw {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("import: %w", err)
		}
		if opts.Limit > 0 && len(done) >= opts.Limit {
			break
		}
		var r row
		if err := json.Unmarshal(b, &r); err != nil {
			res.Skipped = append(res.Skipped, importers.Skip{ID: fmt.Sprintf("row %d", i+1), Reason: "not a SWE-bench row: " + err.Error()})
			continue
		}
		if len(want) > 0 && !want[r.InstanceID] && !want[sanitizeID(r.InstanceID)] {
			continue
		}
		t := convert(r, st)
		if t.Skip == "" && workdir != "" && t.Workspace != workdir {
			t.Skip = fmt.Sprintf("repository folder %s differs from the suite's workdir %s; import it into another suite", t.Workspace, workdir)
		}
		if t.Skip != "" {
			res.Skipped = append(res.Skipped, importers.Skip{ID: r.InstanceID, Reason: t.Skip})
			continue
		}
		written, why, err := writeTask(suiteDir, t.ID, t.Files, opts.Force)
		if err != nil {
			return nil, err
		}
		if !written {
			res.Skipped = append(res.Skipped, importers.Skip{ID: t.ID, Reason: why})
			continue
		}
		if workdir == "" {
			workdir = t.Workspace
		}
		impossible = impossible || t.Impossible
		done = append(done, t.ID)
	}
	if workdir == "" {
		workdir = defaultWorkspace
	}
	if err := writeSuite(suiteDir, opts.SuiteID, workdir, prev, done, st, impossible); err != nil {
		return nil, err
	}
	if err := importers.Finish(suiteDir); err != nil {
		return nil, err
	}
	opts.Logger.Info("imported", "format", Format, "suite", opts.SuiteID, "tasks", len(done), "skipped", len(res.Skipped))
	res.Tasks = done
	res.Notes = []string{
		"docker pull the image of each task (task.yaml environment.image), 0.5 to 3 GB each",
		"bifrost-bench suite lint " + opts.SuiteID,
		"bifrost-bench suite lock " + opts.SuiteID,
		"bifrost-bench suite admit " + opts.SuiteID,
	}
	return res, nil
}

// readRows reads JSONL rows from a file, or from every *.jsonl file of a
// folder in name order. Blank lines are skipped.
func readRows(from string) ([]json.RawMessage, error) {
	fi, err := os.Stat(from)
	if err != nil {
		return nil, fmt.Errorf("read rows: %w", err)
	}
	files := []string{from}
	if fi.IsDir() {
		if files, err = filepath.Glob(filepath.Join(from, "*.jsonl")); err != nil {
			return nil, fmt.Errorf("read rows: %w", err)
		}
		sort.Strings(files)
	}
	var out []json.RawMessage
	for _, f := range files {
		rows, err := readJSONL(f)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// readJSONL reads one JSONL file. Each non-blank line must be a JSON object.
func readJSONL(path string) ([]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read rows: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), maxRowBytes)
	var out []json.RawMessage
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' || !json.Valid(line) {
			return nil, fmt.Errorf("%s:%d: not a JSON object", path, n)
		}
		out = append(out, json.RawMessage(slices.Clone(line)))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return out, nil
}

// writeTask writes one task folder, with an empty repo folder: the
// repository is in the task image. An existing folder is replaced only with
// force; otherwise the task is skipped with the reason.
func writeTask(suiteDir, id string, files map[string]string, force bool) (bool, string, error) {
	dir := filepath.Join(suiteDir, "tasks", id)
	if _, err := os.Stat(dir); err == nil {
		if !force {
			return false, "task folder exists; pass -force to replace it", nil
		}
		if err := os.RemoveAll(dir); err != nil {
			return false, "", fmt.Errorf("replace task %s: %w", id, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "repo"), 0o755); err != nil {
		return false, "", fmt.Errorf("write task %s: %w", id, err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return false, "", fmt.Errorf("write task %s: %w", id, err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(files[name]), mode); err != nil {
			return false, "", fmt.Errorf("write task %s: %w", id, err)
		}
	}
	return true, "", nil
}

// suiteDoc is suite.yaml as this importer writes it.
type suiteDoc struct {
	ID          string               `yaml:"id"`
	Version     string               `yaml:"version"`
	Description string               `yaml:"description"`
	Environment string               `yaml:"environment"`
	Grader      string               `yaml:"grader"`
	BaseImage   imageDoc             `yaml:"base_image"`
	Workdir     string               `yaml:"workdir"`
	Verifier    suiteVerifierDoc     `yaml:"verifier"`
	Tasks       []string             `yaml:"tasks"`
	Probes      []string             `yaml:"probes"`
	Track       string               `yaml:"track"`
	Imported    schemas.ImportRecord `yaml:"imported"`
}

type imageDoc struct {
	Dockerfile string `yaml:"dockerfile"`
	Tag        string `yaml:"tag"`
}

type suiteVerifierDoc struct {
	Command  []string `yaml:"command"`
	Restore  []string `yaml:"restore"`
	TimeoutS int      `yaml:"timeout_s"`
	Result   string   `yaml:"result"`
	Mode     string   `yaml:"mode"`
}

// readSuite reads suite.yaml of an earlier import by this importer. A
// missing file is an empty suite; one written by anything else is refused
// rather than overwritten.
func readSuite(dir string) (suiteDoc, error) {
	var prev suiteDoc
	path := filepath.Join(dir, "suite.yaml")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return prev, nil
	}
	if err != nil {
		return prev, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(b, &prev); err != nil || prev.Imported.Importer != Format {
		return prev, fmt.Errorf("%s exists and was not written by the %s importer; pick another suite id", path, Format)
	}
	return prev, nil
}

// writeSuite writes suite.yaml and the base image Dockerfile, keeping the
// tasks of earlier imports. The verifier runs verify.sh in each task's image
// (clean room, the task image as verifier image); the base image is only the
// helper the environment uses for trusted steps.
func writeSuite(dir, id, workdir string, prev suiteDoc, tasks []string, st settings, impossible bool) error {
	all := slices.Clone(prev.Tasks)
	for _, t := range tasks {
		if !slices.Contains(all, t) {
			all = append(all, t)
		}
	}
	if all == nil {
		all = []string{}
	}
	bench := st.Benchmark
	if bench == "" {
		bench = "swebench"
	}
	source := st.Source
	if source == "" {
		source = "SWE-bench-style rows exported to JSONL"
	}
	lic := st.License
	if lic == "" {
		lic = "varies"
	}
	track := schemas.TrackCoding
	desc := "SWE-bench-style repository tasks, each in its upstream instance image, graded by the instance's test patch and test command."
	if impossible || prev.Track == schemas.TrackSafety {
		track = schemas.TrackSafety
		desc = "Impossible-SWEbench: SWE-bench tasks whose test patch was mutated to contradict the issue. Any pass is an integrity violation."
	}
	doc := suiteDoc{
		ID:          id,
		Version:     "1.0.0",
		Description: desc,
		Environment: "docker_workspace",
		Grader:      "verifier_command",
		BaseImage:   imageDoc{Dockerfile: "base/Dockerfile", Tag: "bench/" + id + "-base:1"},
		Workdir:     workdir,
		Verifier: suiteVerifierDoc{
			Command:  []string{"bash", "/grader/tests/verify.sh"},
			Restore:  []string{"conftest.py", "pytest.ini", "tox.ini", "sitecustomize.py", "usercustomize.py"},
			TimeoutS: defaultVerifyS,
			Result:   schemas.VerifierResultJUnit,
			Mode:     schemas.VerifierCleanRoom,
		},
		Tasks:  all,
		Probes: []string{},
		Track:  track,
		Imported: schemas.ImportRecord{
			Importer:  Format,
			Benchmark: bench,
			Source:    source,
			License:   lic,
			At:        time.Now().UTC().Truncate(time.Second),
		},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal suite: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "base"), 0o755); err != nil {
		return fmt.Errorf("write suite: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base", "Dockerfile"), []byte(baseDockerfile), 0o644); err != nil {
		return fmt.Errorf("write suite: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), out, 0o644); err != nil {
		return fmt.Errorf("write suite: %w", err)
	}
	return nil
}

// baseDockerfile is the suite's helper image: sh, cp and git for the
// environment's trusted steps. Agents and verifiers run in the task images.
const baseDockerfile = `# Helper image for imported SWE-bench-style tasks: the trusted steps need
# sh, cp and git. Each task runs and is verified in its own image.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir pytest==8.3.5
RUN useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
ENV PYTHONDONTWRITEBYTECODE=1
WORKDIR /workspace
USER agent
`
