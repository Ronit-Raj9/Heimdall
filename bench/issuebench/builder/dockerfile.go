package builder

import (
	"fmt"
	"regexp"
	"strings"
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
