package main

// `heimdall connect` (lane Z02, audit/INFRA_I2_AGENT_ONBOARDING.md 5.1):
// one command from "I have an agent" to a first-look report card.
//
//   heimdall connect                         inside a repository: detect the framework
//                                            object and serve it from this machine
//   heimdall connect https://bot.example.com server-side detection (Z01) when the
//                                            server can reach it, else relay from here
//   heimdall connect "curl -X POST ..."      start from a pasted request
//   heimdall connect github.com/org/repo     server-side detection only
//
// It writes .heimdall/agent.yaml (commit it), registers the agent, checks
// it, then offers the first look (5 tasks by 1 trial on the fastest
// benchmark it fits) and prints the report card link.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/connectlocal"
)

const connectUsage = `usage: bifrost-bench connect [<url | "curl ..." | repo | dir>] [-name id] [-H 'Header: value'] [-local] [-model m] [-yes] [-no-first-look] [-json] [-url server]

Connects your agent to Heimdall and runs a first look (5 tasks by 1 trial on
the fastest benchmark it fits, labelled too small for a verdict).

  no argument or a folder   detect the agent in the repository (langgraph.json,
                            ADK root_agent, CrewAI, OpenAI Agents SDK, Pydantic AI,
                            smolagents, DSPy, a plain function) and serve it from here
  an http(s) URL            the server detects it; when it can not reach it (a private
                            or local address), this machine checks it and relays it
  "curl ..."                the request your docs show, as you would paste it
  -local                    always check and relay from this machine
  -yes                      no questions (pick the most likely agent, run the first look)
Writes .heimdall/agent.yaml (commit it; it holds no secret).`

func init() {
	register("connect", "connect your agent (URL, curl, repository) and run a first look: connect [target] [-yes]", runConnect)
}

type connectOptions struct {
	target      string
	dir         string
	name        string
	headers     map[string]string
	local       bool
	model       string
	yes         bool
	noFirstLook bool
	asJSON      bool
	server      string
}

