package schemas

import "errors"

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
}

// WorkerHeartbeat is the body of POST /api/bench/workers/leases/{id}/heartbeat.
type WorkerHeartbeat struct {
	WorkerID string `json:"worker_id"`
}

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
	// ErrUploadTooLarge is an archive past the coordinator's size cap.
	ErrUploadTooLarge = errors.New("upload too large")
	// ErrUploadOffset is a chunk whose offset is not the number of bytes the
	// coordinator already holds.
	ErrUploadOffset = errors.New("upload offset mismatch")
	// ErrWorkerRequest is a malformed worker request.
	ErrWorkerRequest = errors.New("bad worker request")
)
