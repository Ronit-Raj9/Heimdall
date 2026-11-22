package main

// From issues on a worker, runner side (lane W05,
// audit/ISSUEWORKER_CONTRACT.md). A job of kind issuebench.work builds and
// tests one issue in process: a subprocess could not carry the shared
// worker's per-org Docker policy, and the lease secrets (the model key, an
// optional GitHub token) stay in this process's memory. Every log line and
// the result are redacted with the lease's secret values before they leave.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/builder"
	"github.com/maximhq/bifrost/bench/issuebench/work"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/secrets"
	"github.com/maximhq/bifrost/bench/utils"
)

// sharedIssuebenchConcurrencyEnv sets how many issuebench.work jobs one
// shared worker runs at a time, over every org it serves (default 1).
const sharedIssuebenchConcurrencyEnv = "HEIMDALL_SHARED_ISSUEBENCH_CONCURRENCY"

// The build slots: one for an org's own worker, one pool for the whole
// shared worker process (every org loop shares the host's Docker).
var (
	ownIssuebenchSem    = make(chan struct{}, 1)
	sharedIssuebenchSem chan struct{}
	sharedIssuebenchMu  sync.Mutex
)

func sharedIssuebenchSlots() chan struct{} {
	sharedIssuebenchMu.Lock()
	defer sharedIssuebenchMu.Unlock()
	if sharedIssuebenchSem == nil {
		n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(sharedIssuebenchConcurrencyEnv)))
		if err != nil || n < 1 {
			n = 1
		}
		sharedIssuebenchSem = make(chan struct{}, n)
	}
	return sharedIssuebenchSem
}

// ibWorkExecutor runs issuebench.work job leases.
type ibWorkExecutor struct {
	org      string // shared worker: the org this loop serves; "" on an own worker
	egress   ibEgressOptions
	price    func(string, schemas.Tokens) (float64, bool)
	publicGW bool
	sem      chan struct{}
	newDeps  func(dir string) work.Deps                                                                                 // tests
	runWork  func(context.Context, work.Deps, issuebench.WorkRequest, map[string]string) (issuebench.WorkResult, error) // tests; nil is work.Run
}

// newOwnIssuebenchExecutor is the executor of an org's own worker: plain
// Docker, the internet on the default bridge, the gateway through
// BENCH_GATEWAY_PUBLIC_URL, prices from the runner's own snapshot.
func newOwnIssuebenchExecutor(cfg *lib.Config) *ibWorkExecutor {
	host, _ := os.Hostname()
	x := &ibWorkExecutor{
		egress:   ibEgressOptions{Docker: utils.Docker{}, Public: "bridge", Image: egress.DefaultImage, Runner: "own-" + host},
		publicGW: true,
		sem:      ownIssuebenchSem,
	}
	if cfg != nil {
		if prices, err := loadPrices(cfg.Path(cfg.Pricing), schemas.NoopLogger{}); err == nil && prices != nil {
			x.price = graders.PricesFrom(prices.Lookup)
		}
	}
	return x
}

// newSharedIssuebenchExecutor is the executor of one org loop of the shared
// worker: every docker call goes through the org's policy (runsc, image
// namespace), the egress proxy reaches the internet on the build network,
// and the gateway is the one the shared worker reaches on upstream.
func newSharedIssuebenchExecutor(h *sharedHost, oa *app, org string, docker utils.Docker) *ibWorkExecutor {
	public := os.Getenv(buildNetworkEnv)
	if public == "" {
		public = defaultBuildNetwork
	}
	x := &ibWorkExecutor{
		org:    org,
		egress: ibEgressOptions{Docker: docker, Public: public, Image: egress.DefaultImage, Org: org},
		sem:    sharedIssuebenchSlots(),
	}
	if h != nil {
		x.egress.Runtime, x.egress.Runner = h.runtime, h.prefix
	}
	if oa != nil && oa.prices != nil {
		x.price = graders.PricesFrom(oa.prices.Lookup)
	}
	return x
}

