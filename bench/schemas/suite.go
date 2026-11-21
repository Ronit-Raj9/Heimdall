package schemas

import "time"

// Suite is one benchmark pack: a versioned set of tasks, probes, a grader type
// and an environment type, read from benchmarks/<id>/suite.yaml.
type Suite struct {
	ID          string        `json:"id" yaml:"id"`
	Version     string        `json:"version" yaml:"version"`
	Description string        `json:"description,omitempty" yaml:"description"`
	Environment string        `json:"environment" yaml:"environment"` // environment part name, e.g. docker_workspace
	Grader      string        `json:"grader" yaml:"grader"`           // grader part name, e.g. hidden_tests
	BaseImage   ImageSpec     `json:"base_image" yaml:"base_image"`
	Workdir     string        `json:"workdir" yaml:"workdir"`
	Verifier    SuiteVerifier `json:"verifier" yaml:"verifier"`
	TaskIDs     []string      `json:"tasks" yaml:"tasks"`
	ProbeIDs    []string      `json:"probes" yaml:"probes"`

	// Scenario fields (scenario.go). All optional.
	Track    string        `json:"track,omitempty" yaml:"track"`               // leaderboard track; empty means coding
	Grading  string        `json:"grading,omitempty" yaml:"grading"`           // verified (default) | judged
	Output   *OutputSpec   `json:"output,omitempty" yaml:"output"`             // what the verifier reads; default diff
	Services []ServiceSpec `json:"services,omitempty" yaml:"services"`         // started beside the agent in every run
	Judge    *JudgeSpec    `json:"judge,omitempty" yaml:"judge"`               // required when Grading is judged
	Imported *ImportRecord `json:"imported,omitempty" yaml:"imported"`         // set by `bifrost-bench import`
	External []ExternalAPI `json:"external,omitempty" yaml:"external"`         // real HTTP APIs every task's agent may call through the tool proxy
	Canary   string        `json:"canary,omitempty" yaml:"canary"`             // contamination canary GUID, set by `suite canary` (docs/held-out-and-canaries.md)
	Realism  string        `json:"realism,omitempty" yaml:"realism,omitempty"` // Eval integrity area (S2): standard (default) | neutral; an experiment's realism overrides it

	// X4: optional external scanners on the post-run tree (codescan.go).
	CodeScan *SuiteCodeScan `json:"codescan,omitempty" yaml:"codescan,omitempty"`

	Simulator *SimulatorSpec `json:"simulator,omitempty" yaml:"simulator"` // W3 customer support: user simulator settings, pinned like a judge (scenario.go)

	// Requires lists the agent capabilities the benchmark needs. Empty derives
	// them from Track and Environment (suite.Requires).
	Requires CapabilitySet `json:"requires,omitempty" yaml:"requires,omitempty"`

	Tiers map[string]TaskSelector `json:"tiers,omitempty" yaml:"tiers,omitempty"` // W7 suite tiers: pr, nightly, headroom (docs/suite-tiers.md)

	Root     string       `json:"-" yaml:"-"` // absolute folder of the pack
	Tasks    []*Task      `json:"-" yaml:"-"` // loaded tasks, in TaskIDs order
	Probes   []*ProbeSpec `json:"-" yaml:"-"` // loaded probes, in ProbeIDs order
	Lock     *SuiteLock   `json:"-" yaml:"-"` // nil until locked
	LockHash string       `json:"lock_hash,omitempty" yaml:"-"`
}

// ImageSpec names a container image and, optionally, how to build it.
type ImageSpec struct {
	Dockerfile string `json:"dockerfile,omitempty" yaml:"dockerfile"` // relative to the suite root
	Tag        string `json:"tag" yaml:"tag"`
	Digest     string `json:"digest,omitempty" yaml:"digest"` // filled by `suite lock`
	// BaseArg, on an agent's build, names a Dockerfile ARG that receives the
	// suite's base image. The agent image is then built once per suite, on top
	// of that suite's toolchain, and tagged "<tag>-<suite id>". Empty keeps one
	// image for every suite.
	BaseArg string `json:"base_arg,omitempty" yaml:"base_arg"`
	// Context, on an agent's build, is the docker build context, relative to
	// the bench root. Empty uses the Dockerfile's folder.
	Context string `json:"context,omitempty" yaml:"context"`
}

// SuiteVerifier is the grading command and restore policy shared by every task
// of a suite.
type SuiteVerifier struct {
	Command  []string `json:"command" yaml:"command"`
	Restore  []string `json:"restore" yaml:"restore"` // paths reset to the pristine repo before grading
	TimeoutS int      `json:"timeout_s" yaml:"timeout_s"`

	// Result is how the command reports, so a suite in any language can be
	// graded: "junit" (default; a JUnit XML file), "reward" (a file holding a
	// number, or a JSON object {"reward": n, "dimensions": {...}}) or
	// "exit_code" (0 passes). ResultPath is the file inside the verifier
	// container (default /grader/out/junit.xml or /grader/out/reward.json).
	// PassThreshold is the lowest reward that passes (default 1).
	Result        string  `json:"result,omitempty" yaml:"result"`
	ResultPath    string  `json:"result_path,omitempty" yaml:"result_path"`
	PassThreshold float64 `json:"pass_threshold,omitempty" yaml:"pass_threshold"`

	// Mode is clean_room (default) or snapshot (scenario.go).
	Mode string `json:"mode,omitempty" yaml:"mode"`
}

