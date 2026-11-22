package schemas

import (
	"context"
	"time"
)

// This file is the contract for scenarios: tasks that are more than "edit a
// repository and pass hidden tests". A scenario is a workspace (optional),
// services that run beside the agent, one or more sessions, an output, and a
// verifier. docs/scenarios-design.md explains every type here. Every field
// added to an older struct for scenarios is optional and omitted when empty,
// so manifests, locks and seals written before scenarios keep their hashes.

// Suite tracks group suites on the leaderboard and in reports (Suite.Track).
const (
	TrackCoding      = "coding"       // edit a repository, hidden tests (the default)
	TrackTerminal    = "terminal"     // work in a whole container, state checked afterwards
	TrackToolUse     = "tool_use"     // call tools against a service, final state checked
	TrackMCP         = "mcp"          // tools reached over the Model Context Protocol
	TrackMemory      = "memory"       // several sessions, recall checked in the last one
	TrackSkills      = "skills"       // the same task with and without agent skills
	TrackSafety      = "safety"       // attacks delivered through tool results or files
	TrackQA          = "qa"           // a written answer, matched or judged
	TrackBrowser     = "browser"      // a web application operated through a browser, final state checked
	TrackComputerUse = "computer_use" // a desktop operated through screenshots, mouse and keyboard, app state checked
	TrackArtifact    = "artifact"     // produce files (a report, a chart, a model) graded on their content
	TrackSupport     = "support"      // W3 customer support: a helpdesk conversation with a simulated user, final state and policy checked
	TrackResearch    = "research"     // W4 research: web search through Tavily (recorded and replayed), answer and cited sources checked
	TrackVoice       = "voice"        // X8 voice agents: a spoken call with a voice caller, final state checked
)

// TrackMLEngineering is the GPU track (X7, schemas/gpu.go, docs/gpu-tasks.md):
// train a model, speed up a training loop or write a kernel on a GPU, graded
// on held-out data or measured speed in the verifier.
const TrackMLEngineering = "ml_engineering"

// Grading levels (Suite.Grading). A judged suite's outcomes come from a
// pinned judge model, not a deterministic check; reports and the leaderboard
// label it and never place judged and verified results in one comparison.
const (
	GradingVerified = "verified"
	GradingJudged   = "judged"
)

// Output kinds (OutputSpec.Kind): what the verifier reads.
const (
	OutputDiff     = "diff"     // the exported workspace patch (the default)
	OutputAnswer   = "answer"   // a text answer the agent wrote
	OutputState    = "state"    // files exported from services after the run
	OutputArtifact = "artifact" // files the agent produced in the workspace (a report, an image, a model)
)

// Verifier modes (SuiteVerifier.Mode).
const (
	// VerifierCleanRoom grades a pristine copy of the task repo with the
	// agent's diff applied (the default).
	VerifierCleanRoom = "clean_room"
	// VerifierSnapshot grades a frozen copy of the agent's whole container
	// file system, taken after the agent exited, in a fresh container with no
	// network. It is for tasks whose result lives outside the workspace
	// (installed packages, files under /etc, a built binary). The agent
	// process is gone and the hidden files arrive only then, but the agent
	// could have changed the tools the verifier calls, so a snapshot grade is
	// weaker evidence than a clean-room grade and is labelled.
	VerifierSnapshot = "snapshot"
)

// Service roles (ServiceSpec.Role).
const (
	ServiceRoleData = "data" // a database or file store the agent or its tools change
	ServiceRoleTool = "tool" // an HTTP tool server
	ServiceRoleMCP  = "mcp"  // a Model Context Protocol server
	ServiceRoleUser = "user" // a simulated user the agent converses with
)

// TaskEnvironment gives a task its own container image instead of the suite's
// base image plus a repo folder. It is how tasks from benchmarks that ship one
// image or Dockerfile per task are run.
type TaskEnvironment struct {
	// Image is a prebuilt image tag. Dockerfile (relative to the task folder)
	// builds one when Image is empty; it is tagged
	// bench/task-<suite>-<task>:<content hash>. Context is the build context,
	// relative to the task folder (default: the Dockerfile's folder).
	Image      string `json:"image,omitempty" yaml:"image"`
	Dockerfile string `json:"dockerfile,omitempty" yaml:"dockerfile"`
	Context    string `json:"context,omitempty" yaml:"context"`
	// WorkspacePath is the folder inside the image whose content seeds the
	// workspace volume (for example /app or /testbed). Empty seeds the
	// workspace from the task's repo folder as before.
	WorkspacePath string `json:"workspace_path,omitempty" yaml:"workspace_path"`
	// VerifierImage is the image the verifier runs in under clean_room mode.
	// Empty uses the task image.
	VerifierImage string `json:"verifier_image,omitempty" yaml:"verifier_image"`
	// BuildNetwork allows network during the image build only (most upstream
	// Dockerfiles install packages). The run itself never gets it.
	BuildNetwork bool `json:"build_network,omitempty" yaml:"build_network"`
	// User "root" runs the agent (and a scripted control, and the verifier)
	// as root inside the task's own image, for tasks that install packages or
	// change files under /etc. Empty keeps the unprivileged user. It is
	// honoured only when the task has its own image.
	User string `json:"user,omitempty" yaml:"user"`

	// X2: Env is set in the agent's container (and a scripted control's, and
	// the verifier's) under the plan's own variables, which win. A compose
	// task fills it from the workspace service's environment.
	Env map[string]string `json:"env,omitempty" yaml:"env"`
}

