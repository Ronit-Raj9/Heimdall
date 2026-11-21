package handlers

// Connect your agent (lane X10, J3 and J4): the full forms of the bring-your-
// own templates. "Connect your HTTP endpoint" sets the URL, auth (a bearer
// or header token from Settings > Secrets, HMAC request signing), which of
// the task's tools the endpoint may use, model traffic and the timeout.
// "Wrap my existing app" starts the user's own server (from a git repository
// or a Docker image) next to each task through the http-service launcher.

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	agenttemplates "github.com/maximhq/bifrost/bench/agents/templates"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// Parameter kinds of TemplateParam.Kind.
const (
	ParamText      = "text"
	ParamSecret    = "secret"
	ParamBool      = "bool"
	ParamNumber    = "number"
	ParamSelect    = "select"
	ParamMultiline = "multiline"
)

// connectDefaultTimeout is the request timeout of a connected endpoint, in
// seconds; it stays under the default wall budget (900 s).
const connectDefaultTimeout = 600

var connectTimeoutParam = TemplateParam{Name: "timeout_s", Label: "Timeout (seconds)", Kind: ParamNumber, Default: strconv.Itoa(connectDefaultTimeout), Group: "advanced",
	Help: "How long Heimdall waits for one answer. Keep it under the run's time limit (900 s)."}

var connectHTTPParams = []TemplateParam{
	{Name: "url", Label: "Endpoint URL", Placeholder: "https://agent.example.com/v1/answer", Required: true,
		Help: "Heimdall posts each task here as JSON and reads the answer."},
	{Name: "auth_secret", Label: "Auth token", Kind: ParamSecret, Placeholder: "MY_AGENT_TOKEN", Group: "auth",
		Help: "Name of a secret in Settings > Secrets. Sent as Authorization: Bearer <token>. Leave empty if the endpoint needs no auth."},
	{Name: "auth_header", Label: "Token header", Placeholder: "Authorization", Group: "auth",
		Help: "Send the token in another header instead, for example X-API-Key (sent as is, without Bearer)."},
	{Name: "sign_secret", Label: "Signing secret", Kind: ParamSecret, Placeholder: "MY_AGENT_SIGNING_SECRET", Group: "auth",
		Help: "Name of a secret (16 characters or more). Each request then carries X-Heimdall-Signature, an HMAC-SHA256 of the body your server can check."},
	{Name: "tools", Label: "Let my agent call the task's tools", Kind: ParamBool, Default: "false", Group: "tools",
		Help: "Heimdall sends the URLs of the task's tools and MCP servers with each task. Needed for benchmarks that check what the agent changed."},
	{Name: "tool_allow", Label: "Only these tools", Placeholder: "crm, tickets", Group: "tools",
		Help: "Comma-separated tool ids. Empty allows every tool of the task."},
	{Name: "artifacts", Label: "My agent returns files in its answer", Kind: ParamBool, Default: "false", Group: "tools",
		Help: "For benchmarks that grade a file the agent makes (a report, a spreadsheet)."},
	{Name: "model_traffic", Label: "Route my agent's model calls through Heimdall", Kind: ParamBool, Default: "false", Group: "advanced",
		Help: "Each task then carries a gateway URL and a run key for your agent's model calls, so tokens and cost are measured. Off: your agent calls its own provider and reports usage itself."},
	connectTimeoutParam,
}

var connectA2AParams = []TemplateParam{
	{Name: "card_url", Label: "Agent card URL", Placeholder: "https://agent.example.com/.well-known/agent-card.json", Required: true,
		Help: "The agent card URL as Bifrost reaches it."},
	{Name: "streaming", Label: "Stream the answer", Kind: ParamBool, Default: "false", Group: "advanced",
		Help: "Follow the agent's event stream instead of polling the task."},
	{Name: "tools", Label: "Let my agent call the task's tools", Kind: ParamBool, Default: "false", Group: "tools",
		Help: "Heimdall sends the URLs of the task's tools and MCP servers in the message metadata."},
	{Name: "artifacts", Label: "My agent returns files in its answer", Kind: ParamBool, Default: "false", Group: "tools",
		Help: "File parts of the agent's artifacts are written into the task's workspace."},
	connectTimeoutParam,
}

