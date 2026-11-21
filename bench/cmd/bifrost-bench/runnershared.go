package main

// Shared runner mode (web parity lane L03): `runner start -shared` serves
// every org the hosted server assigns to the shared pool. It fetches the
// assignments with HEIMDALL_SHARED_RUNNER_SECRET and runs one worker loop
// per org, each with that org's own runner token (pool "shared"). Like any
// runner it replans every leased experiment with its own project, so it
// runs only what the server's built-in templates reproduce. One shared
// runner serves one runtime: point a runsc runner at a Docker daemon whose
// default runtime is gVisor (DOCKER_HOST) and start it with -runtime runsc.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/tenancy"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/sharedpool"
)

const sharedRunnerUsage = `usage:
  bifrost-bench runner start -shared -server <url> -secret-file <path> [-runtime runc|runsc] [-slots n] [-poll 30s] [-name prefix] [-config deploy/bench.yaml] [-debug]`

// hasSharedFlag reports whether args ask for shared runner mode.
func hasSharedFlag(args []string) bool {
	for _, a := range args {
		if a == "-shared" || a == "--shared" || a == "-shared=true" || a == "--shared=true" {
			return true
		}
	}
	return false
}

type sharedFlags struct {
	server, secretFile, runtime, name, config string
	slots                                     int
	poll                                      time.Duration
	debug                                     bool
}

