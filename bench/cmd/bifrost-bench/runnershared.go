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
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
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
	token  string
	slots  int
	cancel context.CancelFunc
	done   chan struct{}
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
	start func(org, token string, slots int) *sharedLoop) {
	want := map[string]sharedpool.Assignment{}
	if !as.KillSwitch {
		for _, a := range as.Assignments {
			want[a.OrgID] = a
		}
	}
	for org, l := range loops {
		a, ok := want[org]
		if ok && a.Token == l.token && sharedSlots(a.MaxConcurrentRuns, flagSlots) == l.slots {
			continue
		}
		l.cancel()
		<-l.done
		delete(loops, org)
	}
	for org, a := range want {
		if _, ok := loops[org]; !ok {
			loops[org] = start(org, a.Token, sharedSlots(a.MaxConcurrentRuns, flagSlots))
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
	start := func(org, token string, slots int) *sharedLoop {
		lctx, cancel := context.WithCancel(ctx)
		l := &sharedLoop{token: token, slots: slots, cancel: cancel, done: make(chan struct{})}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(l.done)
			name := prefix + "-" + org
			if len(name) > 128 {
				name = name[:128]
			}
			w, err := engine.NewWorker(a.reg, engine.NewPipeline(a.reg, a.cfg.Root, a.logger),
				engine.NewPlanner(a.reg, a.cfg.Root, a.logger, engine.WithIgnoreQuarantine(), engine.WithoutGPUCheck()).Plan,
				engine.WorkerOptions{Server: f.server, Token: token, WorkerID: name, Host: host, Version: benchVersion,
					Slots: slots, Logger: a.logger, Pool: sharedpool.Pool, Labels: []string{"shared", "runtime=" + f.runtime}})
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
