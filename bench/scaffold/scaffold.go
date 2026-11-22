// Package scaffold writes the files of a new Heimdall project, suite or task:
// `bifrost-bench init`, `suite init` and `task init`. Besides the repository
// templates it has scenario templates (tool-use, mcp, browser, memory, qa) that write
// a suite with a complete sample task, and InitScenarioTask copies a task's
// shape into a new one (docs/scenarios.md).
//
// Every file a fresh project needs and that otherwise lives only in the bench
// repository (the gateway stack, the pricing snapshot and Bifrost's
// datasheets, the control agents, the default policy, the suite toolchain
// images) is embedded here, so a project works with only the binary and
// Docker. The generators are pure: they return Files and never touch the
// disk. WriteFiles and InitTask, which reads a git repository with plumbing
// commands, are the only functions that do I/O.
//
// Template files follow three naming rules, so that nothing under
// templates/ is mistaken for something else by git or the Go tool: a path
// segment starting with "dot-" is written with a leading "." instead, a
// ".tmpl" file is rendered with text/template (delimiters [[ ]]) and loses
// the suffix, a ".raw" file loses the suffix unrendered, and a ".gz" file is
// written decompressed without the suffix.
package scaffold

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/voice" // X8 voice agents
)

//go:embed all:templates
var templates embed.FS

// File is one file to write, relative to the folder it is written into.
type File struct {
	Path string      // slash-separated and relative
	Data []byte      // content; empty for a symlink
	Mode fs.FileMode // permission bits: 0o644, or 0o755 for scripts
	Link string      // symlink target when set
}

// Suite template names (`suite init -template`).
const (
	TemplatePythonPytest  = "python-pytest"
	TemplateNodeTest      = "node-test"
	TemplateGoTest        = "go-test"
	TemplateGenericReward = "generic-reward"
)

// Scenario suite template names (`suite init -template`, `task init -kind`).
// Each writes a suite with one complete sample task whose result is not a
// diff: a tool server's final state (tool-use, mcp), a web application's
// final state reached through a browser (browser), or an answer (memory,
// qa). docs/scenarios.md and docs/browser-tasks.md are the user guides.
const (
	TemplateToolUse = "tool-use"
	TemplateMCP     = "mcp"
	TemplateBrowser = "browser"
	TemplateMemory  = "memory"
	TemplateQA      = "qa"
	// TemplateExternal writes a task whose agent calls an external HTTP API
	// through the tool proxy, replayed from a cassette (docs/external-apis.md).
	TemplateExternal = "external-api"
	// TemplateArtifact writes a task graded on the files the agent leaves in
	// its workspace (output kind artifact).
	TemplateArtifact = "artifact"
	// TemplateComputerUse writes a task whose agent operates a desktop
	// service through screenshots, mouse and keyboard (docs/computer-use.md).
	TemplateComputerUse = "computer-use"
	// TemplateSupport writes a customer support suite: a helpdesk MCP
	// server and a simulated customer (W3, docs/customer-support.md).
	TemplateSupport = "support"
	// TemplateResearch writes a research suite: web search through Tavily,
	// replayed from a cassette, answers with cited sources (W4,
	// docs/research-tasks.md).
	TemplateResearch = "research"
	// TemplateVoice writes a voice agent suite: a helpdesk MCP server and a
	// voice caller the agent talks to in audio (X8, docs/voice-agents.md).
	TemplateVoice = "voice"
	// TemplateKnowledgeBase writes a knowledge-base (RAG) suite: questions
	// answered from the customer's documents behind a search service (MCP
	// search_docs and read_doc), matched against a hidden answer (Y14).
	TemplateKnowledgeBase = "knowledge-base"
)

// Grader part names the suite templates select.
const (
	graderHiddenTests     = "hidden_tests"
	graderVerifierCommand = "verifier_command"
)

// idRE is the id pattern of spec/suite.schema.json and spec/task.schema.json.
var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// SuiteTemplates lists the suite templates in a stable order.
func SuiteTemplates() []string {
	return append([]string{TemplatePythonPytest, TemplateNodeTest, TemplateGoTest, TemplateGenericReward}, ScenarioTemplates()...)
}

