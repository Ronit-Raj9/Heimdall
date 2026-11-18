//go:build integration

package environments

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// neutralAgent runs inside a neutral run's agent container and fails on any
// bench-named cue it can see: variables, PATH, mounts, the instruction's
// canary line, other processes' command lines, and checks that the neutral
// tool host answers.
const neutralAgent = `set -e
if env | grep -iE 'bench|heimdall|toolproxy'; then echo "bench-named variable" >&2; exit 3; fi
case "$PATH" in /opt/tools/shim:*) ;; *) echo "bad PATH $PATH" >&2; exit 4;; esac
test -d /opt/tools/shim
if grep -i bench /proc/self/mountinfo; then echo "bench-named mount" >&2; exit 7; fi
test "$(cat /home/agent/.claude/skills/pdf/SKILL.md)" = skill
case "$(readlink /home/agent/.claude/skills/pdf)" in /mnt/data/dirs/*) ;; *) echo "bad skills link" >&2; exit 8;; esac
case "$(readlink /home/agent/.claude/projects)" in /mnt/data/memory/*) ;; *) echo "bad memory link" >&2; exit 9;; esac
echo mem > /home/agent/.claude/projects/note
python3 -c 'import socket, sys; n = socket.gethostbyaddr(socket.gethostbyname(socket.gethostname()))[0]; print("own address answers " + n, file=sys.stderr); sys.exit(10 if "bench" in n else 0)'
test "$AGENT_EVENTS_FILE" = /opt/tools/events/events.jsonl
if test -e /opt/bench; then echo "/opt/bench exists" >&2; exit 5; fi
test "$(cat /tmp/task/instruction.md)" = "Fix the bug."
test "$AGENT_MODEL" = m
test "$AGENT_EVENTS_URL" = http://tools.internal:9200/events
python3 -c 'import os, urllib.request; urllib.request.urlopen(urllib.request.Request(os.environ["AGENT_EVENTS_URL"], data=b"{}\n", method="POST"), timeout=5)'
ps_root=0
for i in 1 2 3 4 5 6 7 8 9 10; do
  for d in /proc/[0-9]*; do
    if grep -q '^Uid:[[:space:]]*0[[:space:]]' "$d/status" 2>/dev/null; then
      ps_root=1
      line=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null || true)
      case "$line" in *bench*|*watch*|*observer*|*heimdall*) echo "root process: $line" >&2; exit 6;; esac
    fi
  done
  test "$ps_root" = 1 && break
  sleep 1
done
test "$ps_root" = 1
`

func TestNeutralRealismInContainer(t *testing.T) {
	taskDir := t.TempDir()
	writeTree(t, taskDir, map[string]string{"repo/README.md": "task\n", "verifier/tests/x.sh": "true\n"})
	e := newDockerWorkspace(DockerWorkspaceConfig{HelperImage: testImage, ProcessWatch: true, EventsCollector: true, ToolProxyImage: DefaultToolProxyImage})
	runID := strings.Replace(utils.NewID("r", 8), "r_", "hd-s2-", 1)
	rc := &schemas.RunContext{
		Experiment: &schemas.Experiment{Realism: schemas.RealismNeutral},
		Run:        &schemas.Run{RunID: runID, ExpID: "e_hds2"},
		Slot:       &schemas.Slot{Condition: schemas.ConditionClean},
		Suite:      &schemas.Suite{ID: "hd-s2-suite", Version: "1.0.0", Workdir: "/workspace", BaseImage: schemas.ImageSpec{Tag: testImage}, Root: taskDir},
		Task:       &schemas.Task{ID: "t1", Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}},
		Agent:      &schemas.AgentConfig{Memory: &schemas.AgentMemory{Paths: []string{"/home/agent/.claude/projects"}}},
		Dir:        t.TempDir(),
	}
	skills := t.TempDir()
	writeTree(t, skills, map[string]string{"SKILL.md": "skill"})
	ctx := context.Background()
	t.Cleanup(func() {
		if err := e.Destroy(ctx, rc); err != nil {
			t.Errorf("destroy: %v", err)
		}
		assertNoLeftovers(t, runID)
	})
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	plan := shPlan(neutralAgent, 90)
	plan.Network = "none"
	plan.Env = map[string]string{"BENCH_MODEL": "m"}
	plan.Dirs = map[string]string{"/home/agent/.claude/skills/pdf": skills}
	plan.Files = map[string]string{"/tmp/bench/instruction.md": "Fix the bug.\n\n" + utils.CanaryLine("instruction.md", "0f6c1a2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b") + "\n"}
	res, err := e.Exec(ctx, rc, plan)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("exec: %+v %v\n%s", res, err, readFile(t, filepath.Join(rc.Dir, "agent.stderr.txt")))
	}
	if plan.Env["BENCH_MODEL"] != "m" || plan.Files["/tmp/bench/instruction.md"] == "" {
		t.Fatalf("the bench's plan was changed: %+v", plan)
	}
	b, err := os.ReadFile(filepath.Join(rc.Dir, RealismFile))
	if err != nil {
		t.Fatalf("realism.json: %v", err)
	}
	if got, err := utils.ReadRealism(b); err != nil || got != schemas.RealismNeutral {
		t.Fatalf("realism.json says %q, %v", got, err)
	}
}
