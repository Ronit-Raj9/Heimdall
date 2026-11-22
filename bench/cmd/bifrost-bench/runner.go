package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/registrycreds"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Runners area (docs/runners.md): `runner start` runs this machine as a
// runner of a coordinator (a hosted organization with a runner token, or a
// local serve with BENCH_WORKER_TOKEN); `runner status` checks the server
// and the token. `worker` is the former name of `runner start`.

func init() {
	register("runner", "run this machine as a runner (runner start) or check its server and token (runner status)", runRunner)
}

const runnerUsage = `usage:
  bifrost-bench runner start -server <url> -token-file <path> [-pool name] [-slots n] [-name name] [-labels a,b] [-spend-cap-usd x] [-config deploy/bench.yaml] [-debug]
  bifrost-bench runner start -shared -server <url> -secret-file <path> [-runtime runc|runsc] [-slots n]
  bifrost-bench runner status -server <url> -token-file <path> [-name name]`

func runRunner(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, runnerUsage)
		return exitUsage
	}
	switch args[0] {
	case "start":
		return runRunnerStart("runner start", args[1:])
	case "status":
		return runRunnerStatus(args[1:], os.Stdout)
	case "build-session": // internal: the shared runner runs org image builds through it (buildsession.go)
		return runBuildSession(args[1:])
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stdout, runnerUsage)
		return exitOK
	}
	fmt.Fprintln(os.Stderr, runnerUsage)
	return exitUsage
}

// runnerFlags are the flags of `runner start` and its alias `worker`.
type runnerFlags struct {
	server, tokenFile, name, pool, labels, config string
	slots                                         int
	spendCap                                      float64
	debug                                         bool
}

func parseRunnerFlags(cmd string, args []string) (*runnerFlags, error) {
	f := &runnerFlags{}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&f.server, "server", "", "coordinator URL: the hosted server (https://...) or a local serve, e.g. http://127.0.0.1:8090 through an SSH tunnel")
	fs.StringVar(&f.tokenFile, "token-file", "", "file holding the runner token (hmr_..., hosted) or the coordinator's BENCH_WORKER_TOKEN (local); mode 0600")
	fs.IntVar(&f.slots, "slots", 1, "slots to run at once")
	fs.StringVar(&f.name, "name", "", "runner name, unique in the organization (default: a random one)")
	fs.StringVar(&f.name, "id", "", "same as -name")
	fs.StringVar(&f.pool, "pool", "", "pool to serve (hosted: must be the token's pool; empty takes it)")
	fs.StringVar(&f.labels, "labels", "", "comma-separated labels shown in the runners list, e.g. gpu,region=eu")
	fs.Float64Var(&f.spendCap, "spend-cap-usd", 0, "stop leasing once this runner spent this much in a UTC day (0: no cap)")
	fs.StringVar(&f.config, "config", defaultConfig, "bench config")
	fs.BoolVar(&f.debug, "debug", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	switch {
	case fs.NArg() != 0:
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case f.server == "" || f.tokenFile == "":
		return nil, errors.New("-server and -token-file are required")
	case f.slots < 1 || f.slots > 64:
		return nil, errors.New("-slots must be 1 to 64")
	case f.spendCap < 0 || math.IsNaN(f.spendCap) || math.IsInf(f.spendCap, 0): // QC2-07-11: a non-finite cap never trips
		return nil, errors.New("-spend-cap-usd must be a finite number, not negative")
	case f.pool != "" && !schemas.ValidPoolName(f.pool):
		return nil, fmt.Errorf("-pool %q: 1 to 64 letters, digits, '.', '_' or '-'", f.pool)
	}
	return f, nil
}