// ScenarioTemplates lists the scenario suite templates in a stable order.
func ScenarioTemplates() []string {
	return []string{TemplateToolUse, TemplateMCP, TemplateBrowser, TemplateMemory, TemplateQA, TemplateExternal, TemplateArtifact, TemplateComputerUse, TemplateSupport, TemplateResearch, TemplateVoice, TemplateKnowledgeBase} // W4 research; X8 voice; Y14 knowledge base
}

// IsScenarioTemplate reports whether tmpl is a scenario suite template.
func IsScenarioTemplate(tmpl string) bool {
	_, ok := scenarioSamples[tmpl]
	return ok
}

// ScenarioSample names the sample task a scenario template writes, or "".
func ScenarioSample(tmpl string) string { return scenarioSamples[tmpl] }

// scenarioSamples names the sample task each scenario template writes.
var scenarioSamples = map[string]string{
	TemplateToolUse:  "restock",
	TemplateMCP:      "restock",
	TemplateBrowser:  "new-customer",
	TemplateMemory:   "launch-date",
	TemplateQA:       "hotel-cap",
	TemplateExternal: "invoice-fx",
	TemplateArtifact: "sales-report",

	TemplateComputerUse: "edit-config",
	TemplateSupport:     "refund-in-policy", // W3 customer support
	TemplateResearch:    "ferry-start",      // W4 research with Tavily

	TemplateVoice: "refund-in-policy", // X8 voice agents

	TemplateKnowledgeBase: "refund-window", // Y14 knowledge base
}

// stateTemplate reports whether a scenario template grades a service's
// final state (as opposed to an answer).
func stateTemplate(tmpl string) bool {
	return tmpl == TemplateToolUse || tmpl == TemplateMCP || tmpl == TemplateBrowser || tmpl == TemplateComputerUse || tmpl == TemplateSupport ||
		tmpl == TemplateVoice // X8 voice agents
}

// inventoryTemplate reports whether a state template is built on the shared
// inventory tool server, its tool-error probe and the shared state verifier.
// The browser template brings its own services, probes, verifier and README.
func inventoryTemplate(tmpl string) bool {
	return tmpl == TemplateToolUse || tmpl == TemplateMCP
}

// ValidID reports whether id is usable as a suite or task id (and folder).
func ValidID(id string) bool { return idRE.MatchString(id) }

// ProjectOptions configures Project.
type ProjectOptions struct {
	Name string // shown in the README; the folder name by default
}

// Project returns every file of a new project folder: deploy/ (bench.yaml
// with relative paths, the compose stack, .env.example, the bench and helper
// image Dockerfiles, the pricing snapshot and Bifrost's config and
// datasheets), the control agents, an empty benchmarks/ with a README, a
// smoke experiment, policies/default.yaml, .gitignore and README.md.
func Project(opts ProjectOptions) ([]File, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "heimdall"
	}
	return renderTree("templates/project", map[string]any{"Name": name})
}

// ProjectDefault returns the embedded default of one project file, by its
// path in a project ("deploy/pricing-snapshot.json", "policies/default.yaml",
// "agents/oracle.yaml"), so a command can fall back to it when a project
// lacks the file.
func ProjectDefault(rel string) ([]byte, error) {
	files, err := Project(ProjectOptions{})
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.Path == rel {
			return f.Data, nil
		}
	}
	return nil, fmt.Errorf("no embedded default for %s", rel)
}

// SuiteOptions configures Suite.
type SuiteOptions struct {
	ID       string
	Template string
	// Image is an existing toolchain image to use instead of building
	// base/Dockerfile; no Dockerfile is written then. Empty builds
	// bench/<id>-base:1 from base/Dockerfile.
	Image string
}

