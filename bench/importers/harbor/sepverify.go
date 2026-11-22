package harbor

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Separate verifier containers (Harbor [verifier] environment_mode =
// "separate"). Harbor builds the verifier from tests/Dockerfile, starts it,
// runs its healthcheck, copies the task's artifacts out of the agent's
// container into it at the same paths, and runs /tests/test.sh there. The
// bench grades in a snapshot of the agent's container, so the importer
// carries the verifier image inside the task image: the part of
// tests/Dockerfile before it first copies from the tests folder is built as
// a stage, its whole file system copied to /opt/harbor-verifier, and at
// verify time the wrapper replays the rest of tests/Dockerfile (the copies
// from the tests folder, which arrive only then, and the RUN steps after
// them), runs the healthcheck, copies the artifacts in, and runs test.sh
// chrooted there with the Dockerfile's environment. The verifier's
// packages, Python and tests are upstream's; only the artifacts cross over.

// verifierRoot is where the verifier image's file system lives in the task
// image.
const verifierRoot = "/opt/harbor-verifier"

// artifact is one entry of task.toml artifacts.
type artifact struct {
	Source          string
	RegularFileOnly bool
	MaxBytes        int64
}

// sepPlan is how a separate-verifier task grades.
type sepPlan struct {
	stage   string            // Dockerfile stage "harbor-verifier"
	env     map[string]string // ENV of tests/Dockerfile
	workdir string            // last WORKDIR of tests/Dockerfile
	steps   []string          // shell lines replayed at verify time
	notes   []string
}

// planSeparate reads tests/Dockerfile, or says why it cannot be carried.
func planSeparate(t *upstreamTask) (*sepPlan, string) {
	b, err := os.ReadFile(filepath.Join(t.TestsDir, "Dockerfile"))
	if err != nil {
		return nil, "grades in a separate verifier container whose image is not built from tests/Dockerfile (none there), which the importer does not map"
	}
	if t.VerifierImage != "" {
		return nil, "grades in a separate verifier container from a prebuilt image ([verifier.environment] docker_image), which the importer does not map"
	}
	sp := &sepPlan{env: map[string]string{}, workdir: "/"}
	var stage []string
	copied := false
	for _, in := range dockerInstructions(string(b)) {
		switch in.Cmd {
		case "FROM":
			if copied || len(stage) > 0 {
				return nil, "tests/Dockerfile has more than one stage, which the importer does not map"
			}
			stage = append(stage, "FROM "+in.Args+" AS harbor-verifier")
			continue
		case "ENV":
			setDockerVars(sp.env, "ENV", in.Args)
		case "WORKDIR":
			wd := strings.Trim(in.Args, `"'`)
			if !strings.HasPrefix(wd, "/") {
				wd = path.Join(sp.workdir, wd)
			}
			sp.workdir = path.Clean(wd)
		case "COPY", "ADD":
			if in.Cmd == "ADD" || strings.HasPrefix(in.Args, "--") {
				return nil, "tests/Dockerfile uses " + in.Cmd + " " + short(in.Args) + ", which the importer does not replay"
			}
			f := strings.Fields(in.Args)
			if len(f) != 2 || strings.ContainsAny(in.Args, "$*?[") {
				return nil, "tests/Dockerfile COPY " + short(in.Args) + " is not one plain source and destination"
			}
			copied = true
			sp.steps = append(sp.steps, copyStep(f[0], f[1], sp.workdir))
			continue
		case "RUN":
			if copied {
				sp.steps = append(sp.steps, "inroot "+shellQuote("cd "+shellQuote(sp.workdir)+" && "+in.Args)+" || fail 'tests/Dockerfile RUN step failed'")
				continue
			}
		case "CMD", "ENTRYPOINT", "LABEL", "EXPOSE", "HEALTHCHECK":
			continue
		case "USER", "ARG", "SHELL", "VOLUME", "ONBUILD", "STOPSIGNAL":
			return nil, "tests/Dockerfile uses " + in.Cmd + ", which the importer does not replay"
		}
		if copied && in.Cmd != "ENV" && in.Cmd != "WORKDIR" {
			return nil, "tests/Dockerfile " + in.Cmd + " after its first COPY is not replayed"
		}
		if !copied {
			stage = append(stage, in.Cmd+" "+in.Args)
		}
	}
	if len(stage) == 0 {
		return nil, "tests/Dockerfile has no FROM"
	}
	sp.stage = strings.Join(stage, "\n") + "\n"
	sp.notes = append(sp.notes, fmt.Sprintf("Separate verifier (`[verifier] environment_mode = \"separate\"`): tests/Dockerfile up to its first COPY is built as a stage and its file system copied to `%s` in the task image; at verify time the rest of tests/Dockerfile is replayed there with the tests, the verifier healthcheck runs, the %d task.toml artifact(s) are copied from the agent's container to the same paths, and test.sh runs chrooted in it as Harbor runs it in the verifier container. The agent can see (and, as root, change) that folder, as it can change any tool a snapshot verifier calls; the tests themselves arrive only at verify time.", verifierRoot, len(t.Artifacts)))
	return sp, ""
}

// copyStep is the shell line for COPY src dst from the tests folder.
func copyStep(src, dst, workdir string) string {
	if !strings.HasPrefix(dst, "/") {
		dst = path.Join(workdir, dst) + map[bool]string{true: "/", false: ""}[strings.HasSuffix(dst, "/")]
	}
	s := path.Clean("/" + src)
	from := `"$U"` + shellQuote(strings.TrimSuffix(s, "/"))
	if s == "/" {
		from = `"$U"`
	}
	to := `"$R"` + shellQuote(dst)
	return fmt.Sprintf(`copy_in %s %s || fail %s`, from, to, shellQuote("tests/Dockerfile COPY "+src+" "+dst+" failed"))
}

// separateTestScript replaces test.sh for a separate-verifier task: it is run
// by the wrapper and drives the chrooted verifier.
func separateTestScript(t *upstreamTask, sp *sepPlan) string {
	var env strings.Builder
	keys := make([]string, 0, len(sp.env))
	for k := range sp.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env.WriteString("PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin HOME=/root LANG=C.UTF-8")
	for _, k := range keys {
		if k != "PATH" {
			fmt.Fprintf(&env, " %s=%s", k, shellQuote(sp.env[k]))
		} else {
			env.Reset()
			fmt.Fprintf(&env, "PATH=%s HOME=/root LANG=C.UTF-8", shellQuote(sp.env[k]))
		}
	}
	for _, k := range sortedEnvKeys(t.VerifierEnv) {
		fmt.Fprintf(&env, " %s=%s", k, shellQuote(t.VerifierEnv[k]))
	}
	var arts strings.Builder
	for _, a := range t.Artifacts {
		fmt.Fprintf(&arts, "copy_artifact %s %t %d\n", shellQuote(a.Source), a.RegularFileOnly, a.MaxBytes)
	}
	hc := "true"
	if t.VerifierHealthcheck != "" {
		hc = t.VerifierHealthcheck
	}
	return fmt.Sprintf(`#!/bin/bash
# Separate verifier driver, written by bifrost-bench import harbor (see IMPORT.md).
set -u
R=%[1]s
U=/grader/tests/upstream
fail() { echo "harbor separate verifier: $*" >&2; mkdir -p /logs/verifier; echo 0 > /logs/verifier/reward.txt; exit 3; }
[ -x "$R/bin/sh" ] || [ -x "$R/usr/bin/sh" ] || fail "$R is missing from the image"
command -v chroot >/dev/null 2>&1 || fail "chroot is not installed in the task image"
inroot() { chroot "$R" /usr/bin/env -i %[2]s /bin/sh -c "$1"; }
# copy_in SRC DST: Docker COPY semantics for a folder or a file.
copy_in() {
	if [ -d "$1" ]; then mkdir -p "$2" && cp -a "$1/." "$2/"
	else case "$2" in */) mkdir -p "$2" && cp -a "$1" "$2";; *) mkdir -p "$(dirname "$2")" && cp -a "$1" "$2";; esac
	fi
}
# copy_artifact PATH REGULAR_ONLY MAX_BYTES: the agent container's PATH to the same path in the verifier.
copy_artifact() {
	[ -e "$1" ] || [ -L "$1" ] || { echo "artifact $1: missing" >&2; return 0; }
	if [ "$2" = true ] && { [ -L "$1" ] || [ ! -f "$1" ]; }; then echo "artifact $1: not a regular file, not copied" >&2; return 0; fi
	if [ "$3" -gt 0 ] && [ -f "$1" ] && [ "$(wc -c < "$1")" -gt "$3" ]; then echo "artifact $1: over $3 bytes, not copied" >&2; return 0; fi
	mkdir -p "$R$(dirname "$1")" && rm -rf "$R$1" && cp -a "$1" "$R$1"
}
rm -rf "$R/tests" "$R/logs/verifier"
mkdir -p "$R/logs/verifier" "$R/tmp" "$R/dev" && chmod 1777 "$R/tmp"
for d in null:3 zero:5 random:8 urandom:9; do
	[ -c "$R/dev/${d%%:*}" ] || mknod -m 666 "$R/dev/${d%%:*}" c 1 "${d#*:}" 2>/dev/null || echo "no /dev/${d%%:*} in the verifier root" >&2
done
# The rest of tests/Dockerfile, with the tests folder as its build context.
%[3]s
# The verifier healthcheck, then the artifacts.
inroot %[4]s || fail "verifier healthcheck failed"
%[5]sinroot %[6]s
code=$?
cp -a "$R/logs/verifier/." /logs/verifier/ 2>/dev/null
exit $code
`, verifierRoot, env.String(), strings.Join(sp.steps, "\n"), shellQuote("cd "+shellQuote(sp.workdir)+" && "+hc), arts.String(),
		shellQuote("cd "+shellQuote(sp.workdir)+" && bash /tests/test.sh"))
}
