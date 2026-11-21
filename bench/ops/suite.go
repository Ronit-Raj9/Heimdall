package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// LoadSuite loads a suite with the first registered loader over root.
func LoadSuite(ctx context.Context, root, ref string) (*schemas.Suite, error) {
	loaders := suite.Builtin(root)
	if len(loaders) == 0 {
		return nil, fmt.Errorf("no suite loader registered")
	}
	return loaders[0].Load(ctx, ref)
}

// LintReport is what suite lint found.
type LintReport struct {
	Suite    string          `json:"suite"`
	Version  string          `json:"version"`
	Tasks    int             `json:"tasks"`
	Probes   int             `json:"probes"`
	Problems []suite.Problem `json:"problems"`
	Errors   int             `json:"errors"`
}

// SuiteLint lints suite ref under benchRoot and prints each problem and the
// summary line to w, as `suite lint` does. A load failure is an error; lint
// problems are in the report (Errors > 0 fails the command).
func SuiteLint(ctx context.Context, benchRoot, ref string, w io.Writer) (*LintReport, error) {
	s, err := LoadSuite(ctx, benchRoot, ref)
	if err != nil {
		return nil, fmt.Errorf("load suite %s: %w", ref, err)
	}
	problems := suite.Lint(s)
	w = out(w)
	for _, p := range problems {
		fmt.Fprintf(w, "%-7s %s: %s\n", p.Severity, p.Path, p.Message)
	}
	fmt.Fprintf(w, "%s@%s: %d tasks, %d probes, %d problems\n", s.ID, s.Version, len(s.Tasks), len(s.Probes), len(problems))
	rep := &LintReport{Suite: s.ID, Version: s.Version, Tasks: len(s.Tasks), Probes: len(s.Probes), Problems: problems}
	if rep.Problems == nil {
		rep.Problems = []suite.Problem{}
	}
	for _, p := range problems {
		if p.Severity == "error" {
			rep.Errors++
		}
	}
	return rep, nil
}

// LockReport is what suite lock wrote.
type LockReport struct {
	Suite    string `json:"suite"`
	Path     string `json:"path"`
	Tasks    int    `json:"tasks"`
	Admitted int    `json:"admitted"`
	Probes   int    `json:"probes"`
	LockHash string `json:"lock_hash"`
}

// LockOptions configure SuiteLock.
type LockOptions struct {
	BenchRoot string
	Ref       string
	// Config is the project config: its helper and tool proxy images are
	// fingerprinted. Nil when only a benchmarks folder was given.
	Config *lib.Config
	Docker utils.Docker
	// Pin records local image ids (-pin-local-images); nil keeps what the
	// previous lock did.
	Pin *bool
}

// SuiteLock writes suite.lock for the suite, printing the drift against the
// previous lock and the summary line to w, as `suite lock` does.
func SuiteLock(ctx context.Context, o LockOptions, w io.Writer) (*LockReport, error) {
	s, err := LoadSuite(ctx, o.BenchRoot, o.Ref)
	if err != nil {
		return nil, fmt.Errorf("load suite %s: %w", o.Ref, err)
	}
	pin := suite.LockPinsLocal(s.Lock) // Lock fingerprints: pinning is sticky unless the flag says otherwise
	if o.Pin != nil {
		pin = *o.Pin
	}
	lock, err := suite.LockWith(s, time.Now().UTC(), LockFingerprintOptions(ctx, o.Config, o.Docker, pin))
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	w = out(w)
	PrintLockChanges(w, s, lock) // Lock fingerprints: drift as a diff
	path := filepath.Join(s.Root, "suite.lock")
	data, err := suite.WriteLock(path, lock)
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	admitted := 0
	for _, tl := range lock.Tasks {
		if tl.Status == schemas.TaskAdmitted {
			admitted++
		}
	}
	fmt.Fprintf(w, "wrote %s (%d tasks, %d admitted, %d probes, fingerprinted)\n", path, len(lock.Tasks), admitted, len(lock.Probes))
	return &LockReport{Suite: s.ID, Path: path, Tasks: len(lock.Tasks), Admitted: admitted, Probes: len(lock.Probes), LockHash: utils.SHA256Bytes(data)}, nil
}

