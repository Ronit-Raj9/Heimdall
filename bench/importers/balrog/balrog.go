package balrog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Name is the importer's registered name.
const Name = "balrog"

// Settings (-set key=value).
const (
	SetEnvs  = "envs"  // comma-separated environments; default every one in upstream's config
	SetImage = "image" // a prebuilt BALROG image tag; skips the build
)

// envs are upstream's environments, in the order of its config.
var envs = []string{"babyai", "babaisai", "textworld", "crafter", "nle", "minihack"}

const (
	upstreamRepo    = "https://github.com/balrog-ai/BALROG"
	serviceID       = "balrog"
	servicePort     = 8000
	stateExportID   = "run"
	stateExportPath = "/state/run.json"
	stateExportMax  = 16 << 20
	suiteVersion    = "1.0.0"
	taskVersion     = "1.0.0"
	verifierTimeout = 900
)

// agentTimeout is the agent's time budget per environment: long episodes
// (Crafter 2000 steps, NetHack until death) get more wall time.
var agentTimeout = map[string]int{
	"babyai": 1800, "babaisai": 1800, "textworld": 1800, "minihack": 1800,
	"crafter": 5400, "nle": 5400,
}

func init() {
	importers.Register(&importer{docker: utils.Docker{}, now: time.Now})
}

type dockerRunner interface {
	Run(ctx context.Context, args ...string) (*utils.DockerResult, error)
	MustRun(ctx context.Context, args ...string) (*utils.DockerResult, error)
}

type importer struct {
	docker dockerRunner
	now    func() time.Time
}

func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"balrog (babyai, babaisai, textworld, crafter, nle, minihack)"},
		Source:     "git clone " + upstreamRepo,
		License:    "MIT",
		Track:      schemas.TrackMCP,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"docker at import time: the importer builds the BALROG image (with network, to install the games and download Boxoban levels and TextWorld games) and reads the episode catalog from it with no network",
			"services, state export and the tool proxy in the run",
		},
		Summary: "BALROG game episodes (BabyAI, Baba Is AI, TextWorld, Crafter, NetHack, MiniHack) played through an MCP game service; the reward is upstream's progression metric.",
	}
}

type settings struct {
	envs  []string
	image string
}

func parseSettings(extra map[string]string) (settings, error) {
	known := []string{SetEnvs, SetImage}
	for k := range extra {
		if !slices.Contains(known, k) {
			return settings{}, fmt.Errorf("unknown setting %q (known: %s)", k, strings.Join(known, ", "))
		}
	}
	s := settings{envs: splitList(extra[SetEnvs]), image: strings.TrimSpace(extra[SetImage])}
	for _, e := range s.envs {
		if !slices.Contains(envs, e) {
			return settings{}, fmt.Errorf("environment %q is not one of %s", e, strings.Join(envs, ", "))
		}
	}
	return s, nil
}

func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.SuiteID == "" || opts.From == "" || opts.BenchRoot == "" {
		return nil, errors.New("balrog: bench root, -from and -suite are required")
	}
	set, err := parseSettings(opts.Extra)
	if err != nil {
		return nil, fmt.Errorf("balrog: %w", err)
	}
	up, err := openUpstream(opts.From)
	if err != nil {
		return nil, fmt.Errorf("balrog: %w", err)
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir()}
	w := &writer{up: up, suiteDir: res.SuiteDir}
	if err := w.writeImage(); err != nil {
		return nil, fmt.Errorf("balrog: %w", err)
	}
	if set.image != "" {
		w.imageTag = set.image
	} else if err := im.buildImage(ctx, w.imageTag, filepath.Join(res.SuiteDir, "image")); err != nil {
		return nil, fmt.Errorf("balrog: %w", err)
	}
	cat, err := im.readCatalog(ctx, w.imageTag, set.envs)
	if err != nil {
		return nil, fmt.Errorf("balrog: %w", err)
	}
	picked, skips := selectEpisodes(cat, set.envs, opts.IDs, opts.Limit)
	res.Skipped = append(res.Skipped, skips...)
	for _, e := range picked {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("balrog: %w", err)
		}
		id := taskID(e)
		dir := filepath.Join(res.SuiteDir, "tasks", id)
		if _, err := os.Stat(dir); err == nil && !opts.Force {
			res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "task folder exists (pass -force to replace it)"})
			continue
		}
		if err := w.writeTask(dir, id, e); err != nil {
			return nil, fmt.Errorf("balrog: task %s: %w", id, err)
		}
		res.Tasks = append(res.Tasks, id)
		if e.Reference == nil {
			w.drafts++
		}
	}
	rec := schemas.ImportRecord{
		Importer:  Name,
		Benchmark: fmt.Sprintf("balrog %s (package %s)", strings.Join(orAll(set.envs), ","), up.version),
		Source:    upstreamRepo,
		Revision:  up.revision,
		License:   up.license,
		At:        im.now().UTC().Truncate(time.Second),
	}
	if err := w.writeSuite(opts.SuiteID, res.Tasks, rec); err != nil {
		return nil, fmt.Errorf("balrog: %w", err)
	}
	if err := importers.Finish(res.SuiteDir); err != nil {
		return nil, err
	}
	res.Notes = []string{
		"the BALROG image " + w.imageTag + " is built on this machine; elsewhere rebuild it with `docker build -t " + w.imageTag + " " + filepath.Join(res.SuiteDir, "image") + "`",
		fmt.Sprintf("%d of %d task(s) have no reference control (no solver or walkthrough upstream, or the environment does not build): they stay drafts at admission", w.drafts, len(res.Tasks)),
		"bifrost-bench suite lint " + opts.SuiteID,
		"bifrost-bench suite lock " + opts.SuiteID,
		"bifrost-bench suite admit " + opts.SuiteID,
	}
	return res, nil
}

func orAll(e []string) []string {
	if len(e) == 0 {
		return envs
	}
	return e
}

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

func (im *importer) readCatalog(ctx context.Context, tag string, envs []string) (*catalog, error) {
	args := []string{"run", "--rm", "--network", "none", "--label", utils.LabelBench + "=1",
		tag, "python3", "-I", "/opt/heimdall/catalog.py"}
	res, err := im.docker.MustRun(ctx, append(args, envs...)...)
	if err != nil {
		return nil, fmt.Errorf("read catalog from %s: %w", tag, err)
	}
	cat, err := parseCatalog(res.Stdout)
	if err != nil {
		return nil, fmt.Errorf("catalog from %s: %w", tag, err)
	}
	return cat, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
