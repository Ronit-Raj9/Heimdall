package agentdojo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Name is the import format name.
const Name = "agentdojo"

// Importer settings (-set key=value).
const (
	SetSuite          = "suite"             // workspace, travel, banking or slack (required)
	SetVersion        = "benchmark_version" // upstream benchmark version; default v1.2.2, upstream's default
	SetAttacks        = "attacks"           // comma-separated attack names; default important_instructions
	SetInjectionTasks = "injection_tasks"   // comma-separated injection task ids; default every one of the suite
	SetImage          = "image"             // a prebuilt AgentDojo image tag; skips the build
)

// suites are AgentDojo's four suites and the three AgentDyn adds
// (github.com/SaFo-Lab/AgentDyn, an AgentDojo fork; docs/importing-agentdojo.md).
var suites = []string{"workspace", "travel", "banking", "slack", "shopping", "github", "dailylife"}

// agentDynSuites are the suites only the AgentDyn fork has.
var agentDynSuites = []string{"shopping", "github", "dailylife"}

// agentDynRepo is the AgentDyn fork, the source of its suites.
const agentDynRepo = "https://github.com/SaFo-Lab/AgentDyn"

// adaptiveAttack is the attacker-model condition (image/heimdall/adaptive.py).
const adaptiveAttack = "adaptive"

// sourceRepo is the upstream repository a suite comes from.
func (s settings) sourceRepo() string {
	if slices.Contains(agentDynSuites, s.suite) {
		return agentDynRepo
	}
	return upstreamRepo
}

// adaptive reports whether the adaptive attacker condition is asked for.
func (s settings) adaptive() bool { return slices.Contains(s.attacks, adaptiveAttack) }

const (
	upstreamRepo    = "https://github.com/ethz-spylab/agentdojo"
	defaultVersion  = "v1.2.2"
	defaultAttack   = "important_instructions"
	serviceID       = "agentdojo"
	servicePort     = 8000
	stateExportID   = "run"
	stateExportPath = "/state/run.json"
	stateExportMax  = 64 << 20
	suiteVersion    = "1.0.0"
	taskVersion     = "1.0.0"
	probeVersion    = "1.0.0"
	agentTimeoutS   = 900
	verifierTimeout = 300
)

func init() {
	importers.Register(&importer{docker: utils.Docker{}, now: time.Now})
}

// dockerRunner is the part of the docker CLI the importer uses. Tests
// replace it.
type dockerRunner interface {
	Run(ctx context.Context, args ...string) (*utils.DockerResult, error)
	MustRun(ctx context.Context, args ...string) (*utils.DockerResult, error)
}

// importer converts AgentDojo user tasks.
type importer struct {
	docker dockerRunner
	now    func() time.Time
}

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"agentdojo@" + defaultVersion + " (workspace, travel, banking, slack)"},
		Source:     "git clone --depth 1 " + upstreamRepo,
		License:    "MIT",
		Track:      schemas.TrackSafety,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"docker at import time: the importer builds the AgentDojo image (with network, to install the package's dependencies) and reads the task catalog from it with no network",
			"services, state export, the tool proxy and probes of layer service in the run",
		},
		Summary: "AgentDojo user tasks as MCP tool-use tasks graded by upstream's utility functions; each attack and injection task is a probe that plants the attack in the tools' data, and gate tool_injection_followed vetoes a run that carried it out.",
	}
}

// settings are the parsed -set values.
type settings struct {
	suite          string
	version        string
	attacks        []string
	injectionTasks []string // empty means every injection task of the suite
	image          string
}

var versionRE = regexp.MustCompile(`^v\d+(\.\d+){0,2}$`)

