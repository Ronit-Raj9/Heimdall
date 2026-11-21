package engine

// Runner jobs (gap G05): hosted CLI jobs that start containers (agent
// build, MCP and dynamic skill scans, monitor certification, GPU, voice and
// visual checks, simulator spread) never run in the hosted server process.
// The org's coordinator queues them; a runner of the org (its own or the
// shared runner, which leases with the org's token) claims one as a lease
// of kind "job" over the same worker routes slots use: lease, heartbeat
// (which carries the job's log lines back and answers 410 once the job is
// cancelled), upload (a zip of result.json and the files the job wrote) and
// complete or release. A coordinator serves one organization, so a runner
// of org A never sees a job of org B.

import (
	"archive/zip"
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
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ErrNoRunner is a runner job submitted while the organization has no live
// runner that runs jobs.
var ErrNoRunner = errors.New("no runner online")

// Runner job timing. Vars so tests can shorten them.
var (
	JobHeartbeatEvery = 2 * time.Second
	JobStaleAfter     = 90 * time.Second
	JobClaimWait      = 2 * time.Minute
	jobWatchEvery     = time.Second
)

// MaxJobFilesBytes caps the project snapshot sent with a job lease.
const MaxJobFilesBytes = 32 << 20

// JobOutcome is what a runner sent back for a job. Archive is the uploaded
// zip (result.json plus files/<project-relative path>), empty when the
// runner sent none; the caller removes it. Error is the job's own failure.
type JobOutcome struct {
	Worker  string
	Archive string
	Error   string
}

// queuedJob is one runner job from submission to its end.
type queuedJob struct {
	job       schemas.JobLease
	log       func(string)
	queuedAt  time.Time
	lease     *jobLease
	refusedBy map[string]bool
	cancelled bool
	finished  bool
	done      chan struct{}
	outcome   JobOutcome
	err       error
}

// jobLease is a queued job out on a runner.
type jobLease struct {
	id, worker string
	q          *queuedJob
	lastBeat   time.Time
	archive    string
	received   int64
	uploading  bool
	closing    bool
}

// runnerJobs is the coordinator's job table, guarded by Coordinator.mu.
type runnerJobs struct {
	queue  []*queuedJob
	leases map[string]*jobLease
	noJobs map[string]bool // workers that refused a job lease (an older runner build)
}

func (c *Coordinator) jobsLocked() *runnerJobs {
	if c.rj.leases == nil {
		c.rj.leases = map[string]*jobLease{}
		c.rj.noJobs = map[string]bool{}
	}
	return &c.rj
}

// liveJobWorkerLocked reports whether a registered worker that runs jobs
// called recently. The caller holds mu.
func (c *Coordinator) liveJobWorkerLocked() bool {
	rj := c.jobsLocked()
	now := c.now()
	for id, w := range c.workers {
		if rj.noJobs[id] || w.info.State == schemas.WorkerLost || now.Sub(w.info.LastSeen) > c.opts.StaleAfter {
			continue
		}
		return true
	}
	return false
}

// HasJobRunner reports whether a live runner of this coordinator can take a
// runner job now.
func (c *Coordinator) HasJobRunner() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.liveJobWorkerLocked()
}

// RunJob queues a runner job and waits for its end: a runner completes it
// (the outcome), gives it back, goes silent, nobody claims it within
// JobClaimWait, or ctx ends (the job is cancelled on the runner at its next
// heartbeat). With no live runner it fails at once with ErrNoRunner. Every
// log line the runner streams goes to log.
func (c *Coordinator) RunJob(ctx context.Context, job schemas.JobLease, log func(string)) (*JobOutcome, error) {
	if log == nil {
		log = func(string) {}
	}
	if len(job.Files) > MaxJobFilesBytes {
		return nil, fmt.Errorf("the project snapshot of %d bytes passes the %d byte cap", len(job.Files), MaxJobFilesBytes)
	}
	q := &queuedJob{job: job, log: log, queuedAt: c.now(), refusedBy: map[string]bool{}, done: make(chan struct{})}
	c.mu.Lock()
	if !c.liveJobWorkerLocked() {
		c.mu.Unlock()
		return nil, ErrNoRunner
	}
	rj := c.jobsLocked()
	rj.queue = append(rj.queue, q)
	c.mu.Unlock()
	t := time.NewTicker(jobWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-q.done:
			if q.err != nil {
				return nil, q.err
			}
			out := q.outcome
			return &out, nil
		case <-ctx.Done():
			c.cancelJob(q)
			return nil, ctx.Err()
		case <-t.C:
			c.watchJob(q)
		}
	}
}

