package mcptest

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
	"gopkg.in/yaml.v3"
)

//go:embed assets/verify.py
var verifyPy []byte

//go:embed assets/call.py
var callPy []byte

// Draft limits and fixed values.
const (
	DefaultMaxTasks = 20
	MaxTasks        = 60
	// ServerFile is the listing Draft keeps at the suite root, so the server
	// report can check arguments against the schemas the tasks were drafted from.
	ServerFile    = "mcp-server.json"
	draftImage    = "bench/coding-core-base:1"
	draftCreator  = "heimdall mcp-test draft"
	suiteVersion  = "1.0.0"
	agentTimeoutS = 600
	maxCallsRun   = 60
)

var (
	serverIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	envNameRe  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	headerRe   = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	slugRe     = regexp.MustCompile(`[^a-z0-9]+`)
)

// DraftOptions describe the benchmark Draft writes.
type DraftOptions struct {
	SuiteID  string
	ServerID string // the external API id, host-name safe; agents see the server under this name
	URL      string // the MCP endpoint
	// AuthEnv names the org secret holding the credential; AuthHeader
	// (default Authorization) and AuthScheme (for example Bearer) say how
	// the proxy sends it. Empty AuthEnv: the server takes no credential.
	AuthEnv, AuthHeader, AuthScheme string
	Report                          *Report
	Tools                           []string // the tools to draft tasks for; empty: every listed tool
	MaxTasks                        int
}

// DraftTask is one drafted task.
type DraftTask struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Title     string `json:"title"`
	Reference bool   `json:"reference"` // the reference control is proven: a sample call with its arguments worked at inspection
}

// DraftResult is what Draft wrote.
type DraftResult struct {
	Suite string      `json:"suite"`
	Tasks []DraftTask `json:"tasks"`
	// NoReference lists tasks whose reference control holds placeholder
	// arguments: their readiness check passes only once someone puts in
	// arguments the server accepts.
	NoReference []string `json:"no_reference"`
	Files       []string `json:"files"`
}

// serverFile is the content of ServerFile.
type serverFile struct {
	Server    string     `json:"server"`
	URL       string     `json:"url"`
	Name      string     `json:"name,omitempty"`
	Version   string     `json:"version,omitempty"`
	CheckedAt string     `json:"checked_at"`
	Scores    Scores     `json:"scores"`
	Tools     []ToolView `json:"tools"`
}

// CheckDraftOptions validates o without the report.
func CheckDraftOptions(o DraftOptions) error {
	var errs []error
	if !scaffold.ValidID(o.SuiteID) || len(o.SuiteID) > 64 {
		errs = append(errs, errors.New("benchmark id: use 1 to 64 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit"))
	}
	if !serverIDRe.MatchString(o.ServerID) {
		errs = append(errs, errors.New("server name: use lowercase letters, digits and dashes, up to 41 characters"))
	}
	if u, err := url.Parse(o.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		errs = append(errs, errors.New("url: an http(s) MCP endpoint with no credentials, query or fragment, for example https://mcp.example.com/mcp"))
	}
	if o.AuthEnv != "" && !envNameRe.MatchString(o.AuthEnv) {
		errs = append(errs, errors.New("secret: an org secret name such as MY_MCP_TOKEN"))
	}
	if o.AuthHeader != "" && !headerRe.MatchString(o.AuthHeader) {
		errs = append(errs, errors.New("auth header: a header name such as Authorization or X-Api-Key"))
	}
	if strings.ContainsAny(o.AuthScheme, " \t\r\n") {
		errs = append(errs, errors.New("auth scheme: one word such as Bearer"))
	}
	return errors.Join(errs...)
}

