package adapters

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// AdapterRemoteHTTP is the name of the remote_http adapter.
const AdapterRemoteHTTP = "remote_http"

// Model traffic of a remote agent (schemas.RemoteAgent.ModelTraffic).
const (
	RemoteTrafficGateway  = "gateway"
	RemoteTrafficExternal = "external"
)

const (
	remoteHTTPVersion  = "1.0.0"
	remoteLauncherFile = "/tmp/bench/remotehttp.py"
	// remoteTimeoutMargin is how much of the wall budget the launcher keeps
	// to write its error before the container is killed.
	remoteTimeoutMargin = 5
)

// envNamePattern is a portable environment variable name.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

//go:embed remotehttp.py
var remoteLauncherSource string

// remoteHTTPAdapter runs an agent that is a deployed service. The container
// it plans is the bench's own launcher (remotehttp.py): it posts the
// session's instruction to the agent's URL and writes the answer it gets
// back to the answer path.
type remoteHTTPAdapter struct {
	cfg CommandConfig
}

// newRemoteHTTPAdapter takes the command adapter's config for the
// evaluation network and the gateway URL.
func newRemoteHTTPAdapter(cfg CommandConfig) *remoteHTTPAdapter {
	return &remoteHTTPAdapter{cfg: cfg}
}

// GetName implements schemas.Part.
func (a *remoteHTTPAdapter) GetName() string { return AdapterRemoteHTTP }

// GetVersion implements schemas.Part.
func (a *remoteHTTPAdapter) GetVersion() string { return remoteHTTPVersion }

// Plan returns the launcher's plan.
//
// Only a task whose output kind is answer can run: the agent is not beside
// the workspace, so the answer is all it can produce (the planner refuses
// other tasks too; this is the adapter's own check). The bearer token is read
// from the host environment variable the config names and reaches the
// container only through env (BENCH_REMOTE_TOKEN, redacted in plan.json),
// never argv or files. The plan asks for egress to exactly the URL's host and
// port (RunPlan.Egress), which the environment grants through a per-run
// forwarder. With model_traffic gateway the request also carries the gateway
// URL, the run key and the model, so the deployment's model calls go through
// this gateway on the run's key and are measured like any agent's.
func (a *remoteHTTPAdapter) Plan(_ context.Context, rc *schemas.RunContext) (*schemas.RunPlan, error) {
	if err := checkRunContext(rc); err != nil {
		return nil, err
	}
	spec := rc.Agent.Remote
	if spec == nil {
		return nil, fmt.Errorf("plan run %s: agent %s has no remote block", rc.Run.RunID, rc.Agent.ID)
	}
	if kind := utils.OutputKind(rc.Suite, rc.Task); kind != schemas.OutputAnswer {
		return nil, fmt.Errorf("plan run %s: adapter remote_http runs only tasks with output kind answer; task %s is %s", rc.Run.RunID, rc.Task.ID, kind)
	}
	target, err := utils.RemoteEgressTarget(spec.URL)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	traffic, err := remoteTraffic(spec)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	instruction, err := readTaskFile(rc.Task, rc.Task.Instruction)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	network := a.cfg.EvalNetwork
	if network == "" && rc.Handle != nil {
		network = rc.Handle.Network
	}
	if network == "" {
		return nil, fmt.Errorf("plan run %s: no evaluation network", rc.Run.RunID)
	}
	plan, err := basePlan(rc, network)
	if err != nil {
		return nil, err
	}
	env, err := a.remoteEnv(rc, spec, traffic, plan.WallS)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	plan.Argv = []string{"python3", "-I", remoteLauncherFile}
	plan.Env = env
	plan.Files = map[string]string{remoteLauncherFile: remoteLauncherSource, InstructionFile: instruction}
	plan.Egress = []string{target}
	return plan, nil
}

// remoteEnv is the launcher's environment.
func (a *remoteHTTPAdapter) remoteEnv(rc *schemas.RunContext, spec *schemas.RemoteAgent, traffic string, wallS int) (map[string]string, error) {
	timeout := spec.TimeoutS
	if limit := wallS - remoteTimeoutMargin; timeout <= 0 || (limit > 0 && timeout > limit) {
		timeout = max(limit, 1)
	}
	sessionID, sessionIndex := rc.Run.RunID, 1
	if rc.Session != nil {
		sessionID, sessionIndex = rc.Session.ID, rc.SessionIndex+1
	}
	env := map[string]string{
		"BENCH_REMOTE_URL":           spec.URL,
		utils.EnvRemoteConnect:       utils.RemoteEgressAddr(rc.Run.RunID),
		"BENCH_REMOTE_TIMEOUT_S":     strconv.Itoa(timeout),
		"BENCH_REMOTE_INSTRUCTION":   InstructionFile,
		"BENCH_REMOTE_SESSION_ID":    sessionID,
		"BENCH_REMOTE_SESSION_INDEX": strconv.Itoa(sessionIndex),
		"BENCH_REMOTE_FILES_DIR":     Workspace,
		"BENCH_REMOTE_TRAFFIC":       traffic,
		utils.EnvAnswerPath:          utils.AnswerContainerPath(rc.Suite, rc.Task),
	}
	if spec.AuthEnv != "" {
		if !envNamePattern.MatchString(spec.AuthEnv) {
			return nil, fmt.Errorf("remote.auth_env %q is not an environment variable name", spec.AuthEnv)
		}
		tok := os.Getenv(spec.AuthEnv)
		if tok == "" {
			return nil, fmt.Errorf("host environment variable %s (remote.auth_env) is not set", spec.AuthEnv)
		}
		env["BENCH_REMOTE_TOKEN"] = tok
	}
	if traffic == RemoteTrafficGateway {
		if rc.Key == nil || rc.Key.Value == "" {
			return nil, fmt.Errorf("model_traffic gateway needs the run key, and the run has none")
		}
		env["BENCH_REMOTE_GATEWAY_URL"] = templateVars(rc, a.cfg, "")["gateway.url"]
		env["BENCH_REMOTE_RUN_KEY"] = rc.Key.Value
		env["BENCH_REMOTE_MODEL"] = rc.Agent.Model
	}
	return env, nil
}

// remoteTraffic is the config's model traffic, external when unset.
func remoteTraffic(spec *schemas.RemoteAgent) (string, error) {
	switch spec.ModelTraffic {
	case "", RemoteTrafficExternal:
		return RemoteTrafficExternal, nil
	case RemoteTrafficGateway:
		return RemoteTrafficGateway, nil
	default:
		return "", fmt.Errorf("remote.model_traffic %q: want gateway or external", spec.ModelTraffic)
	}
}