// suiteAbout is the template-specific paragraph of a suite README.
var suiteAbout = map[string]string{
	TemplatePythonPytest: "Each task's hidden pytest files run with the `hidden_tests` grader. pytest runs in the\n" +
		"helper image (`images.helper` in deploy/bench.yaml), so keep it and base/Dockerfile in step.\n" +
		"Required test ids are `path/test_file.py::test_name`.",
	TemplateNodeTest: "Each task's hidden node:test files run with the `verifier_command` grader and report JUnit\n" +
		"XML. Node's JUnit reporter names every test case with classname `test`, so required test ids\n" +
		"are `test::<test name>`.",
	TemplateGoTest: "Each task's hidden _test.go files run with `go test -json ./...` from a copy of the workspace,\n" +
		"converted to JUnit XML by go-junit (base/gojunit). Required test ids are\n" +
		"`<import path>::<TestName>`.",
	TemplateGenericReward: "Each task's verifier/tests/test.sh decides and writes a reward to /grader/out/reward.json\n" +
		"(the Harbor task shape). A reward of at least `pass_threshold` passes.",
	TemplateToolUse: "Each task starts an HTTP tool server beside the agent, seeded with the task's data. The agent\n" +
		"reaches it through the per-run tool proxy (every call lands in the run's tools.jsonl), and the\n" +
		"hidden verifier checks the server's final state, exported after the agent exits.",
	TemplateMCP: "Each task starts an MCP server (streamable HTTP) beside the agent, seeded with the task's data.\n" +
		"The agent is told about it in its own config format and reaches it through the per-run tool\n" +
		"proxy (every call lands in the run's tools.jsonl); the hidden verifier checks the server's\n" +
		"final state, exported after the agent exits.",
	TemplateBrowser: "Each task starts a web application and a headless browser (Playwright MCP) beside the agent.\n" +
		"The web application is hidden from the agent: the agent drives the browser over MCP through the\n" +
		"per-run tool proxy (every navigate, click and type lands in the run's tools.jsonl), and the\n" +
		"hidden verifier checks the web application's database, exported after the agent exits.",
	TemplateMemory: "Each task runs the agent once per session, resetting the workspace in between, so only the\n" +
		"agent's memory carries facts forward. The last session's answer is matched against the\n" +
		"task's hidden answer.yaml by the answer_match grader, with no model.",
	TemplateKnowledgeBase: "Each task asks one question the agent answers from your documents, which are not in its\n" + // Y14 knowledge base
		"workspace but behind a search service (services/kb/: MCP search_docs and read_doc over\n" +
		"services/kb/docs/, part of the suite's lock). The answer is matched against the task's hidden\n" +
		"answer.yaml by the answer_match grader, with no model; the searches and reads are exported.",
	TemplateQA: "Each task asks one question about the documents in the workspace. The answer is matched\n" +
		"against the task's hidden answer.yaml by the answer_match grader, with no model.",
	TemplateArtifact: "Each task's result is files the agent writes in its workspace. After the agent exits the\n" +
		"bench copies the files the task lists under output.artifacts into the run folder, and the\n" +
		"hidden verifier reads them read-only at /grader/artifacts/<id>/<file name>. A required\n" +
		"artifact that is missing fails the run before the verifier runs.",
	TemplateExternal: "Each task lets the agent call an external HTTP API, reached only through the per-run tool\n" +
		"proxy, which enforces an allowlist, adds the credential and records every call in tools.jsonl.\n" +
		"Runs replay the task's cassette with no network unless an experiment opts in to live calls;\n" +
		"the hidden verifier checks the file the agent wrote.",
	TemplateComputerUse: "Each task starts a desktop beside the agent: a virtual display, a window manager and one GUI\n" +
		"app, seeded with the task's files. The agent sees it only through screenshots and acts with\n" +
		"mouse and keyboard tools over MCP, through the per-run tool proxy (every call lands in the\n" +
		"run's tools.jsonl). Grading never reads a screenshot: the hidden verifier checks the\n" +
		"desktop's state (saved files, app data), dumped as JSON after the agent exits.",
	TemplateResearch: "Each task asks a research question the agent answers by searching the web through Tavily, reached\n" + // W4 research with Tavily
		"only through the per-run tool proxy (every search and result lands in tools.jsonl) and replayed\n" +
		"from the suite's cassette with no network. The hidden verifier checks the answer and that the\n" +
		"cited sources are real pages holding the evidence, with no judge.",
	TemplateSupport: "Each task starts a helpdesk MCP server and a simulated customer (the user simulator) beside\n" +
		"the agent. The agent talks to the customer only with respond_to_user and works the helpdesk\n" +
		"through the per-run tool proxy; the hidden verifier scores the helpdesk's final state, the\n" +
		"support policy and what the customer was told (docs/customer-support.md).",
	TemplateVoice: "Each task starts a helpdesk MCP server and a voice caller beside the agent. The agent dials\n" + // X8 voice agents
		"the caller (heimdall-voice/1 over WebSocket), hears the customer's speech and answers in audio,\n" +
		"and works the helpdesk through the per-run tool proxy. The caller's timeline gives the voice\n" +
		"metrics; the hidden verifier scores the helpdesk's final state, the policy and what the\n" +
		"customer heard (docs/voice-agents.md).",
}