// LockFingerprintOptions is what `suite lock`, `suite admit` and the suites
// API bind into lock fingerprints (docs/lock-fingerprints.md): the versions
// of the built-in environments and graders, the same values the planner's
// registry reports, the helper image of cfg (nil when only -root was
// given), and what the local docker says about each image. An image with a
// registry digest is recorded by it; a locally built one by tag only,
// unless pin records its local image id (`suite lock -pin-local-images`).
// Without docker every image is recorded as "tag only".
func LockFingerprintOptions(ctx context.Context, cfg *lib.Config, d utils.Docker, pin bool) suite.FingerprintOptions {
	reg := &schemas.Registry{
		Environments: environments.Builtin(environments.DockerWorkspaceConfig{}),
		Graders:      append([]schemas.Grader{graders.NewJudge(graders.JudgeConfig{})}, graders.Builtin(graders.HiddenTestsConfig{})...),
	}
	opts := suite.FingerprintOptions{Parts: engine.PartVersions(reg)}
	if cfg != nil {
		opts.Images = map[string]string{suite.ImageHelper: cfg.Images.Helper, suite.ImageToolProxy: cfg.Images.ToolProxy}
	}
	seen := map[string]*environments.ImageInfo{}
	inspect := func(tag string) *environments.ImageInfo {
		if info, ok := seen[tag]; ok {
			return info
		}
		var out *environments.ImageInfo
		if info, err := environments.InspectImage(ctx, d, tag); err == nil && info.ID != "" {
			out = &info
		}
		seen[tag] = out
		return out
	}
	opts.ImageID = func(tag string) (string, bool) {
		if info := inspect(tag); info != nil {
			return info.ID, true
		}
		return "", false
	}
	opts.RepoDigests = func(tag string) []string {
		if info := inspect(tag); info != nil {
			return info.RepoDigests
		}
		return nil
	}
	opts.PinLocal = pin
	return opts
}

// PrintLockChanges prints what writing fresh over the suite's current lock
// changes, one line per suite, task or probe, as `suite lock` reports drift.
// A legacy lock that is only rewritten never prints anything; a lock gaining
// fingerprints prints that once.
func PrintLockChanges(w io.Writer, s *schemas.Suite, fresh *schemas.SuiteLock) {
	if s.Lock == nil {
		return
	}
	for _, c := range suite.Diff(s.Lock, fresh) {
		fmt.Fprintf(w, "  %-5s %-24s %-5s %-8s %v\n", c.Scope, c.ID, c.Level, c.Action, c.Fields)
	}
}

// AdmitEnv is what admission needs from the host: the project config, the
// docker CLI and the assembled registry (the CLI's app).
type AdmitEnv struct {
	Config *lib.Config
	Docker utils.Docker
	Logger schemas.Logger
	// Registry returns the registry; gateway asks for the live gateway (a
	// judged suite's grader calls its model through it).
	Registry func(ctx context.Context, gateway bool) (*schemas.Registry, error)
}

// AdmitReport is what suite admit did.
type AdmitReport struct {
	Suite    string                              `json:"suite"`
	Path     string                              `json:"path,omitempty"` // the suite.lock written
	Problems []suite.Problem                     `json:"problems"`
	Tasks    map[string]*schemas.AdmissionRecord `json:"tasks"`
	Order    []string                            `json:"order"`
	Failed   int                                 `json:"failed"`
}

// ErrAdmitRefused is the cause when static validation refused an admission
// before anything ran.
var ErrAdmitRefused = errors.New("admit refused")

