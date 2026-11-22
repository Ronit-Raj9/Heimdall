package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/agentinfer"
	"github.com/maximhq/bifrost/bench/agents/templates"
	"github.com/maximhq/bifrost/bench/handlers"
)

// Lane Z15: the committed agent file (.heimdall/agent.yaml) and agent
// inference from traces.
//
//	agent file check [-f .heimdall/agent.yaml] [-json]
//	agent file push [-f ...] [-force]            (-server URL -token T, or local -config)
//	agent file from-traces [export.json|export.pb] [-o .heimdall/agent.yaml] [-force] [-json]
const agentFileUsage = `usage: bifrost-bench agent file <check|push|from-traces> ...

  agent file check [-f .heimdall/agent.yaml] [-json]
      check the agent file; prints the agent request or config it becomes
  agent file push [-f .heimdall/agent.yaml] [-force]
      create the agent (or update it when it exists): on the server with -server and -token,
      else in the local project (-config); -force overwrites local files
  agent file from-traces [export.json|export.pb] [-o .heimdall/agent.yaml] [-force] [-json]
      infer the agent from an OTLP trace export file, or with no file from the traces
      the server received (-server); writes an agent file with -o`

func runAgentFile(args []string, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, agentFileUsage)
		return exitUsage
	}
	switch args[0] {
	case "check":
		return runAgentFileCheck(args[1:], out)
	case "push":
		return runAgentFilePush(args[1:], out)
	case "from-traces":
		return runAgentFileFromTraces(args[1:], out)
	case "-h", "-help", "--help", "help":
		fmt.Fprintln(out, agentFileUsage)
		return exitOK
	}
	fmt.Fprintf(os.Stderr, "unknown agent file subcommand %q\n\n%s\n", args[0], agentFileUsage)
	return exitUsage
}

// readAgentFile reads -f, or .heimdall/agent.yaml of the working directory.
func readAgentFile(path string) (*agentinfer.AgentFile, []byte, string, error) {
	if path == "" {
		path = agentinfer.AgentFilePath
		if _, err := os.Stat(path); err != nil {
			alt := strings.TrimSuffix(path, ".yaml") + ".yml"
			if _, err := os.Stat(alt); err == nil {
				path = alt
			}
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, path, fmt.Errorf("%s not found; write one with: %s agent file from-traces <export.json> -o %s", path, progName, agentinfer.AgentFilePath)
		}
		return nil, nil, path, err
	}
	f, err := agentinfer.ParseAgentFile(b)
	return f, b, path, err
}

func runAgentFileCheck(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("agent file check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := fs.String("f", "", "agent file (default "+agentinfer.AgentFilePath+")")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseInterspersed(fs, args); err != nil {
		fmt.Fprintln(os.Stderr, agentFileUsage)
		return exitUsage
	}
	_, b, path, err := readAgentFile(*file)
	if b == nil && err != nil {
		return fail("%v", err)
	}
	res := handlers.CheckAgentFile(b)
	if *asJSON {
		writeJSON(out, res)
	} else if res.OK {
		fmt.Fprintf(out, "%s: ok, agent %s\n", path, res.File.ID)
		if res.Request != nil {
			fmt.Fprintf(out, "  template %s", res.Request.Template)
			if res.Request.Model != nil {
				fmt.Fprintf(out, ", model %s %s", res.Request.Model.Provider, res.Request.Model.Model)
			}
			fmt.Fprintln(out)
		} else {
			fmt.Fprintln(out, "  full agent config")
		}
	} else {
		fmt.Fprintln(out, res.Error)
	}
	if !res.OK {
		return 1
	}
	return exitOK
}

