package builder

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// readOnlyRe matches commands that only look around; they are not recorded
// as install steps.
var readOnlyRe = regexp.MustCompile(`^\s*(ls|cat|head|tail|grep|rg|find|pwd|echo|printf|which|type|env|printenv|wc|file|stat|tree|less|more|true|sed\s+-n|python[0-9.]*\s+(-V|--version)|python[0-9.]*\s+-m\s+pip\s+(list|show|freeze|--version)|pip[0-9.]*\s+(list|show|freeze|--version)|uv\s+--version|git\s+(status|log|diff|show))\b`)

// setupRe matches the parts of a command that only set up the shell for
// the next part (the builder's own cd and export prefix).
var setupRe = regexp.MustCompile(`^\s*(cd\s+\S+|export(\s+[A-Za-z_][A-Za-z0-9_]*=\S*)+)\s*$`)

// splitRe cuts a one-line command at its && ; || and | separators. It does
// not parse quotes: a part that only looks read-only because of a quoted
// separator is rare, and Heimdall's fresh-container check then fails the
// build instead of hiding it.
var splitRe = regexp.MustCompile(`&&|\|\||;|\|`)

// stateChanging says whether a successful command belongs in the
// Dockerfile: not when every part only looks around (after the cd and
// export prefix), as in "cd /testbed && python -V && sed -n 1,80p setup.cfg".
func stateChanging(cmd string) bool {
	c := strings.TrimSpace(cmd)
	if c == "" || strings.HasPrefix(c, "#") {
		return false
	}
	if strings.Contains(c, "\n") || strings.Contains(c, "<<") || strings.Contains(c, ">") {
		return true // heredocs, scripts and redirects may write files
	}
	looked := false
	for _, part := range splitRe.Split(c, -1) {
		switch {
		case strings.TrimSpace(part) == "" || setupRe.MatchString(part):
		case readOnlyRe.MatchString(part):
			looked = true
		default:
			return true
		}
	}
	return !looked
}

// GitInstallRun installs git when the base image lacks it.
const GitInstallRun = "RUN (command -v git >/dev/null || (apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*))"

// Dockerfile writes the environment's Dockerfile from the commands that
// succeeded. The agent never writes it. The repository is copied at
// Workdir; env carries the time pinning (UV_EXCLUDE_NEWER and the like).
func Dockerfile(baseImage string, env []string, commands []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# syntax=docker/dockerfile:1\n")
	fmt.Fprintf(&b, "# Written by Heimdall's issue builder from the commands that succeeded.\n")
	fmt.Fprintf(&b, "FROM %s\n", baseImage)
	fmt.Fprintf(&b, "SHELL [\"/bin/bash\", \"-lc\"]\n")
	// Validation applies the test and fix patches with git apply; slim
	// Python images have no git (every run said patch_failed without it).
	fmt.Fprintf(&b, "%s\n", GitInstallRun)
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			continue
		}
		fmt.Fprintf(&b, "ENV %s=%q\n", k, v)
	}
	fmt.Fprintf(&b, "COPY . %s\n", Workdir)
	fmt.Fprintf(&b, "WORKDIR %s\n", Workdir)
	for _, c := range commands {
		fmt.Fprintf(&b, "RUN %s\n", dockerRunLine(c))
	}
	return b.String()
}

// dockerRunLine keeps a multi-line command one RUN instruction (a heredoc).
func dockerRunLine(cmd string) string {
	c := strings.TrimSpace(cmd)
	if !strings.Contains(c, "\n") {
		return c
	}
	return "<<'HEIMDALL_STEP'\nset -e\n" + c + "\nHEIMDALL_STEP"
}

// SupportDir is the folder of the environment image that holds a
// language's support files (result converters), and the folder of the
// build context they are copied from.
const SupportDir = issuebench.SupportFolder

// stateChangingFor is stateChanging that also treats the language's
// ReadOnlyCommands words as only looking around.
func stateChangingFor(l issuebench.Language, cmd string) bool {
	if !stateChanging(cmd) {
		return false
	}
	c := strings.TrimSpace(cmd)
	if strings.Contains(c, "\n") || strings.Contains(c, "<<") || strings.Contains(c, ">") {
		return true
	}
	words := map[string]bool{}
	for _, w := range issuebench.ToolchainOf(l).ReadOnlyCommands() {
		words[w] = true
	}
	looked := false
	for _, part := range splitRe.Split(c, -1) {
		f := strings.Fields(part)
		switch {
		case len(f) == 0 || setupRe.MatchString(part):
		case readOnlyRe.MatchString(part) || words[f[0]]:
			looked = true
		default:
			return true
		}
	}
	return !looked
}

// DockerfileFor is Dockerfile for one language. When the language has
// support files, they are copied from the build context's heimdall/ folder
// to /heimdall/ before the repository, and the copy the repository COPY
// brings along is removed from the work folder.
func DockerfileFor(l issuebench.Language, baseImage string, env, commands []string) string {
	d := Dockerfile(baseImage, env, commands)
	if len(issuebench.ToolchainOf(l).SupportFiles()) == 0 {
		return d
	}
	repoCopy := fmt.Sprintf("COPY . %s\n", Workdir)
	return strings.Replace(d, repoCopy, fmt.Sprintf("COPY %s/ /%s/\n", SupportDir, SupportDir)+repoCopy+fmt.Sprintf("RUN rm -rf %s/%s\n", Workdir, SupportDir), 1)
}

// installSupport writes the language's support files to /heimdall/ in a
// build container, so the default test command runs there the way it runs
// in the environment image. It is not an install step of the Dockerfile.
func installSupport(ctx context.Context, sb Sandbox, l issuebench.Language) error {
	files := issuebench.ToolchainOf(l).SupportFiles()
	if len(files) == 0 {
		return nil
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("mkdir -p /" + SupportDir)
	for _, n := range names {
		fmt.Fprintf(&b, " && echo %s | base64 -d > /%s/%s", base64.StdEncoding.EncodeToString(files[n]), SupportDir, shellQuote(n))
	}
	exit, out, err := sb.Exec(ctx, b.String(), time.Minute)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("could not write the test result converter (exit %d): %s", exit, tail(out, 500))
	}
	return nil
}