func runConnect(args []string) int {
	o := connectOptions{headers: map[string]string{}}
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.StringVar(&o.name, "name", "", "agent name (default from the host, folder or object)")
	fs.Var(headerFlags(o.headers), "H", "header for the agent, 'Name: value' (repeatable; ${VAR} reads the environment)")
	fs.BoolVar(&o.local, "local", false, "check and relay from this machine, even when the server could reach the agent")
	fs.StringVar(&o.model, "model", "", "model for the first look (default the agent's own)")
	fs.BoolVar(&o.yes, "yes", false, "ask nothing")
	fs.BoolVar(&o.noFirstLook, "no-first-look", false, "register and check only")
	fs.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	fs.StringVar(&o.server, "url", "", "Heimdall server (env HEIMDALL_URL, else your login)")
	fs.SetOutput(io.Discard)
	// The target may come before or after the flags.
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		o.target, rest = args[0], args[1:]
	}
	if err := fs.Parse(rest); err != nil || fs.NArg() > 1 || (o.target != "" && fs.NArg() > 0) {
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "connect:", err)
		}
		fmt.Fprintln(os.Stderr, connectUsage)
		return exitUsage
	}
	if o.target == "" && fs.NArg() == 1 {
		o.target = fs.Arg(0)
	}
	o.dir = "."
	if st, err := os.Stat(o.target); o.target != "" && err == nil && st.IsDir() {
		o.dir, o.target = o.target, ""
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, tok, _ := connectlocal.Resolve(o.server, "")
	if srv == "" || tok == "" {
		return fail("connect: no Heimdall login: run `heimdall login -url https://your-heimdall` first (or set HEIMDALL_URL and HEIMDALL_TOKEN)")
	}
	api := newOnboardAPI(srv, tok)
	out := os.Stderr
	prop, err := proposeConnect(ctx, api, o.target, o.dir, o.headers, o.local)
	if err != nil {
		return fail("connect: %v", err)
	}
	if o.name != "" {
		prop.rename(agentNameFrom(o.name))
	}
	if !o.asJSON {
		printProposal(out, prop)
	}
	if len(prop.Candidates) > 1 && !o.yes && interactive(os.Stdin) {
		pick := askChoice(out, os.Stdin, prop.Candidates)
		if pick > 0 {
			prop.useFinding(prop.Candidates[pick])
			if o.name != "" {
				prop.rename(agentNameFrom(o.name))
			}
			fmt.Fprintf(out, "using %s\n", prop.Candidates[pick].Object)
		}
	}
	if prop.Config == "" {
		return fail("connect: nothing to register: %s", strings.Join(prop.NextSteps, "; "))
	}
	// Secrets the user pasted stay in this process's environment only.
	for k, v := range prop.secretValues {
		_ = os.Setenv(k, v)
	}
	res, err := applyConnect(ctx, api, prop, o.dir, srv)
	if err != nil {
		return fail("connect: %v", err)
	}
	if !o.asJSON {
		if res.Existed {
			fmt.Fprintf(out, "agent %s already exists on %s: kept its config\n", prop.Agent, srv)
		} else {
			fmt.Fprintf(out, "registered agent %s on %s\n", prop.Agent, srv)
		}
		if res.ProjectFile != "" {
			fmt.Fprintf(out, "wrote %s (commit it)\n", res.ProjectFile)
		}
		for _, env := range prop.SecretEnv {
			fmt.Fprintf(out, "note: later `heimdall serve` runs need %s exported (the value you passed for that header)\n", env)
		}
	}

	// Serve it from here while checking it and running the first look.
	var serving chan error
	var serveCancel context.CancelFunc = func() {}
	if prop.Serve != nil && !o.noFirstLook {
		plan := &servePlan{agent: prop.Agent, concurrency: max(1, prop.Serve.Concurrency), dir: o.dir}
		if prop.Serve.Map != nil {
			plan.mapping = prop.Serve.Map
		} else {
			plan.object = prop.Serve.Target
		}
		sctx, cancel := context.WithCancel(ctx)
		serveCancel = cancel
		serving = make(chan error, 1)
		go func() { _, err := plan.run(sctx, srv, tok, prefixWriter(out, "  [serve] ")); serving <- err }()
		time.Sleep(3 * time.Second) // let the first poll register the process
	}
	defer serveCancel()

	ver, verr := api.verify(ctx, prop.Agent, prop.Serve != nil)
	if verr != nil {
		fmt.Fprintf(out, "check: %v\n", verr)
	} else if !o.asJSON {
		printVerify(out, ver)
	}
	result := map[string]any{"proposal": prop, "registered": res}
	if ver != nil {
		result["verify"] = ver
	}
	if o.noFirstLook {
		if prop.Serve != nil && !o.asJSON {
			fmt.Fprintf(out, "next: heimdall serve %s   (keep it running), then heimdall first-look -agent %s\n", serveArg(prop), prop.Agent)
		} else if !o.asJSON {
			fmt.Fprintf(out, "next: heimdall first-look -agent %s\n", prop.Agent)
		}
		return printJSONIf(o.asJSON, result)
	}
	if ver != nil && !ver.OK && prop.Serve != nil {
		return fail("connect: the agent is not being served, so a first look would fail; check the [serve] lines above")
	}
	code := runFirstLookFlow(ctx, api, srv, firstLookOptions{agent: prop.Agent, model: o.model, yes: o.yes, asJSON: o.asJSON, wait: true}, out, result)
	serveCancel()
	if serving != nil {
		select {
		case <-serving:
		case <-time.After(5 * time.Second):
		}
	}
	return code
}

