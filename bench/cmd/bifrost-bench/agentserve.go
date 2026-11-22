package main

// `heimdall serve <target>` (lane Z02, audit/INFRA_I2_AGENT_ONBOARDING.md
// section 4.7): serve an agent to Heimdall from this machine in pull mode
// (Y11). Nothing connects in; the process takes turns over outbound HTTPS.
//
//   heimdall serve app/agent.py:graph     auto-wrap a Python object (LangGraph,
//                                         CrewAI, OpenAI Agents SDK, Pydantic AI,
//                                         ADK, DSPy, smolagents, a function)
//   heimdall serve http://bot.internal:8080 --map auto
//                                         relay a service inside your network
//   heimdall serve .                      what .heimdall/agent.yaml says
//
// `heimdall serve` with no target, or with flags only, is still the web
// server (serve.go).

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/connectlocal"
)

const agentServeUsage = `usage: bifrost-bench serve <target> [-agent name] [-concurrency 1] [-map auto|mapping.json] [-H 'Header: value'] [-url server] [-python python3] [-max-turns 0]

Serves your agent to Heimdall from this machine (pull mode): this process
connects out and takes the evaluation's turns; nothing connects in.

  <target> file.py:object or module:object   a Python agent object, wrapped by type:
                                             LangGraph graph, CrewAI Crew, OpenAI Agents SDK
                                             Agent, Pydantic AI Agent, ADK agent, DSPy module,
                                             smolagents agent, or a plain function
           http(s)://host:port/path          relay a service (one in a private network):
                                             -map auto finds its request shape with a
                                             connection check code, or -map mapping.json
           .                                 the target in .heimdall/agent.yaml

Header values may name environment variables: -H 'Authorization: Bearer ${BOT_TOKEN}'.
The token is HEIMDALL_TOKEN, else the one heimdall login stored.
Without a target, serve runs the Heimdall web server.`

// agentServeTarget reports whether serve's arguments name an agent to
// serve (a first argument that is not a flag) rather than the web server.
func agentServeTarget(args []string) bool {
	return len(args) > 0 && args[0] != "" && !strings.HasPrefix(args[0], "-")
}

type headerFlags map[string]string

func (h headerFlags) String() string { return "" }
func (h headerFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, ":")
	if !ok || strings.TrimSpace(k) == "" {
		return fmt.Errorf("header %q: want 'Name: value'", v)
	}
	h[strings.TrimSpace(k)] = strings.TrimSpace(val)
	return nil
}

type agentServeOptions struct {
	target      string
	agent       string
	concurrency int
	mapArg      string
	headers     map[string]string
	server      string
	python      string
	maxTurns    int
}

func runAgentServe(args []string) int {
	o := agentServeOptions{target: args[0], headers: map[string]string{}}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&o.agent, "agent", "", "Heimdall agent name (default: .heimdall/agent.yaml, else from the target)")
	fs.IntVar(&o.concurrency, "concurrency", 0, "turns served at once (default 1, or the project file's)")
	fs.StringVar(&o.mapArg, "map", "", "relay: auto, or a mapping JSON file (default: the project file's map, else auto)")
	fs.Var(headerFlags(o.headers), "H", "relay: header sent to the service, 'Name: value' (repeatable; ${VAR} reads the environment)")
	fs.StringVar(&o.server, "url", "", "Heimdall server (env HEIMDALL_URL, else your login)")
	fs.StringVar(&o.python, "python", "", "Python interpreter for object targets (default python3)")
	fs.IntVar(&o.maxTurns, "max-turns", 0, "stop after this many turns (0: until Ctrl-C)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "serve:", err)
		}
		fmt.Fprintln(os.Stderr, agentServeUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, tok, _ := connectlocal.Resolve(o.server, "")
	if srv == "" || tok == "" {
		return fail("serve: no Heimdall login: run `heimdall login -url https://your-heimdall` (or set HEIMDALL_URL and HEIMDALL_TOKEN)")
	}
	plan, err := resolveServePlan(ctx, o, os.Stderr)
	if err != nil {
		return fail("serve: %v", err)
	}
	n, err := plan.run(ctx, srv, tok, os.Stderr)
	if err != nil {
		return fail("serve: %v", err)
	}
	fmt.Fprintf(os.Stderr, "served %d turns of %s\n", n, plan.agent)
	return exitOK
}

// servePlan is what `serve` resolved: an object to wrap or a URL to relay.
type servePlan struct {
	agent       string
	object      string                // python object reference
	mapping     *connectlocal.Mapping // relay mapping
	concurrency int
	python      string
	maxTurns    int
	evidence    []string
	dir         string // where the object reference is relative to
}

func isURLTarget(t string) bool {
	return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://")
}

