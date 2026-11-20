package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Worker defaults.
const (
	DefaultWorkerPoll = 5 * time.Second
	workerCallTimeout = 2 * time.Minute
	uploadAttempts    = 3
	workerAPI         = "/api/bench/workers"
)

// SlotRunner runs one attempt of a slot to its seal. *Pipeline is one.
type SlotRunner interface {
	RunSlot(ctx context.Context, in SlotInput) (*SlotResult, error)
}

// Replanner plans an experiment from the worker's own project: its agent
// configs, suites, policies and registered parts. NewPlanner(...).Plan is
// one.
type Replanner func(ctx context.Context, e *schemas.Experiment) (*schemas.Manifest, error)

// WorkerOptions configures a remote Worker.
type WorkerOptions struct {
	Server     string // coordinator base URL, e.g. http://127.0.0.1:8090
	Token      string // the shared worker token; sent only in the Authorization header, never logged
	WorkerID   string // default: host name plus a random suffix
	Host       string
	Version    string
	Slots      int           // concurrent slots; default 1
	PollEvery  time.Duration // wait between lease requests that found no work
	ScratchDir string        // archives are built here; default the system temp folder
	HTTP       *http.Client
	Logger     schemas.Logger
	// Runners area (docs/runners.md): the pool the runner asks for (hosted:
	// its token's pool when empty), free-form labels, and the runner's own
	// spend cap per UTC day (0 = none), kept in SpendFile across restarts.
	Pool        string
	Labels      []string
	SpendCapUSD float64
	SpendFile   string
}

// Worker is the remote side of remote execution: it registers with a
// coordinator, leases slots, refuses any experiment its own project does not
// reproduce, runs each slot with the normal pipeline into its own store, and
// uploads the sealed run folder.
type Worker struct {
	reg    *schemas.Registry
	runner SlotRunner
	replan Replanner
	opts   WorkerOptions
	logger schemas.Logger

	mu         sync.Mutex
	registered *schemas.WorkerRegistered
	exps       map[string]*workerExp
	spend      *runnerSpend // Runners area: nil without a spend cap
	capLogged  atomic.Bool
}

// workerExp is an experiment the worker checked: admitted with its manifest
// and suite, or refused for good with the reason.
type workerExp struct {
	m       *schemas.Manifest
	suite   *schemas.Suite
	refused string
}

// NewWorker returns a worker. reg must hold the worker's own store, where its
// runs are sealed before upload.
func NewWorker(reg *schemas.Registry, runner SlotRunner, replan Replanner, opts WorkerOptions) (*Worker, error) {
	if reg == nil || reg.Store == nil {
		return nil, errors.New("worker: no store registered")
	}
	if runner == nil || replan == nil {
		return nil, errors.New("worker: a slot runner and a replanner are required")
	}
	u, err := url.Parse(opts.Server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("worker: server %q is not an http(s) URL", opts.Server)
	}
	opts.Server = strings.TrimRight(opts.Server, "/")
	if strings.TrimSpace(opts.Token) == "" {
		return nil, errors.New("worker: empty token")
	}
	if opts.Host == "" {
		if h, err := os.Hostname(); err == nil {
			opts.Host = h
		}
	}
	if opts.WorkerID == "" {
		opts.WorkerID = utils.NewID("w", 8)
	}
	if !validWorkerID(opts.WorkerID) {
		return nil, fmt.Errorf("worker: id %q: want 1 to 64 letters, digits, '-', '_' or '.'", opts.WorkerID)
	}
	if opts.Slots <= 0 {
		opts.Slots = 1
	}
	if opts.PollEvery <= 0 {
		opts.PollEvery = DefaultWorkerPoll
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: workerCallTimeout}
	}
	if opts.Logger == nil {
		opts.Logger = schemas.NoopLogger{}
	}
	if err := validLabels(opts.Pool, opts.Labels); err != nil {
		return nil, fmt.Errorf("worker: %w", err)
	}
	if opts.SpendCapUSD < 0 {
		return nil, errors.New("worker: negative spend cap")
	}
	spend, err := newRunnerSpend(opts.SpendCapUSD, opts.SpendFile)
	if err != nil {
		return nil, fmt.Errorf("worker: %w", err)
	}
	return &Worker{reg: reg, runner: runner, replan: replan, opts: opts, logger: opts.Logger, exps: map[string]*workerExp{}, spend: spend}, nil
}