// ServiceSpec is one container started beside the agent for a run and removed
// after it. The agent reaches it by its ID as a host name; no other run can,
// and a service has no route out of the evaluation network.
type ServiceSpec struct {
	ID    string            `json:"id" yaml:"id"`
	Role  string            `json:"role,omitempty" yaml:"role"` // data | tool | mcp | user
	Image string            `json:"image,omitempty" yaml:"image"`
	Build *ImageSpec        `json:"build,omitempty" yaml:"build"` // Dockerfile relative to the suite root (suite services) or the task folder (task services)
	Argv  []string          `json:"argv,omitempty" yaml:"argv"`
	Env   map[string]string `json:"env,omitempty" yaml:"env"`
	// Files maps a container path to a file relative to the task folder (or
	// the suite root for suite services). They are copied in before start and
	// are how a task seeds a database or a tool server's data.
	Files map[string]string `json:"files,omitempty" yaml:"files"`
	Port  int               `json:"port,omitempty" yaml:"port"` // the port the service listens on
	Ready *ReadyCheck       `json:"ready,omitempty" yaml:"ready"`
	// Export lists what is read from the service after the agent exits. Each
	// entry lands in the run folder at state/<service id>/<export id>, is
	// sealed, and is mounted read-only in the verifier at
	// /grader/state/<service id>/<export id>.
	Export []StateExport `json:"export,omitempty" yaml:"export"`
	// MCP describes the endpoint when Role is mcp. Proxy routes the agent's
	// calls through the per-run tool proxy, which records and can fault them;
	// it defaults to true for tool and mcp services.
	MCP   *MCPEndpoint `json:"mcp,omitempty" yaml:"mcp"`
	Proxy *bool        `json:"proxy,omitempty" yaml:"proxy"`
	// Hidden keeps the service off the agent's network: only the verifier and
	// other services reach it.
	Hidden bool `json:"hidden,omitempty" yaml:"hidden"`
	// Verifier lets the verifier reach the service on the run's network,
	// with the proxy variables set. Only an egress service (recorded
	// internet, docs/egress.md) may set it: a test script that downloads at
	// verify time is answered from the recorded cache.
	Verifier bool `json:"verifier,omitempty" yaml:"verifier"`

	// W3 customer support: Gateway lets the service call models through the
	// gateway on its own virtual key, never the agent's (servicegateway.go).
	Gateway *ServiceGateway `json:"gateway,omitempty" yaml:"gateway"`
	Compose *ComposeService `json:"compose,omitempty" yaml:"-"` // X2: settings only a compose file sets (compose.go)
}

// W3 customer support: service gateway access and the user simulator.

// ServiceGateway gives a service its own virtual key for model calls (a
// model-played user, a model-backed tool). The environment mints the key
// when the service starts, passes BENCH_SVC_GATEWAY_URL, BENCH_SVC_GATEWAY_KEY
// and BENCH_SVC_MODEL to the service only, revokes the key at cleanup and
// records the service's spend from the gateway rows apart from the agent's.
// For a role user service in llm simulator mode an empty Provider and Model
// come from the effective SimulatorSpec.
type ServiceGateway struct {
	Provider  string  `json:"provider,omitempty" yaml:"provider"`
	Model     string  `json:"model,omitempty" yaml:"model"`
	BudgetUSD float64 `json:"budget_usd,omitempty" yaml:"budget_usd"` // the key's spend cap; 0 means the bench default
}

// User simulator modes (SimulatorSpec.Mode).
const (
	SimulatorScripted = "scripted" // a deterministic script per task (the default)
	SimulatorLLM      = "llm"      // a pinned model plays the user from a persona
)

// SimulatorSpec pins the user simulator of a suite (Suite.Simulator), like a
// judge: every field is hashed into the manifest with the prompt's sha256.
// An experiment may override any of its fields (Experiment.Simulator) for
// the whole experiment, never per arm, so scripted-user and llm-user runs
// are never in one comparison.
type SimulatorSpec struct {
	Mode          string `json:"mode,omitempty" yaml:"mode"` // scripted (default) | llm
	Provider      string `json:"provider,omitempty" yaml:"provider"`
	Model         string `json:"model,omitempty" yaml:"model"`
	PromptVersion string `json:"prompt_version,omitempty" yaml:"prompt_version"` // names the shipped user prompt (simuser)
	MaxTurns      int    `json:"max_turns,omitempty" yaml:"max_turns"`           // user replies before the user leaves; default 12
	Seed          int64  `json:"seed,omitempty" yaml:"seed"`                     // sent with every simulator call; temperature is always 0
}

// ReadyCheck says when a service is ready. Exactly one of Argv (run inside
// the service container, exit 0 means ready) or HTTP (a path on Port that
// must answer 2xx) is set.
type ReadyCheck struct {
	Argv     []string `json:"argv,omitempty" yaml:"argv"`
	HTTP     string   `json:"http,omitempty" yaml:"http"`
	TimeoutS int      `json:"timeout_s,omitempty" yaml:"timeout_s"` // default 60
}

// StateExport reads one piece of a service's final state: the stdout of Argv
// run inside the service container (a database dump, a JSON query), or the
// file or folder at Path (stored as a tar when a folder).
type StateExport struct {
	ID       string   `json:"id" yaml:"id"`
	Argv     []string `json:"argv,omitempty" yaml:"argv"`
	Path     string   `json:"path,omitempty" yaml:"path"`
	MaxBytes int64    `json:"max_bytes,omitempty" yaml:"max_bytes"` // default 16 MiB; a larger export fails the run's grade as invalid_evaluator
}

