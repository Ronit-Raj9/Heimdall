package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/quickstart"
	"github.com/maximhq/bifrost/bench/scaffold"
)

// First workflow (docs/quickstart.md): `init` inside a git repository.

// detectProbe is the machine probe init uses; tests replace it.
var detectProbe = quickstart.SystemProbe

// runInitRepo detects the repository's test command, the agent CLIs on PATH,
// Docker and the stack's ports, writes .heimdall/ inside the repository, adds
// .heimdall/runs/ to its .gitignore and prints the next commands with their
// cost. pos is empty (the repository holding the working directory) or one
// folder inside a repository. Nothing calls a model.
func runInitRepo(pos []string, testCmd, image, name string, force bool) int {
	dir := "."
	if len(pos) > 1 {
		fmt.Fprintln(os.Stderr, "usage: "+progName+" init [-detect] [-test-cmd <cmd>] [-image <image>] [-force] [repo folder]")
		return exitUsage
	}
	if len(pos) == 1 {
		dir = pos[0]
	}
	top, err := gitTop(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s init: %s is not inside a git repository (%v)\nusage: %s init <dir>            scaffold a project folder\n       %s init [-detect]       inside a git repository: write .heimdall/\n",
			progName, dir, err, progName, progName)
		return exitUsage
	}
	ctx := context.Background()
	det := quickstart.Detect(ctx, top, testCmd, detectProbe())
	files, pf, err := quickstart.Project(quickstart.ProjectOptions{Detection: det, Name: name, Image: image})
	if err != nil {
		return fail("init: %v", err)
	}
	projectDir := filepath.Join(top, quickstart.Dir)
	written, kept, err := quickstart.WriteProject(projectDir, files, force)
	if err != nil {
		return fail("init: %v", err)
	}
	ignored, err := quickstart.EnsureIgnored(top, quickstart.RunsIgnore)
	if err != nil {
		return fail("init: update .gitignore: %v", err)
	}
	printDetection(det)
	fmt.Printf("\nwrote %d files to %s", len(written), projectDir)
	if len(kept) > 0 {
		fmt.Printf(" (kept %d existing; -force replaces the project's own files, never deploy/.env)", len(kept))
	}
	fmt.Println()
	if ignored {
		fmt.Printf("added %s to %s\n", quickstart.RunsIgnore, filepath.Join(top, ".gitignore"))
	}
	printRepoNextSteps(pf, det)
	return exitOK
}

// gitTop returns the top folder of the work tree holding dir.
func gitTop(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func printDetection(d quickstart.Detection) {
	t := d.Tests
	cmd := t.Command
	if cmd == "" {
		cmd = "none found (pass -test-cmd)"
	}
	fmt.Printf("repository  %s\n", d.Root)
	fmt.Printf("tests       %s: %s  [%s confidence; %s]\n", t.Language, cmd, t.Confidence, strings.Join(t.Evidence, "; "))
	fmt.Printf("suite       template %s\n", t.Template)
	for _, a := range t.Also {
		fmt.Printf("            also found %s\n", a)
	}
	if len(d.Agents) == 0 {
		fmt.Println("agent CLIs  none of claude, codex, opencode, gemini on PATH (scripted agents only)")
	}
	for _, a := range d.Agents {
		fmt.Printf("agent CLI   %-8s %s  (%s) -> %s\n", a.Name, a.Version, a.Path, a.Config)
	}
	switch {
	case d.Docker.Available && d.Docker.Compose:
		fmt.Printf("docker      %s, compose %s\n", d.Docker.Version, d.Docker.ComposeVersion)
	case d.Docker.Available:
		fmt.Printf("docker      %s; %s\n", d.Docker.Version, d.Docker.Note)
	default:
		fmt.Printf("docker      unavailable: %s\n", d.Docker.Note)
	}
	for _, p := range d.Ports {
		state := "free"
		if !p.Free {
			state = "IN USE (" + p.Note + ")"
		}
		fmt.Printf("port        %d %s: %s\n", p.Port, p.Name, state)
	}
}

// printRepoNextSteps prints the commands after init and what each costs.
func printRepoNextSteps(pf *quickstart.ProjectFile, d quickstart.Detection) {
	p := progName
	cfg := quickstart.Dir + "/deploy/bench.yaml"
	fmt.Println("\nnext (from the repository root; every step is $0 until a real model runs):")
	step := 0
	say := func(cost, cmd string) {
		step++
		fmt.Printf("  %d. %s\n     cost: %s\n", step, cmd, cost)
	}
	say("$0 (local Docker builds)", fmt.Sprintf("cp \"$(command -v %s)\" %s/deploy/bifrost-bench && docker build -t bench/bifrost-bench:dev %s/deploy && docker build -t %s %s/deploy/helper",
		p, quickstart.Dir, quickstart.Dir, quickstart.HelperImage, quickstart.Dir))
	if pf.Tests.Template != scaffold.TemplatePythonPytest {
		say("$0 (local Docker build); add your toolchain and dependencies to the Dockerfile first",
			fmt.Sprintf("docker build -t bench/%s-base:1 %s/benchmarks/%s/base", pf.Suite, quickstart.Dir, pf.Suite))
	}
	for _, a := range d.Agents {
		id := strings.TrimSuffix(filepath.Base(a.Config), ".yaml")
		say("$0 (local Docker build, no model call)", fmt.Sprintf("docker build -t %s %s/agents/images/%s", a.Image, quickstart.Dir, id))
	}
	if allPortsFree(d) {
		say("$0 (deploy/.env has fresh tokens and no provider key: the fault proxy answers from its scripted model)",
			fmt.Sprintf("docker compose -f %s/deploy/docker-compose.yml --env-file %s/deploy/.env up -d --wait", quickstart.Dir, quickstart.Dir))
	} else {
		say("$0; a port is in use, so a stack may already be running: reuse it (copy its tokens into "+quickstart.Dir+"/deploy/.env) or change the ports first",
			fmt.Sprintf("docker compose -f %s/deploy/docker-compose.yml --env-file %s/deploy/.env up -d --wait", quickstart.Dir, quickstart.Dir))
	}
	say("$0 (no model; runs your tests in containers to prove each task)", p+" tasks mine -limit 10")
	say("$0 with the scripted agents shown; a real agent shows its forecast and asks before spending",
		p+" compare --a agents/oracle.yaml --b agents/noop.yaml --trials 1   # then: --baseline main --candidate HEAD --base agents/<your agent>.yaml --budget-usd 5")
	say("$0 (writes a file)", p+" ci install github -dry-run")
	fmt.Printf("\nevery command finds %s by itself; pass -config %s to be explicit.\n", cfg, cfg)
}

func allPortsFree(d quickstart.Detection) bool {
	for _, p := range d.Ports {
		if !p.Free {
			return false
		}
	}
	return true
}

// projectConfig returns the bench config a first-workflow command uses: the
// -config value when given, else deploy/bench.yaml when it exists in the
// working directory, else the .heimdall project of the repository the
// working directory is in.
func projectConfig(given string, explicit bool) string {
	if explicit {
		return given
	}
	if _, err := os.Stat(given); err == nil {
		return given
	}
	if dir := quickstart.FindProject("."); dir != "" {
		return filepath.Join(dir, "deploy", "bench.yaml")
	}
	return given
}
