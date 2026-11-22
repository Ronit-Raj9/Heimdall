package schemas

import (
	"context"
	"errors"
	"time"
)

// This file holds the remote worker API (docs/remote-execution.md). A
// coordinator (bifrost-bench serve with BENCH_WORKER_TOKEN set) hands out
// slot leases over HTTP; a worker (bifrost-bench worker) runs each slot with
// the normal pipeline on its own machine and uploads the sealed run folder.
// The coordinator verifies the worker's seal, seals the run again into its own
// chain, and owns the lease table and the spend cap. WorkerInfo, the list
// entry, is in scenario.go.

// Worker states (WorkerInfo.State).
const (
	WorkerIdle    = "idle"
	WorkerRunning = "running"
	WorkerLost    = "lost"
)

// WorkerRegister is the body of POST /api/bench/workers/register.
type WorkerRegister struct {
	WorkerID string            `json:"worker_id"`
	Host     string            `json:"host"`
	Version  string            `json:"version"` // bench build the worker runs
	Parts    map[string]string `json:"parts"`   // "<kind>/<name>" -> version of every part the worker registered
	Slots    int               `json:"slots"`   // concurrent runs it offers
	// Runners area (docs/runners.md): the pool the runner asks for and its
	// free-form labels. In hosted mode the pool must be the runner token's
	// pool (empty takes the token's); local mode records both and leases
	// from every attached experiment as before.
	Pool   string   `json:"pool,omitempty"`
	Labels []string `json:"labels,omitempty"`
	// Capabilities lists what the worker's machine offers beyond slots: its
	// GPUs (X7). Nil from a worker without GPUs, which is never leased a slot
	// of a GPU task.
	Capabilities *WorkerCapabilities `json:"capabilities,omitempty"`
	// JobKinds lists the runner job kinds the worker takes (JobLease.JobKind).
	// Empty: every kind (an org's own runner, and every older build).
	JobKinds []string `json:"job_kinds,omitempty"`
}

// WorkerRegistered is the answer to a registration: the upload limits the
// worker must respect. Lease timing comes with each lease.
type WorkerRegistered struct {
	WorkerID       string `json:"worker_id"`
	MaxUploadBytes int64  `json:"max_upload_bytes"`
	ChunkBytes     int    `json:"chunk_bytes"` // largest upload request body the coordinator accepts
}

// WorkerLeaseRequest is the body of POST /api/bench/workers/lease.
type WorkerLeaseRequest struct {
	WorkerID string `json:"worker_id"`
	// Disk is the runner's free disk on its run store (E6). With Low set the
	// runner asks for no work: the coordinator records it and leases nothing.
	Disk *WorkerDisk `json:"disk,omitempty"`
}

// WorkerDisk is a runner's free disk on its run store and the floor it keeps
// (runner.disk.min_free_gb). Low is FreeBytes below MinFreeBytes.
type WorkerDisk struct {
	FreeBytes    int64 `json:"free_bytes"`
	MinFreeBytes int64 `json:"min_free_bytes"`
	Low          bool  `json:"low"`
}

// WorkerLease is one slot the coordinator leased to a worker. The worker
// checks the hashes and part versions against its own project before it runs
// anything, and refuses the lease when any differs.
type WorkerLease struct {
	LeaseID          string            `json:"lease_id"`
	WorkerID         string            `json:"worker_id"`
	ExpID            string            `json:"exp_id"`
	Slot             Slot              `json:"slot"`
	Attempt          int               `json:"attempt"` // 1-based attempt number the run must carry
	ManifestHash     string            `json:"manifest_hash"`
	SuiteLockHash    string            `json:"suite_lock_hash"`
	Parts            map[string]string `json:"parts"` // the manifest's part versions
	BenchVersion     string            `json:"bench_version"`
	HeartbeatEveryMS int64             `json:"heartbeat_every_ms"` // the experiment scheduler's heartbeat interval
	StaleAfterMS     int64             `json:"stale_after_ms"`     // a lease with no heartbeat for this long is lost
	// GPUs are the worker's devices (UUIDs) the coordinator set aside for
	// this lease; the worker runs the slot on exactly these (QC2-10-09).
	GPUs []string `json:"gpus,omitempty"`
	// Kind is LeaseKindJob for a runner job lease (Job set, no slot);
	// empty for a slot lease. A runner that does not run jobs releases a
	// job lease with Refuse set.
	Kind string    `json:"kind,omitempty"`
	Job  *JobLease `json:"job,omitempty"`
	// Traceparent is the W3C trace context of the lease span when the
	// server traces (lane Z14, bench/telemetry); the worker's spans and its
	// calls for this lease join it. Empty with tracing off.
	Traceparent string `json:"traceparent,omitempty"`
}

// LeaseKindJob marks a runner job lease (WorkerLease.Kind).
const LeaseKindJob = "job"

