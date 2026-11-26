package issuebench

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// Issues on a worker (lanes W01-W12, audit/ISSUEWORKER_CONTRACT.md). On the
// hosted site the server never runs Docker: building and testing one issue
// is one runner job of kind WorkJobKind, run in process on the org's own
// worker or the shared worker. The job's snapshot holds WorkRequestFile; its
// result is a WorkResult. Secrets travel in schemas.JobLease.Secrets under
// the Secret* names, are held in memory only and are redacted from every
// log line (RedactValues).

// Job kind and runner command.
const (
	WorkJobKind      = "issuebench.work"
	WorkCommand      = "issuebench"
	WorkSubcommand   = "work"
	WorkRequestFile  = "issuebench-job/request.json"
	WorkResultFile   = "issuebench-job/result.json"
	WorkModeBuild    = "build"    // build the environment, then test the candidate in it
	WorkModeValidate = "validate" // test the candidate in the given environment
)

// Lease secret names (schemas.JobLease.Secrets).
const (
	SecretGitHubToken      = "github_token"       // only for a private repository
	SecretGatewayKey       = "gateway_key"        // the per-job Bifrost virtual key (x-bf-vk)
	SecretGatewayURL       = "gateway_url"        // the gateway as the shared worker reaches it
	SecretGatewayPublicURL = "gateway_public_url" // the gateway as an org's own worker reaches it
)

// Size and time caps.
const (
	MaxWorkRequestBytes       = 4 << 20
	MaxWorkResultBytes        = 8 << 20
	MaxRepoArchiveBytes       = 8 << 20 // environment/repo.tar.gz of one emitted task
	MaxWorkWallSeconds        = 4 * 60 * 60
	DefaultTestTimeoutSeconds = 30 * 60
)

// Docker labels on everything a work job starts.
const (
	LabelWork = "heimdall.issuebench"     // build | validate | egress
	LabelJob  = "heimdall.issuebench.job" // the job id
	LabelOrg  = "heimdall.org"
)

// Pipeline stages a job is noted for (NoteJob).
const (
	StageBuild    = "build"
	StageValidate = "validate"
	StageChecks   = "checks"
)

// EgressAllowEnv adds hosts (comma separated) to DefaultEgressHosts for the
// builder's package installs.
const EgressAllowEnv = "BENCH_ISSUEBENCH_EGRESS_ALLOW"

// DefaultEgressHosts are the only hosts a build sandbox reaches (Python).
var DefaultEgressHosts = []string{"pypi.org", "files.pythonhosted.org", "deb.debian.org", "security.debian.org"}

// WorkArgv is the argv of every work job lease.
func WorkArgv() []string {
	return []string{WorkCommand, WorkSubcommand, "-request", WorkRequestFile, "-result", WorkResultFile}
}

// WorkLimits bound one work job; zero fields take the builder's defaults.
type WorkLimits struct {
	SetupSteps            int `json:"setup_steps,omitempty"`
	VerifySteps           int `json:"verify_steps,omitempty"`
	FeedbackRounds        int `json:"feedback_rounds,omitempty"`
	Attempts              int `json:"attempts,omitempty"`
	CmdTimeoutSeconds     int `json:"cmd_timeout_seconds,omitempty"`
	AttemptTimeoutSeconds int `json:"attempt_timeout_seconds,omitempty"`
	TestTimeoutSeconds    int `json:"test_timeout_seconds,omitempty"`
}

// WorkRequest is issuebench-job/request.json. It never holds a secret.
type WorkRequest struct {
	Mode        string     `json:"mode"`
	OrgID       string     `json:"org_id"`
	JobID       string     `json:"job_id"`
	Repo        string     `json:"repo"`
	Version     string     `json:"version"`
	Candidate   Candidate  `json:"candidate"`
	Env         *EnvSpec   `json:"env,omitempty"` // validate: required; build: the prior env as a hint
	Private     bool       `json:"private"`       // the fetch needs SecretGitHubToken
	Provider    string     `json:"provider,omitempty"`
	Model       string     `json:"model,omitempty"`
	MaxUSD      float64    `json:"max_usd,omitempty"`
	Limits      WorkLimits `json:"limits"`
	EgressHosts []string   `json:"egress_hosts,omitempty"` // empty: DefaultEgressHosts
}