// ID is the worker id it registers under.
func (w *Worker) ID() string { return w.opts.WorkerID }

// Run registers and works Slots slots until ctx ends. A wrong token stops it
// at once; an unreachable coordinator is retried.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.register(ctx); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for range w.opts.Slots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx)
		}()
	}
	wg.Wait()
	return nil
}

// register registers, retrying while the coordinator cannot be reached.
func (w *Worker) register(ctx context.Context) error {
	req := schemas.WorkerRegister{WorkerID: w.opts.WorkerID, Host: w.opts.Host, Version: w.opts.Version,
		Parts: PartVersions(w.reg), Slots: w.opts.Slots, Pool: w.opts.Pool, Labels: w.opts.Labels}
	req.Capabilities = WorkerCapabilities(w.reg) // X7: the GPUs this worker offers; nil without a GPU pool (gpu.go)
	for {
		var out schemas.WorkerRegistered
		err := w.call(ctx, http.MethodPost, workerAPI+"/register", req, &out)
		if err == nil {
			w.mu.Lock()
			w.registered = &out
			w.mu.Unlock()
			w.logger.Info("registered with coordinator", "worker", w.opts.WorkerID, "server", w.opts.Server, "slots", w.opts.Slots)
			return nil
		}
		if errors.Is(err, schemas.ErrWorkerAuth) || errors.Is(err, schemas.ErrWorkerRequest) || errors.Is(err, schemas.ErrWorkerScope) {
			return fmt.Errorf("register with %s: %w", w.opts.Server, err)
		}
		w.logger.Warn("coordinator not reachable; retrying", "server", w.opts.Server, "error", err.Error())
		if !sleepCtx(ctx, w.opts.PollEvery) {
			return nil
		}
	}
}

// loop leases and works one slot at a time until ctx ends.
func (w *Worker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		if reached, spent := w.spend.reached(); reached { // Runners area: the runner's own spend cap
			if !w.capLogged.Swap(true) {
				w.logger.Warn("runner spend cap reached; no further leases until the next UTC day",
					"cap_usd", w.opts.SpendCapUSD, "spent_usd", spent)
			}
			sleepCtx(ctx, w.opts.PollEvery)
			continue
		}
		w.capLogged.Store(false)
		var lease schemas.WorkerLease
		err := w.call(ctx, http.MethodPost, workerAPI+"/lease", schemas.WorkerLeaseRequest{WorkerID: w.opts.WorkerID}, &lease)
		switch {
		case errors.Is(err, schemas.ErrWorkerUnknown):
			if rerr := w.register(ctx); rerr != nil {
				w.logger.Error("register again", "error", rerr.Error())
			}
			continue
		case err != nil:
			if ctx.Err() == nil {
				w.logger.Warn("lease request failed", "error", err.Error())
			}
		case lease.LeaseID != "":
			// An accepted run goes straight on to the next lease; anything
			// else (a refusal, a rejection, a failed run) waits a poll
			// interval, so a lease the worker keeps giving back is not
			// fetched in a tight loop.
			if res := w.Work(ctx, &lease); res != nil && res.Accepted {
				continue
			}
		}
		sleepCtx(ctx, w.opts.PollEvery)
	}
}