// watchJob fails a job nobody claimed in time, or whose runner went silent.
func (c *Coordinator) watchJob(q *queuedJob) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if q.finished {
		return
	}
	now := c.now()
	switch {
	case q.lease == nil && !c.liveJobWorkerLocked():
		c.dequeueLocked(q)
		c.finishLocked(q, nil, ErrNoRunner)
	case q.lease == nil && now.Sub(q.queuedAt) > JobClaimWait:
		c.dequeueLocked(q)
		c.finishLocked(q, nil, fmt.Errorf("no runner claimed the job within %s: every runner is busy, or runs an older build that does not take jobs (update it)", JobClaimWait))
	case q.lease != nil && now.Sub(q.lease.lastBeat) > JobStaleAfter:
		l := q.lease
		c.endJobLocked(l, "lost: no heartbeat")
		c.finishLocked(q, nil, fmt.Errorf("runner %s stopped answering while it ran the job (no heartbeat for %s)", l.worker, JobStaleAfter))
	}
}

// cancelJob drops an unclaimed job, or marks a claimed one so its runner's
// next heartbeat is answered 410 and the runner stops the command.
func (c *Coordinator) cancelJob(q *queuedJob) {
	c.mu.Lock()
	defer c.mu.Unlock()
	q.cancelled = true
	if q.lease == nil {
		c.dequeueLocked(q)
	}
	if !q.finished {
		c.finishLocked(q, nil, context.Canceled)
	}
}

func (c *Coordinator) dequeueLocked(q *queuedJob) {
	rj := c.jobsLocked()
	for i, x := range rj.queue {
		if x == q {
			rj.queue = append(rj.queue[:i:i], rj.queue[i+1:]...)
			return
		}
	}
}

func (c *Coordinator) finishLocked(q *queuedJob, out *JobOutcome, err error) {
	if q.finished {
		return
	}
	q.finished = true
	if out != nil {
		q.outcome = *out
	}
	q.err = err
	close(q.done)
}

// endJobLocked removes a job lease from the table and its worker's running
// list; a later call for it is answered "lease gone" with why.
func (c *Coordinator) endJobLocked(l *jobLease, why string) {
	rj := c.jobsLocked()
	delete(rj.leases, l.id)
	c.ended[l.id] = why
	if l.q.lease == l {
		l.q.lease = nil
	}
	w := c.workers[l.worker]
	if w == nil {
		return
	}
	tag := "job:" + l.q.job.JobID
	for i, s := range w.info.Running {
		if s == tag {
			w.info.Running = append(w.info.Running[:i:i], w.info.Running[i+1:]...)
			break
		}
	}
	w.info.State = stateOf(w)
}

// leaseJobLocked hands w the oldest queued job it has not refused, or nil.
// The caller holds mu and has checked w has a free slot.
func (c *Coordinator) leaseJobLocked(w *remoteWorker) *schemas.WorkerLease {
	rj := c.jobsLocked()
	now := c.now()
	for _, l := range rj.leases { // runners that vanished without a heartbeat: free their slot
		if !l.closing && now.Sub(l.lastBeat) > JobStaleAfter {
			c.endJobLocked(l, "lost: no heartbeat")
			c.finishLocked(l.q, nil, fmt.Errorf("runner %s stopped answering while it ran the job", l.worker))
		}
	}
	if rj.noJobs[w.info.ID] {
		return nil
	}
	for i, q := range rj.queue {
		if q.refusedBy[w.info.ID] || q.cancelled {
			continue
		}
		rj.queue = append(rj.queue[:i:i], rj.queue[i+1:]...)
		id := utils.NewID("j", 12)
		l := &jobLease{id: id, worker: w.info.ID, q: q, lastBeat: now, archive: filepath.Join(c.opts.StagingDir, id+".zip")}
		q.lease = l
		rj.leases[id] = l
		w.info.Running = append(w.info.Running, "job:"+q.job.JobID)
		w.info.State = schemas.WorkerRunning
		job := q.job
		job.Argv = append([]string(nil), job.Argv...)
		job.Collect = append([]string(nil), job.Collect...)
		c.logger.Info("runner job leased", "job", job.JobID, "kind", job.JobKind, "worker", w.info.ID, "lease", id)
		return &schemas.WorkerLease{LeaseID: id, WorkerID: w.info.ID, Kind: schemas.LeaseKindJob, Job: &job,
			HeartbeatEveryMS: JobHeartbeatEvery.Milliseconds(), StaleAfterMS: JobStaleAfter.Milliseconds()}
	}
	return nil
}