// Draft writes a benchmark for the inspected server: one task per tool (up
// to MaxTasks) whose agent must reach the tool's goal through the live
// server, graded from the tool proxy's ledger. Tasks are drafts: their
// instructions are marked unreviewed, and only tools with a working sample
// call get a reference control. It is pure.
func Draft(o DraftOptions) ([]scaffold.File, DraftResult, error) {
	if err := CheckDraftOptions(o); err != nil {
		return nil, DraftResult{}, err
	}
	if o.Report == nil || !o.Report.Reached || len(o.Report.Tools) == 0 {
		return nil, DraftResult{}, errors.New("inspect the server first: it must answer and list at least one tool")
	}
	if o.MaxTasks <= 0 {
		o.MaxTasks = DefaultMaxTasks
	}
	o.MaxTasks = min(o.MaxTasks, MaxTasks)
	want := map[string]bool{}
	for _, t := range o.Tools {
		want[t] = true
	}
	okCall := map[string]ToolCallResult{}
	for _, c := range o.Report.Calls {
		if _, seen := okCall[c.Tool]; c.OK && !seen {
			okCall[c.Tool] = c
		}
	}
	var picked []ToolView
	for _, t := range o.Report.Tools {
		if len(want) == 0 || want[t.Name] {
			picked = append(picked, t)
		}
	}
	if len(picked) == 0 {
		return nil, DraftResult{}, errors.New("none of the chosen tools is on the server's list")
	}
	if len(picked) > o.MaxTasks {
		picked = picked[:o.MaxTasks]
	}

	res := DraftResult{Suite: o.SuiteID, Tasks: []DraftTask{}, NoReference: []string{}}
	var files []scaffold.File
	add := func(path string, data []byte, mode fs.FileMode) {
		files = append(files, scaffold.File{Path: path, Data: data, Mode: mode})
		res.Files = append(res.Files, path)
	}
	used := map[string]bool{}
	var ids []string
	server := serverName(o)
	for _, t := range picked {
		id := taskID(t.Name, used)
		ids = append(ids, id)
		call, hasRef := okCall[t.Name]
		dt := DraftTask{ID: id, Tool: t.Name, Title: taskTitle(t), Reference: hasRef}
		res.Tasks = append(res.Tasks, dt)
		dir := "tasks/" + id + "/"
		add(dir+"task.yaml", taskYAML(o, t, dt), 0o644)
		add(dir+"instruction.md", []byte(instruction(server, t, call, hasRef)), 0o644)
		add(dir+"repo/README.md", []byte("Workspace of this task. The work happens through the MCP server; nothing here needs to change.\n"), 0o644)
		expect, _ := json.MarshalIndent(map[string]any{"server": o.ServerID, "tool": t.Name, "schema": rawOrEmpty(t.InputSchema)}, "", "  ")
		add(dir+"verifier/tests/expect.json", append(expect, '\n'), 0o644)
		add(dir+"verifier/tests/verify.py", verifyPy, 0o755)
		add(dir+"controls/empty.sh", []byte("#!/bin/sh\n# Empty control: does nothing, so the tool is never called. Admission requires it to fail.\nexit 0\n"), 0o755)
		add(dir+"controls/exploit.sh", []byte(exploitScript()), 0o755)
		add(dir+"controls/mutant-wrong-types.sh", []byte(callScript(o.ServerID, t.Name, wrongArgs(t.InputSchema),
			"Mutant control: calls the tool with arguments of the wrong type. Admission requires it to fail.")), 0o755)
		if hasRef {
			add(dir+"solution/reference.sh", []byte(callScript(o.ServerID, t.Name, call.Args,
				"Reference control: calls the tool with arguments the server accepted when it was inspected.")), 0o755)
		} else {
			add(dir+"solution/reference.sh", []byte(callScript(o.ServerID, t.Name, PlaceholderArgs(t.InputSchema),
				"Reference control, NOT PROVEN: the tool was not called at inspection, so these are placeholder\n# arguments from its schema. Replace them with arguments your server accepts, then run the readiness check.")), 0o755)
			res.NoReference = append(res.NoReference, id)
		}
	}
	add("suite.yaml", suiteYAML(o, ids), 0o644)
	sf := serverFile{Server: o.ServerID, URL: o.URL, Name: o.Report.Hello.Name, Version: o.Report.Hello.Version,
		CheckedAt: o.Report.CheckedAt.Format("2006-01-02T15:04:05Z"), Scores: o.Report.Scores, Tools: o.Report.Tools}
	sb, _ := json.MarshalIndent(sf, "", "  ")
	add(ServerFile, append(sb, '\n'), 0o644)
	sort.Strings(res.Files)
	return files, res, nil
}

func serverName(o DraftOptions) string {
	if o.Report != nil && o.Report.Hello.Name != "" {
		return o.Report.Hello.Name
	}
	return o.ServerID
}

func rawOrEmpty(r json.RawMessage) any {
	var v any
	if json.Unmarshal(r, &v) != nil || v == nil {
		return map[string]any{"type": "object"}
	}
	return v
}

func taskID(tool string, used map[string]bool) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(tool), "-"), "-")
	if s == "" {
		s = "tool"
	}
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-")
	}
	id := "tool-" + s
	for n := 2; used[id]; n++ {
		id = fmt.Sprintf("tool-%s-%d", s, n)
	}
	used[id] = true
	return id
}

// firstSentence is the start of a description up to its first full stop.
func firstSentence(d string) string {
	d = strings.Join(strings.Fields(d), " ")
	if i := strings.Index(d, ". "); i > 0 {
		d = d[:i]
	}
	return strings.TrimSuffix(d, ".")
}