func parseSharedFlags(args []string) (*sharedFlags, error) {
	f := &sharedFlags{}
	fs := flag.NewFlagSet("runner start -shared", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Bool("shared", true, "shared runner mode")
	fs.StringVar(&f.server, "server", "", "hosted server URL")
	fs.StringVar(&f.secretFile, "secret-file", "", "file holding "+sharedpool.SecretEnv+"; mode 0600")
	fs.StringVar(&f.runtime, "runtime", quota.RuntimeRunc, "serve orgs whose runtime is runc or runsc (gVisor)")
	fs.IntVar(&f.slots, "slots", 1, "slots per org (capped by the org's max concurrent runs)")
	fs.DurationVar(&f.poll, "poll", 30*time.Second, "how often to fetch the assignments")
	fs.StringVar(&f.name, "name", "", "runner name prefix (default: shared-<host>)")
	fs.StringVar(&f.config, "config", defaultConfig, "bench config")
	fs.BoolVar(&f.debug, "debug", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	switch {
	case fs.NArg() != 0:
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case f.server == "" || f.secretFile == "":
		return nil, errors.New("-server and -secret-file are required")
	case f.runtime != quota.RuntimeRunc && f.runtime != quota.RuntimeRunsc:
		return nil, errors.New("-runtime must be runc or runsc")
	case f.slots < 1 || f.slots > 64:
		return nil, errors.New("-slots must be 1 to 64")
	case f.poll < time.Second:
		return nil, errors.New("-poll must be at least 1s")
	}
	return f, nil
}

// fetchAssignments asks the server for the shared runner's assignments.
func fetchAssignments(ctx context.Context, c *http.Client, server, secret, runtime string) (*sharedpool.Assignments, error) {
	u := strings.TrimRight(server, "/") + "/api/bench/admin/pool/assignments?runtime=" + url.QueryEscape(runtime)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("assignments: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out sharedpool.Assignments
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("assignments: %w", err)
	}
	return &out, nil
}

// sharedLoop is one org's worker loop.
type sharedLoop struct {
	token    string
	slots    int
	platform string // platformKey of the assignment's platform; "" on the runner's Docker
	cancel   context.CancelFunc
	done     chan struct{}
}

// sharedSlots is the slot count for one assignment.
func sharedSlots(max, flagSlots int) int {
	if max > 0 && max < flagSlots {
		return max
	}
	return flagSlots
}

// reconcileShared starts, restarts and stops loops so that exactly the
// assigned orgs run. start begins one org's loop.
func reconcileShared(loops map[string]*sharedLoop, as *sharedpool.Assignments, flagSlots int,
	start func(a sharedpool.Assignment, slots int) *sharedLoop) {
	want := map[string]sharedpool.Assignment{}
	if !as.KillSwitch {
		for _, a := range as.Assignments {
			want[a.OrgID] = a
		}
	}
	for org, l := range loops {
		a, ok := want[org]
		if ok && a.Token == l.token && sharedSlots(a.MaxConcurrentRuns, flagSlots) == l.slots && platformKey(a.Platform) == l.platform {
			continue
		}
		l.cancel()
		<-l.done
		delete(loops, org)
	}
	for org, a := range want {
		if _, ok := loops[org]; !ok {
			loops[org] = start(a, sharedSlots(a.MaxConcurrentRuns, flagSlots))
		}
	}
}

func runSharedRunner(args []string) int {
	f, err := parseSharedFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner start -shared: %v\n%s\n", err, sharedRunnerUsage)
		return exitUsage
	}
	secret, err := readTokenFile(f.secretFile)
	if err != nil {
		return fail("%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, f.config, appOptions{gateway: true, debug: f.debug})
	if err != nil {
		return fail("%v", err)
	}
	host, _ := os.Hostname()
	prefix := f.name
	if prefix == "" {
		prefix = "shared-" + host
	}
	var wg sync.WaitGroup
	start := func(as sharedpool.Assignment, slots int) *sharedLoop {
		org, token := as.OrgID, as.Token
		lctx, cancel := context.WithCancel(ctx)
		l := &sharedLoop{token: token, slots: slots, platform: platformKey(as.Platform), cancel: cancel, done: make(chan struct{})}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(l.done)
			name := prefix + "-" + org
			if len(name) > 128 {
				name = name[:128]
			}
			reg, root := sharedOrgProject(lctx, a, org)
			labels := []string{"shared", "runtime=" + f.runtime}
			if as.Platform != nil { // X19a: the org's own sandbox platform runs its runs
				pr, err := sandboxRegistry(lctx, a, reg, as.Platform)
				if err != nil {
					a.logger.Error("shared runner: org's worker platform not usable; its runs wait", "org", org, "platform", as.Platform.Name, "error", err.Error())
					return
				}
				reg = pr
				labels = append(labels, "platform="+as.Platform.Name)
			}
			plan := engine.NewPlanner(reg, root, a.logger, engine.WithIgnoreQuarantine(), engine.WithoutGPUCheck(), engine.WithAgentLibrary(suite.LibraryDir())).Plan
			w, err := engine.NewWorker(reg, engine.NewPipeline(reg, root, a.logger),
				sharedEnsureImages(a, reg, root, plan),
				engine.WorkerOptions{Server: f.server, Token: token, WorkerID: name, Host: host, Version: benchVersion,
					Slots: slots, Logger: a.logger, Pool: sharedpool.Pool, Labels: labels,
					Jobs: newRunnerJobExecutor(a)}) // gap G05: hosted CLI jobs (runnerjob.go)
			if err != nil {
				a.logger.Error("shared runner: worker not started", "org", org, "error", err.Error())
				return
			}
			a.logger.Info("shared runner: serving org", "org", org, "slots", slots, "runtime", f.runtime)
			if err := w.Run(lctx); err != nil && lctx.Err() == nil {
				a.logger.Error("shared runner: worker stopped", "org", org, "error", err.Error())
			}
		}()
		return l
	}
	client := &http.Client{Timeout: 30 * time.Second}
	loops := map[string]*sharedLoop{}
	a.logger.Info(fmt.Sprintf("shared runner %s: runtime %s, %d slot(s) per org, server %s", prefix, f.runtime, f.slots, f.server))
	tick := time.NewTicker(f.poll)
	defer tick.Stop()
	for {
		as, err := fetchAssignments(ctx, client, f.server, secret, f.runtime)
		switch {
		case err != nil && ctx.Err() == nil:
			a.logger.Warn("shared runner: assignments not fetched; keeping the current ones", "error", err.Error())
		case err == nil:
			if as.KillSwitch && len(loops) > 0 {
				a.logger.Warn("shared runner: kill switch is on; stopping every org's worker")
			}
			reconcileShared(loops, as, f.slots, start)
		}
		select {
		case <-ctx.Done():
			for _, l := range loops {
				l.cancel()
			}
			wg.Wait()
			return exitOK
		case <-tick.C:
		}
	}
}

// sharedProjectsEnv names the server's per-org project folders as the shared
// runner sees them (read-only mount, deploy/hosted/docker-compose.yml). With
// it the runner plans each org's runs from that org's own suites and agents
// (catalog adds, uploads, web edits); without it, from its own checkout.
const sharedProjectsEnv = "HEIMDALL_SHARED_PROJECTS_DIR"

// sharedOrgProject is the registry and project root the shared runner uses
// for org: the org's project folder when it is mounted, else the runner's own.
func sharedOrgProject(ctx context.Context, a *app, org string) (*schemas.Registry, string) {
	dir := strings.TrimSpace(os.Getenv(sharedProjectsEnv))
	if dir == "" || tenancy.ValidOrgID(org) != nil {
		return withLibrary(a.reg, a.cfg.Path(a.cfg.BenchmarksRoot)), a.cfg.Root
	}
	root := filepath.Join(dir, org)
	if fi, err := os.Stat(filepath.Join(root, "benchmarks")); err != nil || !fi.IsDir() {
		return withLibrary(a.reg, a.cfg.Path(a.cfg.BenchmarksRoot)), a.cfg.Root
	}
	// Agent and task containers are started by the host's docker daemon,
	// which cannot see a path inside the server's volume: bind mounts of
	// task files would come up empty. Mirror the project into a folder the
	// host sees at the same path, and keep the mirror fresh.
	if m := strings.TrimSpace(os.Getenv(sharedMirrorEnv)); m != "" {
		dst := filepath.Join(m, org)
		if err := mirrorTree(root, dst); err != nil {
			a.logger.Warn("shared runner: mirror org project", "org", org, "error", err.Error())
		} else {
			go func() {
				t := time.NewTicker(15 * time.Second)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						if err := mirrorTree(root, dst); err != nil {
							a.logger.Warn("shared runner: mirror org project", "org", org, "error", err.Error())
						}
					}
				}
			}()
			root = dst
		}
	}
	reg := *a.reg
	// The built-in library is a host path mounted at the same path here, so
	// it needs no mirror (suite/library.go).
	reg.Loaders = suite.Layered(filepath.Join(root, "benchmarks"), suite.LibraryBenchmarks(suite.LibraryDir()))
	reg.Adapters = adapters.Builtin(adapters.CommandConfig{
		GatewayAgentURL: a.cfg.Gateway.AgentURL, EvalNetwork: a.cfg.Networks.Eval, BenchRoot: root,
		GatewayPublicURL: a.cfg.Gateway.PublicURL,
	})
	return &reg, root
}

