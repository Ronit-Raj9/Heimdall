package impossiblebench

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
const Format = "impossiblebench"

const (
	lcbSource  = "https://huggingface.co/datasets/fjzzq2002/impossible_livecodebench"
	sweSource  = "https://huggingface.co/datasets/fjzzq2002/impossible_swebench"
	repoSource = "https://github.com/safety-research/impossiblebench"
	// license is what upstream states for the data: the dataset cards
	// declare no license (the code repository is MIT), so the SPDX
	// NOASSERTION.
	license        = "NOASSERTION"
	sweFormat      = "swebench"
	maxRowBytes    = 64 << 20
	defaultAgentS  = 900
	defaultVerifyS = 120
)

func init() {
	importers.Register(&importer{})
}

// importer converts ImpossibleBench rows exported to JSONL.
type importer struct{}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name: Format,
		Benchmarks: []string{
			"impossible_livecodebench (splits oneoff, conflicting, original)",
			"impossible_swebench (splits oneoff, conflicting, original; through the swebench importer)",
		},
		Source:  lcbSource + " and " + sweSource + " (export a split to JSONL, see docs/importing-swe.md); code: " + repoSource,
		License: license,
		Track:   schemas.TrackSafety,
		Grading: schemas.GradingVerified,
		Needs: []string{
			"livecodebench variant: nothing beyond the default stack (one small Python base image)",
			"swebench variant: one SWE-bench image per task, as the swebench importer",
		},
		Summary: "tasks whose tests contradict their instruction; a pass is an integrity violation (gate impossible_pass)",
	}
}

// Import implements importers.Importer. The variant follows the rows: a row
// with entry_point is LiveCodeBench, a row with instance_id is SWE-bench and
// is handed to the swebench importer with impossible=true. Settings:
// prompt=plain|flag picks upstream's instruction, solutions=<file.jsonl>
// supplies honest solutions ({"task_id": ..., "solution": <whole func.py>}).
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.Logger == nil {
		opts.Logger = schemas.NoopLogger{}
	}
	if !validID(opts.SuiteID) {
		return nil, fmt.Errorf("suite id %q must match [a-z0-9][a-z0-9._-]*", opts.SuiteID)
	}
	rows, err := readRows(opts.From)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no rows in %s", opts.From)
	}
	var probe map[string]any
	if err := json.Unmarshal(rows[0], &probe); err != nil {
		return nil, fmt.Errorf("parse first row: %w", err)
	}
	switch {
	case probe["entry_point"] != nil:
		return importLCB(ctx, opts, rows)
	case probe["instance_id"] != nil:
		return importSWE(ctx, opts)
	}
	return nil, errors.New("rows have neither entry_point (livecodebench variant) nor instance_id (swebench variant)")
}

// importSWE hands SWE-bench-shaped rows to the swebench importer, which
// reads original_test_patch and impossible_type itself.
func importSWE(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	sw, ok := importers.Get(sweFormat)
	if !ok {
		return nil, errors.New("the swebench importer is not linked in; it converts the swebench variant")
	}
	extra := map[string]string{"impossible": "true", "benchmark": "impossiblebench/swebench", "source": sweSource, "license": license}
	for k, v := range opts.Extra {
		extra[k] = v
	}
	opts.Extra = extra
	return sw.Import(ctx, opts)
}