var connectOpenAIParams = []TemplateParam{
	{Name: "url", Label: "Base URL", Placeholder: "https://agent.example.com/v1", Required: true,
		Help: "Heimdall posts to <base URL>/chat/completions (or give the full .../chat/completions or .../responses URL)."},
	{Name: "model", Label: "Model name", Placeholder: "my-agent", Help: "Sent as the model field. Set it when your endpoint picks its own model; evaluations then need no model."},
	{Name: "api_key_secret", Label: "API key", Kind: ParamSecret, Placeholder: "MY_AGENT_API_KEY", Group: "auth",
		Help: "Name of a secret in Settings > Secrets, sent as a Bearer token. Leave empty if the endpoint needs no key."},
	{Name: "tools", Label: "Offer the task's MCP tools to my endpoint", Kind: ParamBool, Default: "true", Group: "tools",
		Help: "Heimdall lists the task's MCP tools in each request and runs the tool calls your endpoint makes."},
	connectTimeoutParam,
}

// Framework hints of the wrap-app template.
var wrapFrameworks = []string{"langgraph", "crewai", "openai-agents", "custom"}

var wrapAppParams = []TemplateParam{
	{Name: "framework", Label: "Framework", Kind: ParamSelect, Options: wrapFrameworks, Default: "custom",
		Help: "Only a hint: it suggests the start command. Your server must answer POST <run path> with {status, final_message}."},
	{Name: "repo_url", Label: "Git repository", Placeholder: "https://github.com/your-org/support-agent.git",
		Help: "A public https repository, built into the agent image. Give this or a Docker image."},
	{Name: "repo_ref", Label: "Branch or tag", Placeholder: "main", Group: "advanced"},
	{Name: "image", Label: "Docker image", Placeholder: "ghcr.io/your-org/support-agent@sha256:...",
		Help: "An image that already holds your app and python3. Hosted runs need it pinned by digest and approved."},
	{Name: "command", Label: "Start command", Placeholder: "uvicorn app:api --host 127.0.0.1 --port 8000", Required: true,
		Help: "Starts your server inside the task's container. It must listen on 127.0.0.1."},
	{Name: "port", Label: "Port", Kind: ParamNumber, Default: "8000"},
	{Name: "health_path", Label: "Health check path", Default: "/health", Help: "Heimdall waits until GET on this path answers 200."},
	{Name: "run_path", Label: "Run path", Default: "/run", Group: "advanced",
		Help: "Heimdall posts {instruction, workspace, base_url, api_key, model, session_id} here."},
	{Name: "env", Label: "Environment", Kind: ParamMultiline, Placeholder: "LOG_LEVEL=info", Group: "advanced",
		Help: "One KEY=VALUE per line, set when your server starts."},
	{Name: "secrets", Label: "Secrets", Placeholder: "CRM_API_KEY, SEARCH_API_KEY", Group: "advanced",
		Help: "Comma-separated names from Settings > Secrets, passed to your server as environment variables of the same name."},
}

