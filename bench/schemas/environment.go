package schemas

import (
	"context"
	"time"
)

// Environment creates, runs in and destroys the isolated workspace of one run.
type Environment interface {
	Part
	// Create builds a fresh workspace from the task repo, installs the clean
	// notes file and the arm's files, and returns its handle.
	Create(ctx context.Context, rc *RunContext) (*EnvironmentHandle, error)
	// Exec runs the agent's plan in the workspace and waits for it, enforcing
	// the wall budget. The raw agent stdout is written to the run folder.
	Exec(ctx context.Context, rc *RunContext, plan *RunPlan) (*ExecResult, error)
	// ExportDiff extracts the workspace changes in a trusted container and
	// applies the path allowlist.
	ExportDiff(ctx context.Context, rc *RunContext) (*Diff, error)
	// Destroy removes every container, volume and network of the run. It is
	// safe to call more than once and after a failed Create.
	Destroy(ctx context.Context, rc *RunContext) error
}

// EnvironmentHandle identifies a run's workspace.
type EnvironmentHandle struct {
	Volume      string            `json:"volume"`       // docker volume holding /workspace
	AuxVolume   string            `json:"aux_volume"`   // per-run volume mounted at /opt/bench: tool shims in shim/ (prepended to PATH), shim logs in log/; never part of the diff
	Network     string            `json:"network"`      // internal evaluation network
	Workdir     string            `json:"workdir"`      // path inside containers
	BaselineRef string            `json:"baseline_ref"` // commit or tree id of the starting state
	Labels      map[string]string `json:"labels"`
	FileHashes  map[string]string `json:"file_hashes"` // sha256 of every file laid down at prepare (notes, arm files)
	Protected   map[string]string `json:"protected"`   // sha256 of protected files before the run (G1)
}

// ExecResult is how the agent process ended.
type ExecResult struct {
	ExitCode   int       `json:"exit_code"`
	TimedOut   bool      `json:"timed_out"`
	Started    time.Time `json:"started"`
	Ended      time.Time `json:"ended"`
	StdoutPath string    `json:"stdout_path"` // agent.stream.jsonl in the run folder
	StderrPath string    `json:"stderr_path"`
	Truncated  bool      `json:"truncated"` // output exceeded the capture cap
	// OOMKilled is true when the kernel's out-of-memory killer ended the
	// agent (docker State.OOMKilled from the container's cgroup memory
	// events, or a Kubernetes container terminated with reason OOMKilled).
	// The run is then an infrastructure error, never the agent's failure.
	OOMKilled bool `json:"oom_killed,omitempty"`
}

// Diff is the exported workspace patch.
type Diff struct {
	Path     string       `json:"path"` // workspace.diff in the run folder
	SHA256   string       `json:"sha256"`
	Files    []DiffFile   `json:"files"`
	Rejected []DiffReject `json:"rejected,omitempty"` // paths dropped by the allowlist
}

// DiffFile is one changed path.
type DiffFile struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added | modified | deleted
}

// DiffReject is a path the allowlist dropped, with the reason.
type DiffReject struct {
	Path   string `json:"path"`
	Reason string `json:"reason"` // symlink | device | submodule | outside_workspace | cache
}
