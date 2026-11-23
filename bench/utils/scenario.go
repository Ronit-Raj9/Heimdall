package utils

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Control kinds: what a task's admission control files are (TaskControls).
// The kind follows the task's output and the reference file's extension, so a
// task written before scenarios is always a patch task.
const (
	ControlPatch  = "patch"  // .diff files applied to the workspace
	ControlScript = "script" // .sh files run in the agent's place, services up
	ControlAnswer = "answer" // text files holding an answer
)

// DefaultAnswerPath is where an answer task's agent writes its answer,
// relative to the workdir, when the task names no answer_path.
const DefaultAnswerPath = "answer.txt"

// DefaultMemoryDir is the container path of the memory volume kept between
// the sessions of a multi-session task (BENCH_MEMORY_DIR).
const DefaultMemoryDir = "/memory"

// DefaultMCPPath is the MCP endpoint path when a service names none.
const DefaultMCPPath = "/mcp"

// Agent environment variables of scenario runs.
const (
	EnvMCPServers   = "BENCH_MCP_SERVERS"
	EnvMemoryDir    = "BENCH_MEMORY_DIR"
	EnvSessionID    = "BENCH_SESSION_ID"
	EnvSessionIndex = "BENCH_SESSION_INDEX"
	EnvSessionCount = "BENCH_SESSION_COUNT"
	EnvAnswerPath   = "BENCH_ANSWER_PATH"
)

// OutputFile is the output kind of a task whose test reads one fixed file
// the agent writes (output: {kind: file, path: ...}), such as BFCL's
// /app/result.json. Compatibility words name it "a file the agent writes"
// (fix wave 3, N187).
const OutputFile = "file"

// OutputKind is the task's output kind: the task's own, else the suite's,
// else diff.
func OutputKind(s *schemas.Suite, t *schemas.Task) string {
	if t != nil && t.Output != nil && t.Output.Kind != "" {
		return t.Output.Kind
	}
	if s != nil && s.Output != nil && s.Output.Kind != "" {
		return s.Output.Kind
	}
	return schemas.OutputDiff
}

// AnswerPath is the workspace-relative file an answer task's agent writes:
// the task's answer_path, else the suite's, else answer.txt.
func AnswerPath(s *schemas.Suite, t *schemas.Task) string {
	if t != nil && t.Output != nil && t.Output.AnswerPath != "" {
		return t.Output.AnswerPath
	}
	if s != nil && s.Output != nil && s.Output.AnswerPath != "" {
		return s.Output.AnswerPath
	}
	return DefaultAnswerPath
}

// AnswerContainerPath is AnswerPath under the suite's workdir.
func AnswerContainerPath(s *schemas.Suite, t *schemas.Task) string {
	workdir := "/workspace"
	if s != nil && s.Workdir != "" {
		workdir = s.Workdir
	}
	return path.Join(workdir, AnswerPath(s, t))
}

// VerifierMode is the suite's verifier mode, clean_room when unset.
func VerifierMode(s *schemas.Suite) string {
	if s != nil && s.Verifier.Mode != "" {
		return s.Verifier.Mode
	}
	return schemas.VerifierCleanRoom
}

// Grading is the suite's grading level, verified when unset.
func Grading(s *schemas.Suite) string {
	if s != nil && s.Grading != "" {
		return s.Grading
	}
	return schemas.GradingVerified
}

// Track is the suite's leaderboard track, coding when unset.
func Track(s *schemas.Suite) string {
	if s != nil && s.Track != "" {
		return s.Track
	}
	return schemas.TrackCoding
}

// ControlKind says what the task's admission controls are: answer files for
// an answer task, scripts when the reference is a .sh file, patches
// otherwise.
func ControlKind(s *schemas.Suite, t *schemas.Task) string {
	switch {
	case OutputKind(s, t) == schemas.OutputAnswer:
		return ControlAnswer
	case t != nil && strings.HasSuffix(t.Controls.Reference, ".sh"):
		return ControlScript
	default:
		return ControlPatch
	}
}

// TaskServices lists the services of one run: the suite's first, then the
// task's, in file order.
func TaskServices(s *schemas.Suite, t *schemas.Task) []schemas.ServiceSpec {
	var out []schemas.ServiceSpec
	if s != nil {
		out = append(out, s.Services...)
	}
	if t != nil {
		out = append(out, t.Services...)
	}
	return out
}

// ServiceProxied reports whether the agent's calls to a service go through
// the per-run tool proxy: as the service says, else true for tool and mcp
// services.
func ServiceProxied(spec schemas.ServiceSpec) bool {
	if spec.Proxy != nil {
		return *spec.Proxy
	}
	return spec.Role == schemas.ServiceRoleTool || spec.Role == schemas.ServiceRoleMCP
}

// HasExports reports whether any service declares a state export.
func HasExports(services []schemas.ServiceSpec) bool {
	for _, sv := range services {
		if len(sv.Export) > 0 {
			return true
		}
	}
	return false
}

// MCPServers maps every started mcp service the agent can see to the URL of
// its MCP endpoint: the handle's URL, with the service's MCP path added when
// the URL does not already end with it.
func MCPServers(services []schemas.ServiceHandle) map[string]string {
	out := map[string]string{}
	for _, h := range services {
		if h.Spec.Role == schemas.ServiceRoleExternal && h.Spec.MCP != nil && h.URL != "" { // an external MCP server: the proxy URL is its endpoint
			out[h.Spec.ID] = strings.TrimRight(h.URL, "/")
			continue
		}
		if h.Spec.Role != schemas.ServiceRoleMCP || h.Spec.Hidden || h.URL == "" {
			continue
		}
		p := DefaultMCPPath
		if h.Spec.MCP != nil && h.Spec.MCP.Path != "" {
			p = h.Spec.MCP.Path
		}
		u := strings.TrimRight(h.URL, "/")
		if !strings.HasSuffix(u, p) && !h.GatewayAuth { // B2 MCP through Bifrost: the URL is already the gateway's endpoint for the server
			u += p
		}
		out[h.Spec.ID] = u
	}
	return out
}

// MCPServersJSON is MCPServers as the JSON object BENCH_MCP_SERVERS holds:
// {"<id>": {"url": "..."}}, with keys sorted.
func MCPServersJSON(services []schemas.ServiceHandle) (string, error) {
	type server struct {
		URL string `json:"url"`
	}
	m := map[string]server{}
	for id, u := range MCPServers(services) {
		m[id] = server{URL: u}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encode mcp servers: %w", err)
	}
	return string(b), nil
}