func runAgentFilePush(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("agent file push", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c connOpts
	addConnFlags(fs, &c)
	file := fs.String("f", "", "agent file (default "+agentinfer.AgentFilePath+")")
	force := fs.Bool("force", false, "local: overwrite existing agent files")
	if _, err := parseInterspersed(fs, args); err != nil {
		fmt.Fprintln(os.Stderr, agentFileUsage)
		return exitUsage
	}
	c.finish(fs)
	f, _, path, err := readAgentFile(*file)
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if strings.TrimSpace(c.server) != "" {
		cl, err := newSyncClient(&syncOpts{server: c.server, token: c.token, org: c.org})
		if err != nil {
			return fail("%v", err)
		}
		verb, err := pushAgentFile(ctx, &httpEval{c: cl}, f)
		if err != nil {
			return fail("%s: %v", path, err)
		}
		fmt.Fprintf(out, "%s agent %s from %s on %s\n", verb, f.ID, path, c.server)
		return exitOK
	}
	root, err := projectRoot(projectConfig(c.config, c.configSet))
	if err != nil {
		return fail("%v", err)
	}
	files, err := localAgentFiles(f)
	if err != nil {
		return fail("%s: %v", path, err)
	}
	if err := writeTemplate(root, files, *force); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(out, "wrote agents/%s.yaml from %s in %s\n", f.ID, path, root)
	return exitOK
}

// pushAgentFile creates the agent from the file on the server, or updates
// its config when it exists already. It returns "created" or "updated".
func pushAgentFile(ctx context.Context, h *httpEval, f *agentinfer.AgentFile) (string, error) {
	if len(f.Config) > 0 {
		y, err := f.ConfigYAML()
		if err != nil {
			return "", err
		}
		if err := h.call(ctx, http.MethodPut, "/agents/"+urlPathEscape(f.ID)+"/config", handlers.AgentConfigPut{YAML: y}, nil); err != nil {
			return "", err
		}
		return "updated", nil
	}
	req, err := f.ToRequest()
	if err != nil {
		return "", err
	}
	err = h.call(ctx, http.MethodPost, "/agents/from-template", req, nil)
	if err == nil {
		return "created", nil
	}
	if !strings.Contains(err.Error(), ": 409 ") {
		return "", err
	}
	// It exists: render the same request without writing, then save it as
	// the agent's config.
	req.DryRun = true
	var dry handlers.AgentFromTemplateResponse
	if err := h.call(ctx, http.MethodPost, "/agents/from-template", req, &dry); err != nil {
		return "", err
	}
	if err := h.call(ctx, http.MethodPut, "/agents/"+urlPathEscape(f.ID)+"/config", handlers.AgentConfigPut{YAML: dry.YAML}, nil); err != nil {
		return "", err
	}
	return "updated", nil
}

// localAgentFiles renders the file into the project's agents folder.
func localAgentFiles(f *agentinfer.AgentFile) ([]templates.File, error) {
	if len(f.Config) > 0 {
		y, err := f.ConfigYAML()
		if err != nil {
			return nil, err
		}
		return []templates.File{{Path: "agents/" + f.ID + ".yaml", Content: []byte(y), Mode: 0o644}}, nil
	}
	req, err := f.ToRequest()
	if err != nil {
		return nil, err
	}
	hreq := handlers.AgentFromTemplateRequest{Template: req.Template, ID: req.ID, Notes: req.Notes, Params: req.Params}
	if req.Model != nil {
		hreq.Model = &handlers.AgentModelRef{Provider: req.Model.Provider, Model: req.Model.Model}
	}
	rendered, err := handlers.RenderAgentRequest(hreq)
	if err != nil {
		return nil, err
	}
	out := make([]templates.File, 0, len(rendered))
	for _, r := range rendered {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(r.Rel, ".sh") {
			mode = 0o755
		}
		out = append(out, templates.File{Path: "agents/" + r.Rel, Content: r.Data, Mode: mode})
	}
	return out, nil
}

func runAgentFileFromTraces(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("agent file from-traces", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c connOpts
	addConnFlags(fs, &c)
	outPath := fs.String("o", "", "write the agent file here (for example "+agentinfer.AgentFilePath+")")
	force := fs.Bool("force", false, "overwrite -o")
	asJSON := fs.Bool("json", false, "print the draft as JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) > 1 {
		fmt.Fprintln(os.Stderr, agentFileUsage)
		return exitUsage
	}
	c.finish(fs)
	var d agentinfer.Draft
	switch {
	case len(pos) == 1:
		b, err := os.ReadFile(pos[0])
		if err != nil {
			return fail("%v", err)
		}
		ct := "application/json"
		if ext := strings.ToLower(filepath.Ext(pos[0])); ext == ".pb" || ext == ".bin" || ext == ".proto" || len(b) > 0 && b[0] != '{' && b[0] != ' ' && b[0] != '\n' {
			ct = "application/x-protobuf"
		}
		if d, err = agentinfer.InferFromOTLP(b, ct); err != nil {
			return fail("%s: %v", pos[0], err)
		}
	case strings.TrimSpace(c.server) != "":
		cl, err := newSyncClient(&syncOpts{server: c.server, token: c.token, org: c.org})
		if err != nil {
			return fail("%v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var info handlers.TraceConnectInfo
		if err := (&httpEval{c: cl}).call(ctx, http.MethodGet, "/connect/traces", nil, &info); err != nil {
			return fail("%v", err)
		}
		if info.Draft == nil {
			fmt.Fprintf(out, "no traces arrived yet. Export to %s with Authorization: Bearer <%s>\n", info.Endpoint, info.TokenHint)
			return 1
		}
		d = *info.Draft
	default:
		fmt.Fprintln(os.Stderr, "give an OTLP export file, or -server to read the traces the server received\n\n"+agentFileUsage)
		return exitUsage
	}
	if *asJSON {
		writeJSON(out, d)
	} else {
		fmt.Fprintf(out, "%s (confidence %s, %d traces, %d spans)\n", d.Summary(), d.Confidence, d.Traces, d.Spans)
		for _, e := range d.Evidence {
			fmt.Fprintf(out, "  seen in your traces: %s = %s (%s)\n", e.Field, e.Value, e.From)
		}
		for _, q := range d.Questions {
			fmt.Fprintf(out, "  to answer: %s\n", q)
		}
	}
	if *outPath == "" {
		return exitOK
	}
	b, err := agentinfer.FileFromDraft(d).Marshal(d.Questions...)
	if err != nil {
		return fail("%v", err)
	}
	if _, err := os.Stat(*outPath); err == nil && !*force {
		return fail("%s exists; -force overwrites it", *outPath)
	}
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		return fail("%v", err)
	}
	if err := os.WriteFile(*outPath, b, 0o644); err != nil {
		return fail("%v", err)
	}
	if !*asJSON {
		fmt.Fprintf(out, "wrote %s; answer the TODO lines, then: %s agent file push\n", *outPath, progName)
	}
	return exitOK
}