// Suite returns the files of a new suite folder from a template: suite.yaml,
// base/Dockerfile (unless opts.Image names an existing image), README.md and
// the model-outage probe. The suite starts with no tasks.
func Suite(opts SuiteOptions) ([]File, error) {
	if !ValidID(opts.ID) {
		return nil, fmt.Errorf("suite id %q: want lowercase letters, digits, '.', '_' or '-'", opts.ID)
	}
	about, ok := suiteAbout[opts.Template]
	if !ok {
		return nil, fmt.Errorf("unknown suite template %q (want %s)", opts.Template, strings.Join(SuiteTemplates(), ", "))
	}
	image := strings.TrimSpace(opts.Image)
	var build string
	if image == "" {
		image = "bench/" + opts.ID + "-base:1"
		build = "docker build -t " + image + " benchmarks/" + opts.ID + "/base"
	} else {
		build = "# base image " + image + ": uid 1000 user, git, /workspace and /grader/out owned by it"
	}
	data := map[string]any{
		"ID": opts.ID, "Template": opts.Template, "Image": image,
		"Dockerfile": strings.TrimSpace(opts.Image) == "", "About": about, "Build": build,
		"Sample": scenarioSamples[opts.Template], "State": stateTemplate(opts.Template), "Memory": opts.Template == TemplateMemory,
		"Artifact": opts.Template == TemplateArtifact,
	}
	files, err := renderTree("templates/suites/"+opts.Template, data)
	if err != nil {
		return nil, err
	}
	common, err := renderTree("templates/suites/common", data)
	if err != nil {
		return nil, err
	}
	if IsScenarioTemplate(opts.Template) {
		if common, err = scenarioCommon(opts.Template, common, data); err != nil {
			return nil, err
		}
	}
	files = append(files, common...)
	if opts.Template == TemplateSupport { // W3 customer support: the user simulator beside the helpdesk server
		for rel, b := range simuser.Files() {
			files = append(files, File{Path: "services/helpdesk/" + rel, Data: b, Mode: 0o644})
		}
	}
	if opts.Template == TemplateVoice { // X8 voice agents: the text simulator and the voice caller beside the helpdesk server
		for rel, b := range simuser.Files() {
			files = append(files, File{Path: "services/helpdesk/" + rel, Data: b, Mode: 0o644})
		}
		for rel, b := range voice.Files(voice.CallerFiles) {
			files = append(files, File{Path: "services/helpdesk/" + rel, Data: b, Mode: 0o644})
		}
	}
	if strings.TrimSpace(opts.Image) != "" {
		kept := files[:0]
		for _, f := range files {
			if !strings.HasPrefix(f.Path, "base/") {
				kept = append(kept, f)
			}
		}
		files = kept
	}
	sortFiles(files)
	return files, nil
}