// connectProposal is what connect found and will register.
type connectProposal struct {
	Input      string                  `json:"input"`
	Kind       string                  `json:"kind"`  // repo | url | curl | other
	Where      string                  `json:"where"` // heimdall: the server calls it; local: heimdall serve on this machine
	Summary    string                  `json:"summary"`
	Agent      string                  `json:"agent"`
	Framework  string                  `json:"framework,omitempty"`
	Config     string                  `json:"config,omitempty"`
	Serve      *connectlocal.ServeSpec `json:"serve,omitempty"`
	Evidence   []string                `json:"evidence,omitempty"`
	Warnings   []string                `json:"warnings,omitempty"`
	SecretEnv  []string                `json:"secret_env,omitempty"`
	Secrets    []string                `json:"secrets,omitempty"` // names to add under Settings > Secrets
	Candidates []connectlocal.Finding  `json:"candidates,omitempty"`
	NextSteps  []string                `json:"next_steps,omitempty"`
	Detection  map[string]any          `json:"server_detection,omitempty"`

	secretValues map[string]string
}

func (p *connectProposal) rename(id string) {
	old := p.Agent
	p.Agent = id
	if p.Serve != nil {
		p.Config = pullAgentYAML(id, p.Framework)
	} else if old != "" && p.Config != "" {
		p.Config = strings.Replace(p.Config, "id: "+old, "id: "+id, 1)
	}
}

func (p *connectProposal) useFinding(f connectlocal.Finding) {
	p.Framework = f.Framework
	p.Evidence = f.Evidence
	p.Summary = fmt.Sprintf("%s agent %s", f.Framework, f.Object)
	if f.Object != "" && f.Language == "python" {
		p.Serve = &connectlocal.ServeSpec{Target: f.Object, Concurrency: 1}
		p.Config = pullAgentYAML(p.Agent, f.Framework)
		p.NextSteps = []string{"heimdall serve " + f.Object}
		return
	}
	p.Serve, p.Config = nil, ""
	p.NextSteps = []string{f.Note}
}

// proposeConnect works out what the target is and how to connect it,
// without registering anything.
func proposeConnect(ctx context.Context, api *onboardAPI, target, dir string, headers map[string]string, local bool) (*connectProposal, error) {
	p := &connectProposal{Input: target}
	switch {
	case target == "":
		p.Kind, p.Where = "repo", "local"
		d, err := connectlocal.DetectDir(dir)
		if err != nil {
			return nil, err
		}
		abs, _ := filepath.Abs(dir)
		p.Agent = agentNameFrom(filepath.Base(abs))
		if pf, _, err := connectlocal.ReadProject(dir); err == nil {
			p.Agent = pf.Agent
		}
		p.Candidates = d.Findings
		best := d.Best()
		if best == nil {
			p.Summary = "no agent framework object found in " + abs
			p.NextSteps = []string{"start your agent's server and run heimdall connect http://localhost:<port>",
				"or name the object: heimdall serve path/to/file.py:object"}
			return p, nil
		}
		p.useFinding(*best)
		return p, nil
	case connectlocal.LooksLikeCurl(target):
		p.Kind = "curl"
		c, err := connectlocal.ParseCurl(target)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			c.Headers[k] = v
		}
		if !local {
			if d, err := api.detect(ctx, target); err == nil && d.config() != "" {
				return p.fromServer(d), nil
			} else if err == nil && !localOnly(d.failureCode()) {
				p.Detection = d.Raw
			}
		}
		res := connectlocal.Probe(ctx, connectlocal.ProbeOptions{Curl: c})
		return p.fromProbe(res, c.URL, c.Headers, c.SecretHeaders()), nil
	case isURLTarget(target):
		p.Kind = "url"
		if !local {
			d, err := api.detect(ctx, target)
			switch {
			case err == nil && d.config() != "":
				return p.fromServer(d), nil
			case err == nil && !localOnly(d.failureCode()) && d.failureCode() != "":
				// The server reached it but could not map it: say why, then try from here.
				p.Detection = d.Raw
				p.Warnings = append(p.Warnings, "server detection: "+detectionMessage(d))
			case err != nil && !missingRoute(err):
				return nil, err
			}
		}
		res := connectlocal.Probe(ctx, connectlocal.ProbeOptions{URL: target, Headers: headers})
		return p.fromProbe(res, target, headers, (&connectlocal.CurlRequest{Headers: headers}).SecretHeaders()), nil
	default:
		p.Kind = "other"
		d, err := api.detect(ctx, target)
		if err != nil {
			if missingRoute(err) {
				return nil, fmt.Errorf("%q is not a URL, a curl command or a folder, and this server has no connect detection for repositories, images or platform ids yet", target)
			}
			return nil, err
		}
		if d.config() == "" {
			return nil, fmt.Errorf("the server could not connect %q: %s", target, detectionMessage(d))
		}
		return p.fromServer(d), nil
	}
}