// JobLease is one hosted CLI job a runner runs (runner jobs, gap G05): the
// runner unpacks Files (a zip of the project subset the job needs) into a
// scratch project, runs `bifrost-bench <Argv>` there with -config pointing
// at a runner-local config, streams its log lines back on heartbeats, and
// uploads a zip holding result.json and every file under the Collect paths
// the job wrote.
type JobLease struct {
	JobID   string   `json:"job_id"`
	JobKind string   `json:"job_kind"`
	Argv    []string `json:"argv"`
	Files   []byte   `json:"files,omitempty"`   // zip of the project snapshot
	Collect []string `json:"collect,omitempty"` // project-relative folders or files to bring back
	// Secrets are values only for the claiming runner (a per-job model key,
	// a repository token). The runner holds them in memory only; they are
	// never logged, stored or written to a file. Use Redacted to log a lease.
	Secrets map[string]string `json:"secrets,omitempty"`
}

// Redacted returns a copy of the lease with every secret value replaced by
// "[redacted]", for log lines and errors.
func (j JobLease) Redacted() JobLease {
	if j.Secrets == nil {
		return j
	}
	s := make(map[string]string, len(j.Secrets))
	for k := range j.Secrets {
		s[k] = "[redacted]"
	}
	j.Secrets = s
	return j
}

// JobComplete is the job part of a complete call: the job's error, empty
// when it succeeded. The result and the collected files are in the
// uploaded archive.
type JobComplete struct {
	Error string `json:"error,omitempty"`
}

// WorkerHeartbeat is the body of POST /api/bench/workers/leases/{id}/heartbeat.
type WorkerHeartbeat struct {
	WorkerID string `json:"worker_id"`
	// Logs are new log lines of a runner job lease (already redacted by
	// the runner); empty for slot leases.
	Logs []string `json:"logs,omitempty"`
	// Events are the slot run's live progress events since the last
	// heartbeat (run_started, stage), display only: the sealed run the
	// worker uploads stays the record. Empty for job leases.
	Events []WorkerEvent `json:"events,omitempty"`
}

// WorkerEvent is one live progress event of a leased slot's run. The slot
// id is the lease's; the coordinator never takes it from the worker.
type WorkerEvent struct {
	Kind    string    `json:"kind"` // run_started | stage
	RunID   string    `json:"run_id,omitempty"`
	Stage   Stage     `json:"stage,omitempty"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// Live event bounds of one heartbeat: a worker sends at most
// MaxHeartbeatEvents events, each message at most MaxWorkerEventMessage
// bytes; the routes and the coordinator cut anything longer.
const (
	MaxHeartbeatEvents    = 200
	MaxWorkerEventMessage = 1024
)

// WorkerUploadState is the answer to one upload chunk: how many bytes of the
// archive the coordinator holds for the lease.
type WorkerUploadState struct {
	LeaseID string `json:"lease_id"`
	Bytes   int64  `json:"bytes"`
}

// WorkerComplete is the body of POST /api/bench/workers/leases/{id}/complete,
// sent after the whole archive (a tar.gz of the sealed run folder) is
// uploaded. CostUSD is the cost the worker observed for the run, or unknown
// with the reason; it is never reported as zero when it was not observed.
type WorkerComplete struct {
	WorkerID      string  `json:"worker_id"`
	RunID         string  `json:"run_id"`
	SealSHA256    string  `json:"seal_sha256"` // sha256 of the run's seal.json on the worker
	ArchiveSHA256 string  `json:"archive_sha256"`
	ArchiveBytes  int64   `json:"archive_bytes"`
	Outcome       Outcome `json:"outcome"`
	CostUSD       Value   `json:"cost_usd"`
	// Gateway is the worker's own gateway preflight for this run, sent when
	// the manifest defers the preflight to runners (hosted); the
	// coordinator records it in remote/worker.json (Tenancy area).
	Gateway *GatewayManifest `json:"gateway,omitempty"`
	// Images are the worker's own image ids for this run, sent with a
	// deferred manifest, which a hosted server plans without Docker; the
	// coordinator records them in remote/worker.json (Deploy area,
	// engine/remoteimages.go).
	Images *RunnerImages `json:"images,omitempty"`
	// Job completes a runner job lease (runner jobs, gap G05).
	Job *JobComplete `json:"job,omitempty"`
}

// RunnerImages are the local image ids a runner resolved for one run of a
// deferred (hosted) manifest, by role: base, helper, toolproxy and
// service/<id> as in Manifest.SuiteImages, plus task/<task id> for a task's
// own image and agent/<arm> for the arm's agent image. A role whose image
// could not be inspected is left out (Deploy area).
type RunnerImages struct {
	IDs map[string]string `json:"ids"`
}

// WorkerRelease is the body of POST /api/bench/workers/leases/{id}/release:
// the worker gives a lease back without a sealed run. Refuse says the worker
// refused the experiment itself (a manifest hash, suite lock hash or part
// version differs), so the coordinator stops offering it to that worker.
// CostUSD is what the run spent when its agent had started; nil when no
// agent started.
type WorkerRelease struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
	Refuse   bool   `json:"refuse,omitempty"`
	CostUSD  *Value `json:"cost_usd,omitempty"`
}

// WorkerResult is the coordinator's decision on an uploaded run. A rejected
// upload is not an error of the call: Accepted is false and Reason says why,
// and the slot is released for another run.
type WorkerResult struct {
	LeaseID  string `json:"lease_id"`
	RunID    string `json:"run_id"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
	SealHash string `json:"seal_hash,omitempty"` // the coordinator's seal of the accepted run
}

