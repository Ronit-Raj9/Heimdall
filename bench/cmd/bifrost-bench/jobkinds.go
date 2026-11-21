package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
)

// jobEnv is what a web parity job kind runs with.
type jobEnv struct {
	App                *app
	OrgID, ProjectRoot string
	Hosted             bool
	Services           handlers.Services
}

// jobKindSpec describes one job kind registered with registerJobKind.
type jobKindSpec struct {
	Kind          string
	Local, Hosted bool
	Opts          []jobs.KindOption
	Validate      func(ctx context.Context, env jobEnv, raw json.RawMessage) (normalized any, target string, err error)
	Run           func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error)
}

var (
	jobKindsMu sync.Mutex
	jobKinds   []jobKindSpec
)

// registerJobKind adds a kind; called from init() in cmd/bifrost-bench/jobs_<area>.go.
func registerJobKind(s jobKindSpec) {
	jobKindsMu.Lock()
	defer jobKindsMu.Unlock()
	for _, k := range jobKinds {
		if k.Kind == s.Kind && ((k.Local && s.Local) || (k.Hosted && s.Hosted)) { // a local and a hosted variant may share a name
			panic("job kind registered twice: " + s.Kind)
		}
	}
	jobKinds = append(jobKinds, s)
}

// jobEnvFor builds the env of a job of the launcher's org.
func (h *jobsHub) jobEnvFor(l *launcher) jobEnv {
	env := jobEnv{App: l.a, OrgID: l.org, ProjectRoot: l.a.cfg.Root, Hosted: h.hosted}
	s := handlers.Services{OrgID: l.org, Hosted: h.hosted, Launcher: l}
	for _, hook := range serviceHooks {
		hook(l.a, &s)
	}
	env.Services = s
	return env
}

// registerJobKinds registers every jobKindSpec this server mode serves
// (end of registerOps).
func (h *jobsHub) registerJobKinds() {
	jobKindsMu.Lock()
	specs := append([]jobKindSpec(nil), jobKinds...)
	jobKindsMu.Unlock()
	for _, s := range specs {
		if (h.hosted && !s.Hosted) || (!h.hosted && !s.Local) || s.Run == nil {
			continue
		}
		s := s
		opts := append([]jobs.KindOption(nil), s.Opts...)
		if s.Validate != nil {
			opts = append(opts, jobs.WithValidator(h.validator(func(ctx context.Context, l *launcher, raw json.RawMessage) (any, string, error) {
				return s.Validate(ctx, h.jobEnvFor(l), raw)
			})))
		}
		h.runner.Register(s.Kind, h.withLauncher(func(ctx context.Context, l *launcher, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
			return s.Run(ctx, h.jobEnvFor(l), j, log, progress)
		}), opts...)
	}
}

// cliArgs turns a job's params into the CLI arguments of this binary.
type cliArgs func(params json.RawMessage) ([]string, error)

// cliJob is a kind that runs this binary as a subprocess with args from the
// params. Phase 0 helper; lane L02 hardens it.
func cliJob(kind string, local, hosted bool, args cliArgs) jobKindSpec {
	return jobKindSpec{
		Kind: kind, Local: local, Hosted: hosted,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			if _, err := args(raw); err != nil {
				return nil, "", fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
			}
			return raw, kind, nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
			argv, err := args(j.Params)
			if err != nil {
				return nil, err
			}
			return nil, runCLI(ctx, env.ProjectRoot, argv, log)
		},
	}
}

// runCLI runs os.Executable() with argv in dir, a whitelisted environment,
// each output line logged; cancel kills the process group.
func runCLI(ctx context.Context, dir string, argv []string, log jobs.Logger) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.MkdirTemp("", "heimdall-job-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	cmd := exec.Command(exe, argv...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		case <-done:
		}
	}()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		log.Printf("%s", sc.Text())
	}
	err = cmd.Wait()
	close(done)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Errorf("%s exited with code %d", kindLabel(argv), ee.ExitCode())
	}
	return err
}

func kindLabel(argv []string) string {
	if len(argv) == 0 {
		return "command"
	}
	return argv[0]
}