func detectionMessage(d *remoteDetection) string {
	if d.Failure != nil {
		msg := d.Failure.Message
		if d.Failure.Fix != "" {
			msg += " (" + d.Failure.Fix + ")"
		}
		return msg
	}
	if d.Summary != "" {
		return d.Summary
	}
	return "no agent found"
}

// fromServer takes the server's detection (Z01): Heimdall calls the agent.
func (p *connectProposal) fromServer(d *remoteDetection) *connectProposal {
	p.Where = "heimdall"
	p.Summary = d.Summary
	p.Framework = d.Kind
	p.Config = d.config()
	p.Evidence = d.Evidence
	p.Detection = d.Raw
	p.Agent = agentIDOfYAML(p.Config)
	for _, s := range d.Secrets {
		name := s.Name
		if name == "" {
			name = s.Header
		}
		if name != "" {
			p.Secrets = append(p.Secrets, name)
		}
	}
	if len(p.Secrets) > 0 {
		p.NextSteps = append(p.NextSteps, "add the secrets "+strings.Join(p.Secrets, ", ")+" under Settings > Secrets before the first look")
	}
	return p
}

// fromProbe takes this machine's probe: heimdall serve relays the agent.
func (p *connectProposal) fromProbe(res *connectlocal.ProbeResult, target string, headers map[string]string, secret []string) *connectProposal {
	p.Where = "local"
	p.Evidence = append(p.Evidence, res.Evidence...)
	p.Warnings = append(p.Warnings, res.Warnings...)
	u, _ := url.Parse(target)
	if p.Agent == "" && u != nil {
		p.Agent = agentNameFrom(strings.Split(u.Hostname(), ".")[0])
	}
	if res.Mapping == nil {
		p.Summary = "could not find how to call " + target + " (" + res.Code + ")"
		p.NextSteps = append(p.NextSteps, fixFor(res.Code, target))
		if len(res.Fields) > 0 {
			p.NextSteps = append(p.NextSteps, "the answer had the fields "+strings.Join(res.Fields, ", ")+": write a mapping file and run heimdall serve "+target+" -map mapping.json")
		}
		return p
	}
	m := res.Mapping
	if m.Headers == nil {
		m.Headers = map[string]string{}
	}
	p.secretValues = connectlocal.MoveSecretsToEnv(m.Headers, secret)
	for k := range p.secretValues {
		p.SecretEnv = append(p.SecretEnv, k)
	}
	p.Framework = "http"
	if m.Kind == connectlocal.KindOpenAI {
		p.Framework = "openai-compatible"
	}
	p.Summary = fmt.Sprintf("%s agent at %s %s, answer at %s; relayed from this machine", p.Framework, m.Method, m.URL, orBody(m.Answer))
	p.Serve = &connectlocal.ServeSpec{Target: target, Concurrency: 2, Map: m}
	p.Config = pullAgentYAML(p.Agent, p.Framework)
	p.NextSteps = []string{"heimdall serve " + target + "   (or heimdall serve . from this folder)"}
	return p
}

func fixFor(code, target string) string {
	switch code {
	case "auth":
		return "the agent needs credentials: heimdall connect " + target + " -H 'Authorization: Bearer ${MY_AGENT_TOKEN}'"
	case "unreachable":
		return "nothing answers at " + target + " from this machine: start the agent, or run heimdall connect on a machine that can reach it"
	case "path":
		return "no route answered: pass the agent's exact endpoint, like " + strings.TrimRight(target, "/") + "/chat, or paste the curl command from its docs"
	}
	return "paste the curl command from the agent's docs: heimdall connect \"curl ...\""
}

var yamlIDLine = strings.NewReplacer("\"", "", "'", "")

