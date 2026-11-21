package main

// Runner jobs, runner side (gap G05): engine.JobExecutor for `runner start`
// and the shared runner. A job lease carries the argv of one CLI command and
// a zip of the project files it needs. The runner unpacks the zip into a
// scratch project, writes a runner-local config there (the runner's own
// runs root and pricing, the snapshot's benchmarks), runs
// `bifrost-bench <argv>` with cliJob's process rules (own process group,
// whitelisted environment, SIGTERM then SIGKILL on cancel), redacts every
// log line and collects the files the command wrote under the job's
// collect paths.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbconfig"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/evidencestore/evconfig"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/secrets"
	"gopkg.in/yaml.v3"
)

// runnerJobCommands are the CLI command groups a runner job may run. A
// lease naming any other (serve, runner, worker...) is refused.
var runnerJobCommands = map[string]bool{
	"agent": true, "mcp": true, "skill": true, "monitor": true, "gpu": true,
	"voice": true, "visual": true, "simulator": true, "evidence": true,
}

// Runner job limits.
const (
	runnerJobMaxFiles     = 200
	runnerJobMaxFileBytes = 8 << 20
	runnerJobMaxUnzipped  = 256 << 20
)

// runnerJobExecutor runs job leases with the runner's own config.
type runnerJobExecutor struct {
	cfg *lib.Config
}

func newRunnerJobExecutor(a *app) engine.JobExecutor {
	if a == nil || a.cfg == nil {
		return nil
	}
	return runnerJobExecutor{cfg: a.cfg}
}

// RunJob implements engine.JobExecutor.
func (x runnerJobExecutor) RunJob(ctx context.Context, job *schemas.JobLease, log func(string)) (*engine.JobRun, error) {
	if job == nil || len(job.Argv) == 0 || !runnerJobCommands[job.Argv[0]] {
		return nil, errors.New("this runner refuses the job: its command is not one runner jobs may run")
	}
	dir, err := os.MkdirTemp("", "heimdall-runner-job-*")
	if err != nil {
		return nil, err
	}
	run := &engine.JobRun{Dir: dir, Cleanup: func() { os.RemoveAll(dir) }}
	if err := unpackJobFiles(job.Files, dir); err != nil {
		return run, fmt.Errorf("unpack the project snapshot: %w", err)
	}
	cfgPath, err := x.writeConfig(dir)
	if err != nil {
		return run, err
	}
	argv := append([]string(nil), job.Argv...)
	for i := range argv {
		if argv[i] == cliConfig {
			argv[i] = cfgPath
		}
	}
	start := time.Now().Add(-time.Second)
	res, runErr := runCLI(ctx, jobEnv{ProjectRoot: dir}, argv, &lineLogger{log: log})
	if res != nil {
		if raw, err := json.Marshal(res); err == nil {
			run.Result = json.RawMessage(secrets.Redact(string(raw)))
		}
	}
	files, err := collectJobFiles(dir, job.Collect, start)
	if err != nil && runErr == nil {
		runErr = err
	}
	run.Files = files
	if runErr != nil {
		return run, errors.New(secrets.Redact(runErr.Error()))
	}
	return run, nil
}

// writeConfig writes the runner-local config of one job at
// <dir>/.heimdall-jobs/job.yaml (so the bench root is dir): the runner's
// runs root and pricing, the snapshot's benchmarks when it has them, and no
// database, evidence storage or auth section.
func (x runnerJobExecutor) writeConfig(dir string) (string, error) {
	c := *x.cfg
	c.RunsRoot = c.Path(c.RunsRoot)
	c.Pricing = c.Path(c.Pricing)
	c.BenchmarksRoot = c.Path(c.BenchmarksRoot)
	if st, err := os.Stat(filepath.Join(dir, "benchmarks")); err == nil && st.IsDir() {
		c.BenchmarksRoot = filepath.Join(dir, "benchmarks")
	}
	c.Database = dbconfig.Config{}
	c.Storage = evconfig.Config{}
	c.Auth.Mode = ""
	data, err := yaml.Marshal(&c)
	if err != nil {
		return "", fmt.Errorf("write job config: %w", err)
	}
	cdir := filepath.Join(dir, jobConfigDir)
	if err := os.MkdirAll(cdir, 0o700); err != nil {
		return "", fmt.Errorf("write job config: %w", err)
	}
	p := filepath.Join(cdir, "job.yaml")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return "", fmt.Errorf("write job config: %w", err)
	}
	return p, nil
}

// unpackJobFiles extracts the snapshot zip into dir: regular files only,
// local paths only, capped in total size.
func unpackJobFiles(data []byte, dir string) error {
	if len(data) == 0 {
		return nil
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var total int64
	for _, f := range zr.File {
		name := filepath.FromSlash(f.Name)
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() || !filepath.IsLocal(name) {
			return fmt.Errorf("snapshot entry %q is not a plain file inside the project", f.Name)
		}
		total += int64(f.UncompressedSize64)
		if total > runnerJobMaxUnzipped {
			return fmt.Errorf("snapshot unpacks to more than %d bytes", runnerJobMaxUnzipped)
		}
		dst := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// collectJobFiles lists the regular files under the collect paths that the
// job wrote (modified after start), project-relative with forward slashes.
func collectJobFiles(dir string, collect []string, start time.Time) ([]string, error) {
	var out []string
	for _, c := range collect {
		c = filepath.Clean(filepath.FromSlash(c))
		if !filepath.IsLocal(c) {
			continue
		}
		root := filepath.Join(dir, c)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.ModTime().Before(start) || info.Size() > runnerJobMaxFileBytes {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return nil
			}
			if len(out) >= runnerJobMaxFiles {
				return fmt.Errorf("the job wrote more than %d files under %s", runnerJobMaxFiles, c)
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// lineLogger is a jobs.Logger that hands each redacted line to log.
type lineLogger struct {
	log     func(string)
	partial string
}

func (l *lineLogger) emit(s string) {
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		l.log(secrets.Redact(line))
	}
}

func (l *lineLogger) Printf(format string, args ...any) { l.emit(fmt.Sprintf(format, args...)) }
func (l *lineLogger) Warnf(format string, args ...any) {
	l.emit("warning: " + fmt.Sprintf(format, args...))
}
func (l *lineLogger) Errorf(format string, args ...any) {
	l.emit("error: " + fmt.Sprintf(format, args...))
}
func (l *lineLogger) Write(p []byte) (int, error) {
	s := l.partial + string(p)
	i := strings.LastIndexByte(s, '\n')
	if i < 0 {
		l.partial = s
		return len(p), nil
	}
	l.partial = s[i+1:]
	l.emit(s[:i])
	return len(p), nil
}