// resolveServePlan reads the target, the project file and the flags.
func resolveServePlan(ctx context.Context, o agentServeOptions, log io.Writer) (*servePlan, error) {
	p := &servePlan{agent: o.agent, concurrency: o.concurrency, python: o.python, maxTurns: o.maxTurns, dir: "."}
	target := o.target
	var pf *connectlocal.ProjectFile
	isDir := false
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		isDir = true
	}
	if target == "." || isDir || strings.HasSuffix(target, "agent.yaml") {
		root := target
		if filepath.Base(target) == ".heimdall" {
			root = filepath.Dir(target)
		}
		f, path, err := connectlocal.ReadProject(root)
		if err != nil {
			return nil, fmt.Errorf("read %s: %v (run heimdall connect first, or name the target)", path, err)
		}
		if f.Serve == nil || f.Serve.Target == "" {
			return nil, fmt.Errorf("%s has no serve target: agent %s runs on Heimdall's side, nothing to serve", path, f.Agent)
		}
		pf, target = f, f.Serve.Target
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			p.dir = root
		} else {
			p.dir = filepath.Dir(filepath.Dir(path))
		}
	} else if f, _, err := connectlocal.ReadProject("."); err == nil && f.Serve != nil && f.Serve.Target == target {
		pf = f
	}
	if pf != nil {
		if p.agent == "" {
			p.agent = pf.Agent
		}
		if p.concurrency == 0 {
			p.concurrency = pf.Serve.Concurrency
		}
	}
	if p.concurrency <= 0 {
		p.concurrency = 1
	}
	if isURLTarget(target) {
		if p.agent == "" {
			u, _ := url.Parse(target)
			p.agent = agentNameFrom(strings.Split(u.Hostname(), ".")[0])
		}
		switch {
		case o.mapArg != "" && o.mapArg != "auto":
			b, err := os.ReadFile(o.mapArg)
			if err != nil {
				return nil, err
			}
			var m connectlocal.Mapping
			if err := json.Unmarshal(b, &m); err != nil {
				return nil, fmt.Errorf("%s: %v", o.mapArg, err)
			}
			if err := m.Validate(); err != nil {
				return nil, fmt.Errorf("%s: %v", o.mapArg, err)
			}
			p.mapping = &m
		case o.mapArg == "" && pf != nil && pf.Serve.Map != nil:
			p.mapping = pf.Serve.Map
			if p.mapping.Headers == nil {
				p.mapping.Headers = map[string]string{}
			}
			for k, v := range o.headers {
				p.mapping.Headers[k] = v
			}
		default:
			fmt.Fprintf(log, "finding how to call %s (a few small requests with a connection check code)...\n", target)
			res := connectlocal.Probe(ctx, connectlocal.ProbeOptions{URL: target, Headers: o.headers})
			for _, e := range res.Evidence {
				fmt.Fprintln(log, "  "+e)
			}
			for _, w := range res.Warnings {
				fmt.Fprintln(log, "  warning: "+w)
			}
			if res.Mapping == nil {
				return nil, fmt.Errorf("could not find how to call %s (%s)%s", target, res.Code, fieldsHint(res.Fields))
			}
			p.mapping = res.Mapping
		}
		return p, nil
	}
	if !strings.Contains(target, ":") {
		return nil, fmt.Errorf("target %q is neither file.py:object, module:object, an http(s) URL nor '.'", target)
	}
	p.object = target
	if p.agent == "" {
		_, obj, _ := strings.Cut(target, ":")
		file, _, _ := strings.Cut(target, ":")
		base := strings.TrimSuffix(filepath.Base(file), ".py")
		if base == "agent" || base == "main" || base == "app" {
			if abs, err := filepath.Abs(p.dir); err == nil {
				base = filepath.Base(abs)
			}
		}
		if base == "" {
			base = obj
		}
		p.agent = agentNameFrom(base)
	}
	return p, nil
}

func fieldsHint(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return "; the answer had the fields " + strings.Join(fields, ", ") + ": write a mapping file with the one that holds the reply"
}

// run serves until ctx ends or maxTurns turns were answered.
func (p *servePlan) run(ctx context.Context, srv, tok string, log io.Writer) (int, error) {
	if p.mapping != nil {
		fmt.Fprintf(log, "relaying agent %s to %s %s (answer at %s) with %d worker(s); Ctrl-C stops\n",
			p.agent, p.mapping.Method, p.mapping.URL, orBody(p.mapping.Answer), p.concurrency)
		fw := &connectlocal.Forwarder{Mapping: p.mapping}
		r := &connectlocal.Relay{Pull: &connectlocal.PullClient{Base: srv, Token: tok, UA: "heimdall-relay/" + benchVersion},
			Agent: p.agent, Concurrency: p.concurrency, Forward: fw.Forward, MaxTurns: p.maxTurns,
			Log: func(f string, a ...any) { fmt.Fprintf(log, f+"\n", a...) }}
		n, err := r.Run(ctx)
		if err != nil {
			var pe *connectlocal.PullError
			if errors.As(err, &pe) && pe.Status == 404 {
				return n, fmt.Errorf("%v: is %s a pull agent on this server? heimdall connect registers it", err, p.agent)
			}
		}
		return n, err
	}
	return p.runPython(ctx, srv, tok, log)
}

func orBody(path string) string {
	if path == "" {
		return "the whole body"
	}
	return path
}

// pythonCommand is the command line that serves an object with the
// Python package (heimdall.cli, which wraps it by type).
func (p *servePlan) pythonCommand() (string, []string) {
	py := p.python
	if py == "" {
		py = "python3"
	}
	args := []string{"-m", "heimdall.cli", "serve", p.object, "--agent", p.agent, "--concurrency", fmt.Sprint(p.concurrency)}
	if p.maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprint(p.maxTurns))
	}
	return py, args
}

func (p *servePlan) runPython(ctx context.Context, srv, tok string, log io.Writer) (int, error) {
	py, args := p.pythonCommand()
	check := exec.CommandContext(ctx, py, "-c", "import heimdall.wrap")
	check.Dir = p.dir
	if out, err := check.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("%s can not import the Heimdall Python package (%s): pip install the heimdall-bench wheel from %s/downloads "+
			"(listed at %s/api/bench/public/downloads), or run with uvx --from <that wheel> heimdall serve %s",
			py, clip(strings.TrimSpace(string(out)), 200), srv, srv, p.object)
	}
	fmt.Fprintf(log, "serving %s as agent %s with %s (Ctrl-C stops)\n", p.object, p.agent, py)
	cmd := exec.CommandContext(ctx, py, args...)
	cmd.Dir = p.dir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), "HEIMDALL_URL="+srv, "HEIMDALL_TOKEN="+tok)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return 0, nil
	}
	return 0, err
}
