package harbor

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// dockerfileInfo is what the importer needs from an upstream Dockerfile:
// the final stage's working directory and user.
type dockerfileInfo struct {
	Workdir string // last WORKDIR of the final stage; "" when none or not literal
	User    string // last USER of the final stage; "" when none
	Stages  int    // FROM lines
}

// dockerInstruction is one logical Dockerfile line (continuations joined).
type dockerInstruction struct {
	Cmd  string // upper-case instruction, e.g. WORKDIR
	Args string
}

// parseDockerfile reads the instructions of src. WORKDIR and USER reset at
// each FROM, so the result describes the final stage. A WORKDIR that uses a
// variable is reported as "" because its value is only known to docker.
func parseDockerfile(src string) dockerfileInfo {
	var info dockerfileInfo
	for _, in := range dockerInstructions(src) {
		switch in.Cmd {
		case "FROM":
			info.Stages++
			info.Workdir, info.User = "", ""
		case "WORKDIR":
			wd := strings.Trim(strings.TrimSpace(in.Args), `"'`)
			switch {
			case strings.Contains(wd, "$") || wd == "":
				info.Workdir = ""
			case strings.HasPrefix(wd, "/"):
				info.Workdir = path.Clean(wd)
			case info.Workdir != "":
				info.Workdir = path.Join(info.Workdir, wd)
			default:
				info.Workdir = path.Join("/", wd)
			}
		case "USER":
			info.User = strings.TrimSpace(in.Args)
		}
	}
	return info
}

// dockerInstructions splits src into logical instructions. Comment lines are
// dropped, a trailing backslash joins the next line, and heredoc bodies are
// skipped so their lines are never read as instructions.
func dockerInstructions(src string) []dockerInstruction {
	var out []dockerInstruction
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for strings.HasSuffix(line, `\`) && i+1 < len(lines) {
			i++
			next := strings.TrimSpace(lines[i])
			if strings.HasPrefix(next, "#") {
				line = strings.TrimSuffix(line, `\`) + `\`
				continue
			}
			line = strings.TrimSuffix(line, `\`) + " " + next
		}
		if m := heredocRE.FindStringSubmatch(line); m != nil {
			for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != m[1] {
				i++
			}
			i++
		}
		cmd, args, _ := strings.Cut(line, " ")
		out = append(out, dockerInstruction{Cmd: strings.ToUpper(cmd), Args: strings.TrimSpace(args)})
	}
	return out
}

var heredocRE = regexp.MustCompile(`<<-?["']?([A-Za-z_][A-Za-z0-9_]*)["']?`)

// skillCopyRE matches a COPY or ADD of the build context's skills folder,
// which SkillsBench Dockerfiles sometimes use to install skills for every
// agent at once.
var skillCopyRE = regexp.MustCompile(`(?i)^\s*(COPY|ADD)\s+(--\S+\s+)*\.?/?skills/?\s`)

// stripSkillCopies comments out every COPY or ADD of the skills folder, so
// skills reach the agent only through Task.Skills and an arm with
// skills.enabled false really runs without them. It returns the new
// Dockerfile and how many lines it disabled.
func stripSkillCopies(src string) (string, int) {
	lines := strings.Split(src, "\n")
	n := 0
	for i, l := range lines {
		if skillCopyRE.MatchString(l) && !strings.HasSuffix(strings.TrimSpace(l), `\`) {
			lines[i] = "# Disabled by bifrost-bench import harbor: skills are installed by the bench (Task.Skills), not baked in.\n# " + strings.TrimSpace(l)
			n++
		}
	}
	return strings.Join(lines, "\n"), n
}

// bakeSection is the block appended to a task's Dockerfile: the verifier's
// tool installs moved from test.sh to build time, root's git settings made
// system-wide, the folders test.sh writes, and the task's environment
// variables. It switches to root for the
// installs and back to the final stage's user afterwards.
func bakeSection(steps []string, dirs []string, env map[string]string, user string) string {
	var b strings.Builder
	b.WriteString("\n# Added by bifrost-bench import harbor. The bench verifier runs with no\n")
	b.WriteString("# network, so the tools the upstream test script downloaded when it ran are\n")
	b.WriteString("# installed here, at build time. They are visible to the agent.\n")
	b.WriteString("USER root\n")
	for _, s := range steps {
		fmt.Fprintf(&b, "RUN %s\n", s)
	}
	// Harbor runs the agent as root; the bench runs it as uid 1000. Root's
	// git settings (the identity a task's setup configured) become system
	// settings, and git trusts repositories owned by another user.
	b.WriteString("RUN if command -v git >/dev/null 2>&1; then { [ ! -f /root/.gitconfig ] || cat /root/.gitconfig >> /etc/gitconfig; } && git config --system --add safe.directory '*'; fi\n")
	if len(dirs) > 0 {
		// World-writable without the sticky bit, so a verifier running as any
		// user can empty what the agent left there before the tests run.
		fmt.Fprintf(&b, "RUN mkdir -p %s && chmod 0777 %s\n", strings.Join(dirs, " "), strings.Join(dirs, " "))
	}
	for _, k := range sortedEnvKeys(env) {
		fmt.Fprintf(&b, "ENV %s=%s\n", k, dockerQuote(env[k]))
	}
	if user != "" {
		fmt.Fprintf(&b, "USER %s\n", user)
	}
	return b.String()
}

// dockerQuote quotes an ENV value for a Dockerfile.
func dockerQuote(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "\n", `\n`)
	return `"` + r.Replace(v) + `"`
}