var (
	workRepoRE    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	workSHARE     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	workVersionRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}$`)
	workHostRE    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// Check validates a request on both sides of the lease.
func (r WorkRequest) Check() error {
	switch {
	case r.Mode != WorkModeBuild && r.Mode != WorkModeValidate:
		return errors.New("mode must be build or validate")
	case r.OrgID == "" || r.JobID == "":
		return errors.New("org_id and job_id are required")
	case !workRepoRE.MatchString(r.Repo) || strings.Contains(r.Repo, ".."):
		return errors.New("repo must be owner/name")
	case r.Candidate.Repo != r.Repo:
		return errors.New("candidate.repo must equal repo")
	case !workSHARE.MatchString(r.Candidate.BaseSHA):
		return errors.New("candidate.base_sha must be a full commit id")
	case !workVersionRE.MatchString(r.Version):
		return errors.New("version must be letters, digits, . _ - only")
	case strings.TrimSpace(r.Candidate.TestPatch) == "" || strings.TrimSpace(r.Candidate.FixPatch) == "":
		return errors.New("candidate needs test_patch and fix_patch")
	case r.Mode == WorkModeBuild && (r.Provider == "" || r.Model == "" || r.MaxUSD <= 0):
		return errors.New("a build needs provider, model and max_usd")
	case r.Mode == WorkModeValidate && (r.Env == nil || strings.TrimSpace(r.Env.Dockerfile) == ""):
		return errors.New("a validate job needs env with a Dockerfile")
	case len(r.EgressHosts) > 32:
		return errors.New("at most 32 egress hosts")
	}
	for _, h := range r.EgressHosts {
		if !workHostRE.MatchString(h) {
			return fmt.Errorf("egress host %q is not a host name", h)
		}
	}
	return nil
}

// Hosts is the request's egress allowlist: the request's hosts (or
// DefaultEgressHosts), the package hosts of the request's language and the
// operator's EgressAllowEnv hosts, at most 32.
func (r WorkRequest) Hosts() []string {
	base := r.EgressHosts
	if len(base) == 0 {
		base = DefaultEgressHosts
	}
	lang := r.Candidate.Language
	if r.Env != nil && r.Env.Language != "" {
		lang = r.Env.Language
	}
	var out []string
	seen := map[string]bool{}
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] || len(out) >= 32 || !workHostRE.MatchString(h) {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, h := range base {
		add(h)
	}
	if l := LanguageFor(lang); l != nil {
		for _, h := range ToolchainOf(l).EgressHosts() {
			add(h)
		}
	}
	for _, h := range strings.Split(os.Getenv(EgressAllowEnv), ",") {
		add(h)
	}
	return out
}

// WorkResult is the job's result.json.
type WorkResult struct {
	Mode       string       `json:"mode"`
	Worker     string       `json:"worker,omitempty"` // set by the server from the lease
	Build      *BuildResult `json:"build,omitempty"`
	Validation *Validation  `json:"validation,omitempty"` // tests only: Checks is zero
	Stop       string       `json:"stop,omitempty"`       // plain words when the job ended before a result
	CostUSD    float64      `json:"cost_usd"`
	Seconds    float64      `json:"seconds"`
}

// WorkParams are the params of an issuebench.work job (stored by the job
// runner: no secret here).
type WorkParams struct {
	Request WorkRequest `json:"request"`
	KeyID   string      `json:"key_id,omitempty"`
	Consent bool        `json:"consent"`
}

// Readiness is GET /api/bench/issuebench/readiness.
type Readiness struct {
	Hosted       bool   `json:"hosted"`
	WorkerOnline bool   `json:"worker_online"` // a worker takes issuebench.work now; always true locally
	GitHub       bool   `json:"github"`        // a GitHub token for the scan
	Gateway      bool   `json:"gateway"`       // the server has a model gateway
	DefaultModel bool   `json:"default_model"` // BENCH_ISSUEBENCH_PROVIDER and _MODEL are set
	Message      string `json:"message,omitempty"`
}

type jobNoteKey struct{}

// WithJobNote returns ctx carrying f, called with every job a stage queues.
func WithJobNote(ctx context.Context, f func(stage, jobID string)) context.Context {
	return context.WithValue(ctx, jobNoteKey{}, f)
}

// NoteJob tells the pipeline which job runs a stage now (no-op without one).
func NoteJob(ctx context.Context, stage, jobID string) {
	if f, ok := ctx.Value(jobNoteKey{}).(func(stage, jobID string)); ok && f != nil {
		f(stage, jobID)
	}
}

// DockerFunc runs `docker args...` and returns the combined output. A
// command that ran and exited non-zero returns *ExitError.
type DockerFunc func(ctx context.Context, stdin []byte, args ...string) ([]byte, error)

// ExitError is a docker command that exited non-zero.
type ExitError struct {
	Code   int
	Output []byte
}

func (e *ExitError) Error() string {
	s := strings.TrimSpace(string(e.Output))
	if len(s) > 400 {
		s = s[len(s)-400:]
	}
	return fmt.Sprintf("exit status %d: %s", e.Code, s)
}

// ExecDocker adapts a command builder (utils.Docker.Command, which applies a
// shared worker's policy) to a DockerFunc.
func ExecDocker(command func(ctx context.Context, args ...string) *exec.Cmd) DockerFunc {
	return func(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
		cmd := command(ctx, args...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return buf.Bytes(), &ExitError{Code: ee.ExitCode(), Output: buf.Bytes()}
		}
		return buf.Bytes(), err
	}
}

// RedactValues replaces every secret value (8 bytes or longer) in s, and
// the git header form of a GitHub token, with [redacted].
func RedactValues(s string, secrets map[string]string) string {
	var vals []string
	for k, v := range secrets {
		if len(v) < 8 {
			continue
		}
		vals = append(vals, v)
		if k == SecretGitHubToken {
			vals = append(vals, base64.StdEncoding.EncodeToString([]byte("x-access-token:"+v)))
		}
	}
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	for _, v := range vals {
		s = strings.ReplaceAll(s, v, "[redacted]")
	}
	return s
}
