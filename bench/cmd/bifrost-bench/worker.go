package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// workerTokenEnv names the coordinator's worker token. Unset, the worker API
// is off.
const workerTokenEnv = "BENCH_WORKER_TOKEN"

var _ handlers.WorkerAPI = (*engine.Coordinator)(nil)

func init() {
	register("worker", "lease slots from a coordinator (serve with BENCH_WORKER_TOKEN) and run them here", runWorker)
}

// runWorker runs this machine as a remote worker. It needs the same project
// at the same commit as the coordinator and its own local stack (gateway,
// fault proxy, docker): every leased experiment is planned again here and
// refused when its manifest does not come out the same.
func runWorker(args []string) int {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	server := fs.String("server", "", "coordinator URL, e.g. http://127.0.0.1:8090 (through an SSH tunnel)")
	tokenFile := fs.String("token-file", "", "file holding the worker token (the coordinator's BENCH_WORKER_TOKEN)")
	slots := fs.Int("slots", 1, "slots to run at once")
	id := fs.String("id", "", "worker id (default: a random one)")
	config := fs.String("config", defaultConfig, "bench config")
	debug := fs.Bool("debug", false, "debug logging")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *server == "" || *tokenFile == "" {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench worker -server <url> -token-file <path> [-slots n] [-id name] [-config deploy/bench.yaml]")
		return exitUsage
	}
	token, err := readTokenFile(*tokenFile)
	if err != nil {
		return fail("%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{gateway: true, debug: *debug})
	if err != nil {
		return fail("%v", err)
	}
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	w, err := engine.NewWorker(a.reg, engine.NewPipeline(a.reg, a.cfg.Root, a.logger),
		engine.NewPlanner(a.reg, a.cfg.Root, a.logger, engine.WithIgnoreQuarantine(), engine.WithoutGPUCheck()).Plan, // Suite health area: a replan reproduces a frozen manifest (engine/taskscreen.go); X7: no GPU check, the coordinator leases GPU slots by the GPUs a worker registers
		engine.WorkerOptions{Server: *server, Token: token, WorkerID: *id, Host: host, Version: benchVersion,
			Slots: *slots, Logger: a.logger})
	if err != nil {
		return fail("%v", err)
	}
	a.logger.Info(fmt.Sprintf("worker %s: %d slots, runs sealed under %s, coordinator %s", w.ID(), *slots, a.store.Root(), *server))
	if err := w.Run(ctx); err != nil {
		return fail("worker: %v", err)
	}
	return exitOK
}

// readTokenFile reads the worker token. A file others can read is refused,
// since the token is the worker API's only authentication.
func readTokenFile(p string) (string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s is readable by others (mode %o); chmod 600 it", p, info.Mode().Perm())
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", errors.New("token file is empty")
	}
	return tok, nil
}

// withWorkers turns worker mode on for serve when BENCH_WORKER_TOKEN is set
// and the server can launch experiments: every experiment the launcher runs
// is then opened to remote workers. It returns the launcher to serve (the
// same one when worker mode is off) and the worker API, nil when off.
func withWorkers(a *app, l handlers.Launcher) (handlers.Launcher, handlers.WorkerAPI) {
	token := os.Getenv(workerTokenEnv)
	if token == "" {
		return l, nil
	}
	base, ok := l.(*launcher)
	if !ok || base == nil {
		a.logger.Warn("worker mode needs a server that launches experiments; " + workerTokenEnv + " is ignored")
		return l, nil
	}
	c, err := engine.NewCoordinator(a.reg, engine.RemoteOptions{Token: token, Logger: a.logger})
	if err != nil {
		a.logger.Error("worker mode off: " + err.Error())
		return l, nil
	}
	a.logger.Info("worker mode on: remote workers may lease slots of launched experiments (docs/remote-execution.md)")
	return &workerLauncher{launcher: base, coord: c}, c
}

// workerLauncher is the serve launcher with every run opened to remote
// workers. It shares the base launcher's live runs, so Active and Events
// are the base's.
type workerLauncher struct {
	*launcher
	coord *engine.Coordinator
}

// Start runs the experiment like the base launcher, with the coordinator
// attached.
func (l *workerLauncher) Start(ctx context.Context, expID string) error {
	m, err := l.a.store.LoadManifest(ctx, expID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	if r, ok := l.active[expID]; ok && !r.done {
		l.mu.Unlock()
		return fmt.Errorf("experiment %s is already running", expID)
	}
	run := &liveRun{subs: map[chan schemas.LiveEvent]bool{}}
	l.active[expID] = run
	l.mu.Unlock()
	go l.execute(m, run)
	return nil
}

// execute is the base launcher's execute with RunOptions.Remote set.
func (l *workerLauncher) execute(m *schemas.Manifest, run *liveRun) {
	ctx := context.Background()
	events := make(chan engine.Event, 256)
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for ev := range events {
			run.publish(schemas.LiveEvent{Kind: ev.Kind, SlotID: ev.SlotID, RunID: ev.RunID, Stage: ev.Stage, Outcome: ev.Outcome, Message: ev.Message, At: ev.At})
			if ev.Kind == engine.EventRunSealed {
				l.rebuild(ctx)
			}
		}
	}()
	janitor := engine.NewJanitor(l.a.reg.Gateway, l.a.docker, l.a.logger)
	sched := engine.NewScheduler(l.a.reg, l.a.cfg.Root, l.a.logger, janitor)
	_, err := sched.Run(ctx, m, engine.RunOptions{Resume: true, Events: events, Remote: l.coord})
	close(events)
	<-forwarded
	if err != nil {
		run.publish(schemas.LiveEvent{Kind: "error", Message: err.Error(), At: time.Now()})
	}
	if an, _, aerr := l.a.analyze(ctx, m.ExpID, ""); aerr != nil {
		run.publish(schemas.LiveEvent{Kind: "error", Message: "analyze: " + aerr.Error(), At: time.Now()})
	} else {
		run.publish(schemas.LiveEvent{Kind: "experiment_done", Message: string(an.Verdict.Decision), At: time.Now()})
	}
	l.rebuild(ctx)
	run.finish()
}

func (l *workerLauncher) rebuild(ctx context.Context) {
	if err := l.ix.Rebuild(ctx); err != nil {
		l.a.logger.Warn("index rebuild failed", "error", err.Error())
	}
}

// minWorkerToken is the shortest worker token doctor accepts without a
// warning.
const minWorkerToken = 24

// workerModeCheck is doctor's worker mode row. It never shows the token.
func workerModeCheck(loopback bool) schemas.DoctorCheck {
	row := schemas.DoctorCheck{ID: "worker mode"}
	tok := os.Getenv(workerTokenEnv)
	switch {
	case tok == "":
		row.Status, row.Detail = schemas.DoctorSkip, "off: "+workerTokenEnv+" is not set"
	case len(tok) < minWorkerToken:
		row.Status = schemas.DoctorWarn
		row.Detail = fmt.Sprintf("on, but %s is shorter than %d characters; it is the worker API's only authentication", workerTokenEnv, minWorkerToken)
	case loopback:
		row.Status, row.Detail = schemas.DoctorPass, "on; serve listens on loopback, so workers reach it through an SSH tunnel"
	default:
		row.Status = schemas.DoctorWarn
		row.Detail = "on, and serve listens beyond loopback with no TLS: the token crosses the network in clear text unless a tunnel or proxy wraps it"
	}
	return row
}
