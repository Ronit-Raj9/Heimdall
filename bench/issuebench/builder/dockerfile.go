package builder

import (
	"fmt"
	"regexp"
	"strings"
)

// readOnlyRe matches commands that only look around; they are not recorded
// as install steps.
var readOnlyRe = regexp.MustCompile(`^\s*(ls|cat|head|tail|grep|rg|find|pwd|echo|which|type|env|printenv|wc|file|stat|tree|less|more|python[0-9.]*\s+(-V|--version)|pip[0-9.]*\s+(list|show|freeze|--version)|uv\s+--version|git\s+(status|log|diff|show))\b`)

// stateChanging says whether a successful command belongs in the Dockerfile.
func stateChanging(cmd string) bool {
	c := strings.TrimSpace(cmd)
	if c == "" || strings.HasPrefix(c, "#") {
		return false
	}
	return !readOnlyRe.MatchString(c)
}

// Dockerfile writes the environment's Dockerfile from the commands that
// succeeded. The agent never writes it. The repository is copied at
// Workdir; env carries the time pinning (UV_EXCLUDE_NEWER and the like).
func Dockerfile(baseImage string, env []string, commands []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# syntax=docker/dockerfile:1\n")
	fmt.Fprintf(&b, "# Written by Heimdall's issue builder from the commands that succeeded.\n")
	fmt.Fprintf(&b, "FROM %s\n", baseImage)
	fmt.Fprintf(&b, "SHELL [\"/bin/bash\", \"-lc\"]\n")
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