// Verifier result formats (SuiteVerifier.Result).
const (
	VerifierResultJUnit    = "junit"
	VerifierResultReward   = "reward"
	VerifierResultExitCode = "exit_code"
)

// TaskByID returns the loaded task with id, or nil.
func (s *Suite) TaskByID(id string) *Task {
	for _, t := range s.Tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// ProbeByID returns the loaded probe with id, or nil.
func (s *Suite) ProbeByID(id string) *ProbeSpec {
	for _, p := range s.Probes {
		if p.ID == id {
			return p
		}
	}
	return GenericProbe(id) // lane A07: generic fault probes (genericprobes.go)
}

// SuiteLock pins every task and probe of a suite version by hash
// (benchmarks/<id>/suite.lock, JSON).
type SuiteLock struct {
	Suite     string               `json:"suite"`
	Version   string               `json:"version"`
	BaseImage ImageSpec            `json:"base_image"`
	Tasks     map[string]TaskLock  `json:"tasks"`
	Probes    map[string]ProbeLock `json:"probes"`
	CreatedAt time.Time            `json:"created_at"`

	// Fingerprints binds every evaluator setting, every file the suite,
	// its tasks and probes name, and the image identities
	// (docs/lock-fingerprints.md). Nil in a legacy lock, which then encodes
	// byte for byte as it did before the section existed.
	Fingerprints *LockFingerprints `json:"fingerprints,omitempty"`
}

// LockFingerprintsVersion is the fingerprint format `suite lock` writes.
const LockFingerprintsVersion = 1

// LockFingerprints is the versioned fingerprint section of a suite lock.
type LockFingerprints struct {
	Version int                    `json:"version"`
	Suite   Fingerprint            `json:"suite"`
	Tasks   map[string]Fingerprint `json:"tasks"`
	Probes  map[string]Fingerprint `json:"probes"`

	Calibration string `json:"calibration,omitempty"` // W8 judge calibration: sha256 of calibration/labels.jsonl, set only when the suite has one
}

// Fingerprint pins one suite, task or probe. Settings is the sha256 of the
// canonical JSON of its evaluator settings and Fields the sha256 of each
// top-level setting, so drift names the setting that changed. Files is the
// sha256 over every file and folder the settings name, volatile junk
// excluded. Images maps an image role to its identity: "<tag>@<local image
// id>", or "tag only: <tag>" when the image could not be inspected.
type Fingerprint struct {
	Settings string            `json:"settings"`
	Fields   map[string]string `json:"fields,omitempty"`
	Files    string            `json:"files"`
	Images   map[string]string `json:"images,omitempty"`
}

// FingerprintDrift is one difference between the fingerprints a lock
// recorded and the ones computed from the files on disk.
type FingerprintDrift struct {
	Scope  string   `json:"scope"`            // suite | task | probe
	ID     string   `json:"id"`               // suite, task or probe id
	Part   string   `json:"part"`             // settings | files | image
	Fields []string `json:"fields,omitempty"` // settings keys or image roles that differ
}

// TaskLock pins one task version. A comparison is valid only between runs of
// identical task locks.
type TaskLock struct {
	Version           string           `json:"version"`
	InstructionSHA256 string           `json:"instruction_sha256"`
	RepoTreeSHA256    string           `json:"repo_tree_sha256"`
	VerifierSHA256    string           `json:"verifier_sha256"`
	ControlsSHA256    string           `json:"controls_sha256"`
	FixturesSHA256    string           `json:"fixtures_sha256"`
	Status            TaskStatus       `json:"status"`
	Admission         *AdmissionRecord `json:"admission,omitempty"`
}

// ProbeLock pins one probe version.
type ProbeLock struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// TaskStatus is a task's lifecycle state.
type TaskStatus string

// Task lifecycle states.
const (
	TaskDraft       TaskStatus = "draft"
	TaskAdmitted    TaskStatus = "admitted"
	TaskQuarantined TaskStatus = "quarantined"
	TaskRetired     TaskStatus = "retired"
)

// AdmissionRecord is the result of `suite admit` for one task version.
type AdmissionRecord struct {
	Admitted bool             `json:"admitted"`
	Checks   []AdmissionCheck `json:"checks"`
	At       time.Time        `json:"at"`
	// Grader names the grader part that ran the checks. ReferenceRepeats is
	// how many times in a row the reference solution was graded for the
	// determinism check, and ReferenceResults the outcome of each run (pass,
	// fail, invalid_evaluator or error). All three are empty in records
	// written before they existed.
	Grader           string   `json:"grader,omitempty"`
	ReferenceRepeats int      `json:"reference_repeats,omitempty"`
	ReferenceResults []string `json:"reference_results,omitempty"`
	// Fingerprint is the sha256 of the suite and task fingerprints (settings
	// and files) the checks ran against; empty in a record written before
	// lock fingerprints existed. A record whose fingerprint no longer matches
	// the lock admits nothing.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// AdmissionCheck is one admission control (reference passes, empty patch
// fails, mutant fails, exploit scores 0, null agent scores 0, probe keeps the
// reference passing, instruction edited, no secrets, verifier without
// network).
type AdmissionCheck struct {
	Name   string `json:"name"`
	Want   string `json:"want"`
	Got    string `json:"got"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}
