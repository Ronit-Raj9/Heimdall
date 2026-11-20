package schemas

import (
	"context"
	"errors"
	"time"
)

// Store owns the run folder, the only source of truth (DESIGN.md 16.1). The
// in-memory index the API reads is rebuilt from it at start.
type Store interface {
	Part
	Root() string
	// CreateExperiment writes manifest.json and every slot.json. It refuses to
	// overwrite an existing experiment.
	CreateExperiment(ctx context.Context, m *Manifest) error
	LoadManifest(ctx context.Context, expID string) (*Manifest, error)
	ListExperiments(ctx context.Context) ([]string, error)
	// NewRun creates the run folder for a slot and returns its path.
	NewRun(ctx context.Context, slot *Slot, run *Run) (string, error)
	// AppendTransition appends to the run's transitions.jsonl and fsyncs.
	AppendTransition(ctx context.Context, run *Run, t Transition) error
	// WriteRunFile writes one file into an unsealed run folder.
	WriteRunFile(ctx context.Context, run *Run, name string, data []byte) error
	// Lease takes the slot for owner. It returns false when another live owner
	// holds it; a lease whose heartbeat is older than staleAfter is reclaimed.
	Lease(ctx context.Context, slotID, owner string, staleAfter time.Duration) (bool, error)
	Heartbeat(ctx context.Context, slotID, owner string) error
	Release(ctx context.Context, slotID, owner string) error
	// Seal hashes every file of the run folder into seal.json, chains it to
	// the previous seal of the experiment, and makes the folder read-only.
	Seal(ctx context.Context, run *Run) (*SealRecord, error)
	// Verify recomputes a run's seal.
	Verify(ctx context.Context, expID, runID string) error
	// SlotRuns returns every run of a slot, sealed or not, with its transitions.
	SlotRuns(ctx context.Context, expID, slotID string) ([]RunRecord, error)
	// SealedRuns loads every counted, sealed run with the chosen grade source.
	SealedRuns(ctx context.Context, expID, gradeSource string) ([]SealedRun, error)
	// WriteRegrade writes a child artifact outside the sealed run.
	WriteRegrade(ctx context.Context, expID, regradeID, runID string, g *Grade) error
	WriteAnalysis(ctx context.Context, a *Analysis, files map[string][]byte) error
}

// RunRecord is a run with its transition log, for resume.
type RunRecord struct {
	Run         Run          `json:"run"`
	Dir         string       `json:"dir"`
	Transitions []Transition `json:"transitions"`
	Sealed      bool         `json:"sealed"`
	// SealPending marks a sealed run whose seal was never finalized: seal.json
	// was written but the folder was not yet made read-only, so its seals.log
	// entry may be missing. Store.Seal on it finishes the seal.
	SealPending bool `json:"seal_pending,omitempty"`
}

// SealRecord is the content of seal.json. The run's seal hash is the sha256 of
// seal.json, which lists every other file of the folder and not itself.
type SealRecord struct {
	RunID        string            `json:"run_id"`
	ManifestHash string            `json:"manifest_hash"`
	Files        map[string]string `json:"files"` // relative path -> sha256
	PrevSeal     string            `json:"prev_seal"`
	SealedAt     time.Time         `json:"sealed_at"`
	// Format names the mandatory artifact set Seal checked before writing
	// this record (store.SealFormatV1). Empty marks a legacy seal or a run
	// without a canonical record: its files are certified as present, not
	// as a complete current-format run (audit 1-A-05).
	Format string `json:"format,omitempty"`
	Hash   string `json:"-"` // sha256 of seal.json bytes, filled when read
}

// Sentinel errors shared across packages.
var (
	ErrExists       = errors.New("already exists")
	ErrNotFound     = errors.New("not found")
	ErrSealed       = errors.New("run is sealed")
	ErrSealMismatch = errors.New("seal verification failed")
	ErrLeaseHeld    = errors.New("lease held by another owner")
	// ErrLeaseBusy is a lease change that gave up waiting for the slot's lock:
	// another party (perhaps a suspended process) holds it. It says nothing
	// about whether the lease itself is live or stale.
	ErrLeaseBusy = errors.New("slot lease lock busy")
	// ErrNotLaunched marks an environment Exec error that is certain the
	// agent process never started (a create or network join failed before
	// launch). The pipeline then treats the attempt as aborted before agent
	// start, which the scheduler retries, instead of a counted infra_error.
	ErrNotLaunched = errors.New("agent never launched")
)