func (f *runnerFlags) labelList() []string {
	var out []string
	for _, l := range strings.Split(f.labels, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// runRunnerStart runs this machine as a runner. It needs the same project at
// the same commit as the coordinator and its own local stack (gateway, fault
// proxy, docker): every leased experiment is planned again here and refused
// when its manifest does not come out the same.
func runRunnerStart(cmd string, args []string) int {
	// Lane Z14: OTLP spans when OTEL_* names an endpoint (telemetry.go).
	startTelemetry("heimdall-runner")
	if hasSharedFlag(args) { // shared runner pool (runnershared.go)
		return runSharedRunner(args)
	}
	f, err := parseRunnerFlags(cmd, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n%s\n", cmd, err, runnerUsage)
		return exitUsage
	}
	token, err := readTokenFile(f.tokenFile)
	if err != nil {
		return fail("%v", err)
	}
	// An org's own worker runs inside the org's network: a self-hosted
	// sandbox platform there may sit at a private or plain http address. The
	// public-only guard (G3 B6) protects the hosted server and the shared
	// worker, which never take this path.
	sandbox.AllowPrivateEndpoints = true
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, f.config, appOptions{gateway: true, debug: f.debug})
	if err != nil {
		return fail("%v", err)
	}
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	a.reg = withLibrary(a.reg, a.cfg.Path(a.cfg.BenchmarksRoot)) // built-in library suites (suite/library.go), e.g. the demo runner
	hy := newRunnerHygiene(a)                                    // E6: janitor, image GC, build cache prune, free disk floor (runnerhygiene.go)
	go hy.Run(ctx)
	startPublicIngress(ctx, a) // Y03: an org's own worker may publish /gw and /rt behind its own proxy (runneringress.go)
	w, err := engine.NewWorker(a.reg, engine.NewPipeline(a.reg, a.cfg.Root, a.logger),
		engine.NewPlanner(a.reg, a.cfg.Root, a.logger, engine.WithIgnoreQuarantine(), engine.WithoutGPUCheck(), engine.WithAgentLibrary(suite.LibraryDir())).Plan, // Suite health area: a replan reproduces a frozen manifest (engine/taskscreen.go); X7: no GPU check, the coordinator leases GPU slots by the GPUs a runner registers
		engine.WorkerOptions{Server: f.server, Token: token, WorkerID: f.name, Host: host, Version: benchVersion,
			Slots: f.slots, Logger: a.logger, Pool: f.pool, Labels: f.labelList(),
			SpendCapUSD: f.spendCap, SpendFile: filepath.Join(a.store.Root(), ".runner", "spend.json"),
			Jobs: newRunnerJobExecutor(a), // gap G05: hosted CLI jobs (runnerjob.go)
			Disk: hy.DiskState, ImageUse: hy.NoteImages, KeepRunFolders: a.cfg.Runner.Disk.KeepRunFolders,
			PullPrivate: func(ctx context.Context, ref string, auths []schemas.RegistryAuth) error { // Y10: the org's registry logins
				return registrycreds.Pull(ctx, a.docker, ref, auths)
			},
			Prefetch: runnerPrefetch(a.logger, utils.Docker{})}) // Z13: images pinned by registry digest are pulled before their slots (images.go)
	if err != nil {
		return fail("%v", err)
	}
	msg := fmt.Sprintf("runner %s: %d slots, runs sealed under %s, coordinator %s", w.ID(), f.slots, a.store.Root(), f.server)
	if f.pool != "" {
		msg += ", pool " + f.pool
	}
	if f.spendCap > 0 {
		msg += fmt.Sprintf(", spend cap %.2f USD per UTC day", f.spendCap)
	}
	a.logger.Info(msg)
	if err := w.Run(ctx); err != nil {
		return fail("%s: %v", cmd, err)
	}
	return exitOK
}

// runRunnerStatus checks that the server answers and accepts the token, and
// prints what the token is scoped to and the runners registered with it.
// Exit 0 when the token is accepted, even before this runner registered.
func runRunnerStatus(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("runner status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server := fs.String("server", "", "coordinator URL")
	tokenFile := fs.String("token-file", "", "file holding the runner token")
	name := fs.String("name", "", "this runner's name, to show its state")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *server == "" || *tokenFile == "" {
		fmt.Fprintln(os.Stderr, runnerUsage)
		return exitUsage
	}
	token, err := readTokenFile(*tokenFile)
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	who, err := engine.FetchWhoami(ctx, *server, token, nil)
	switch {
	case errors.Is(err, schemas.ErrWorkerAuth):
		return fail("server %s refused the token: unknown, revoked or expired (%v)", *server, err)
	case errors.Is(err, schemas.ErrWorkerScope):
		return fail("server %s: the token belongs to another organization (%v)", *server, err)
	case err != nil:
		return fail("server %s: %v", *server, err)
	}
	printRunnerStatus(out, *server, *name, who)
	return exitOK
}

func printRunnerStatus(out io.Writer, server, name string, who *schemas.RunnerWhoami) {
	fmt.Fprintf(out, "server      %s (reachable, token accepted)\n", server)
	fmt.Fprintf(out, "mode        %s\n", who.Mode)
	if who.Mode == "hosted" {
		fmt.Fprintf(out, "org         %s\npool        %s\ntoken       %s\n", who.OrgID, who.Pool, who.TokenID)
	}
	fmt.Fprintf(out, "open        %d experiment(s) this token may lease from now\n", who.Open)
	found := false
	for _, w := range who.Workers {
		if name != "" && w.ID != name {
			continue
		}
		found = true
		ago := who.Now.Sub(w.LastSeen).Round(time.Second)
		fmt.Fprintf(out, "runner      %s: %s, %d/%d slots, pool %s, version %s, last seen %s ago, %d accepted\n",
			w.ID, w.State, len(w.Running), w.Slots, orDash(w.Pool), orDash(w.Version), ago, w.Completed)
	}
	if !found {
		if name != "" {
			fmt.Fprintf(out, "runner      %s: not registered with this token (start it with `bifrost-bench runner start`)\n", name)
		} else {
			fmt.Fprintln(out, "runner      none registered with this token yet")
		}
	}
}