// deps are the work core's dependencies for one job in dir.
func (x *ibWorkExecutor) deps(dir string, req issuebench.WorkRequest) work.Deps {
	if x.newDeps != nil {
		return x.newDeps(dir)
	}
	eg := x.egress
	if eg.Org == "" {
		eg.Org = req.OrgID
	}
	own := x.org == ""
	return work.Deps{
		Docker:   issuebench.ExecDocker(eg.Docker.Command),
		Checkout: work.Checkouter{Git: work.ExecGit}.Checkout,
		Sandbox: func(ctx context.Context, r issuebench.WorkRequest) (builder.SandboxFactory, func(), error) {
			if own { // the shared worker ensures the proxy image once at start
				if err := ensureIssuebenchEgressImage(ctx, utils.Docker{}); err != nil {
					return nil, nil, fmt.Errorf("the build sandbox's egress proxy image is not available: %w", err)
				}
			}
			return ibEgressSandbox(ctx, eg, r)
		},
		Price:            x.price,
		Scratch:          dir,
		Runtime:          eg.Runtime,
		UsePublicGateway: x.publicGW,
	}
}

// RunJob implements engine.JobExecutor for issuebench.work.
func (x *ibWorkExecutor) RunJob(ctx context.Context, job *schemas.JobLease, log func(string)) (*engine.JobRun, error) {
	if job == nil || job.JobKind != issuebench.WorkJobKind || !slices.Equal(job.Argv, issuebench.WorkArgv()) {
		return nil, errors.New("this runner refuses the job: it is not an issuebench.work job with the issuebench work command")
	}
	sec := job.Secrets
	redact := func(s string) string { return secrets.Redact(issuebench.RedactValues(s, sec)) }
	logf := func(format string, args ...any) {
		if log == nil {
			return
		}
		for _, line := range strings.Split(strings.TrimRight(fmt.Sprintf(format, args...), "\n"), "\n") {
			log(redact(line))
		}
	}
	dir, err := os.MkdirTemp("", "heimdall-issuebench-*")
	if err != nil {
		return nil, err
	}
	run := &engine.JobRun{Dir: dir, Cleanup: func() { os.RemoveAll(dir) }}
	if err := unpackJobFiles(job.Files, dir); err != nil {
		return run, errors.New(redact("unpack the job files: " + err.Error()))
	}
	req, err := readWorkRequest(filepath.Join(dir, filepath.FromSlash(issuebench.WorkRequestFile)))
	if err != nil {
		return run, errors.New(redact(err.Error()))
	}
	if x.org != "" && req.OrgID != x.org {
		return run, errors.New("this job belongs to another organization")
	}
	if x.sem != nil {
		select {
		case x.sem <- struct{}{}:
		default:
			logf("waiting for a free build slot on this worker")
			select {
			case x.sem <- struct{}{}:
			case <-ctx.Done():
				return run, ctx.Err()
			}
		}
		defer func() { <-x.sem }()
	}
	deps := x.deps(dir, req)
	deps.Log = logf
	runWork := x.runWork
	if runWork == nil {
		runWork = work.Run
	}
	res, runErr := runWork(ctx, deps, req, sec)
	raw, err := json.Marshal(res)
	switch {
	case err != nil:
		return run, fmt.Errorf("encode the work result: %w", err)
	case len(raw) > issuebench.MaxWorkResultBytes:
		return run, fmt.Errorf("the work result is %d bytes, over the %d byte cap", len(raw), issuebench.MaxWorkResultBytes)
	}
	run.Result = json.RawMessage(redact(string(raw)))
	if runErr != nil {
		return run, errors.New(redact(runErr.Error()))
	}
	return run, nil
}

// readWorkRequest reads and checks issuebench-job/request.json.
func readWorkRequest(path string) (issuebench.WorkRequest, error) {
	var req issuebench.WorkRequest
	f, err := os.Open(path)
	if err != nil {
		return req, fmt.Errorf("the job has no %s: %w", issuebench.WorkRequestFile, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, issuebench.MaxWorkRequestBytes+1))
	if err != nil {
		return req, fmt.Errorf("read %s: %w", issuebench.WorkRequestFile, err)
	}
	if len(data) > issuebench.MaxWorkRequestBytes {
		return req, fmt.Errorf("%s is over the %d byte cap", issuebench.WorkRequestFile, issuebench.MaxWorkRequestBytes)
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return req, fmt.Errorf("%s is not a work request: %w", issuebench.WorkRequestFile, err)
	}
	if err := req.Check(); err != nil {
		return req, fmt.Errorf("the work request is not valid: %w", err)
	}
	return req, nil
}