// jobLeaseLocked returns the worker's live job lease, or nil when leaseID
// is not a job lease. The caller holds mu.
func (c *Coordinator) jobLeaseLocked(leaseID, workerID string, scope schemas.RunnerScope) (*jobLease, error) {
	l, ok := c.jobsLocked().leases[leaseID]
	if !ok {
		return nil, nil
	}
	if _, err := c.workerLocked(workerID, scope); err != nil {
		return nil, err
	}
	if l.worker != workerID {
		return nil, fmt.Errorf("lease %s belongs to another worker: %w", leaseID, schemas.ErrLeaseGone)
	}
	c.workers[workerID].info.LastSeen = c.now().UTC()
	return l, nil
}

// jobHeartbeat handles a heartbeat of a job lease: it keeps the lease fresh,
// passes the lines it carries to the job's log and answers ErrLeaseGone once
// the job was cancelled. handled is false for a slot lease.
func (c *Coordinator) jobHeartbeat(ctx context.Context, leaseID, workerID string, scope schemas.RunnerScope) (bool, error) {
	c.mu.Lock()
	l, err := c.jobLeaseLocked(leaseID, workerID, scope)
	if l == nil && err == nil {
		c.mu.Unlock()
		return false, nil
	}
	if err != nil {
		c.mu.Unlock()
		return true, err
	}
	q := l.q
	if q.cancelled {
		c.endJobLocked(l, "cancelled")
		c.mu.Unlock()
		return true, fmt.Errorf("lease %s: %w: the job was cancelled", leaseID, schemas.ErrLeaseGone)
	}
	l.lastBeat = c.now()
	c.mu.Unlock()
	for _, line := range schemas.HeartbeatLogsFrom(ctx) {
		q.log(line)
	}
	return true, nil
}

// jobUpload appends one chunk of a job lease's result zip.
func (c *Coordinator) jobUpload(leaseID, workerID string, scope schemas.RunnerScope, offset int64, data []byte) (bool, int64, error) {
	c.mu.Lock()
	l, err := c.jobLeaseLocked(leaseID, workerID, scope)
	switch {
	case l == nil && err == nil:
		c.mu.Unlock()
		return false, 0, nil
	case err != nil:
		c.mu.Unlock()
		return true, 0, err
	case l.q.cancelled:
		c.endJobLocked(l, "cancelled")
		c.mu.Unlock()
		return true, 0, fmt.Errorf("lease %s: %w: the job was cancelled", leaseID, schemas.ErrLeaseGone)
	case l.closing || l.uploading:
		have := l.received
		c.mu.Unlock()
		return true, have, fmt.Errorf("lease %s: another upload call is in progress: %w", leaseID, schemas.ErrUploadOffset)
	case offset+int64(len(data)) == l.received && len(data) > 0: // the answer to the last chunk was lost: acknowledge
		have := l.received
		c.mu.Unlock()
		return true, have, nil
	case offset != l.received:
		have := l.received
		c.mu.Unlock()
		return true, have, fmt.Errorf("lease %s: offset %d, coordinator holds %d bytes: %w", leaseID, offset, have, schemas.ErrUploadOffset)
	case l.received+int64(len(data)) > c.opts.MaxUploadBytes:
		c.mu.Unlock()
		return true, offset, fmt.Errorf("lease %s: result passes %d bytes: %w", leaseID, c.opts.MaxUploadBytes, schemas.ErrUploadTooLarge)
	}
	l.uploading = true
	l.lastBeat = c.now()
	c.mu.Unlock()
	werr := appendFile(l.archive, data)
	c.mu.Lock()
	defer c.mu.Unlock()
	l.uploading = false
	if werr != nil {
		return true, l.received, fmt.Errorf("lease %s: stage upload: %w", leaseID, werr)
	}
	l.received += int64(len(data))
	return true, l.received, nil
}