// SuiteAdmit admits the tasks of suite ref (only one when only is set) with
// the environment and grader the suite names, looked up by name in the
// registry, builds the suite's base image and the helper image first when
// they are missing, and writes every result to suite.lock. Before any image
// is built or container started it runs the static validation of `task
// validate` on every task it admits and refuses, writing nothing, on any
// error. It prints what `suite admit` prints to w. A task that fails its
// controls is counted in Failed (the command exits 1), not an error.
func SuiteAdmit(ctx context.Context, env AdmitEnv, ref, only string, w io.Writer) (*AdmitReport, error) {
	w = out(w)
	reg, err := env.Registry(ctx, false)
	if err != nil {
		return nil, err
	}
	s, err := LoadSuite(ctx, env.Config.Path(env.Config.BenchmarksRoot), ref)
	if err != nil {
		return nil, fmt.Errorf("load suite %s: %w", ref, err)
	}
	if only != "" && s.TaskByID(only) == nil {
		return nil, fmt.Errorf("suite %s has no task %q in its tasks list", s.ID, only)
	}
	if s.Grading == schemas.GradingJudged { // run kernel: the judge grader calls its model through the gateway
		if reg, err = env.Registry(ctx, true); err != nil {
			return nil, err
		}
	}
	deps := suite.AdmitDeps{Logger: env.Logger, GPUs: reg.GPUs} // X7: GPU tasks hold the machine's GPUs (gpu.go)
	// The first registered part of a name wins, as in the engine's findPart:
	// the app registers the gateway-wired judge ahead of Builtin's unwired one.
	for _, e := range reg.Environments {
		if deps.Env == nil && e.GetName() == s.Environment {
			deps.Env = e
		}
	}
	for _, g := range reg.Graders {
		if deps.Grader == nil && g.GetName() == s.Grader {
			deps.Grader = g
		}
	}
	if deps.Env == nil {
		return nil, fmt.Errorf("suite %s: environment %q is not registered", s.ID, s.Environment)
	}
	if deps.Grader == nil {
		return nil, fmt.Errorf("suite %s: grader %q is not registered", s.ID, s.Grader)
	}
	rep := &AdmitReport{Suite: s.ID, Tasks: map[string]*schemas.AdmissionRecord{}, Order: []string{}}
	problems := suite.StaticValidation(ctx, s, only)
	rep.Problems = problems
	if rep.Problems == nil {
		rep.Problems = []suite.Problem{}
	}
	for _, p := range problems {
		fmt.Fprintf(w, "%-7s %s: %s\n", p.Severity, p.Path, p.Message)
	}
	if n := suite.Errors(problems); n > 0 {
		noun := "errors"
		if n == 1 {
			noun = "error"
		}
		return rep, fmt.Errorf("%w: static validation found %d %s; fix them (bifrost-bench task validate <suite>/<task> shows the same list) and admit again",
			ErrAdmitRefused, n, noun)
	}
	if err := EnsureImages(ctx, env.Config, env.Docker, s, w); err != nil {
		return rep, err
	}
	fpo := LockFingerprintOptions(ctx, env.Config, env.Docker, suite.LockPinsLocal(s.Lock))
	var lock *schemas.SuiteLock
	if only != "" { // one task: re-hash only it, other entries stay untouched
		lock, _, err = suite.LockTasks(s, []string{only}, time.Now().UTC(), fpo)
	} else {
		lock, err = suite.LockWith(s, time.Now().UTC(), fpo)
	}
	if err != nil {
		return rep, fmt.Errorf("lock: %w", err)
	}
	for _, t := range s.Tasks {
		if only != "" && t.ID != only {
			continue
		}
		rec, err := suite.Admit(ctx, deps, s, t)
		if err != nil {
			return rep, fmt.Errorf("admit %s: %w", t.ID, err)
		}
		if err := suite.RecordAdmission(lock, t.ID, rec); err != nil {
			return rep, err
		}
		if !rec.Admitted {
			rep.Failed++
		}
		rep.Tasks[t.ID] = rec
		rep.Order = append(rep.Order, t.ID)
		PrintAdmission(w, t.ID, rec)
	}
	path := filepath.Join(s.Root, "suite.lock")
	if _, err := suite.WriteLock(path, lock); err != nil {
		return rep, fmt.Errorf("write %s: %w", path, err)
	}
	rep.Path = path
	fmt.Fprintf(w, "wrote %s\n", path)
	return rep, nil
}

// PrintAdmission prints one task's admission checks.
func PrintAdmission(w io.Writer, id string, rec *schemas.AdmissionRecord) {
	status := "admitted"
	if !rec.Admitted {
		status = "NOT ADMITTED"
	}
	fmt.Fprintf(w, "%-4s %s\n", id, status)
	for _, c := range rec.Checks {
		mark := "ok  "
		if !c.Passed {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "     %s %-40s want %-6s got %s\n", mark, c.Name, c.Want, c.Got)
		if !c.Passed && c.Detail != "" {
			fmt.Fprintf(w, "          %s\n", c.Detail)
		}
	}
}

