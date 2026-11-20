package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/agents/templates"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Labels on agent images built by `agent build`. They are not the
// bifrost-bench=1 label, so the janitor never treats an image as a leftover.
const (
	labelAgent = "bifrost-bench.agent"
	labelSuite = "bifrost-bench.suite"
)

const agentUsage = `usage: bifrost-bench agent <init|build|check> ...

  agent init <id> [-template command|python|http-service] [-tag <image:tag>] [-force]
      write agents/<id>.yaml and agents/images/<id>/ from a template
  agent build <agents/id.yaml> [-suite <suite>]
      build the agent image (per suite when build.base_arg is set); prints tag and image id
  agent check <agents/id.yaml> -suite <suite> [-task <task>] [-json] [-no-build] [-runs checks]
      free conformance run on the fault proxy's scripted model; exit 1 on any FAIL

every subcommand takes -config deploy/bench.yaml (the project root is its grandparent)`

func init() {
	register("agent", "your own agent: agent init|build|check (template, per-suite image, free conformance check)", runAgent)
}

func runAgent(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, agentUsage)
		return exitUsage
	}
	switch args[0] {
	case "init":
		return runAgentInit(args[1:], os.Stdout)
	case "build":
		return runAgentBuild(args[1:], os.Stdout)
	case "check":
		return runAgentCheck(args[1:], os.Stdout)
	case "diff": // Agent diff area: two versions of an agent config (agentdiff.go)
		return runAgentDiff(args[1:], os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown agent subcommand %q\n\n%s\n", args[0], agentUsage)
		return exitUsage
	}
}

// runAgentInit writes a template into the project. It never overwrites a
// file unless -force is given, so a second init cannot destroy an agent.
func runAgentInit(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("agent init", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config; the project root is its grandparent")
	tmpl := fs.String("template", "python", "template: "+strings.Join(templates.Names, ", "))
	tag := fs.String("tag", "", "image tag (default bench/agent-<id>:1)")
	force := fs.Bool("force", false, "overwrite existing files")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench agent init <id> [-template command|python|http-service] [-tag image:tag] [-force] [-config deploy/bench.yaml]")
		return exitUsage
	}
	id := pos[0]
	if *tag == "" {
		*tag = "bench/agent-" + id + ":1"
	}
	files, err := templates.Render(*tmpl, id, *tag)
	if err != nil {
		return fail("%v", err)
	}
	root, err := projectRoot(*config)
	if err != nil {
		return fail("%v", err)
	}
	if err := writeTemplate(root, files, *force); err != nil {
		return fail("%v", err)
	}
	for _, f := range files {
		fmt.Fprintf(out, "wrote %s\n", filepath.Join(root, filepath.FromSlash(f.Path)))
	}
	fmt.Fprintf(out, "\nnext:\n  bifrost-bench agent build agents/%s.yaml -suite <suite>\n  bifrost-bench agent check agents/%s.yaml -suite <suite>\n", id, id)
	return exitOK
}

// runAgentBuild builds the agent image for one suite and prints its tag and
// image id. Build output streams to stderr.
func runAgentBuild(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("agent build", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	suiteRef := fs.String("suite", "", "suite the image is built for (required with build.base_arg)")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench agent build <agents/id.yaml> [-suite <suite>] [-config deploy/bench.yaml]")
		return exitUsage
	}
	ctx := context.Background()
	cfg, err := loadConfig(*config)
	if err != nil {
		return fail("%v", err)
	}
	agent, err := loadAgentConfig(pos[0])
	if err != nil {
		return fail("%v", err)
	}
	s, err := agentSuite(ctx, cfg, agent, *suiteRef)
	if err != nil {
		return fail("%v", err)
	}
	img, info, err := buildAgentImage(ctx, utils.Docker{}, cfg.Root, agent, s, os.Stderr)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(out, "tag    %s\nimage  %s\n", img.Tag, info.ID)
	for _, d := range info.RepoDigests {
		fmt.Fprintf(out, "digest %s\n", d)
	}
	return exitOK
}