// MCPEndpoint is how an MCP service is reached. Transport is "http"
// (streamable HTTP at Path on the service's Port). A server that only speaks
// stdio is wrapped by a bridge inside its own service image.
type MCPEndpoint struct {
	Transport string `json:"transport,omitempty" yaml:"transport"` // http (default)
	Path      string `json:"path,omitempty" yaml:"path"`           // default /mcp
}

// SessionSpec is one agent session of a multi-session task. Sessions run in
// order, each as a fresh agent process. The agent's memory paths
// (AgentMemory.Paths) and the /memory volume persist between them; nothing
// else does unless KeepWorkspace is set.
type SessionSpec struct {
	ID          string `json:"id" yaml:"id"`
	Instruction string `json:"instruction" yaml:"instruction"` // file relative to the task folder
	// Files is a folder (relative to the task folder) copied into the
	// workspace before the session starts, for example a chat transcript to
	// read and remember.
	Files string `json:"files,omitempty" yaml:"files"`
	// KeepWorkspace keeps the workspace from the previous session. The
	// default resets it to the task's starting state, so the only way to
	// carry knowledge forward is the agent's memory.
	KeepWorkspace bool `json:"keep_workspace,omitempty" yaml:"keep_workspace"`
	WallS         int  `json:"wall_s,omitempty" yaml:"wall_s"` // session wall budget; default the agent's budget
}

// OutputSpec says what the verifier reads. For kind answer the agent writes
// its answer to AnswerPath in the workspace (default answer.txt); when the
// file is missing the final message is used and the source is recorded. For
// kind artifact the agent leaves the files Artifacts lists in the workspace.
type OutputSpec struct {
	Kind       string `json:"kind,omitempty" yaml:"kind"` // diff | answer | state | artifact
	AnswerPath string `json:"answer_path,omitempty" yaml:"answer_path"`
	// Artifacts are the files a task with output kind artifact must produce.
	Artifacts []ArtifactSpec `json:"artifacts,omitempty" yaml:"artifacts"`

	Visual *VisualCheck `json:"visual,omitempty" yaml:"visual"` // X1 vision grading: graded by grader visual (visual.go)
}

// ArtifactSpec is one file, or a glob of files, that a task with output kind
// artifact expects the agent to leave in the workspace. Path is relative to
// the workspace root and may use the glob patterns *, ? and [...] (no
// recursive **). After the agent exits a trusted helper copies each match to
// artifacts/<id>/<base name> in the run folder, where the seal covers it, and
// the verifier reads it read-only at /grader/artifacts/<id>/<base name>. A
// match that leaves the workspace through a symlink, is not a regular file,
// or is over MaxBytes is recorded with an error and not copied.
type ArtifactSpec struct {
	ID       string `json:"id" yaml:"id"`
	Path     string `json:"path" yaml:"path"`
	Required bool   `json:"required,omitempty" yaml:"required"`   // a required artifact without one usable file fails the run's grade
	MaxBytes int64  `json:"max_bytes,omitempty" yaml:"max_bytes"` // per file; default 32 MiB
}

// AnswerSpec is the hidden expected answer of a task with output kind answer,
// read from the task's verifier folder (TaskVerifier.Answer, default
// answer.yaml). The answer_match grader checks it without a model.
type AnswerSpec struct {
	// Match is exact, contains_all, regex, numeric, choice or json.
	Match     string   `json:"match" yaml:"match"`
	Expected  []string `json:"expected" yaml:"expected"`                     // exact/choice: any one matches; contains_all: every one must appear; regex: any one matches
	Normalize []string `json:"normalize,omitempty" yaml:"normalize"`         // lower, strip_punct, collapse_space, trim
	Tolerance float64  `json:"tolerance,omitempty" yaml:"tolerance"`         // numeric: absolute tolerance
	Category  string   `json:"category,omitempty" yaml:"category"`           // reported as a breakdown, e.g. temporal-reasoning
	Question  string   `json:"question,omitempty" yaml:"question"`           // the question, for the judge prompt
	Rubric    string   `json:"rubric,omitempty" yaml:"rubric"`               // judge grader: what a correct answer must contain
	Abstain   bool     `json:"abstain,omitempty" yaml:"abstain"`             // the correct behaviour is to say the answer is not known
	WrongOnes []string `json:"wrong_answers,omitempty" yaml:"wrong_answers"` // admission controls: answers that must fail
	// GeneratedWrong are plausible wrong answers written by the bench, not
	// the author (scaffold.GenerateWrongAnswers: the reference with a number
	// changed, a statement negated, a yes or no flipped, or another row's
	// reference). Admission grades them exactly like WrongOnes; they are
	// kept apart so the Build tab can show which ones the author wrote.
	GeneratedWrong []string `json:"generated_wrong_answers,omitempty" yaml:"generated_wrong_answers"`
}