// EnsureImages builds the suite's base image from base_image.dockerfile and
// the helper image from <project>/deploy/helper/Dockerfile, each only when it
// is missing, so a fresh project needs nothing but the binary and Docker. An
// image that exists is never rebuilt. "building ..." lines go to w, docker's
// own output to stderr.
func EnsureImages(ctx context.Context, cfg *lib.Config, d utils.Docker, s *schemas.Suite, w io.Writer) error {
	suiteDockerfile := ""
	if s.BaseImage.Dockerfile != "" {
		// Resolved again right before the build: the build context is the
		// Dockerfile's folder, and docker follows a symlink to it.
		real, err := utils.ResolveInside(s.Root, s.BaseImage.Dockerfile)
		if err != nil {
			return fmt.Errorf("suite %s base image: base_image.dockerfile: %w", s.ID, err)
		}
		suiteDockerfile = real
	}
	if err := EnsureImage(ctx, d, s.BaseImage.Tag, suiteDockerfile, w); err != nil {
		return fmt.Errorf("suite %s base image: %w", s.ID, err)
	}
	if err := ensureServiceImages(ctx, d, s, w); err != nil {
		return err
	}
	if helper := cfg.Images.Helper; helper != "" && helper != s.BaseImage.Tag {
		helperDockerfile := filepath.Join(cfg.Root, "deploy", "helper", "Dockerfile")
		if _, err := os.Stat(helperDockerfile); err != nil {
			helperDockerfile = ""
		}
		if err := EnsureImage(ctx, d, helper, helperDockerfile, w); err != nil {
			return fmt.Errorf("helper image (images.helper): %w", err)
		}
	}
	return nil
}

// ensureServiceImages builds every missing service image of the suite and
// its tasks, each once: from build.dockerfile when the service has a build
// section (relative to the suite root for suite services, the task folder
// for task services), else, for a service that names only an image, from
// services/<service id>/Dockerfile in the suite folder when it exists (the
// layout `suite init -template tool-use|mcp` writes).
func ensureServiceImages(ctx context.Context, d utils.Docker, s *schemas.Suite, w io.Writer) error {
	done := map[string]bool{}
	for _, b := range ServiceBuilds(s) {
		if done[b.Tag] {
			continue
		}
		done[b.Tag] = true
		dockerfile := ""
		if b.Dockerfile != "" {
			real, err := utils.ResolveInside(b.Root, b.Dockerfile)
			if err != nil {
				return fmt.Errorf("service %s: build.dockerfile: %w", b.Service, err)
			}
			dockerfile = real
		}
		if err := EnsureImage(ctx, d, b.Tag, dockerfile, w); err != nil {
			return fmt.Errorf("service %s image: %w", b.Service, err)
		}
	}
	return nil
}

// ServiceBuild is one service image EnsureImages looks after.
type ServiceBuild struct {
	Service, Tag string
	Root         string // folder the Dockerfile must stay inside
	Dockerfile   string // relative to Root; empty when nothing builds it
}

// ServiceBuilds lists the service images of a suite, suite services first.
func ServiceBuilds(s *schemas.Suite) []ServiceBuild {
	var out []ServiceBuild
	add := func(sv schemas.ServiceSpec, root string) {
		switch {
		case sv.Build != nil && sv.Build.Tag != "":
			out = append(out, ServiceBuild{Service: sv.ID, Tag: sv.Build.Tag, Root: root, Dockerfile: sv.Build.Dockerfile})
		case sv.Image != "":
			b := ServiceBuild{Service: sv.ID, Tag: sv.Image, Root: s.Root}
			conv := filepath.Join("services", sv.ID, "Dockerfile")
			if _, err := os.Stat(filepath.Join(s.Root, conv)); err == nil {
				b.Dockerfile = conv
			}
			out = append(out, b)
		}
	}
	for _, sv := range s.Services {
		add(sv, s.Root)
	}
	for _, t := range s.Tasks {
		for _, sv := range t.Services {
			add(sv, t.Dir)
		}
	}
	return out
}

// EnsureImage builds tag from dockerfile (in the Dockerfile's folder) when
// docker does not have the image.
func EnsureImage(ctx context.Context, d utils.Docker, tag, dockerfile string, w io.Writer) error {
	if tag == "" {
		return fmt.Errorf("no image tag")
	}
	res, err := d.Run(ctx, "image", "inspect", "--format", "{{.Id}}", tag)
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return nil
	}
	if dockerfile == "" {
		return fmt.Errorf("image %s is missing and no Dockerfile builds it; build or pull it first", tag)
	}
	fmt.Fprintf(out(w), "building %s from %s\n", tag, dockerfile)
	bin := d.Binary
	if bin == "" {
		bin = "docker"
	}
	cmd := exec.CommandContext(ctx, bin, "build", "-t", tag, "-f", dockerfile, filepath.Dir(dockerfile))
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build %s: %w", tag, err)
	}
	return nil
}
