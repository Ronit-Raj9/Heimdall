package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbconfig"
	"github.com/maximhq/bifrost/bench/evidencestore/evconfig"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/secrets"
	"gopkg.in/yaml.v3"
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
	// Args is the argv builder of a cliJob kind (nil otherwise); runner
	// job variants reuse it (gap G05, jobs_runnerjob.go).
	Args cliArgs
	// ExactResult: the kind's Run redacts its result by the exact secret
	// values it handled, and the pattern scrub is skipped for the result
	// (log and error keep it). issuebench.work returns task content: the
	// scrub's URL userinfo rule rewrote a test id such as
	// test_url[http://user:pass@example.com], and the task's reference answer
	// could then never pass admission.
	ExactResult bool
	// OkExit, on a cliJob kind, may turn the command's exit into a finished
	// job: it gets the exit code and the command's whole output and returns
	// the job's result. Only scan.skill sets it (fix wave 2, N23): a scan
	// that blocks a skill exits 1, and that is a verdict, not a failure.
	OkExit cliOkExit
}

// cliOkExit maps a CLI job's exit code and output to a finished job's
// result; ok false keeps the usual handling (exit 0 succeeds, any other
// code fails the job).
type cliOkExit func(code int, stdout []byte) (result map[string]any, ok bool)

// cliOkExits maps the leading command words of a CLI job's argv (for
// example "skill scan") to the OkExit of its kind, so every path that runs
// the command through runCLI (the hosted in-process variant too) maps the
// exit the same way.
var cliOkExits = map[string]cliOkExit{}

// withOkExit sets a cliJob kind's OkExit for the command words it runs.
func withOkExit(spec jobKindSpec, command string, fn cliOkExit) jobKindSpec {
	spec.OkExit = fn
	cliOkExits[command] = fn
	return spec
}

// okExitFor returns the OkExit registered for argv's command words.
func okExitFor(argv []string) cliOkExit {
	if len(argv) < 2 {
		return nil
	}
	return cliOkExits[argv[0]+" "+argv[1]]
}

// cliOutputCap bounds the output runCLI keeps for an OkExit.
const cliOutputCap = 8 << 20

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

// jobEnvFor builds the env of a job of the launcher's org. It starts from
// the org's request-path services (launcher.services), so a job sees the
// same run index, suites, agents and job API as the org's handlers; before
// K4 it ran the service hooks over an empty Services, so Index, Suites and
// Agents were nil and every run-reading job failed ("this server cannot
// read runs"). A launcher whose deps were never bound still gets its own
// run index.
func (h *jobsHub) jobEnvFor(l *launcher) jobEnv {
	env := jobEnv{App: l.a, OrgID: l.org, ProjectRoot: l.a.cfg.Root, Hosted: h.hosted}
	var s handlers.Services
	if l.services != nil {
		s = *l.services
	} else {
		s = handlers.Services{OrgID: l.org, Hosted: h.hosted}
		for _, hook := range serviceHooks {
			hook(l.a, &s)
		}
	}
	s.OrgID, s.Hosted, s.Launcher = l.org, h.hosted, l
	if s.Index == nil && l.ix != nil && l.a.store != nil {
		s.Index = indexAdapter{ix: l.ix, root: l.a.store.Root()}
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
			return runRedacted(ctx, s, h.jobEnvFor(l), j, log, progress)
		}), opts...)
	}
}

// runRedacted runs a spec with its log, error and result redacted
// (secrets.Redact), so no job kind can leak a key into job_logs or the API.
func runRedacted(ctx context.Context, s jobKindSpec, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	rl := secrets.Logger(log)
	res, err := s.Run(ctx, env, j, rl, progress)
	rl.Flush()
	if err != nil {
		err = errors.New(secrets.Redact(err.Error()))
	}
	if s.ExactResult {
		return res, err
	}
	return redactResult(res), err
}

// redactResult masks secrets in a job result by round-tripping it through
// JSON; a result that does not marshal is returned as is.
func redactResult(res any) any {
	if res == nil {
		return nil
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return res
	}
	clean := secrets.Redact(string(raw))
	if clean == string(raw) {
		return res
	}
	var out any
	if json.Unmarshal([]byte(clean), &out) != nil {
		return map[string]string{"result": clean}
	}
	return out
}

// cliArgs turns a job's params into the CLI arguments of this binary. An
// argument equal to cliConfig is replaced by the path of the org's config
// (a redacted copy written for the job), so commands read the org's roots.
type cliArgs func(params json.RawMessage) ([]string, error)

// cliConfig is the argv placeholder of the job's -config path.
const cliConfig = "{{config}}"

// cliKillGrace is how long a cancelled subprocess has between SIGTERM and
// SIGKILL.
var cliKillGrace = 10 * time.Second

// cliExecutable is the binary cliJob runs (this binary; tests swap it).
var cliExecutable = os.Executable

// cliJob is a kind that runs this binary as a subprocess with args from the
// params: its own process group in the project root, a whitelisted
// environment (no database URL, master key or S3 credentials), every output
// line logged, SIGTERM then SIGKILL after cliKillGrace on cancel. The last
// stdout line that is a JSON object becomes the job's result.
func cliJob(kind string, local, hosted bool, args cliArgs) jobKindSpec {
	return jobKindSpec{
		Kind: kind, Local: local, Hosted: hosted, Args: args,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			if _, err := args(raw); err != nil {
				return nil, "", fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
			}
			if len(raw) == 0 {
				raw = json.RawMessage("{}")
			}
			return raw, cliTarget(raw), nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
			argv, err := args(j.Params)
			if err != nil {
				return nil, err
			}
			return runCLI(ctx, env, withJobConfig(argv), log)
		},
	}
}