// AllWrong is every wrong answer admission grades: the author's, then the
// generated ones, without duplicates.
func (a *AnswerSpec) AllWrong() []string {
	if a == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, w := range append(append([]string(nil), a.WrongOnes...), a.GeneratedWrong...) {
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// JudgeSpec pins the judge model of a judged suite (Suite.Judge). Every field
// is hashed into the manifest. Judge calls go through the gateway on their
// own key, never the run's, and their cost is reported apart from the
// agent's.
type JudgeSpec struct {
	Provider      string `json:"provider" yaml:"provider"`
	Model         string `json:"model" yaml:"model"`
	PromptVersion string `json:"prompt_version" yaml:"prompt_version"` // names the embedded prompt template
	Votes         int    `json:"votes,omitempty" yaml:"votes"`         // odd number of independent calls, majority decides; default 1
	MaxTokens     int    `json:"max_tokens,omitempty" yaml:"max_tokens"`

	Auditor *JudgeAuditorSpec `json:"auditor,omitempty" yaml:"auditor,omitempty"` // W8 judge calibration: optional second judge from another provider; disagreement marks a run contested, never overrules

	// Custom judge prompt (prompt_version custom-v1): the org's prompt file
	// in the suite folder, pinned by its sha256 (a changed file refuses to
	// grade until the pin is updated), and the weighted share of the task's
	// criteria a pass needs (default 1: every criterion).
	PromptFile   string   `json:"prompt_file,omitempty" yaml:"prompt_file"`
	PromptSHA256 string   `json:"prompt_sha256,omitempty" yaml:"prompt_sha256"`
	PassAt       *float64 `json:"pass_at,omitempty" yaml:"pass_at"`
}

// JudgeCriterion is one criterion of a custom judge prompt's verdict
// (JudgeRecord.Criteria): its weight from the task's rubric, whether the
// majority of parsable votes found it met, and the judge's reason.
type JudgeCriterion struct {
	ID     string  `json:"id"`
	Text   string  `json:"text"`
	Weight float64 `json:"weight"`
	Met    bool    `json:"met"`
	Reason string  `json:"reason,omitempty"`
}

// JudgeRecord is what one judged grade rested on (Grade.Judge).
type JudgeRecord struct {
	Model         string   `json:"model"`
	PromptVersion string   `json:"prompt_version"`
	PromptSHA256  string   `json:"prompt_sha256"`
	Votes         []string `json:"votes"` // each call's verdict: correct | incorrect | unparsable
	USD           Value    `json:"usd"`   // judge cost; never added to the agent's cost

	// PriceRef names the price snapshot USD was priced from.
	PriceRef string `json:"price_ref,omitempty"`

	// W8 judge calibration: the model id the gateway reported, the
	// calibration record in force at grading ("<record id>@<hash>", empty
	// when none), and the auditor's opinion when the suite names one.
	ServedModel string      `json:"served_model,omitempty"`
	Calibration string      `json:"calibration,omitempty"`
	Audit       *JudgeAudit `json:"audit,omitempty"`

	// The rest of the judge's calibration identity: the provider it was
	// called through and, for a vision judge, how its votes decide. A
	// calibration record covers a grade only when these match what it
	// measured (QC2-25-03, QC2-25-05). Empty on grades sealed before them.
	Provider  string  `json:"provider,omitempty"`
	Aggregate string  `json:"aggregate,omitempty"`
	PassScore float64 `json:"pass_score,omitempty"`

	// Criteria is a custom judge prompt's per-criterion verdict, with
	// Score the weighted share met and PassAt the share a pass needs.
	Criteria []JudgeCriterion `json:"criteria,omitempty"`
	Score    *float64         `json:"score,omitempty"`
	PassAt   float64          `json:"pass_at,omitempty"`
}

// ImportRecord says where an imported suite came from (Suite.Imported).
type ImportRecord struct {
	Importer  string    `json:"importer" yaml:"importer"`           // importer name, e.g. harbor
	Benchmark string    `json:"benchmark" yaml:"benchmark"`         // e.g. terminal-bench@2.1
	Source    string    `json:"source" yaml:"source"`               // repository or dataset URL
	Revision  string    `json:"revision,omitempty" yaml:"revision"` // commit or dataset revision imported
	License   string    `json:"license" yaml:"license"`             // SPDX id as upstream states it
	At        time.Time `json:"at" yaml:"at"`
}

// AgentMCP says how an agent is told about the run's MCP servers. The engine
// resolves every proxied mcp service to a URL and the adapter renders them in
// the agent's own format. Template variables for argv, env and files:
// {{mcp.servers_json}} (a JSON object {"<id>": {"url": "..."}}) and
// {{mcp.<id>.url}}. The same JSON is always set in BENCH_MCP_SERVERS.
type AgentMCP struct {
	// Format is codex_toml, claude_json, opencode_json, generic_json or env.
	// Empty lets the adapter choose its native format.
	Format string `json:"format,omitempty" yaml:"format"`
	// ConfigPath is the container path the rendered config is written to.
	// Empty uses the adapter's native path.
	ConfigPath string `json:"config_path,omitempty" yaml:"config_path"`
}

// AgentSkills controls agent skills (folders holding a SKILL.md). A task can
// carry skills (Task.Skills) and a config can add its own (Extra). Enabled
// false installs none: an arm that sets skills.enabled to false is the
// "without skills" arm of a skill comparison.
type AgentSkills struct {
	Enabled *bool    `json:"enabled,omitempty" yaml:"enabled"` // default true
	Dir     string   `json:"dir,omitempty" yaml:"dir"`         // container folder the agent reads skills from; empty uses the adapter's native folder
	Extra   []string `json:"extra,omitempty" yaml:"extra"`     // skill folders relative to the bench root, installed for every task
	// Set picks one of the task's alternative skill sets instead of its
	// skills folder: the task folder skill-sets/<Set>. A task without that
	// folder installs no task skills. Empty uses the task's skills folder.
	Set string `json:"set,omitempty" yaml:"set"`
}

// AgentMemory names the container paths that hold the agent's own memory.
// They are kept between the sessions of a multi-session task and are empty at
// the first session. The /memory volume is always kept and its path is given
// in BENCH_MEMORY_DIR.
type AgentMemory struct {
	Paths []string `json:"paths,omitempty" yaml:"paths"`
}

// RemoteAgent describes an agent that is a deployed service instead of a
// container image (adapter remote_http). Only tasks with output kind answer
// can run against it: the agent is not beside the workspace. ModelTraffic
// says where its model calls go: "gateway" when the deployment is pointed at
// this bench's gateway with the run key, "external" when they are out of
// sight, in which case tokens, cost and steps are unknown, never zero.
type RemoteAgent struct {
	URL          string `json:"url" yaml:"url"`
	AuthEnv      string `json:"auth_env,omitempty" yaml:"auth_env"` // name of the host environment variable holding the bearer token
	TimeoutS     int    `json:"timeout_s,omitempty" yaml:"timeout_s"`
	ModelTraffic string `json:"model_traffic,omitempty" yaml:"model_traffic"` // gateway | external (default)

	// Remote coverage (remote.go, docs/remote-execution.md). All optional.
	// Tools "expose" publishes the run's tool proxy to the deployment behind
	// a per-run bearer token, which also lets it run state tasks. Artifacts
	// "inline" declares that the deployment returns artifact files in its
	// response, which lets it run artifact tasks. SignEnv names the host
	// environment variable holding the secret the launcher signs each
	// request body with (HMAC-SHA256).
	Tools     string `json:"tools,omitempty" yaml:"tools"`         // expose | off (default)
	Artifacts string `json:"artifacts,omitempty" yaml:"artifacts"` // inline | none (default)
	SignEnv   string `json:"sign_env,omitempty" yaml:"sign_env"`

	// Org-secret auth (lane X10, J3). AuthSecret and SignSecret name org
	// secrets (Settings > Secrets) used instead of the host variables
	// AuthEnv and SignEnv, so a hosted org never reads the server's own
	// environment. AuthHeader is the header the token goes in: empty or
	// Authorization sends "Bearer <token>", any other sends the raw token.
	// ToolAllow limits, with tools expose, which tool and MCP services (and
	// external APIs) the deployment is sent; empty sends every one.
	AuthSecret string   `json:"auth_secret,omitempty" yaml:"auth_secret"`
	AuthHeader string   `json:"auth_header,omitempty" yaml:"auth_header"`
	SignSecret string   `json:"sign_secret,omitempty" yaml:"sign_secret"`
	ToolAllow  []string `json:"tool_allow,omitempty" yaml:"tool_allow"`

	// Mapping and Auth (lane Y10, remotemap.go): the endpoint's own request
	// and answer shape, and OAuth 2, Entra ID, SigV4, Google ID token or
	// mutual TLS auth. nil keeps Heimdall's shape and the token above.
	Mapping *RemoteMapping `json:"mapping,omitempty" yaml:"mapping"`
	Auth    *RemoteAuth    `json:"auth,omitempty" yaml:"auth"`
	// Lane Y11 (schemas/conversation.go). Conversation has the bench drive
	// chat tasks turn by turn; Repo tarball lets the deployment run coding
	// tasks (repo in, patch out).
	Conversation *ConversationSpec `json:"conversation,omitempty" yaml:"conversation"`
	Repo         string            `json:"repo,omitempty" yaml:"repo"` // tarball | none (default)
}

// ToolCall is one tool invocation recorded by the per-run tool proxy
// (tools.jsonl in the run folder). Arguments and results are kept in full up
// to a size cap; larger ones are truncated and their hash still covers the
// whole value.
type ToolCall struct {
	Seq          int       `json:"seq"` // 1-based order within the run
	Session      string    `json:"session,omitempty"`
	Server       string    `json:"server"`   // service id
	Tool         string    `json:"tool"`     // tool name; for plain HTTP tool servers the method and path
	Protocol     string    `json:"protocol"` // mcp | http | external
	Arguments    string    `json:"arguments,omitempty"`
	ArgsSHA256   string    `json:"args_sha256"`
	Result       string    `json:"result,omitempty"`
	ResultSHA256 string    `json:"result_sha256"`
	IsError      bool      `json:"is_error"`
	Status       int       `json:"status,omitempty"` // HTTP status from the tool server
	Fault        string    `json:"fault,omitempty"`  // the injected fault that answered instead of the server: error | timeout | poison
	Nonce        string    `json:"nonce,omitempty"`
	Started      time.Time `json:"started"`
	LatencyMS    int64     `json:"latency_ms"`
	Undelivered  bool      `json:"undelivered,omitempty"` // the proxy could not hand the answer to the agent (it hung up first)
	// FaultDelivered is set on a lost_ack row whose synthetic timeout answer
	// reached the agent. Undelivered there means the real answer was
	// withheld, so it cannot say whether the agent saw the fault.
	FaultDelivered bool `json:"fault_delivered,omitempty"`
	// RetryAfterS is the wait a rate_limit fault told the agent to respect
	// (the Retry-After header or the isError text), so a metric can check
	// whether the next call to the same tool waited that long.
	RetryAfterS int `json:"retry_after_s,omitempty"`
	// Effects is how many times the call reached the tool server: 2 on a
	// duplicate_effect row, whose call was forwarded twice; 0 means once.
	Effects int `json:"effects,omitempty"`

	// External API calls (Protocol external) only.
	Replayed  bool `json:"replayed,omitempty"`  // answered from the task's cassette, no network
	Unmatched bool `json:"unmatched,omitempty"` // replay mode and the cassette holds no matching call; the agent got a 599
	Blocked   bool `json:"blocked,omitempty"`   // refused by the proxy: not on the allowlist, or over a call or size cap

	// Rows that are not tool calls, and traffic the proxy could not parse
	// (schemas/toolrows.go). Kind is empty on a tool call, as on every row
	// written before kinds existed.
	Kind                string `json:"kind,omitempty"`
	HTTPMethod          string `json:"http_method,omitempty"`
	Path                string `json:"path,omitempty"`
	ContentType         string `json:"content_type,omitempty"`          // of the request
	ResponseContentType string `json:"response_content_type,omitempty"` // of the answer
	RequestBytes        int64  `json:"request_bytes,omitempty"`         // every byte of the request body, counted while streamed
	ResponseBytes       int64  `json:"response_bytes,omitempty"`        // every byte of the answer body, counted while streamed
	Unparsed            bool   `json:"unparsed,omitempty"`              // the proxy relayed this traffic without parsing all of it
	UnparsedReason      string `json:"unparsed_reason,omitempty"`

	// AgentID is the team member whose call this was (agents.go), from the
	// per-member route the call came in on; empty outside team runs.
	AgentID string `json:"agent_id,omitempty"`
}

// ToolFault is a fault the tool proxy injects for one run (ProbeApply.Tool,
// probe layer LayerTool). Mode error answers the matching calls with a tool
// error, timeout delays them past TimeoutS and then errors, poison lets the
// real call through and appends Text to its result (an injected instruction).
//
// Safety probes area (S1) modes: describe_poison, rug_pull, shadow and
// schema_poison rewrite the tools/list answers of Server (MCP tool
// poisoning, docs/tool-proxy.md); plausible_wrong (Patch) and schema_drift
// (Rename) rewrite a real result while it stays a success; lost_ack forwards
// the call to the server and answers the agent with a timeout instead.
type ToolFault struct {
	Mode     string `json:"mode" yaml:"mode"`                     // error | timeout | poison | describe_poison | rug_pull | shadow | schema_poison | plausible_wrong | schema_drift | lost_ack | rate_limit | slow | flaky | partial | auth_expired | tool_removed | input_drift | duplicate_effect
	Server   string `json:"server,omitempty" yaml:"server"`       // service id; empty matches every server
	Tool     string `json:"tool,omitempty" yaml:"tool"`           // tool name; empty matches every tool
	FromCall int    `json:"from_call,omitempty" yaml:"from_call"` // first affected matching call, 1-based (default 1)
	Calls    int    `json:"calls,omitempty" yaml:"calls"`         // how many matching calls are affected; 0 means all from FromCall
	Message  string `json:"message,omitempty" yaml:"message"`     // error text
	TimeoutS int    `json:"timeout_s,omitempty" yaml:"timeout_s"`
	Text     string `json:"text,omitempty" yaml:"text"` // poison: appended to the result; {{nonce}} is replaced per run
	Nonce    string `json:"nonce,omitempty" yaml:"-"`
	// Agent limits the fault to calls on these team members' own proxy
	// routes (comma-separated member ids), set by the tool layer from
	// ProbeApply.Agent; empty faults every caller, as before.
	Agent string `json:"agent,omitempty" yaml:"-"`

	Patch  map[string]any    `json:"patch,omitempty" yaml:"patch"`   // Safety probes area (S1): plausible_wrong merge patch; schema_poison parameters
	Rename map[string]string `json:"rename,omitempty" yaml:"rename"` // Safety probes area (S1): schema_drift field renames, old to new; input_drift parameter renames, old to new

	// Resilience modes (toolproxy/faultmodes.go).
	Limit       int     `json:"limit,omitempty" yaml:"limit"`                 // rate_limit: calls allowed per window
	WindowS     int     `json:"window_s,omitempty" yaml:"window_s"`           // rate_limit: window length in seconds
	RetryAfterS int     `json:"retry_after_s,omitempty" yaml:"retry_after_s"` // rate_limit: the Retry-After value; 0 = until the window frees a call
	DelayMS     int     `json:"delay_ms,omitempty" yaml:"delay_ms"`           // slow: hold before forwarding
	Prob        float64 `json:"prob,omitempty" yaml:"prob"`                   // flaky: failure probability, seeded by the nonce
	Keep        int     `json:"keep,omitempty" yaml:"keep"`                   // partial: array items kept in the result
	Refresh     string  `json:"refresh,omitempty" yaml:"refresh"`             // auth_expired: the tool whose call renews the token
	Version     string  `json:"version,omitempty" yaml:"version"`             // input_drift: the new API version named in errors

	// Tokens is how many tokens of plausible filler inflate appends to a
	// real result (context pressure, stream.go).
	Tokens int `json:"tokens,omitempty" yaml:"tokens,omitempty"`
}

// LayerTool is the probe layer of tool faults: it acts in the tool proxy.
const LayerTool ProbeLayerName = "tool"

// LayerService is the probe layer that changes how the run's services start
// (ProbeApply.ServiceEnv).
const LayerService ProbeLayerName = "service"

// FamilyHostileTool is the failure family of attacks delivered through a tool
// result. It is not one of the three PS2 families.
const FamilyHostileTool PS2Family = "hostile_tool"

// Answer is the agent's exported answer (answer.txt in the run folder).
type Answer struct {
	Text      string `json:"text"`
	Source    string `json:"source"` // file | final_message | missing
	SHA256    string `json:"sha256"`
	Truncated bool   `json:"truncated,omitempty"`
}

// SessionResult is how one session of a multi-session run ended.
type SessionResult struct {
	ID       string    `json:"id"`
	Index    int       `json:"index"` // 1-based
	ExitCode int       `json:"exit_code"`
	TimedOut bool      `json:"timed_out"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended"`
	Stdout   string    `json:"stdout_path"` // agent.stream.<index>.jsonl in the run folder
}

// ServiceResult is one service of a run: what ran and whether it came up.
type ServiceResult struct {
	ID      string `json:"id"`
	Role    string `json:"role,omitempty"`
	Image   string `json:"image"`
	ImageID string `json:"image_id,omitempty"`
	Ready   bool   `json:"ready"`
	Error   string `json:"error,omitempty"`
	Proxied bool   `json:"proxied,omitempty"`
}

// StateFile is one exported piece of service state in the run folder.
type StateFile struct {
	Service string `json:"service"`
	ID      string `json:"id"`
	Path    string `json:"path"` // relative to the run folder: state/<service>/<id>
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
	Error   string `json:"error,omitempty"`

	// W3 customer support: set on the service gateway export
	// (state/<service>/_gateway.json): the service's own model spend.
	Gateway *ServiceGatewayUsage `json:"gateway,omitempty"`
}

// ServiceGatewayUsage is what a service's own virtual key spent in one run,
// read from the gateway rows of that key (W3 customer support,
// environments/servicegateway.go). It is never part of the agent's cost.
type ServiceGatewayUsage struct {
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	KeyID      string  `json:"virtual_key_id,omitempty"`
	Requests   int     `json:"requests"`
	Errors     int     `json:"errors,omitempty"`
	Tokens     Tokens  `json:"tokens"`
	USD        Value   `json:"usd"`
	PriceRef   string  `json:"price_ref,omitempty"`
	BudgetUSD  float64 `json:"budget_usd,omitempty"`
	OverBudget bool    `json:"over_budget,omitempty"`
	Complete   bool    `json:"complete"`
	Reason     string  `json:"reason,omitempty"`     // why the rows are incomplete or the spend unknown
	FaultMode  string  `json:"fault_mode,omitempty"` // scripted: answered by the fault proxy's scripted model (smoke experiments)
}

// ArtifactFile is one artifact file of a run (Evidence.Artifacts). A spec
// that matched nothing has one entry with Error set and no Path. The file is
// content the agent produced: it is graded and offered for download, never
// rendered.
type ArtifactFile struct {
	ID     string `json:"id"`             // the ArtifactSpec it matched
	Name   string `json:"name,omitempty"` // base name of the file
	Path   string `json:"path,omitempty"` // relative to the run folder: artifacts/<id>/<name>; empty when not copied
	Source string `json:"source"`         // where it was read, relative to the workspace root (the spec's path when nothing matched)
	SHA256 string `json:"sha256,omitempty"`
	Bytes  int64  `json:"bytes"`
	Error  string `json:"error,omitempty"` // why it was not copied: missing, outside the workspace, not a regular file, over a size cap, too many matches
}

// ServiceHandle is a started service as the rest of the run sees it.
type ServiceHandle struct {
	Spec      ServiceSpec `json:"spec"`
	Container string      `json:"container"`
	Host      string      `json:"host"` // host name on the run's network (the service id)
	// URL is where the agent reaches the service: the tool proxy's address
	// for a proxied service, the service's own otherwise.
	URL      string `json:"url,omitempty"`
	Upstream string `json:"upstream,omitempty"` // the service's own address, behind the proxy
	Network  string `json:"network,omitempty"`  // X2: the run network the service is on (its first, for a hidden one the hidden network)
	// B2 MCP through Bifrost: URL is the gateway's per-server MCP endpoint
	// and the agent authenticates there with the run's MCP key
	// (Authorization: Bearer); the adapter adds the header. The key's value
	// is never written to disk with the handle.
	GatewayAuth bool   `json:"gateway_auth,omitempty"`
	GatewayKey  string `json:"-"`
}

// ServiceEnvironment is implemented by environments that can run services
// beside the agent. StartServices is called in the prepare stage after
// Create; ExportState in the export stage after the agent exited;
// Destroy removes the services with everything else.
type ServiceEnvironment interface {
	StartServices(ctx context.Context, rc *RunContext) ([]ServiceHandle, []ServiceResult, error)
	ExportState(ctx context.Context, rc *RunContext) ([]StateFile, error)
}

// SessionEnvironment is implemented by environments that can run several
// sessions in one run. BeginSession prepares the workspace for session index
// (0-based): it resets it unless the session keeps it, lays the session's
// files, and leaves the memory volume and memory paths untouched.
type SessionEnvironment interface {
	BeginSession(ctx context.Context, rc *RunContext, index int) error
}

// SnapshotEnvironment is implemented by environments that can freeze the
// agent container's file system after it exited, for snapshot grading. The
// returned image reference is removed by Destroy.
type SnapshotEnvironment interface {
	Snapshot(ctx context.Context, rc *RunContext) (string, error)
}

// AnswerEnvironment is implemented by environments that can read the agent's
// answer file from the workspace in a trusted container.
type AnswerEnvironment interface {
	ExportAnswer(ctx context.Context, rc *RunContext) (*Answer, error)
}

// ArtifactEnvironment is implemented by environments that can copy the
// agent's artifact files (OutputSpec.Artifacts) out of the workspace in a
// trusted container into artifacts/ in the run folder. A file the agent got
// wrong is an entry with Error set; an error return means the export itself
// broke.
type ArtifactEnvironment interface {
	ExportArtifacts(ctx context.Context, rc *RunContext) ([]ArtifactFile, error)
}

// WorkerInfo is one remote worker as GET /workers lists it.
type WorkerInfo struct {
	ID        string   `json:"id"`
	Host      string   `json:"host"`
	Version   string   `json:"version"` // bench build the worker runs
	State     string   `json:"state"`   // idle | running | lost
	Slots     int      `json:"slots"`   // concurrent runs it offers
	Running   []string `json:"running"` // slot ids it holds
	Completed int      `json:"completed"`
	// Rejected counts the worker's rejected uploads since it last
	// registered; at 3 it is leased nothing more until it registers again.
	Rejected int       `json:"rejected,omitempty"`
	LastSeen time.Time `json:"last_seen"`
	// Runners area (docs/runners.md): the runner's pool and labels; in
	// hosted mode also the runner token it registered with.
	Pool    string    `json:"pool,omitempty"`
	Labels  []string  `json:"labels,omitempty"`
	TokenID string    `json:"token_id,omitempty"`
	GPUs    []GPUInfo `json:"gpus,omitempty"` // X7: the GPUs the worker registered
	// Disk is the free disk the worker last reported with a lease request
	// (E6); Low means it takes no work until space is freed.
	Disk *WorkerDisk `json:"disk,omitempty"`
}

// External API modes (ExternalAPI.Mode, Experiment.External). Live and record
// runs reach the real host and are not repeatable; replay runs are answered
// from a cassette with no network and count like any other run.
const (
	ExternalLive   = "live"   // every allowed call goes to the real host
	ExternalRecord = "record" // as live, and each call is written to a cassette in the run folder
	ExternalReplay = "replay" // every call is answered from the cassette; nothing leaves the bench
)

// ServiceRoleExternal is the role of the handle a run gets for each external
// API it may call (ServiceHandle.Spec.Role, ServiceResult.Role). It is never
// a role a task declares for a service.
const ServiceRoleExternal = "external"

// Recorded internet (docs/egress.md, package egress): ServiceRoleEgress is
// the role of the forward proxy service the agent reaches as HTTP(S)_PROXY;
// EgressLive is the experiment egress mode that reaches real hosts.
const (
	ServiceRoleEgress = "egress"
	EgressLive        = "live-allowlisted"
)

// ExternalAPI is a real HTTP API outside the bench that a task's agent may
// call (Suite.External, Task.External; docs/external-apis.md). The agent
// reaches it only through the per-run tool proxy at
// http://toolproxy:9200/x/<id>/<path>; the proxy enforces the allowlist and
// caps, injects the credential, records every call in the tool ledger, and
// in replay mode answers from the cassette without any network.
type ExternalAPI struct {
	ID      string `json:"id" yaml:"id"`             // host-name safe; the path segment after /x/
	BaseURL string `json:"base_url" yaml:"base_url"` // http(s)://host[:port][/path]; the host is fixed
	// Allow is the allowlist: a call passes when its method and path (after
	// the base URL) match one entry. Empty allows nothing.
	Allow   []ExternalAllow   `json:"allow,omitempty" yaml:"allow"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers"` // static headers the proxy adds to every call
	// AuthEnv names a host environment variable whose value the proxy sends
	// in AuthHeader (default Authorization), after AuthScheme and a space
	// when AuthScheme is set. The agent never sees it, and it is redacted
	// from the ledger, the cassette and the logs.
	AuthEnv    string `json:"auth_env,omitempty" yaml:"auth_env"`
	AuthHeader string `json:"auth_header,omitempty" yaml:"auth_header"`
	AuthScheme string `json:"auth_scheme,omitempty" yaml:"auth_scheme"` // e.g. Bearer
	// Mode is live, record or replay. Empty means replay when the cassette
	// exists; live and record also need the experiment to opt in with
	// `external:`.
	Mode     string `json:"mode,omitempty" yaml:"mode"`
	Cassette string `json:"cassette,omitempty" yaml:"cassette"`   // JSONL, relative to the task folder (task APIs) or the suite root (suite APIs)
	MaxCalls int    `json:"max_calls,omitempty" yaml:"max_calls"` // calls per run; default 200
	MaxBytes int64  `json:"max_bytes,omitempty" yaml:"max_bytes"` // per request and per response body; default 4 MiB
	TimeoutS int    `json:"timeout_s,omitempty" yaml:"timeout_s"` // per call; default 30
	// MCP marks the API as a streamable HTTP MCP server outside the bench
	// (docs/mcp-server-testing.md): base_url is its MCP endpoint, agents get
	// it as an MCP server (BENCH_MCP_SERVERS and their native config) at
	// http://toolproxy:9200/x/<id>, an empty allowlist allows POST and DELETE
	// on the endpoint, ledger rows name the tool called, and mode live needs
	// no experiment opt-in because the server itself is what is measured.
	MCP bool `json:"mcp,omitempty" yaml:"mcp"`
}

// ExternalAllow is one allowlist entry of an external API.
type ExternalAllow struct {
	Methods    []string `json:"methods,omitempty" yaml:"methods"` // default GET
	PathPrefix string   `json:"path_prefix" yaml:"path_prefix"`   // matched against the path after the base URL, e.g. /rates
}
