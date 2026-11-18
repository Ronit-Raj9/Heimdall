package environments

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Kubernetes area (X3): the pieces of the docker_workspace environment that
// another driver reuses unchanged (bench/k8s). Each is a thin export of the
// code the Docker driver runs, so both drivers seed, launch, observe and
// export a run the same way; the behaviour stays where it always was.

// Container paths and defaults of the docker_workspace environment.
const (
	AgentHome      = agentHome
	AuxMount       = auxMount
	DefaultWorkdir = defaultWorkdir
	DefaultUser    = defaultUser
	DefaultPids    = defaultPids
	DefaultWallS   = defaultWallS
	DefaultPath    = defaultPath
	BaselineDate   = baselineDate
	// FSWatchMaxEvents is the workspace observer's detail cap, its argv[1].
	FSWatchMaxEvents = fsWatchMaxEvents
	// MaxDiffBytes caps an exported patch.
	MaxDiffBytes = maxDiffBytes
	// DefaultOutputCap caps each captured agent stream.
	DefaultOutputCap = defaultOutputCap
)

// Scripts the trusted helper and the observers run.
var (
	// PopulateScript seeds /workspace (BENCH_SRC_TAR=1: from a tar on stdin)
	// and prints the baseline commit id last.
	PopulateScript = populateScript
	// ExportScript prints the workspace's patch against $BENCH_BASE.
	ExportScript = exportScript
	// ProcWatchScript and FSWatchScript are the observers' python programs.
	ProcWatchScript = procwatchScript
	FSWatchScript   = fswatchScript
)

// RunIDs returns the experiment and run ids of a run context.
func RunIDs(rc *schemas.RunContext) (string, string, error) { return runIDs(rc) }

// TaskRepo is the real path of the task's repo folder.
func TaskRepo(rc *schemas.RunContext) (string, error) { return taskRepo(rc) }

// WorkspacePath is the task image folder that seeds the workspace, or "".
func WorkspacePath(rc *schemas.RunContext) string { return workspacePath(rc) }

// CleanNotes returns where the clean notes file goes and its host file.
func CleanNotes(rc *schemas.RunContext) (string, string, error) { return cleanNotes(rc) }

// ProtectedHashes hashes every repo file matching one of globs.
func ProtectedHashes(repoDir string, globs []string) (map[string]string, error) {
	return protectedHashes(repoDir, globs)
}

// Workdir is the suite's workdir, /workspace by default.
func Workdir(rc *schemas.RunContext) string { return workdir(rc) }

// LastRef reads the baseline commit id from the populate script's output.
func LastRef(out []byte) (string, error) { return lastRef(out) }

// IsRoot reports whether a user value is root.
func IsRoot(user string) bool { return isRoot(user) }

// FrozenImageID is the agent image id the manifest froze for the run's arm.
func FrozenImageID(rc *schemas.RunContext) string { return frozenImageID(rc) }

// StreamPaths are the run folder files of the agent's stdout and stderr.
func StreamPaths(rc *schemas.RunContext) (string, string, int) { return streamPaths(rc) }

// WithTaskEnv is plan with the task environment's variables (environment.env)
// added under the plan's own, as the Docker driver gives the agent; plan
// itself is not changed.
func WithTaskEnv(rc *schemas.RunContext, plan *schemas.RunPlan) *schemas.RunPlan {
	return withTaskEnv(rc, plan)
}

// AgentLaunch is how the agent process starts: the entrypoint ("" keeps the
// image's), the arguments after it and the KEY=VALUE environment.
type AgentLaunch struct {
	Entrypoint string
	Args       []string
	Env        []string
}

// BuildAgentLaunch is the Docker driver's launch for a plan with no links:
// plan files and long values ride in BENCH_WRAP_* variables decoded by a
// `sh -c` wrapper, PATH is pathValue and HOME the agent home.
func BuildAgentLaunch(plan *schemas.RunPlan, pathValue string) (*AgentLaunch, error) {
	l, err := buildLaunch(plan, pathValue, agentHome, nil)
	if err != nil {
		return nil, err
	}
	return &AgentLaunch{Entrypoint: l.Entrypoint, Args: l.Args, Env: l.Env}, nil
}

// FilteredDiff is a patch after the path allowlist.
type FilteredDiff struct {
	Kept     []byte
	Files    []schemas.DiffFile
	Rejected []schemas.DiffReject
}

// FilterDiff applies the path allowlist to a raw patch.
func FilterDiff(raw []byte) (*FilteredDiff, error) {
	f, err := filterDiff(raw)
	if err != nil {
		return nil, err
	}
	return &FilteredDiff{Kept: f.Kept, Files: f.Files, Rejected: f.Rejected}, nil
}

// OutputWriter caps one captured agent stream and removes secrets from it.
type OutputWriter struct {
	capped *cappedWriter
	red    *redactingWriter
}

// NewOutputWriter writes at most limit bytes of the stream to w with every
// secret replaced; Close flushes.
func NewOutputWriter(w io.Writer, limit int64, secrets ...string) *OutputWriter {
	red := newRedactingWriter(w, secrets...)
	o := &OutputWriter{capped: &cappedWriter{w: red, limit: limit}}
	if rw, ok := red.(*redactingWriter); ok {
		o.red = rw
	}
	return o
}

// Write implements io.Writer; it never fails the writer.
func (o *OutputWriter) Write(p []byte) (int, error) { return o.capped.Write(p) }

// Truncated reports whether the stream went over the cap.
func (o *OutputWriter) Truncated() bool { return o.capped.truncated }