// withJobConfig makes sure a CLI job reads the server's resolved config: an
// argv with no -config gets "-config {{config}}" before its first flag (or
// at the end), so no subprocess falls back to deploy/bench.yaml in the
// project folder.
func withJobConfig(argv []string) []string {
	for _, a := range argv {
		if a == "-config" || a == "--config" || strings.HasPrefix(a, "-config=") || strings.HasPrefix(a, "--config=") {
			return argv
		}
	}
	at := len(argv)
	for i, a := range argv {
		if strings.HasPrefix(a, "-") {
			at = i
			break
		}
	}
	out := make([]string, 0, len(argv)+2)
	out = append(out, argv[:at]...)
	out = append(out, "-config", cliConfig)
	return append(out, argv[at:]...)
}

// cliTarget is what a CLI job acts on: the first of suite, exp_id, agent,
// path in its params, else empty.
func cliTarget(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, k := range []string{"suite", "exp_id", "agent", "path"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// jobConfigDir holds the redacted config copies of running CLI jobs, one
// level under the project root so the copy's bench root is the project root.
const jobConfigDir = ".heimdall-jobs"

// writeJobConfig writes a copy of the org's config for one CLI job with the
// database, evidence storage and auth sections cleared (they may hold
// credentials the subprocess must not see) and its roots made absolute.
func writeJobConfig(env jobEnv) (string, func(), error) {
	if env.App == nil || env.App.cfg == nil {
		return "", nil, errors.New("this job needs the org config, which is not available here")
	}
	c := *env.App.cfg
	c.RunsRoot = c.Path(c.RunsRoot)
	c.BenchmarksRoot = c.Path(c.BenchmarksRoot)
	c.Pricing = c.Path(c.Pricing)
	c.Database = dbconfig.Config{}
	c.Storage = evconfig.Config{}
	c.Auth.Mode = ""
	data, err := yaml.Marshal(&c)
	if err != nil {
		return "", nil, fmt.Errorf("write job config: %w", err)
	}
	dir := filepath.Join(env.ProjectRoot, jobConfigDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("write job config: %w", err)
	}
	f, err := os.CreateTemp(dir, "job-*.yaml")
	if err != nil {
		return "", nil, fmt.Errorf("write job config: %w", err)
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("write job config: %v", errors.Join(werr, cerr))
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// runCLI runs cliExecutable with argv in env.ProjectRoot and returns the
// last JSON object line of its output as the result.
func runCLI(ctx context.Context, env jobEnv, argv []string, log jobs.Logger) (any, error) {
	exe, err := cliExecutable()
	if err != nil {
		return nil, err
	}
	argv = append([]string(nil), argv...)
	for i, a := range argv {
		if a != cliConfig {
			continue
		}
		path, cleanup, err := writeJobConfig(env)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		for k := i; k < len(argv); k++ {
			if argv[k] == cliConfig {
				argv[k] = path
			}
		}
		break
	}
	home, err := os.MkdirTemp("", "heimdall-job-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	cmd := exec.Command(exe, argv...)
	cmd.Dir = env.ProjectRoot
	cmd.Env = cliEnv(home)
	cmd.Stdout, cmd.Stderr = w, w
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		w.Close()
		return nil, fmt.Errorf("start %s: %w", kindLabel(argv), err)
	}
	w.Close()
	pgid := cmd.Process.Pid

	var last string
	okExit := okExitFor(argv)
	var output bytes.Buffer // the whole output, kept only for an OkExit
	lines := make(chan struct{})
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if t := bytes.TrimSpace([]byte(line)); len(t) > 1 && t[0] == '{' && json.Valid(t) {
				last = string(t)
			}
			if okExit != nil && output.Len() < cliOutputCap {
				output.WriteString(line)
				output.WriteByte('\n')
			}
			log.Printf("%s", line)
		}
	}()

	exited := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			log.Warnf("cancel requested: stopping %s (SIGTERM, SIGKILL after %s)", kindLabel(argv), cliKillGrace)
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(cliKillGrace):
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		case <-exited:
		}
	}()
	err = cmd.Wait()
	close(exited)
	_ = syscall.Kill(-pgid, syscall.SIGKILL) // stragglers the command left in its group
	select {
	case <-lines:
	case <-time.After(2 * time.Second):
		r.Close()
		<-lines
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var ee *exec.ExitError
	if okExit != nil && (err == nil || errors.As(err, &ee)) {
		code := 0
		if ee != nil {
			code = ee.ExitCode()
		}
		if res, ok := okExit(code, output.Bytes()); ok {
			if s, _ := res["summary"].(string); s != "" {
				log.Printf("%s", s)
			}
			return res, nil
		}
	}
	if errors.As(err, &ee) {
		return nil, fmt.Errorf("%s exited with code %d; the log above says why", kindLabel(argv), ee.ExitCode())
	}
	if err != nil {
		return nil, err
	}
	if last == "" {
		return nil, nil
	}
	var res any
	if json.Unmarshal([]byte(last), &res) != nil {
		return nil, nil
	}
	return res, nil
}

// cliEnv is the whole environment of a CLI job: PATH, a throwaway HOME and
// TMPDIR, and the locale. Nothing else from the server passes, so the
// database URL, master key, S3 and provider credentials stay out.
func cliEnv(home string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + home}
	for _, k := range []string{"LANG", "LC_ALL", "TZ", "DOCKER_HOST"} {
		if v, ok := os.LookupEnv(k); ok && !strings.ContainsAny(v, "\n") {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func kindLabel(argv []string) string {
	if len(argv) == 0 {
		return "command"
	}
	return argv[0]
}