// jobComplete ends a job lease with the runner's outcome. The uploaded zip
// must match the size and sha256 the runner reports.
func (c *Coordinator) jobComplete(leaseID string, scope schemas.RunnerScope, req *schemas.WorkerComplete) (bool, *schemas.WorkerResult, error) {
	c.mu.Lock()
	l, err := c.jobLeaseLocked(leaseID, req.WorkerID, scope)
	if l == nil && err == nil {
		c.mu.Unlock()
		return false, nil, nil
	}
	if err == nil && (l.closing || l.uploading) {
		err = fmt.Errorf("lease %s: a call is already in progress: %w", leaseID, schemas.ErrLeaseGone)
	}
	if err != nil {
		c.mu.Unlock()
		return true, nil, err
	}
	l.closing = true
	received := l.received
	c.mu.Unlock()

	out := &schemas.WorkerResult{LeaseID: leaseID, Accepted: true}
	res := JobOutcome{Worker: l.worker}
	if req.Job != nil {
		res.Error = strings.TrimSpace(req.Job.Error)
	}
	if received > 0 {
		sum, size, herr := sha256File(l.archive)
		switch {
		case herr != nil:
			out.Accepted, out.Reason = false, "read the uploaded result: "+herr.Error()
		case size != req.ArchiveBytes || sum != req.ArchiveSHA256:
			out.Accepted, out.Reason = false, "the uploaded result does not match its reported size and sha256"
		default:
			res.Archive = l.archive
		}
	}
	if !out.Accepted {
		c.remove(l.archive)
		if res.Error == "" {
			res.Error = out.Reason
		}
	}
	c.mu.Lock()
	c.endJobLocked(l, "completed")
	if w := c.workers[l.worker]; w != nil {
		w.info.Completed++
	}
	q := l.q
	finished := q.finished
	c.finishLocked(q, &res, nil)
	c.mu.Unlock()
	if finished && res.Archive != "" { // the job was cancelled meanwhile: nobody reads the result
		c.remove(res.Archive)
	}
	return true, out, nil
}

// jobRelease ends a job lease the runner gave back. A refusal (a runner that
// does not take jobs) puts the job back at the head of the queue and never
// offers that runner a job again.
func (c *Coordinator) jobRelease(leaseID string, scope schemas.RunnerScope, req *schemas.WorkerRelease) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.jobLeaseLocked(leaseID, req.WorkerID, scope)
	if l == nil && err == nil {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	q := l.q
	c.endJobLocked(l, "released: "+req.Reason)
	c.remove(l.archive)
	if req.Refuse && !q.finished && !q.cancelled {
		c.jobsLocked().noJobs[l.worker] = true
		q.refusedBy[l.worker] = true
		rj := c.jobsLocked()
		rj.queue = append([]*queuedJob{q}, rj.queue...)
		return true, nil
	}
	c.finishLocked(q, nil, fmt.Errorf("runner %s gave the job back: %s", l.worker, req.Reason))
	return true, nil
}

// --- runner side ---

// JobRun is what a JobExecutor produced: the job's JSON result (nil when it
// printed none), the scratch project it ran in, the project-relative files
// to send back, and how to remove the scratch project.
type JobRun struct {
	Result  json.RawMessage
	Dir     string
	Files   []string
	Cleanup func()
}

// JobExecutor runs runner jobs on a worker (cmd/bifrost-bench provides it:
// `bifrost-bench <argv>` in a scratch project). log takes one line at a
// time; the executor redacts secrets before calling it. A nil run with an
// error is a job that failed before it produced anything.
type JobExecutor interface {
	RunJob(ctx context.Context, job *schemas.JobLease, log func(string)) (*JobRun, error)
}

// maxJobLogBuffer bounds the lines a runner holds between heartbeats.
const maxJobLogBuffer = 5000