// RunnerScope is what a hosted runner's token allows (Runners area,
// docs/runners.md): one organization and one pool. The worker routes put it
// on the context of every coordinator call in hosted mode; local mode has
// none, and the coordinator checks the shared worker token instead.
type RunnerScope struct {
	OrgID   string `json:"org_id"`
	Pool    string `json:"pool"`
	TokenID string `json:"token_id"` // runner_tokens.id
}

type runnerScopeKey struct{}

// WithRunnerScope returns ctx carrying a hosted runner's scope.
func WithRunnerScope(ctx context.Context, s RunnerScope) context.Context {
	return context.WithValue(ctx, runnerScopeKey{}, s)
}

// RunnerScopeFrom returns the hosted runner scope on ctx, if any.
func RunnerScopeFrom(ctx context.Context) (RunnerScope, bool) {
	if ctx == nil {
		return RunnerScope{}, false
	}
	s, ok := ctx.Value(runnerScopeKey{}).(RunnerScope)
	return s, ok
}

// RunnerWhoami is the answer of GET /api/bench/workers/whoami: what the
// caller's token is scoped to and the runners registered with it, for
// `bifrost-bench runner status`.
type RunnerWhoami struct {
	Mode    string       `json:"mode"` // local | hosted
	OrgID   string       `json:"org_id,omitempty"`
	Pool    string       `json:"pool,omitempty"`
	TokenID string       `json:"token_id,omitempty"`
	Workers []WorkerInfo `json:"workers"`          // registered with this token (local mode: every worker)
	Open    int          `json:"open_experiments"` // experiments this token may lease from now
	Now     time.Time    `json:"now"`
}

// Sentinel errors of the worker API. The HTTP handler maps them to statuses.
var (
	// ErrWorkerAuth is a missing or wrong bearer token.
	ErrWorkerAuth = errors.New("worker token missing or wrong")
	// ErrWorkerUnknown is a worker id the coordinator has no registration
	// for; the worker registers again.
	ErrWorkerUnknown = errors.New("worker not registered")
	// ErrLeaseGone is a lease the coordinator no longer holds for the worker:
	// it was lost (missed heartbeats), released, completed, or its
	// experiment stopped. A late upload for such a lease is refused.
	ErrLeaseGone = errors.New("lease gone")
	// ErrLeaseReattaching: the server kept the lease across a restart and
	// has not re-attached it yet; the worker retries (503).
	ErrLeaseReattaching = errors.New("the server restarted and is re-attaching this lease; retry shortly")
	// ErrUploadTooLarge is an archive past the coordinator's size cap.
	ErrUploadTooLarge = errors.New("upload too large")
	// ErrUploadOffset is a chunk whose offset is not the number of bytes the
	// coordinator already holds.
	ErrUploadOffset = errors.New("upload offset mismatch")
	// ErrWorkerRequest is a malformed worker request.
	ErrWorkerRequest = errors.New("bad worker request")
	// ErrWorkerScope is a hosted runner call outside its token's
	// organization or pool, or for a runner registered with another token
	// (Runners area). The handler answers 403.
	ErrWorkerScope = errors.New("outside the runner token's scope")
)

type heartbeatLogsKey struct{}

// WithHeartbeatLogs returns ctx carrying the log lines a heartbeat brought
// (runner job leases); the worker routes set it so the WorkerAPI keeps its
// signature.
func WithHeartbeatLogs(ctx context.Context, lines []string) context.Context {
	if len(lines) == 0 {
		return ctx
	}
	return context.WithValue(ctx, heartbeatLogsKey{}, lines)
}

type heartbeatEventsKey struct{}

// WithHeartbeatEvents returns ctx carrying the live events a heartbeat of a
// slot lease brought.
func WithHeartbeatEvents(ctx context.Context, events []WorkerEvent) context.Context {
	if len(events) == 0 {
		return ctx
	}
	return context.WithValue(ctx, heartbeatEventsKey{}, events)
}

// HeartbeatEventsFrom returns the heartbeat live events on ctx, if any.
func HeartbeatEventsFrom(ctx context.Context) []WorkerEvent {
	if ctx == nil {
		return nil
	}
	e, _ := ctx.Value(heartbeatEventsKey{}).([]WorkerEvent)
	return e
}

// HeartbeatLogsFrom returns the heartbeat log lines on ctx, if any.
func HeartbeatLogsFrom(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(heartbeatLogsKey{}).([]string)
	return l
}