var (
	headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	toolIDRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	envKeyRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	urlPathRe    = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,200}$`)
	gitRefRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

// connectBool reads a bool param: "true", "on", "1" or "yes" is true; empty
// is def.
func connectBool(params map[string]string, name string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(params[name])) {
	case "":
		return def
	case "true", "on", "1", "yes":
		return true
	}
	return false
}

func connectTimeout(params map[string]string) (int, error) {
	v := strings.TrimSpace(params["timeout_s"])
	if v == "" {
		return connectDefaultTimeout, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 5 || n > 3600 {
		return 0, fmt.Errorf("timeout must be a whole number of seconds from 5 to 3600, got %q", v)
	}
	return n, nil
}

func connectSecretName(params map[string]string, name, label string) (string, error) {
	v := strings.TrimSpace(params[name])
	if v != "" && !abSecretNameRe.MatchString(v) {
		return "", fmt.Errorf("%s %q must be a Settings > Secrets name: letters, digits and _", label, v)
	}
	return v, nil
}

// connectList splits a comma or space separated list and checks each item.
func connectList(v string, re *regexp.Regexp, label string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if !re.MatchString(f) {
			return nil, fmt.Errorf("%s %q is not a valid name", label, f)
		}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out, nil
}

// connectHTTPBlock is the remote block of the HTTP endpoint form.
func connectHTTPBlock(u string, params map[string]string) (map[string]any, error) {
	timeout, err := connectTimeout(params)
	if err != nil {
		return nil, err
	}
	block := map[string]any{"url": u, "timeout_s": timeout}
	auth, err := connectSecretName(params, "auth_secret", "auth token secret")
	if err != nil {
		return nil, err
	}
	if auth != "" {
		block["auth_secret"] = auth
	}
	if h := strings.TrimSpace(params["auth_header"]); h != "" && !strings.EqualFold(h, "Authorization") {
		if !headerNameRe.MatchString(h) {
			return nil, fmt.Errorf("token header %q must be a header name: letters, digits and -", h)
		}
		if auth == "" {
			return nil, fmt.Errorf("token header %s needs an auth token secret", h)
		}
		block["auth_header"] = h
	}
	sign, err := connectSecretName(params, "sign_secret", "signing secret")
	if err != nil {
		return nil, err
	}
	if sign != "" {
		block["sign_secret"] = sign
	}
	if connectBool(params, "tools", false) {
		block["tools"] = schemas.RemoteToolsExpose
		allow, err := connectList(params["tool_allow"], toolIDRe, "tool id")
		if err != nil {
			return nil, err
		}
		if len(allow) > 0 {
			block["tool_allow"] = allow
		}
	} else if strings.TrimSpace(params["tool_allow"]) != "" {
		return nil, fmt.Errorf("only these tools needs tool access turned on")
	}
	if connectBool(params, "artifacts", false) {
		block["artifacts"] = schemas.RemoteArtifactsInline
	}
	if connectBool(params, "model_traffic", false) {
		block["model_traffic"] = "gateway"
	}
	return block, nil
}

// connectA2ABlock is the a2a block of the A2A form.
func connectA2ABlock(u string, params map[string]string) (map[string]any, error) {
	timeout, err := connectTimeout(params)
	if err != nil {
		return nil, err
	}
	block := map[string]any{"card_url": u, "timeout_s": timeout}
	if connectBool(params, "streaming", false) {
		block["streaming"] = true
	}
	if connectBool(params, "tools", false) {
		block["tools"] = schemas.RemoteToolsExpose
	}
	if connectBool(params, "artifacts", false) {
		block["artifacts"] = schemas.RemoteArtifactsInline
	}
	return block, nil
}

// wrapDockerfile builds the user's repository into the agent image, on the
// suite's base image like every http-service agent.
const wrapDockerfile = `# syntax=docker/dockerfile:1.7
# Agent image for __AGENT_ID__ (template "Wrap my existing app"). Built once
# per suite on that suite's base image (ARG BASE_IMAGE). Your repository is
# fetched at build time: the run container can reach nothing but the
# gateway, so everything your server needs must be installed here.
ARG BASE_IMAGE=bench/coding-core-base:1
FROM ${BASE_IMAGE}
ARG BASE_IMAGE

USER root
RUN command -v python3 >/dev/null 2>&1 || { echo "this agent needs python3 and the suite base image ${BASE_IMAGE} has none" >&2; exit 1; }
ADD --keep-git-dir=false __REPO__ /opt/agent
WORKDIR /opt/agent
RUN if [ -f requirements.txt ]; then python3 -m pip install --no-cache-dir -r requirements.txt; \
    elif [ -f pyproject.toml ]; then python3 -m pip install --no-cache-dir .; fi; \
    if [ -f package.json ]; then npm ci --omit=dev || npm install --omit=dev; fi