// Work runs one lease: it checks the experiment against the worker's own
// project and refuses it when anything differs, runs the slot with the
// pipeline under a heartbeat, and uploads the sealed run folder. A lease lost
// while the slot runs stops the run (sealed aborted_infra locally) and
// nothing is uploaded.
func (w *Worker) Work(ctx context.Context, lease *schemas.WorkerLease) *schemas.WorkerResult {
	// The lease is kept fresh from the moment it is granted: admission,
	// preflight and image checks can take longer than the stale threshold on
	// a loaded worker, and a lease that only starts beating at RunSlot is
	// lost to the coordinator's sweep before the slot even runs.
	sctx, stop := w.heartbeat(ctx, lease)
	defer stop()
	exp, reason, permanent := w.admit(ctx, lease)
	if reason != "" {
		w.logger.Warn("lease refused", "lease", lease.LeaseID, "experiment", lease.ExpID, "reason", reason)
		stop()
		w.release(ctx, lease, reason, permanent, nil)
		return nil
	}
	slot := exp.slot(lease.Slot.SlotID)
	if slot == nil || !reflect.DeepEqual(*slot, lease.Slot) {
		stop()
		w.release(ctx, lease, "the leased slot is not the manifest's slot "+lease.Slot.SlotID, true, nil)
		return nil
	}
	// Tenancy area: a hosted manifest defers the gateway preflight to this
	// runner; it runs before every slot and refuses the lease on a failure
	// (remotegateway.go).
	runM, runnerGW, why := w.runnerPreflight(ctx, exp.m)
	if why != "" {
		w.logger.Warn("lease refused", "lease", lease.LeaseID, "experiment", lease.ExpID, "reason", why)
		stop()
		w.release(ctx, lease, why, false, nil)
		return nil
	}
	// Deploy area: a deferred manifest froze no image ids; the lock's image
	// pins are checked again here and the ids this slot runs are recorded
	// (remoteimages.go).
	if why := w.checkSlotImages(ctx, exp); why != "" {
		w.logger.Warn("lease refused", "lease", lease.LeaseID, "experiment", lease.ExpID, "reason", why)
		stop()
		w.release(ctx, lease, why, true, nil)
		return nil
	}
	runnerImgs := w.runnerImages(ctx, exp, slot)
	res, err := w.runner.RunSlot(sctx, SlotInput{Manifest: runM, Suite: exp.suite, Slot: slot, Attempt: lease.Attempt})
	if res != nil {
		res.RunnerGateway = runnerGW  // Tenancy area: uploaded with the run
		res.RunnerImages = runnerImgs // Deploy area: uploaded with the run
	}
	if serr := w.spend.add(res, exp.m); serr != nil { // Runners area: the runner's own spend cap
		w.logger.Warn("record runner spend", "error", serr.Error())
	}
	if cause := context.Cause(sctx); errors.Is(cause, ErrLeaseLost) {
		w.logger.Error("lease lost while the slot ran; the run stays in the local store and is not uploaded",
			"lease", lease.LeaseID, "slot", slot.SlotID, "cause", cause.Error())
		return nil
	}
	if res == nil || res.Seal == nil {
		why := "the run was not sealed on the worker"
		if err != nil {
			why += ": " + err.Error()
		}
		var cost *schemas.Value
		if res != nil && res.Run != nil && res.Run.Timing.AgentStarted != nil {
			v := costValue(res)
			cost = &v
		}
		stop()
		w.release(ctx, lease, why, false, cost)
		return nil
	}
	out, err := w.uploadTo(sctx, ctx, lease, res)
	if err != nil {
		w.logger.Error("upload failed", "lease", lease.LeaseID, "run", res.Run.RunID, "error", err.Error())
		if !errors.Is(err, schemas.ErrLeaseGone) {
			stop()
			v := costValue(res)
			w.release(ctx, lease, "upload failed: "+err.Error(), false, &v)
		}
		return nil
	}
	if out.Accepted {
		w.logger.Info("run accepted", "lease", lease.LeaseID, "run", out.RunID, "outcome", string(res.Run.Outcome))
	} else {
		w.logger.Error("run rejected by the coordinator", "lease", lease.LeaseID, "run", out.RunID, "reason", out.Reason)
	}
	return out
}

// admit checks a leased experiment once and caches the answer. It refuses
// for good when the manifest does not hash to the lease's manifest hash, the
// local suite lock or part versions differ, or the manifest recomputed by
// planning the same experiment with the worker's own project differs (only
// what a second plan cannot repeat is taken from the coordinator:
// adoptUnrepeatable). A manifest that cannot be fetched is a refusal for
// this lease only.
func (w *Worker) admit(ctx context.Context, lease *schemas.WorkerLease) (*workerExp, string, bool) {
	w.mu.Lock()
	e, ok := w.exps[lease.ExpID]
	w.mu.Unlock()
	if ok {
		if e.refused != "" {
			return nil, e.refused, true
		}
		if e.m.Hash != lease.ManifestHash {
			return nil, fmt.Sprintf("lease manifest hash %s is not the admitted manifest's %s", lease.ManifestHash, e.m.Hash), true
		}
		return e, "", false
	}
	var m schemas.Manifest
	if err := w.call(ctx, http.MethodGet, workerAPI+"/manifests/"+url.PathEscape(lease.ExpID)+"?worker_id="+url.QueryEscape(w.opts.WorkerID), nil, &m); err != nil {
		return nil, "fetch manifest: " + err.Error(), false
	}
	e = &workerExp{m: &m}
	var reason string
	e.suite, reason = w.check(ctx, lease, &m)
	if reason != "" {
		e.refused = reason
	}
	w.mu.Lock()
	w.exps[lease.ExpID] = e
	w.mu.Unlock()
	if reason != "" {
		return nil, reason, true
	}
	return e, "", false
}