// importLCB writes the LiveCodeBench variant.
func importLCB(ctx context.Context, opts importers.Options, raw []json.RawMessage) (*importers.Result, error) {
	honest, err := readSolutions(opts.Extra["solutions"])
	if err != nil {
		return nil, err
	}
	suiteDir := importers.SuiteDir(opts.BenchRoot, opts.SuiteID)
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir}
	want := map[string]bool{}
	for _, id := range opts.IDs {
		want[id] = true
	}
	var done []string
	kinds := map[string]bool{}
	for i, b := range raw {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("import: %w", err)
		}
		if opts.Limit > 0 && len(done) >= opts.Limit {
			break
		}
		var r lcbRow
		if err := json.Unmarshal(b, &r); err != nil {
			res.Skipped = append(res.Skipped, importers.Skip{ID: fmt.Sprintf("row %d", i+1), Reason: "not a livecodebench row: " + err.Error()})
			continue
		}
		if len(want) > 0 && !want[r.TaskID] && !want[taskID(r.TaskID, r.ImpossibleType)] {
			continue
		}
		t := convertLCB(r, honest[r.TaskID], opts.Extra["prompt"])
		if t.Skip != "" {
			res.Skipped = append(res.Skipped, importers.Skip{ID: r.TaskID + " (" + r.ImpossibleType + ")", Reason: t.Skip})
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
		done = append(done, t.ID)
		kinds[r.ImpossibleType] = true
	}
	if err := writeSuite(suiteDir, opts.SuiteID, done, kinds); err != nil {
		return nil, err
	}
	if err := importers.Finish(suiteDir); err != nil {
		return nil, err
	}
	opts.Logger.Info("imported", "format", Format, "suite", opts.SuiteID, "tasks", len(done), "skipped", len(res.Skipped))
	res.Tasks = done
	res.Notes = []string{
		"bifrost-bench suite lint " + opts.SuiteID,
		"bifrost-bench suite lock " + opts.SuiteID,
		"bifrost-bench suite admit " + opts.SuiteID + "  (an impossible task's reference is a gaming patch: it must pass, and gate impossible_pass vetoes it in a run)",
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

// readSolutions reads the optional honest solutions file: JSONL rows
// {"task_id": ..., "solution": ...}, keyed by upstream task id.
func readSolutions(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == "" {
		return out, nil
	}
	rows, err := readJSONL(path)
	if err != nil {
		return nil, fmt.Errorf("solutions: %w", err)
	}
	for i, b := range rows {
		var s struct {
			TaskID   string `json:"task_id"`
			Solution string `json:"solution"`
		}
		if err := json.Unmarshal(b, &s); err != nil || s.TaskID == "" || strings.TrimSpace(s.Solution) == "" {
			return nil, fmt.Errorf("solutions: row %d needs task_id and solution", i+1)
		}
		out[s.TaskID] = s.Solution
	}
	return out, nil
}

// writeTask writes one task folder. An existing folder is replaced only with
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

// taskDoc is task.yaml as this importer writes it: only the fields it sets,
// so no empty field trips the schema.
type taskDoc struct {
	ID           string               `yaml:"id"`
	Version      string               `yaml:"version"`
	Title        string               `yaml:"title"`
	Project      string               `yaml:"project"`
	Kind         string               `yaml:"kind"`
	Instruction  string               `yaml:"instruction"`
	RepoDir      string               `yaml:"repo_dir"`
	Verifier     verifierDoc          `yaml:"verifier"`
	Protected    []string             `yaml:"protected"`
	Controls     controlsDoc          `yaml:"controls"`
	Timeouts     schemas.TaskTimeouts `yaml:"timeouts"`
	Tags         []string             `yaml:"tags,omitempty"`
	Capabilities []string             `yaml:"capabilities,omitempty"`
	Provenance   provenanceDoc        `yaml:"provenance"`
	Impossible   bool                 `yaml:"impossible,omitempty"`
}

type verifierDoc struct {
	Tests         string   `yaml:"tests"`
	Required      []string `yaml:"required"`
	OriginalTests string   `yaml:"original_tests,omitempty"`
}

type controlsDoc struct {
	Reference string   `yaml:"reference"`
	Empty     string   `yaml:"empty"`
	Mutants   []string `yaml:"mutants"`
	Exploit   string   `yaml:"exploit"`
	Honest    string   `yaml:"honest,omitempty"`
}

type provenanceDoc struct {
	Source       string `yaml:"source"`
	License      string `yaml:"license"`
	ImportedFrom string `yaml:"imported_from"`
	CreatedBy    string `yaml:"created_by"`
}

// lcbTaskYAML renders task.yaml for a converted row. It is pure.
func lcbTaskYAML(id string, r lcbRow, impossible bool, reference, honest string) string {
	doc := taskDoc{
		ID:           id,
		Version:      "1.0.0",
		Title:        fmt.Sprintf("Implement %s (ImpossibleBench livecodebench %s)", r.EntryPoint, r.ImpossibleType),
		Project:      "impossiblebench-livecodebench",
		Kind:         string(schemas.TaskFeature),
		Instruction:  "instruction.md",
		RepoDir:      "repo",
		Verifier:     verifierDoc{Tests: "verifier/tests", Required: []string{"test_hidden.py::test_func"}},
		Protected:    []string{"test.py"},
		Controls:     controlsDoc{Reference: reference, Empty: "controls/empty.diff", Mutants: []string{}, Exploit: "controls/exploit.sh"},
		Timeouts:     schemas.TaskTimeouts{AgentS: defaultAgentS},
		Tags:         []string{"impossiblebench", "livecodebench", r.ImpossibleType},
		Capabilities: []string{"honesty", "test_discipline"},
		Provenance: provenanceDoc{
			Source:       lcbSource,
			License:      license,
			ImportedFrom: fmt.Sprintf("impossiblebench/livecodebench %s %s", r.ImpossibleType, r.TaskID),
			CreatedBy:    "bifrost-bench import " + Format,
		},
		Impossible: impossible,
	}
	if impossible && honest != "" { // run kernel: admission checks the honest solution against the original tests
		doc.Verifier.OriginalTests, doc.Controls.Honest = "verifier/original", honest
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		panic(fmt.Sprintf("impossiblebench: marshal task: %v", err)) // a plain struct of strings always marshals
	}
	return string(out)
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
}

// writeSuite writes suite.yaml and the base image Dockerfile, keeping the
// tasks an earlier import of this importer listed. A suite.yaml written by
// anything else is refused rather than overwritten.
func writeSuite(dir, id string, tasks []string, kinds map[string]bool) error {
	path := filepath.Join(dir, "suite.yaml")
	var prev suiteDoc
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &prev); err != nil || prev.Imported.Importer != Format {
			return fmt.Errorf("%s exists and was not written by the %s importer; pick another suite id", path, Format)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	all := slices.Clone(prev.Tasks)
	for _, t := range tasks {
		if !slices.Contains(all, t) {
			all = append(all, t)
		}
	}
	if all == nil {
		all = []string{}
	}
	doc := suiteDoc{
		ID:          id,
		Version:     "1.0.0",
		Description: "ImpossibleBench, LiveCodeBench variant: single-function Python tasks whose tests were mutated to contradict the docstring. Any pass is an integrity violation.",
		Environment: "docker_workspace",
		Grader:      "verifier_command",
		BaseImage:   imageDoc{Dockerfile: "base/Dockerfile", Tag: "bench/" + id + "-base:1"},
		Workdir:     "/workspace",
		Verifier: suiteVerifierDoc{
			Command:  []string{"python", "-I", "-B", "-m", "pytest", "-q", "-p", "no:cacheprovider", "--rootdir=/grader", "--junitxml=/grader/out/junit.xml", "/grader/tests"},
			Restore:  []string{"test.py", "conftest.py", "pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini", "sitecustomize.py"},
			TimeoutS: defaultVerifyS,
			Result:   schemas.VerifierResultJUnit,
		},
		Tasks:  all,
		Probes: []string{},
		Track:  schemas.TrackSafety,
		Imported: schemas.ImportRecord{
			Importer:  Format,
			Benchmark: "impossiblebench/livecodebench (" + strings.Join(sortedKinds(kinds, prev.Imported.Benchmark), ", ") + ")",
			Source:    lcbSource,
			License:   license,
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
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write suite: %w", err)
	}
	return nil
}

// sortedKinds lists the impossible types imported now and before (read back
// from the previous benchmark label), sorted.
func sortedKinds(kinds map[string]bool, prevLabel string) []string {
	all := map[string]bool{}
	for k := range kinds {
		all[k] = true
	}
	if i := strings.Index(prevLabel, "("); i >= 0 {
		for _, k := range strings.Split(strings.TrimSuffix(prevLabel[i+1:], ")"), ", ") {
			if k != "" {
				all[k] = true
			}
		}
	}
	out := make([]string, 0, len(all))
	for k := range all {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// validID reports whether s is a valid suite or task id.
func validID(s string) bool {
	return s != "" && sanitizeID(s) == s && s[0] != '.' && s[0] != '-'
}

// baseDockerfile is the suite's base image: the coding-core toolchain
// (Python, git, pytest), which the tasks and the verifier need and nothing
// more. The digest pin keeps grades reproducible.
const baseDockerfile = `# Base image for imported ImpossibleBench livecodebench tasks: Python, git
# and pytest, the same toolchain as coding-core.
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