// agentIDOfYAML reads the top-level id of an agent config.
func agentIDOfYAML(doc string) string {
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "id:") {
			return strings.TrimSpace(yamlIDLine.Replace(strings.TrimPrefix(line, "id:")))
		}
	}
	return ""
}

func serveArg(p *connectProposal) string {
	if p.Serve == nil {
		return ""
	}
	return "."
}

// connectApplied is what connect registered and wrote.
type connectApplied struct {
	Agent       string `json:"agent"`
	Existed     bool   `json:"existed"`
	ProjectFile string `json:"project_file,omitempty"`
}

// applyConnect registers the agent and writes .heimdall/agent.yaml.
func applyConnect(ctx context.Context, api *onboardAPI, p *connectProposal, dir, server string) (*connectApplied, error) {
	if p.Agent == "" {
		return nil, errors.New("no agent name: pass -name")
	}
	exists, err := api.registerAgent(ctx, p.Agent, p.Config)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", p.Agent, err)
	}
	out := &connectApplied{Agent: p.Agent, Existed: exists}
	pf := &connectlocal.ProjectFile{Server: server, Agent: p.Agent, Framework: p.Framework, Serve: p.Serve, Config: p.Config}
	path, err := connectlocal.WriteProject(dir, pf)
	if err != nil {
		return out, fmt.Errorf("write the project file: %w", err)
	}
	out.ProjectFile = path
	return out, nil
}

func printProposal(w io.Writer, p *connectProposal) {
	fmt.Fprintf(w, "found: %s\n", p.Summary)
	for _, e := range p.Evidence {
		fmt.Fprintf(w, "  %s\n", e)
	}
	for _, x := range p.Warnings {
		fmt.Fprintf(w, "  warning: %s\n", x)
	}
	if p.Where == "local" && p.Serve != nil {
		fmt.Fprintln(w, "runs: served from this machine with heimdall serve (outbound only; nothing connects in)")
	} else if p.Where == "heimdall" {
		fmt.Fprintln(w, "runs: Heimdall calls the agent from its worker")
	}
	if p.Config == "" {
		for _, s := range p.NextSteps {
			fmt.Fprintf(w, "next: %s\n", s)
		}
	}
}

func printVerify(w io.Writer, v *verifyResult) {
	fmt.Fprintf(w, "check (%s):\n", v.Source)
	for _, r := range v.Rows {
		mark := "PASS"
		switch {
		case r.Skipped:
			mark = "SKIP"
		case !r.OK:
			mark = "FAIL"
		}
		line := fmt.Sprintf("  %-4s %-10s %s", mark, r.Check, r.Detail)
		if r.Fix != "" && !r.OK {
			line += "  (fix: " + r.Fix + ")"
		}
		fmt.Fprintln(w, line)
	}
}

func askChoice(w io.Writer, in io.Reader, cands []connectlocal.Finding) int {
	fmt.Fprintln(w, "more than one agent found:")
	for i, c := range cands {
		what := c.Object
		if what == "" {
			what = c.Note
		}
		fmt.Fprintf(w, "  %d) %s %s (%s)\n", i+1, c.Framework, what, c.Confidence)
	}
	fmt.Fprint(w, "which one? [1] ")
	var n int
	if _, err := fmt.Fscanln(in, &n); err != nil || n < 1 || n > len(cands) {
		return 0
	}
	return n - 1
}

func printJSONIf(asJSON bool, v any) int {
	if asJSON {
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
	}
	return exitOK
}

// prefixWriter prefixes every line written through it.
func prefixWriter(w io.Writer, prefix string) io.Writer {
	return &prefixed{w: w, prefix: prefix, start: true}
}

type prefixed struct {
	w      io.Writer
	prefix string
	start  bool
}

func (p *prefixed) Write(b []byte) (int, error) {
	var out []byte
	for _, c := range b {
		if p.start {
			out = append(out, p.prefix...)
			p.start = false
		}
		out = append(out, c)
		if c == '\n' {
			p.start = true
		}
	}
	if _, err := p.w.Write(out); err != nil {
		return 0, err
	}
	return len(b), nil
}