// check runs the admission checks of admit and creates the experiment in the
// worker's store.
func (w *Worker) check(ctx context.Context, lease *schemas.WorkerLease, m *schemas.Manifest) (*schemas.Suite, string) {
	h, err := ManifestHash(m)
	switch {
	case err != nil:
		return nil, err.Error()
	case h != m.Hash || h != lease.ManifestHash:
		return nil, fmt.Sprintf("manifest hashes to %s; it records %s and the lease %s", h, m.Hash, lease.ManifestHash)
	case m.SuiteLockHash != lease.SuiteLockHash:
		return nil, fmt.Sprintf("manifest suite lock hash %s is not the lease's %s", m.SuiteLockHash, lease.SuiteLockHash)
	case m.BenchVersion != BenchVersion:
		return nil, fmt.Sprintf("manifest bench version %s is not this worker's %s", m.BenchVersion, BenchVersion)
	}
	parts := PartVersions(w.reg)
	if m.Gateway.Deferred() { // Tenancy area: deferred manifest: skip only the gateway/* parts (the runner's, recorded per run); every other part must match (remotegateway.go)
		parts = withoutGatewayParts(parts)
	}
	if d := mapDiff(parts, m.Parts); d != "" {
		return nil, "part versions differ from the manifest's: " + d
	}
	suite, err := LoadSuite(ctx, w.reg, m.Experiment.Suite)
	if err != nil {
		return nil, "load suite: " + err.Error()
	}
	if suite.LockHash != m.SuiteLockHash {
		return nil, fmt.Sprintf("local suite lock hash %s is not the manifest's %s", suite.LockHash, m.SuiteLockHash)
	}
	exp := m.Experiment
	local, err := w.replan(ctx, &exp)
	if err != nil {
		return nil, "plan the experiment locally: " + err.Error()
	}
	adoptUnrepeatable(local, m)
	lh, err := ManifestHash(local)
	if err != nil {
		return nil, err.Error()
	}
	if lh != m.Hash {
		return nil, fmt.Sprintf("manifest recomputed locally hashes to %s, not %s; differs in %s", lh, m.Hash, manifestDiff(local, m))
	}
	if err := w.reg.Store.CreateExperiment(ctx, m); err != nil {
		if !errors.Is(err, schemas.ErrExists) {
			return nil, "create the experiment in the local store: " + err.Error()
		}
		stored, lerr := w.reg.Store.LoadManifest(ctx, m.ExpID)
		if lerr != nil || stored.Hash != m.Hash {
			return nil, "the local store holds another manifest for " + m.ExpID
		}
	}
	return suite, ""
}

// adoptUnrepeatable copies into a locally planned manifest the fields a
// second plan cannot repeat: the creation time, the random slot and pair ids
// (by position, when the layout has the same length; any other difference
// in the slots still shows), the gateway preflight record and each arm's
// local image id, which describe the coordinator's machine.
func adoptUnrepeatable(local, remote *schemas.Manifest) {
	local.CreatedAt = remote.CreatedAt
	if len(local.Slots) == len(remote.Slots) {
		// The run inputs are keyed by slot id, so they follow the adopted ids.
		inputs := make(map[string]schemas.RunInputIdentity, len(local.RunInputs))
		for i := range local.Slots {
			if in, ok := local.RunInputs[local.Slots[i].SlotID]; ok {
				inputs[remote.Slots[i].SlotID] = in
			}
			local.Slots[i].SlotID, local.Slots[i].PairID = remote.Slots[i].SlotID, remote.Slots[i].PairID
		}
		if local.RunInputs != nil {
			local.RunInputs = inputs
		}
	}
	local.Gateway = remote.Gateway
	if remote.Gateway.Deferred() { // Tenancy area: a hosted plan has no gateway part (remotegateway.go)
		local.Parts = withoutGatewayParts(local.Parts)
	}
	if hostedImagesAdopted(remote) { // Deploy area: a hosted plan (no Docker) froze no image ids; the runner records its own per slot (remoteimages.go)
		local.SuiteImages, local.TaskImages = nil, nil
	}
	for id, am := range local.Arms {
		if ra, ok := remote.Arms[id]; ok {
			am.ImageID = ra.ImageID
			if hostedImagesAdopted(remote) { // per-task image ids follow the hosted plan too (audit 8-B-06)
				am.TaskImageIDs = ra.TaskImageIDs
			}
			for mid, rm := range ra.Members {
				if lm, ok := am.Members[mid]; ok {
					lm.ImageID = rm.ImageID
					am.Members[mid] = lm
				}
			}
			local.Arms[id] = am
		}
	}
	if local.Compatibility != nil && remote.Compatibility != nil {
		local.Compatibility.ImageID = remote.Compatibility.ImageID
		if hostedImagesAdopted(remote) {
			local.Compatibility.TaskImageIDs = remote.Compatibility.TaskImageIDs
		}
	}
	local.Hash = ""
}