// withLibrary is reg with its suite loaders over benchRoot layered on the
// server's built-in benchmark library (suite/library.go); reg itself when
// HEIMDALL_LIBRARY_DIR is unset.
func withLibrary(reg *schemas.Registry, benchRoot string) *schemas.Registry {
	lib := suite.LibraryDir()
	if lib == "" {
		return reg
	}
	out := *reg
	out.Loaders = suite.Layered(benchRoot, suite.LibraryBenchmarks(lib))
	return &out
}

// sharedMirrorEnv names the folder (bind-mounted at the same path on the
// host) where the shared runner mirrors each org's project.
const sharedMirrorEnv = "HEIMDALL_SHARED_PROJECTS_MIRROR"

// mirrorTree copies src into dst: new and changed files (size or mtime),
// folders, modes. Links are skipped, .git and .heimdall left out, and files
// gone from src removed from dst.
func mirrorTree(src, dst string) error {
	seen := map[string]bool{}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		skip, dir := mirrorSkip(rel, d.IsDir())
		if !skip && rel == ".heimdall" { // only enter it for agent version snapshots
			if fi, err := os.Stat(filepath.Join(path, "agent-versions")); err != nil || !fi.IsDir() {
				skip, dir = true, true
			}
		}
		if skip {
			if dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen[rel] = true
		out := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return nil
		case d.IsDir():
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			return os.Chmod(out, 0o755) // the runner's umask must not narrow it

		case !d.Type().IsRegular():
			return nil
		}
		si, err := d.Info()
		if err != nil {
			return err
		}
		if di, err := os.Stat(out); err == nil && di.Size() == si.Size() && di.ModTime().Equal(si.ModTime()) {
			if di.Mode().Perm()&0o444 != 0o444 { // a mirror written before the mode fix
				return os.Chmod(out, di.Mode().Perm()|0o444)
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tmp := out + ".mirror-tmp"
		// Readable by everyone (task containers run as other users), and
		// executable when the source is: explicit, so the umask cannot
		// narrow it to owner-only (which made /srv/server.py unreadable).
		mode := si.Mode().Perm() | 0o444
		if si.Mode().Perm()&0o111 != 0 {
			mode |= 0o111
		}
		if err := os.WriteFile(tmp, b, mode); err != nil {
			return err
		}
		if err := os.Chmod(tmp, mode); err != nil {
			return err
		}
		if err := os.Chtimes(tmp, si.ModTime(), si.ModTime()); err != nil {
			return err
		}
		return os.Rename(tmp, out)
	})
	if err != nil {
		return err
	}
	var stale []string
	_ = filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dst, path)
		if rel != "." && !seen[rel] {
			stale = append(stale, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	for _, p := range stale {
		_ = os.RemoveAll(p)
	}
	return nil
}

// mirrorSkip reports whether mirrorTree leaves rel out (dir: skip the whole
// folder). .git and .heimdall are left out, except the frozen agent version
// snapshots under .heimdall/agent-versions, which a pinned arm
// (agents/<id>.yaml@v<N>) reads when the runner re-plans (A06).
func mirrorSkip(rel string, isDir bool) (skip, dir bool) {
	r := filepath.ToSlash(rel)
	if r == "." {
		return false, false
	}
	keep := filepath.ToSlash(schemas.AgentVersionsDir)
	switch {
	case r == ".heimdall" && isDir:
		return false, false
	case r == keep || strings.HasPrefix(r, keep+"/"):
		return false, false
	case strings.HasPrefix(r, ".heimdall/"):
		return true, isDir
	}
	base := filepath.Base(rel)
	if isDir && (base == ".git" || base == ".heimdall") {
		return true, true
	}
	return false, false
}

// sharedEnsureImages builds the suite's missing images (base, services)
// before planning a leased experiment, as `suite admit` does locally: an org
// that added a suite on the website has no other way to build them, and a
// missing image would otherwise fail every run at prepare as a pull error.
func sharedEnsureImages(a *app, reg *schemas.Registry, root string, plan engine.Replanner) engine.Replanner {
	var mu sync.Mutex
	built := map[string]bool{}
	return func(ctx context.Context, e *schemas.Experiment) (*schemas.Manifest, error) {
		if e != nil && e.Suite != "" {
			mu.Lock()
			done := built[e.Suite]
			mu.Unlock()
			if !done {
				if s, err := engine.LoadSuite(ctx, reg, e.Suite); err == nil {
					cfg := *a.cfg
					cfg.Root = root
					if err := ops.EnsureImages(ctx, &cfg, a.docker, s, io.Discard); err != nil {
						a.logger.Warn("shared runner: build suite images", "suite", e.Suite, "error", err.Error())
					} else {
						mu.Lock()
						built[e.Suite] = true
						mu.Unlock()
					}
				}
			}
		}
		return plan(ctx, e)
	}
}