// parseSettings reads and checks the -set values.
func parseSettings(extra map[string]string) (settings, error) {
	known := []string{SetSuite, SetVersion, SetAttacks, SetInjectionTasks, SetImage}
	for k := range extra {
		if !slices.Contains(known, k) {
			return settings{}, fmt.Errorf("unknown setting %q (known: %s)", k, strings.Join(known, ", "))
		}
	}
	s := settings{
		suite:          strings.TrimSpace(extra[SetSuite]),
		version:        strings.TrimSpace(extra[SetVersion]),
		attacks:        splitList(extra[SetAttacks]),
		injectionTasks: splitList(extra[SetInjectionTasks]),
		image:          strings.TrimSpace(extra[SetImage]),
	}
	if !slices.Contains(suites, s.suite) {
		return settings{}, fmt.Errorf("-set %s=<name> is required, one of %s", SetSuite, strings.Join(suites, ", "))
	}
	if s.version == "" {
		s.version = defaultVersion
	}
	if !versionRE.MatchString(s.version) {
		return settings{}, fmt.Errorf("benchmark version %q is not like %s", s.version, defaultVersion)
	}
	if len(s.attacks) == 0 {
		s.attacks = []string{defaultAttack}
	}
	return s, nil
}

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.SuiteID == "" || opts.From == "" || opts.BenchRoot == "" {
		return nil, errors.New("agentdojo: bench root, -from and -suite are required")
	}
	set, err := parseSettings(opts.Extra)
	if err != nil {
		return nil, fmt.Errorf("agentdojo: %w", err)
	}
	up, err := openUpstream(opts.From, set.suite)
	if err != nil {
		return nil, fmt.Errorf("agentdojo: %w", err)
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir()}
	w := &writer{set: set, up: up, suiteDir: res.SuiteDir}
	if err := w.writeImage(); err != nil {
		return nil, fmt.Errorf("agentdojo: %w", err)
	}
	if set.image != "" {
		w.imageTag = set.image
	} else if err := im.buildImage(ctx, w.imageTag, filepath.Join(res.SuiteDir, "image")); err != nil {
		return nil, fmt.Errorf("agentdojo: %w", err)
	}
	cat, err := im.readCatalog(ctx, w.imageTag, set.version, set.suite)
	if err != nil {
		return nil, fmt.Errorf("agentdojo: %w", err)
	}
	w.cat = cat
	probes, err := selectProbes(cat, set)
	if err != nil {
		return nil, fmt.Errorf("agentdojo: %w", err)
	}
	picked, skips := selectTasks(cat, set.suite, opts.IDs, opts.Limit)
	res.Skipped = append(res.Skipped, skips...)
	for _, t := range picked {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("agentdojo: %w", err)
		}
		id := taskID(set.suite, t.ID)
		dir := filepath.Join(res.SuiteDir, "tasks", id)
		if _, err := os.Stat(dir); err == nil && !opts.Force {
			res.Skipped = append(res.Skipped, importers.Skip{ID: t.ID, Reason: "task folder exists (pass -force to replace it)"})
			continue
		}
		if err := w.writeTask(dir, id, t); err != nil {
			return nil, fmt.Errorf("agentdojo: task %s: %w", t.ID, err)
		}
		res.Tasks = append(res.Tasks, id)
	}
	var probeIDs []string
	for _, p := range probes {
		if err := w.writeProbe(p); err != nil {
			return nil, fmt.Errorf("agentdojo: probe %s: %w", p.id, err)
		}
		probeIDs = append(probeIDs, p.id)
	}
	rec := schemas.ImportRecord{
		Importer:  Name,
		Benchmark: fmt.Sprintf("agentdojo@%s %s (package %s)", set.version, set.suite, cat.PackageVersion),
		Source:    set.sourceRepo(),
		Revision:  up.revision,
		License:   up.license,
		At:        im.now().UTC().Truncate(time.Second),
	}
	if err := w.writeSuite(opts.SuiteID, res.Tasks, probeIDs, rec); err != nil {
		return nil, fmt.Errorf("agentdojo: %w", err)
	}
	if err := importers.Finish(res.SuiteDir); err != nil {
		return nil, err
	}
	res.Notes = []string{
		"the AgentDojo image " + w.imageTag + " is built on this machine; elsewhere rebuild it with `docker build -t " + w.imageTag + " " + filepath.Join(res.SuiteDir, "image") + "`",
		"bifrost-bench suite lint " + opts.SuiteID,
		"bifrost-bench suite lock " + opts.SuiteID,
		"bifrost-bench suite admit " + opts.SuiteID,
		"the agent's answer is read from answer.txt, where upstream reads the model's final message; the attack addresses the model as \"the AI language model\" (docs/importing-agentdojo.md)",
	}
	return res, nil
}

// buildImage builds the AgentDojo image from the context the writer laid
// down, unless an image with the tag is already present.
func (im *importer) buildImage(ctx context.Context, tag, dir string) error {
	res, err := im.docker.Run(ctx, "image", "inspect", "--format", "{{.Id}}", tag)
	if err != nil {
		return fmt.Errorf("inspect image %s: %w", tag, err)
	}
	if res.ExitCode == 0 {
		return nil
	}
	if _, err := im.docker.MustRun(ctx, "build", "--label", utils.LabelBench+"=1", "-t", tag, dir); err != nil {
		return fmt.Errorf("build image %s: %w", tag, err)
	}
	return nil
}

// readCatalog runs catalog.py inside the image with no network and parses
// what it prints.
func (im *importer) readCatalog(ctx context.Context, tag, version, suite string) (*catalog, error) {
	res, err := im.docker.MustRun(ctx, "run", "--rm", "--network", "none", "--label", utils.LabelBench+"=1",
		tag, "python3", "-I", "/opt/heimdall/catalog.py", version, suite)
	if err != nil {
		return nil, fmt.Errorf("read catalog from %s: %w", tag, err)
	}
	cat, err := parseCatalog(res.Stdout)
	if err != nil {
		return nil, fmt.Errorf("catalog from %s: %w", tag, err)
	}
	if cat.Suite != suite || cat.BenchmarkVersion != version {
		return nil, fmt.Errorf("catalog from %s is for %s %s, asked for %s %s", tag, cat.Suite, cat.BenchmarkVersion, suite, version)
	}
	return cat, nil
}

// splitList splits a comma-separated setting, dropping blanks.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