// heartbeat keeps the lease fresh until stop is called, and cancels the
// returned context with an ErrLeaseLost cause on loss under the scheduler's
// rules (leaseLoss): the coordinator says the lease is gone, or no heartbeat
// succeeded for longer than the stale threshold.
func (w *Worker) heartbeat(ctx context.Context, lease *schemas.WorkerLease) (context.Context, func()) {
	sctx, cancel := context.WithCancelCause(ctx)
	every := time.Duration(lease.HeartbeatEveryMS) * time.Millisecond
	if every <= 0 {
		every = DefaultHeartbeatEvery
	}
	stale := time.Duration(lease.StaleAfterMS) * time.Millisecond
	if stale <= 0 {
		stale = DefaultStaleAfter
	}
	quit, done := make(chan struct{}), make(chan struct{})
	var stopped atomic.Bool
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		last := time.Now()
		for {
			select {
			case <-quit:
				return
			case <-sctx.Done():
				return
			case <-t.C:
			}
			err := w.call(sctx, http.MethodPost, workerAPI+"/leases/"+url.PathEscape(lease.LeaseID)+"/heartbeat",
				schemas.WorkerHeartbeat{WorkerID: w.opts.WorkerID}, nil)
			if sctx.Err() != nil {
				return
			}
			if err == nil {
				last = time.Now()
				continue
			}
			mapped := err
			switch {
			case errors.Is(err, schemas.ErrLeaseGone):
				mapped = fmt.Errorf("%w: %w", schemas.ErrLeaseHeld, err)
			case errors.Is(err, schemas.ErrWorkerUnknown):
				mapped = fmt.Errorf("%w: %w", schemas.ErrNotFound, err)
			}
			reason := leaseLoss(mapped, time.Since(last), stale)
			if reason == "" {
				w.logger.Warn("lease heartbeat failed; retrying", "lease", lease.LeaseID, "error", err.Error())
				continue
			}
			cancel(fmt.Errorf("%w: lease %s, slot %s: %s", ErrLeaseLost, lease.LeaseID, lease.Slot.SlotID, reason))
			return
		}
	}()
	return sctx, func() {
		if stopped.Swap(true) {
			return
		}
		close(quit)
		<-done
		cancel(nil)
	}
}

// upload packs the sealed run folder, sends it in chunks and completes the
// lease.
func (w *Worker) upload(ctx context.Context, lease *schemas.WorkerLease, res *SlotResult) (*schemas.WorkerResult, error) {
	return w.uploadTo(ctx, ctx, lease, res)
}