// Close flushes the redacted stream and returns the first write error.
func (o *OutputWriter) Close() error {
	var ferr error
	if o.red != nil {
		ferr = o.red.Flush()
	}
	return firstErr(o.capped.err, ferr)
}

// ParseProcLines turns process observer lines into records and a status.
func ParseProcLines(lines [][]byte, secret string, session int, agentEnd time.Time, st schemas.ObserverStatus) ([]schemas.ProcessRecord, schemas.ObserverStatus) {
	return parseProcLines(lines, secret, session, agentEnd, st)
}

// ParseFSLines turns workspace observer lines into file events and a
// status, with secrets redacted from paths.
func ParseFSLines(lines [][]byte, secret string, session int, st schemas.ObserverStatus) ([]schemas.FileEvent, schemas.ObserverStatus) {
	evs, st := parseFSLines(lines, session, st)
	for i := range evs {
		evs[i].Path, evs[i].From = redact(evs[i].Path, secret), redact(evs[i].From, secret)
	}
	return evs, st
}

// WriteProcessRecords writes processes.jsonl into the run folder.
func WriteProcessRecords(dir string, session int, recs []schemas.ProcessRecord) error {
	return writeJSONL(dir, schemas.ProcessesFile, session, recs)
}

// WriteFileEvents writes the workspace observer's file events.
func WriteFileEvents(dir string, session int, evs []schemas.FileEvent) error {
	return writeJSONL(dir, schemas.FSEventsFile, session, evs)
}

// AppendObserverStatus adds statuses to observers.json.
func AppendObserverStatus(dir string, add ...schemas.ObserverStatus) error {
	return appendObserverStatus(dir, add...)
}

// WriteCollectorState records whether the agent event collector ran.
func WriteCollectorState(rc *schemas.RunContext, state, reason string) {
	writeCollectorState(rc, state, reason)
}

// MaxLedgerBytes caps a copied collector ledger.
const MaxLedgerBytes = maxLedgerBytes

// AgentEventEnv adds the agent event variables the Docker driver gives an
// agent (eventEnvView, standard view) to env: the collector URL and the
// OpenTelemetry exporter variables when collector is true, each only when
// the agent config did not set it. The events file variable is left out:
// another driver has no reader for the Docker aux volume it names.
func AgentEventEnv(rc *schemas.RunContext, runID string, env []string, collector bool) []string {
	e := newDockerWorkspace(DockerWorkspaceConfig{EventsCollector: true})
	if collector {
		if _, had := collectorRuns.LoadOrStore(runID, true); !had {
			defer collectorRuns.Delete(runID)
		}
	}
	all := e.eventEnvView(rc, runID, env, standardView)
	out := make([]string, 0, len(all))
	for _, kv := range all[len(env):] {
		if strings.HasPrefix(kv, "BENCH_EVENTS_FILE=") {
			continue
		}
		out = append(out, kv)
	}
	return append(append([]string(nil), env...), out...)
}

// Answer export pieces (answer.go): the helper script, its exit codes and
// cap, and the answer file's path relative to the workspace.
const (
	AnswerScript      = answerScript
	AnswerExitMissing = answerMissing
	AnswerExitEscapes = answerEscapes
	MaxAnswerBytes    = maxAnswerBytes
)

// AnswerRel is the answer file relative to the workspace root.
func AnswerRel(rc *schemas.RunContext) (string, error) { return answerRel(rc) }

// Artifact export pieces (artifacts.go).

// ArtifactScript streams the matched artifact files as a tar (manifest.json
// first) from the trusted helper; argv holds the workspace roots.
const ArtifactScript = artifactScript

// ArtifactEnv are the helper's environment values for the run's artifact
// specs (BENCH_SPECS, BENCH_TOTAL, BENCH_MATCHES), after the same spec
// checks the Docker driver makes.
func ArtifactEnv(rc *schemas.RunContext) (map[string]string, []schemas.ArtifactSpec, error) {
	specs := utils.OutputArtifacts(rc.Suite, rc.Task)
	reqs := make([]artifactRequest, 0, len(specs))
	for _, a := range specs {
		if err := utils.CheckArtifactSpec(a); err != nil {
			return nil, nil, err
		}
		reqs = append(reqs, artifactRequest{ID: a.ID, Path: a.Path, MaxBytes: utils.ArtifactMaxBytes(a)})
	}
	payload, err := json.Marshal(reqs)
	if err != nil {
		return nil, nil, err
	}
	return map[string]string{"BENCH_SPECS": string(payload), "BENCH_TOTAL": strconv.FormatInt(utils.MaxArtifactTotalBytes, 10),
		"BENCH_MATCHES": strconv.Itoa(utils.MaxArtifactMatches)}, specs, nil
}

// ReadArtifactStream reads the helper's stream into dir with every cap
// enforced again, then drains at most the allowed trailer.
func ReadArtifactStream(r io.Reader, dir string, specs []schemas.ArtifactSpec) ([]schemas.ArtifactFile, error) {
	files, err := readArtifactStream(r, dir, specs)
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(io.Discard, io.LimitReader(r, maxArtifactTrailerBytes))
	switch {
	case err != nil:
		return nil, fmt.Errorf("read helper stream: %w", err)
	case n >= maxArtifactTrailerBytes:
		return nil, errors.New("helper wrote past the end of its stream")
	}
	return files, nil
}

// ResetArtifactsDir empties the run's artifacts folder.
func ResetArtifactsDir(rc *schemas.RunContext) (string, error) {
	dir := filepath.Join(rc.Dir, utils.ArtifactsDir)
	return dir, resetDir(dir)
}