// workJob runs one job lease: the executor under a heartbeat that streams
// the log and stops the job when the server cancels it, then the result zip
// upload and the complete call.
func (w *Worker) workJob(ctx context.Context, lease *schemas.WorkerLease) {
	if w.opts.Jobs == nil || lease.Job == nil {
		w.release(ctx, lease, "this runner does not run hosted jobs (it was started without job support)", true, nil)
		return
	}
	var mu sync.Mutex
	var buf []string
	logf := func(line string) {
		mu.Lock()
		buf = append(buf, line)
		if len(buf) > maxJobLogBuffer {
			buf = buf[len(buf)-maxJobLogBuffer:]
		}
		mu.Unlock()
	}
	take := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := buf
		buf = nil
		return out
	}
	path := workerAPI + "/leases/" + url.PathEscape(lease.LeaseID) + "/heartbeat"
	jctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	beat := func(c context.Context) error {
		lines := take()
		err := w.call(c, http.MethodPost, path, schemas.WorkerHeartbeat{WorkerID: w.opts.WorkerID, Logs: lines}, nil)
		if err != nil && !errors.Is(err, schemas.ErrLeaseGone) && len(lines) > 0 { // keep the lines for the next try
			mu.Lock()
			buf = append(lines, buf...)
			mu.Unlock()
		}
		return err
	}
	every := time.Duration(lease.HeartbeatEveryMS) * time.Millisecond
	if every <= 0 {
		every = JobHeartbeatEvery
	}
	quit, beatDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(beatDone)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-jctx.Done():
				return
			case <-t.C:
			}
			if err := beat(jctx); errors.Is(err, schemas.ErrLeaseGone) {
				cancel(fmt.Errorf("%w: %v", ErrLeaseLost, err))
				return
			} else if err != nil && jctx.Err() == nil {
				w.logger.Warn("job heartbeat failed; retrying", "lease", lease.LeaseID, "error", err.Error())
			}
		}
	}()
	w.logger.Info("runner job started", "job", lease.Job.JobID, "kind", lease.Job.JobKind, "lease", lease.LeaseID)
	run, runErr := w.opts.Jobs.RunJob(jctx, lease.Job, logf)
	close(quit)
	<-beatDone
	if run != nil && run.Cleanup != nil {
		defer run.Cleanup()
	}
	if jctx.Err() != nil {
		w.logger.Warn("runner job stopped: the server cancelled it or the lease is gone", "job", lease.Job.JobID)
		return
	}
	if err := beat(ctx); errors.Is(err, schemas.ErrLeaseGone) { // the last lines, and a last cancel check
		return
	}
	req := schemas.WorkerComplete{WorkerID: w.opts.WorkerID, Job: &schemas.JobComplete{}}
	if runErr != nil {
		req.Job.Error = runErr.Error()
	}
	if run != nil {
		f, err := os.CreateTemp(w.opts.ScratchDir, "bench-job-*.zip")
		if err == nil {
			defer os.Remove(f.Name())
			err = packJobResult(f, run)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err == nil {
			var sum string
			var size int64
			if sum, size, err = sha256File(f.Name()); err == nil {
				if err = w.sendChunks(ctx, lease, f.Name(), size); err == nil {
					req.ArchiveSHA256, req.ArchiveBytes = sum, size
				}
			}
		}
		if err != nil && req.Job.Error == "" {
			req.Job.Error = "send the job result: " + err.Error()
		}
	}
	var out schemas.WorkerResult
	if err := w.call(ctx, http.MethodPost, workerAPI+"/leases/"+url.PathEscape(lease.LeaseID)+"/complete", req, &out); err != nil {
		w.logger.Warn("complete runner job", "job", lease.Job.JobID, "error", err.Error())
	}
}

// packJobResult writes the result zip: result.json (when the job printed
// one) and files/<rel> for every collected file.
func packJobResult(dst io.Writer, run *JobRun) error {
	zw := zip.NewWriter(dst)
	if len(run.Result) > 0 {
		f, err := zw.Create("result.json")
		if err != nil {
			return err
		}
		if _, err := f.Write(run.Result); err != nil {
			return err
		}
	}
	for _, rel := range run.Files {
		rel = filepath.ToSlash(filepath.Clean(rel))
		if !filepath.IsLocal(rel) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(run.Dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		f, err := zw.Create("files/" + rel)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	return zw.Close()
}

// JobResultFile is one file a runner job sent back.
type JobResultFile struct {
	Path string // project-relative, forward slashes
	Data []byte
}

// ReadJobResult reads a job result zip: result.json and the files, each
// capped at maxFile bytes. Paths that are not local are refused.
func ReadJobResult(archive string, maxFile int64) (json.RawMessage, []JobResultFile, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, nil, fmt.Errorf("open job result: %w", err)
	}
	defer zr.Close()
	var result json.RawMessage
	var files []JobResultFile
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > uint64(maxFile) {
			return nil, nil, fmt.Errorf("job result file %s is larger than %d bytes", f.Name, maxFile)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxFile+1))
		rc.Close()
		if err != nil {
			return nil, nil, err
		}
		if int64(len(data)) > maxFile {
			return nil, nil, fmt.Errorf("job result file %s is larger than %d bytes", f.Name, maxFile)
		}
		switch {
		case f.Name == "result.json":
			if json.Valid(bytes.TrimSpace(data)) {
				result = json.RawMessage(bytes.TrimSpace(data))
			}
		case strings.HasPrefix(f.Name, "files/"):
			rel := strings.TrimPrefix(f.Name, "files/")
			if !filepath.IsLocal(filepath.FromSlash(rel)) {
				return nil, nil, fmt.Errorf("job result file %q leaves the project", f.Name)
			}
			files = append(files, JobResultFile{Path: rel, Data: data})
		}
	}
	return result, files, nil
}