// uploadTo is upload with the complete call on its own context. Work sends
// the chunks on the heartbeat context, so a lost lease stops them, but sends
// complete on its parent: the coordinator itself refuses a lease that is
// gone, and once it has completed the lease a heartbeat racing the call is
// answered "lease gone: completed", which would otherwise cancel the call
// and drop the coordinator's answer for a run it already accepted.
func (w *Worker) uploadTo(ctx, completeCtx context.Context, lease *schemas.WorkerLease, res *SlotResult) (*schemas.WorkerResult, error) {
	sealRaw, err := os.ReadFile(filepath.Join(res.Dir, sealJSONFile))
	if err != nil {
		return nil, fmt.Errorf("read seal.json: %w", err)
	}
	f, err := os.CreateTemp(w.opts.ScratchDir, "bench-upload-*.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("create archive: %w", err)
	}
	defer func() {
		if rerr := os.Remove(f.Name()); rerr != nil {
			w.logger.Warn("remove archive", "path", f.Name(), "error", rerr.Error())
		}
	}()
	err = packRun(res.Dir, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	sum, size, err := sha256File(f.Name())
	if err != nil {
		return nil, fmt.Errorf("hash archive: %w", err)
	}
	if err := w.sendChunks(ctx, lease, f.Name(), size); err != nil {
		return nil, err
	}
	req := schemas.WorkerComplete{WorkerID: w.opts.WorkerID, RunID: res.Run.RunID, SealSHA256: sha256Hex(sealRaw),
		ArchiveSHA256: sum, ArchiveBytes: size, Outcome: res.Run.Outcome, CostUSD: costValue(res),
		Gateway: res.RunnerGateway, // Tenancy area: the runner's own preflight when the manifest defers it
		Images:  res.RunnerImages}  // Deploy area: the runner's image ids when the manifest defers it
	var out schemas.WorkerResult
	if err := w.call(completeCtx, http.MethodPost, workerAPI+"/leases/"+url.PathEscape(lease.LeaseID)+"/complete", req, &out); err != nil {
		return nil, fmt.Errorf("complete: %w", err)
	}
	return &out, nil
}

// sendChunks sends the archive in chunks no larger than the coordinator
// accepts. A failed chunk is retried at the same offset.
func (w *Worker) sendChunks(ctx context.Context, lease *schemas.WorkerLease, p string, size int64) error {
	chunk := DefaultChunkBytes
	w.mu.Lock()
	if w.registered != nil && w.registered.ChunkBytes > 0 {
		chunk = w.registered.ChunkBytes
		if w.registered.MaxUploadBytes > 0 && size > w.registered.MaxUploadBytes {
			w.mu.Unlock()
			return fmt.Errorf("archive of %d bytes passes the coordinator's cap of %d: %w", size, w.registered.MaxUploadBytes, schemas.ErrUploadTooLarge)
		}
	}
	w.mu.Unlock()
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, chunk)
	base := workerAPI + "/leases/" + url.PathEscape(lease.LeaseID) + "/upload?worker_id=" + url.QueryEscape(w.opts.WorkerID)
	for off := int64(0); off < size; {
		n, err := io.ReadFull(f, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read archive: %w", err)
		}
		var state schemas.WorkerUploadState
		var cerr error
		for range uploadAttempts {
			cerr = w.callRaw(ctx, base+"&offset="+strconv.FormatInt(off, 10), buf[:n], &state)
			if cerr == nil || errors.Is(cerr, schemas.ErrLeaseGone) || errors.Is(cerr, schemas.ErrUploadTooLarge) || ctx.Err() != nil {
				break
			}
		}
		if cerr != nil {
			return fmt.Errorf("upload at offset %d: %w", off, cerr)
		}
		off += int64(n)
		if state.Bytes != off {
			return fmt.Errorf("coordinator holds %d bytes after offset %d: %w", state.Bytes, off, schemas.ErrUploadOffset)
		}
	}
	return nil
}

// release gives a lease back without a run.
func (w *Worker) release(ctx context.Context, lease *schemas.WorkerLease, reason string, refuse bool, cost *schemas.Value) {
	req := schemas.WorkerRelease{WorkerID: w.opts.WorkerID, Reason: reason, Refuse: refuse, CostUSD: cost}
	if err := w.call(ctx, http.MethodPost, workerAPI+"/leases/"+url.PathEscape(lease.LeaseID)+"/release", req, nil); err != nil {
		w.logger.Warn("release lease", "lease", lease.LeaseID, "error", err.Error())
	}
}

// call sends a JSON request with the bearer token and decodes the answer.
func (w *Worker) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	return w.do(ctx, method, path, "application/json", body, out)
}

// callRaw posts raw bytes (an upload chunk).
func (w *Worker) callRaw(ctx context.Context, path string, data []byte, out any) error {
	return w.do(ctx, http.MethodPost, path, "application/octet-stream", bytes.NewReader(data), out)
}

// do sends one request and maps the API's error statuses to the worker API's
// sentinel errors.
func (w *Worker) do(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, w.opts.Server+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+w.opts.Token)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := w.opts.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, strings.SplitN(path, "?", 2)[0], err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("read answer: %w", err)
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode answer: %w", err)
	}
	return nil
}

