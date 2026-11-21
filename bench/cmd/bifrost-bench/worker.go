package main

import (
	"errors"
	"fmt"
	"github.com/maximhq/bifrost/bench/selfmetrics"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// workerTokenEnv names the coordinator's worker token. Unset, the worker API
// is off.
const workerTokenEnv = "BENCH_WORKER_TOKEN"

var _ handlers.WorkerAPI = (*engine.Coordinator)(nil)

func init() {
	register("worker", "alias of `runner start` (same flags): lease slots from a coordinator and run them here", runWorker)
}

// runWorker is the former name of `runner start`, kept as an alias with the
// same flags (runner.go).
func runWorker(args []string) int { return runRunnerStart("worker", args) }

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
	registerCoordinatorMetrics(selfmetrics.Default, func() []*engine.Coordinator { return []*engine.Coordinator{c} }) // N9 (servemetrics.go)
	base.coord = c                                                                                                    // Jobs area: the experiment.run job runs with the coordinator attached (servejobs.go)
	return &workerLauncher{launcher: base, coord: c}, c
}

// workerLauncher is the serve launcher with every run opened to remote
// workers. It is the base launcher (Start, Active, Events, Cancel, Resume
// are the base's): the base's experiment.run job attaches coord to the
// scheduler (Jobs area, servejobs.go).
type workerLauncher struct {
	*launcher
	coord *engine.Coordinator
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
