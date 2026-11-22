package adapters

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

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

// authHeaderPattern is a header name remote.auth_header may hold.
var authHeaderPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

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

// Metadata implements schemas.AdapterMetadata.
func (a *remoteHTTPAdapter) Metadata() schemas.AdapterMetadataInfo {
	return schemas.AdapterMetadataInfo{AdapterVersion: remoteHTTPVersion}
}

// Validate implements schemas.AdapterValidator. Plan remains the legacy entry
// point and performs the same checks, and more, for old callers.
func (a *remoteHTTPAdapter) Validate(_ context.Context, rc *schemas.RunContext) error {
	if err := checkRunContext(rc); err != nil {
		return err
	}
	if rc.Agent.Remote == nil {
		return fmt.Errorf("plan run %s: agent %s has no remote block", rc.Run.RunID, rc.Agent.ID)
	}
	return nil
}

// BuildExecutionPlan implements schemas.AdapterRunner.
func (a *remoteHTTPAdapter) BuildExecutionPlan(ctx context.Context, rc *schemas.RunContext) (*schemas.ExecutionPlan, error) {
	return a.Plan(ctx, rc)
}

// Cleanup implements schemas.AdapterCleanup. The launcher's files live in the
// run container and are removed with it.
func (a *remoteHTTPAdapter) Cleanup(context.Context, *schemas.RunContext) error { return nil }

// Plan returns the launcher's plan.
//
// An answer task always runs. The agent is not beside the workspace, so a
// state task runs only with remote.tools expose (it changes the services
// through the published tool proxy, rc.RemoteTools), an artifact task only
// with remote.artifacts inline (the files arrive in the response), and a diff
// task never (utils.RemoteOutputAllowed; the planner refuses them too, this
// is the adapter's own check). The bearer token is read
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
	if err := utils.CheckRemoteAgent(spec); err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	kind := utils.OutputKind(rc.Suite, rc.Task)
	if ok, why := utils.RemoteOutputAllowed(spec, kind); !ok {
		return nil, fmt.Errorf("plan run %s: adapter remote_http cannot run task %s (output %s): %s", rc.Run.RunID, rc.Task.ID, kind, why)
	}
	if kind == schemas.OutputState && rc.RemoteTools == nil {
		return nil, fmt.Errorf("plan run %s: task %s is a state task and remote.tools is expose, but no tool proxy was published to the agent (the task has no proxied service or external API)", rc.Run.RunID, rc.Task.ID)
	}
	target, err := utils.RemoteEgressTarget(spec.URL)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	traffic, err := remoteTraffic(spec)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	if rc.NoPaidTraffic && traffic != RemoteTrafficGateway {
		// QC2-15-08: an external deployment's model calls are outside this
		// bench, so nothing can hold them to the scripted model.
		return nil, fmt.Errorf("plan run %s: %s", rc.Run.RunID, RemoteNoPaidReason)
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
	env, err := a.remoteEnv(rc, spec, traffic, plan.WallS, kind)
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	plan.Argv = []string{"python3", "-I", remoteLauncherFile}
	plan.Env = env
	plan.Files = map[string]string{remoteLauncherFile: remoteLauncherSource, InstructionFile: instruction}
	chat, err := chatPlanFile(rc) // Chat tasks (chat.go): the conversation, sent as messages
	if err != nil {
		return nil, fmt.Errorf("plan run %s: %w", rc.Run.RunID, err)
	}
	if chat != "" {
		plan.Files[chatFile] = chat
		plan.Env["BENCH_REMOTE_CHAT"] = chatFile
	}
	plan.Egress = []string{target}
	return plan, nil
}

// remoteEnv is the launcher's environment.
func (a *remoteHTTPAdapter) remoteEnv(rc *schemas.RunContext, spec *schemas.RemoteAgent, traffic string, wallS int, kind string) (map[string]string, error) {
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
		"BENCH_REMOTE_OUTPUT":        kind,
		utils.EnvAnswerPath:          utils.AnswerContainerPath(rc.Suite, rc.Task),
	}
	switch {
	case spec.AuthSecret != "": // J3: an org secret, expanded by the engine (engine/secrets.go)
		if !envNamePattern.MatchString(spec.AuthSecret) {
			return nil, fmt.Errorf("remote.auth_secret %q is not a secret name", spec.AuthSecret)
		}
		env["BENCH_REMOTE_TOKEN"] = "{{secret." + spec.AuthSecret + "}}"
	case spec.AuthEnv != "":
		if !envNamePattern.MatchString(spec.AuthEnv) {
			return nil, fmt.Errorf("remote.auth_env %q is not an environment variable name", spec.AuthEnv)
		}
		tok := os.Getenv(spec.AuthEnv)
		if tok == "" {
			return nil, fmt.Errorf("host environment variable %s (remote.auth_env) is not set", spec.AuthEnv)
		}
		env["BENCH_REMOTE_TOKEN"] = tok
	}
	if h := spec.AuthHeader; h != "" && !strings.EqualFold(h, "Authorization") {
		if !authHeaderPattern.MatchString(h) {
			return nil, fmt.Errorf("remote.auth_header %q is not a header name", h)
		}
		env["BENCH_REMOTE_AUTH_HEADER"] = h
	}
	if traffic == RemoteTrafficGateway {
		if rc.Key == nil || rc.Key.Value == "" {
			return nil, fmt.Errorf("model_traffic gateway needs the run key, and the run has none")
		}
		env["BENCH_REMOTE_GATEWAY_URL"] = templateVars(rc, a.cfg, "")["gateway.url"]
		if a.cfg.GatewayPublicURL != "" {
			env["BENCH_REMOTE_GATEWAY_URL"] = strings.TrimRight(a.cfg.GatewayPublicURL, "/")
		}
		env["BENCH_REMOTE_RUN_KEY"] = rc.Key.Value
		env["BENCH_REMOTE_MODEL"] = rc.Agent.Model
	}
	if err := remoteCoverageEnv(rc, spec, env); err != nil {
		return nil, err
	}
	return env, nil
}

// remoteTraffic is the config's model traffic, external when unset.
// RemoteNoPaidReason refuses a no-paid-traffic run (smoke, scripted_upstream,
// agent check) of a remote agent whose model traffic is external.
const RemoteNoPaidReason = "a smoke or scripted_upstream run needs remote.model_traffic gateway: with external model traffic the deployment's model calls are outside the bench and cannot be held to the scripted model"

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