// apiError turns an error answer into an error that wraps the matching
// sentinel.
func apiError(status int, raw []byte) error {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		msg = body.Error.Message
	}
	var sentinel error
	switch status {
	case http.StatusUnauthorized:
		sentinel = schemas.ErrWorkerAuth
	case http.StatusNotFound:
		sentinel = schemas.ErrWorkerUnknown
		if strings.Contains(msg, "not open to workers") || strings.Contains(msg, "route not found") {
			sentinel = schemas.ErrNotFound
		}
	case http.StatusGone:
		sentinel = schemas.ErrLeaseGone
	case http.StatusConflict:
		sentinel = schemas.ErrUploadOffset
	case http.StatusRequestEntityTooLarge:
		sentinel = schemas.ErrUploadTooLarge
	case http.StatusBadRequest:
		sentinel = schemas.ErrWorkerRequest
	case http.StatusForbidden: // Runners area: outside the runner token's organization or pool
		sentinel = schemas.ErrWorkerScope
	default:
		return fmt.Errorf("coordinator answered %d: %s", status, msg)
	}
	return fmt.Errorf("%w (%d): %s", sentinel, status, msg)
}

func (e *workerExp) slot(id string) *schemas.Slot {
	for i := range e.m.Slots {
		if e.m.Slots[i].SlotID == id {
			s := e.m.Slots[i]
			return &s
		}
	}
	return nil
}

// costValue is a finished run's cost as the worker reports it: observed, or
// unknown with the reason, never zero for a cost that was not seen.
func costValue(res *SlotResult) schemas.Value {
	if res == nil {
		return schemas.Unknown("no run result on the worker", schemas.EvidenceGateway)
	}
	if c, ok := spendCost(res.Dir, res.Metrics, res.Run); ok {
		return schemas.Observed(c, schemas.EvidenceGateway)
	}
	return schemas.Unknown("cost_usd was not observed on the worker", schemas.EvidenceGateway)
}

// mapDiff lists the keys whose values differ between got and want, sorted,
// or "" when the maps are equal.
func mapDiff(got, want map[string]string) string {
	var out []string
	for k, v := range want {
		if g, ok := got[k]; !ok {
			out = append(out, k+" missing here")
		} else if g != v {
			out = append(out, fmt.Sprintf("%s %s here, %s in the manifest", k, g, v))
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			out = append(out, k+" only here")
		}
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

// manifestDiff names the top-level manifest fields that differ, and the arms
// whose config hash differs.
func manifestDiff(a, b *schemas.Manifest) string {
	var out []string
	if a.ExpID != b.ExpID {
		out = append(out, "exp_id")
	}
	for id, am := range b.Arms {
		if la, ok := a.Arms[id]; !ok || la.ConfigHash != am.ConfigHash {
			out = append(out, "arm "+id+" config hash")
		} else if !reflect.DeepEqual(la, am) {
			out = append(out, "arm "+id+" files")
		}
	}
	checks := []struct {
		name string
		same bool
	}{
		{"experiment", reflect.DeepEqual(a.Experiment, b.Experiment)},
		{"suite", a.SuiteID == b.SuiteID && a.SuiteVersion == b.SuiteVersion && a.SuiteLockHash == b.SuiteLockHash},
		{"parts", reflect.DeepEqual(a.Parts, b.Parts)},
		{"slots", reflect.DeepEqual(a.Slots, b.Slots)},
		{"arm_order", reflect.DeepEqual(a.ArmOrder, b.ArmOrder)},
		{"policy_hash", a.PolicyHash == b.PolicyHash},
		{"budget_diffs", reflect.DeepEqual(a.BudgetDiffs, b.BudgetDiffs)},
		{"compatibility", reflect.DeepEqual(a.Compatibility, b.Compatibility)},
		// Deploy area: the fields a host can change name themselves too.
		{"suite_images", reflect.DeepEqual(a.SuiteImages, b.SuiteImages)},
		{"task_images", reflect.DeepEqual(a.TaskImages, b.TaskImages)},
		{"fingerprint_hash", a.FingerprintHash == b.FingerprintHash},
		{"task_identities", reflect.DeepEqual(a.TaskIdentities, b.TaskIdentities)},
		{"run_inputs", reflect.DeepEqual(a.RunInputs, b.RunInputs)},
		{"gateway", reflect.DeepEqual(a.Gateway, b.Gateway)},
	}
	for _, c := range checks {
		if !c.same {
			out = append(out, c.name)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "a field outside the compared ones"
	}
	return strings.Join(out, ", ")
}

// sleepCtx waits d or until ctx ends, and reports whether ctx is still live.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