// scenarioCommon swaps the repository-task README of common for the
// scenario README and adds what every scenario template shares: the base
// Dockerfile, and for the inventory templates the tool-error probe and the
// state verifier of the sample task. The browser template keeps its own
// README and verifier and takes only the base Dockerfile.
func scenarioCommon(tmpl string, common []File, data map[string]any) ([]File, error) {
	shared, err := renderTree("templates/scenario", data)
	if err != nil {
		return nil, err
	}
	out := common[:0]
	for _, f := range common {
		if f.Path != "README.md" {
			out = append(out, f)
		}
	}
	for _, f := range shared {
		switch {
		case f.Path == "base/Dockerfile":
		case f.Path == "README.md" && tmpl != TemplateBrowser && tmpl != TemplateExternal && tmpl != TemplateComputerUse && tmpl != TemplateResearch && tmpl != TemplateSupport && tmpl != TemplateVoice: // W3: support brings its own README; X8: voice too
		case inventoryTemplate(tmpl) && f.Path == "probes/tool-error.yaml":
		case (inventoryTemplate(tmpl) || tmpl == TemplateComputerUse) && f.Path == "verify.py":
			f.Path = "tasks/" + scenarioSamples[tmpl] + "/verifier/tests/verify.py"
		default:
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// WriteFiles writes files under dir, creating folders as needed. An existing
// file is an error unless overwrite is set.
func WriteFiles(dir string, files []File, overwrite bool) error {
	for _, f := range files {
		if err := checkRel(f.Path); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if _, err := os.Lstat(dst); err == nil {
			if !overwrite {
				return fmt.Errorf("write %s: file exists", dst)
			}
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("write %s: %w", dst, err)
			}
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
		if f.Link != "" {
			if err := os.Symlink(f.Link, dst); err != nil {
				return fmt.Errorf("write %s: %w", dst, err)
			}
			continue
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(dst, f.Data, mode); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
		// WriteFile applies the umask; scripts must stay executable.
		if err := os.Chmod(dst, mode); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	return nil
}

// renderTree turns every embedded file under root into a File, applying the
// naming rules in the package comment.
func renderTree(root string, data any) ([]File, error) {
	var files []File
	err := fs.WalkDir(templates, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := templates.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		f, err := renderFile(rel, raw, data)
		if err != nil {
			return fmt.Errorf("template %s: %w", p, err)
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", root, err)
	}
	sortFiles(files)
	return files, nil
}

// renderFile applies the naming rules to one template file.
func renderFile(rel string, raw []byte, data any) (File, error) {
	parts := strings.Split(rel, "/")
	for i, s := range parts {
		if strings.HasPrefix(s, "dot-") {
			parts[i] = "." + strings.TrimPrefix(s, "dot-")
		}
	}
	out := strings.Join(parts, "/")
	body := raw
	switch {
	case strings.HasSuffix(out, ".tmpl"):
		out = strings.TrimSuffix(out, ".tmpl")
		b, err := render(rel, string(raw), data)
		if err != nil {
			return File{}, err
		}
		body = b
	case strings.HasSuffix(out, ".raw"):
		out = strings.TrimSuffix(out, ".raw")
	case strings.HasSuffix(out, ".gz"):
		out = strings.TrimSuffix(out, ".gz")
		b, err := gunzip(raw)
		if err != nil {
			return File{}, err
		}
		body = b
	}
	mode := fs.FileMode(0o644)
	if strings.HasSuffix(out, ".sh") {
		mode = 0o755
	}
	return File{Path: out, Data: body, Mode: mode}, nil
}

// render executes one text/template with [[ ]] delimiters, so files that use
// {{ }} themselves (agent configs, compose) need no escaping.
func render(name, text string, data any) ([]byte, error) {
	t, err := template.New(name).Delims("[[", "]]").Option("missingkey=error").Funcs(template.FuncMap{
		"q":    quote,
		"flow": flowList,
	}).Parse(text)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// quote renders s as a double-quoted scalar, valid YAML because it is valid
// JSON.
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// flowList renders a YAML flow sequence of quoted strings.
func flowList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	return out, nil
}

func sortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

// checkRel refuses paths that are empty, absolute, or leave the folder.
func checkRel(p string) error {
	if p == "" || path.IsAbs(p) || strings.HasPrefix(p, "/") {
		return errors.New("want a relative path")
	}
	c := path.Clean(p)
	if c != p || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return errors.New("path leaves the folder or is not clean")
	}
	return nil
}