// buildAgentImage resolves the agent's image for suite s (nil when the
// config has no base_arg and no suite was named) and builds it.
func buildAgentImage(ctx context.Context, d utils.Docker, root string, agent *schemas.AgentConfig, s *schemas.Suite, log io.Writer) (engine.AgentImage, environments.ImageInfo, error) {
	img, err := engine.ResolveAgentImage(agent, s, root)
	if err != nil {
		return img, environments.ImageInfo{}, err
	}
	if img.Dockerfile == "" {
		return img, environments.ImageInfo{}, fmt.Errorf("agent %s has no build.dockerfile; build %s yourself or add a build section", agent.ID, img.Tag)
	}
	labels := map[string]string{labelAgent: agent.ID}
	if img.PerSuite {
		labels[labelSuite] = s.ID
	}
	info, err := environments.BuildImage(ctx, d, environments.ImageBuild{
		Dockerfile: img.Dockerfile, Context: img.Context, Tag: img.Tag, Args: img.BuildArgs, Labels: labels,
	}, log)
	if err != nil {
		return img, info, fmt.Errorf("build agent %s: %w", agent.ID, err)
	}
	return img, info, nil
}

// loadAgentConfig reads an agent YAML the way the planner does (strict decode
// into schemas.AgentConfig) after validating it against
// spec/agent.schema.json, so a schema error names the field.
func loadAgentConfig(path string) (*schemas.AgentConfig, error) {
	var probe schemas.AgentConfig
	if err := suite.DecodeYAMLFile(path, path, suite.SchemaAgent, &probe); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent config: %w", err)
	}
	cfg, err := engine.ResolveAgentConfig(raw, nil)
	if err != nil {
		return nil, fmt.Errorf("agent config %s: %w", path, err)
	}
	return &cfg, nil
}

// agentSuite loads the named suite. With no name it returns nil, which is
// only valid for an agent without build.base_arg.
func agentSuite(ctx context.Context, cfg *lib.Config, agent *schemas.AgentConfig, ref string) (*schemas.Suite, error) {
	if ref == "" {
		if agent.Build != nil && agent.Build.BaseArg != "" {
			return nil, fmt.Errorf("agent %s builds one image per suite (build.base_arg); name the suite with -suite", agent.ID)
		}
		return nil, nil
	}
	reg := &schemas.Registry{Loaders: suite.Builtin(cfg.Path(cfg.BenchmarksRoot))}
	s, err := engine.LoadSuite(ctx, reg, ref)
	if err != nil {
		return nil, fmt.Errorf("load suite %s: %w", ref, err)
	}
	return s, nil
}

// projectRoot is the grandparent of the config path, the rule LoadConfig
// uses, without reading the file: `agent init` works before a config exists.
func projectRoot(config string) (string, error) {
	abs, err := filepath.Abs(config)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", config, err)
	}
	return filepath.Dir(filepath.Dir(abs)), nil
}

// writeTemplate writes rendered template files under root. It checks every
// path first, so it writes all files or none.
func writeTemplate(root string, files []templates.File, force bool) error {
	if !force {
		var exists []string
		for _, f := range files {
			p := filepath.Join(root, filepath.FromSlash(f.Path))
			if _, err := os.Stat(p); err == nil {
				exists = append(exists, p)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("check %s: %w", p, err)
			}
		}
		if len(exists) > 0 {
			return fmt.Errorf("refusing to overwrite %s (use -force)", strings.Join(exists, ", "))
		}
	}
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, f.Content, f.Mode); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
		if err := os.Chmod(p, f.Mode); err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
	}
	return nil
}

// parseInterspersed parses flags that may come before, between or after the
// positional arguments ("agent init <id> -template python"), which the flag
// package alone stops at.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