func taskTitle(t ToolView) string {
	goal := firstSentence(t.Description)
	if goal == "" {
		goal = humanize(t.Name)
	}
	return clip("Through the server: "+lowerFirst(goal), 110)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	if len(s) > 1 && s[1] >= 'A' && s[1] <= 'Z' { // an acronym stays as it is
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// instruction is the request a user of the server would send. It states the
// goal from the tool's own description and the inputs, never the grading.
func instruction(server string, t ToolView, call ToolCallResult, hasRef bool) string {
	var b strings.Builder
	goal := firstSentence(t.Description)
	if goal == "" {
		goal = "Use the server's " + humanize(t.Name) + " operation"
	}
	fmt.Fprintf(&b, "<!-- Drafted by Heimdall from the MCP server's tool list. Review it before you rely on it. -->\n\n")
	fmt.Fprintf(&b, "You can use the MCP server %q. Use it to do this:\n\n%s.\n", server, goal)
	if hasRef && len(call.Args) > 0 {
		keys := make([]string, 0, len(call.Args))
		for k := range call.Args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\nUse these values:\n\n")
		for _, k := range keys {
			v, _ := json.Marshal(call.Args[k])
			fmt.Fprintf(&b, "- %s: %s\n", k, v)
		}
	}
	b.WriteString("\nWhen you are done, reply with what the server gave back, in one or two sentences.\n")
	return b.String()
}

func suiteYAML(o DraftOptions, tasks []string) []byte {
	ext := map[string]any{"id": o.ServerID, "base_url": o.URL, "mcp": true, "mode": "live", "max_calls": maxCallsRun, "timeout_s": 30}
	if o.AuthEnv != "" {
		ext["auth_env"] = o.AuthEnv
		if o.AuthHeader != "" {
			ext["auth_header"] = o.AuthHeader
		}
		if o.AuthScheme != "" {
			ext["auth_scheme"] = o.AuthScheme
		}
	}
	doc := map[string]any{
		"id":          o.SuiteID,
		"version":     suiteVersion,
		"description": clip("Tests of the MCP server "+serverName(o)+" ("+o.URL+"), run by standard agents against the live server.", 300),
		"track":       "tool_use",
		"environment": "docker_workspace",
		"grader":      "verifier_command",
		"base_image":  map[string]any{"tag": draftImage},
		"workdir":     "/workspace",
		"verifier": map[string]any{"command": []string{"python3", "/grader/tests/verify.py"}, "result": "reward",
			"result_path": "/grader/out/reward.json", "pass_threshold": 1, "restore": []string{}, "timeout_s": 120},
		"external": []any{ext},
		"tasks":    tasks,
		"probes":   []string{},
	}
	b, _ := yaml.Marshal(doc)
	head := "# Drafted by Heimdall from the MCP server's tool list (" + draftCreator + ").\n" +
		"# The server is the thing under test: every task runs against the live server\n" +
		"# through the run's tool proxy, which adds the credential and records each call.\n"
	return append([]byte(head), b...)
}

func taskYAML(o DraftOptions, t ToolView, dt DraftTask) []byte {
	reviewed := false
	controls := map[string]any{"reference": "solution/reference.sh", "empty": "controls/empty.sh", "exploit": "controls/exploit.sh", "mutants": []string{"controls/mutant-wrong-types.sh"}}
	doc := map[string]any{
		"id":           dt.ID,
		"version":      "1.0.0",
		"title":        dt.Title,
		"project":      o.SuiteID,
		"kind":         "feature",
		"instruction":  "instruction.md",
		"repo_dir":     "repo",
		"verifier":     map[string]any{"tests": "verifier/tests", "required": []string{}},
		"controls":     controls,
		"timeouts":     map[string]any{"agent_s": agentTimeoutS},
		"capabilities": []string{"tool_use"},
		"difficulty":   "easy",
		"provenance": map[string]any{"source": o.URL + " (tool " + t.Name + ")", "license": "proprietary",
			"created_by": draftCreator, "instruction_reviewed": &reviewed},
	}
	b, _ := yaml.Marshal(doc)
	return b
}

func callScript(server, tool string, args map[string]any, comment string) string {
	if args == nil {
		args = map[string]any{}
	}
	aj, _ := json.Marshal(args)
	return "#!/bin/sh\n# " + comment + "\npython3 -I - " + shellQuote(server) + " " + shellQuote(tool) + " " + shellQuote(string(aj)) + " <<'PY'\n" + string(callPy) + "PY\n"
}

func exploitScript() string {
	return `#!/bin/sh
# Exploit control: claims success without calling the tool, and writes fake
# reward files for a careless verifier. The grade comes from the tool proxy's
# ledger, so admission requires it to fail.
echo "I called the tool and it worked." > "${BENCH_ANSWER_PATH:-answer.txt}"
mkdir -p /grader/out 2>/dev/null && echo '{"reward": 1}' > /grader/out/reward.json 2>/dev/null
echo '{"reward": 1}' > reward.json
exit 0
`
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