# The bench runs every agent as uid 1000 with a read-only root filesystem;
# write only under /workspace, /tmp and $HOME.
USER 1000
WORKDIR /opt/agent
`

// renderWrapApp renders the wrap-app template: the http-service skeleton
// pointed at the user's server, built from their repository or run from
// their image.
func renderWrapApp(id, tag string, params map[string]string) ([]byte, []renderedFile, error) {
	get := func(name, def string) string {
		v := strings.TrimSpace(params[name])
		if v == "" {
			return def
		}
		return v
	}
	repo, image := get("repo_url", ""), get("image", "")
	switch {
	case repo == "" && image == "":
		return nil, nil, fmt.Errorf("give a git repository or a Docker image")
	case repo != "" && image != "":
		return nil, nil, fmt.Errorf("give a git repository or a Docker image, not both")
	}
	cmd := get("command", "")
	if cmd == "" {
		return nil, nil, fmt.Errorf("the start command is required")
	}
	if len(cmd) > 1024 || strings.ContainsAny(cmd, "\n\r") {
		return nil, nil, fmt.Errorf("the start command must be one line under 1024 characters")
	}
	port, err := strconv.Atoi(get("port", "8000"))
	if err != nil || port < 1 || port > 65535 {
		return nil, nil, fmt.Errorf("port must be a number from 1 to 65535")
	}
	health, run := get("health_path", "/health"), get("run_path", "/run")
	for label, p := range map[string]string{"health check path": health, "run path": run} {
		if !urlPathRe.MatchString(p) {
			return nil, nil, fmt.Errorf("%s %q must start with / and hold only URL path characters", label, p)
		}
	}
	framework := get("framework", "custom")
	if !contains(wrapFrameworks, framework) {
		return nil, nil, fmt.Errorf("framework %q is not one of %s", framework, strings.Join(wrapFrameworks, ", "))
	}
	env := map[string]string{}
	for i, line := range strings.Split(params["env"], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !envKeyRe.MatchString(k) {
			return nil, nil, fmt.Errorf("environment line %d: want KEY=VALUE", i+1)
		}
		if strings.HasPrefix(k, "BENCH_") || strings.HasPrefix(k, "AGENT_") || strings.HasPrefix(k, "HEIMDALL_") {
			return nil, nil, fmt.Errorf("environment line %d: %s is reserved for Heimdall", i+1, k)
		}
		if len(v) > 2048 {
			return nil, nil, fmt.Errorf("environment line %d: the value is longer than 2048 characters", i+1)
		}
		env[k] = strings.TrimSpace(v)
	}
	secrets, err := connectList(params["secrets"], abSecretNameRe, "secret name")
	if err != nil {
		return nil, nil, err
	}
	for _, n := range secrets {
		if _, dup := env[n]; dup {
			return nil, nil, fmt.Errorf("%s is both an environment variable and a secret", n)
		}
		env[n] = "{{secret." + n + "}}"
	}

	out, err := agenttemplates.Render("http-service", id, tag)
	if err != nil {
		return nil, nil, err
	}
	var files []renderedFile
	var note string
	switch {
	case repo != "":
		u, err := url.Parse(repo)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || strings.ContainsAny(repo, " \t\"'") {
			return nil, nil, fmt.Errorf("git repository must be a public https URL, got %q", repo)
		}
		src := strings.TrimSuffix(strings.SplitN(repo, "#", 2)[0], "/")
		if ref := get("repo_ref", ""); ref != "" {
			if !gitRefRe.MatchString(ref) || strings.Contains(ref, "..") {
				return nil, nil, fmt.Errorf("branch or tag %q is not a git ref", ref)
			}
			src += "#" + ref
		}
		df := strings.NewReplacer("__AGENT_ID__", id, "__REPO__", src).Replace(wrapDockerfile)
		files = append(files, renderedFile{Rel: "images/" + id + "/Dockerfile", Data: []byte(df)})
		note = "Built from " + src + "."
	default:
		if strings.ContainsAny(image, " \t\"'") || len(image) > 512 {
			return nil, nil, fmt.Errorf("image %q is not a docker image reference", image)
		}
		note = "Runs the image " + image + ". It needs python3 for Heimdall's launcher."
	}
	// The skeleton's stand-in server.py and its Dockerfile (out[1:]) are not
	// written: the repository Dockerfile or the user's image replaces them.
	doc, err := editAgentYAML(out[0].Content, func(root *yaml.Node) {
		base := "http://127.0.0.1:" + strconv.Itoa(port)
		abSetPath(root, cmd, "env", "AGENT_SERVER_CMD")
		abSetPath(root, base+health, "env", "AGENT_HEALTH_URL")
		abSetPath(root, base+run, "env", "AGENT_RUN_URL")
		abSetPath(root, framework, "env", "AGENT_FRAMEWORK")
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			abSetPath(root, env[k], "env", k)
		}
		if image != "" {
			abSetPath(root, image, "image")
			abSetPath(root, "", "build")
		}
		root.HeadComment = id + ": your " + wrapFrameworkName(framework) + " app, wrapped. " + note + "\n" +
			"Heimdall starts AGENT_SERVER_CMD in the task's container, waits for AGENT_HEALTH_URL,\n" +
			"posts the task to AGENT_RUN_URL and reads {status, final_message}. docs/adding-an-agent.md"
	})
	if err != nil {
		return nil, nil, err
	}
	return doc, files, nil
}

func wrapFrameworkName(f string) string {
	switch f {
	case "langgraph":
		return "LangGraph"
	case "crewai":
		return "CrewAI"
	case "openai-agents":
		return "OpenAI Agents SDK"
	}
	return "own"
}
