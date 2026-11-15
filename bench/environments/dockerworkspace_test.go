package environments

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestAgentCreateArgs(t *testing.T) {
	plan := &schemas.RunPlan{Image: "img:1", MemGB: 4, CPUs: 1.5, Pids: 0, User: ""}
	h := &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"}
	l := &launch{Entrypoint: "claude", Args: []string{"-p", "x"}}
	args := agentCreateArgs("bench-r_1", utils.LabelArgs("e_1", "r_1"), plan, h, "evalnet", "/tmp/envf", l, nil, false)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"create --name bench-r_1",
		"--label bifrost-bench=1",
		"--label bifrost-bench.run=r_1",
		"--user 1000",
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--pids-limit 512",
		"--memory 4294967296 --memory-swap 4294967296",
		"--cpus 1.5",
		"--read-only",
		"--tmpfs /tmp:exec,size=512m",
		"--tmpfs /home/agent:exec,size=512m,mode=0700,uid=1000,gid=1000",
		"--network evalnet",
		"-v bench-ws-r_1:/workspace",
		"-v bench-aux-r_1:/opt/bench",
		"--log-driver none",
		"-w /workspace",
		"--env-file /tmp/envf",
		"--entrypoint claude img:1 -p x",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q:\n%s", want, joined)
		}
	}
	mounts := 0
	for i, a := range args {
		if a == "-v" || a == "--mount" || strings.HasPrefix(a, "--volume") {
			mounts++
			if src := args[i+1]; strings.HasPrefix(src, "/") {
				t.Errorf("host bind mount %q", src)
			}
		}
		if strings.Contains(a, "docker.sock") || a == "--privileged" {
			t.Errorf("forbidden arg %q", a)
		}
	}
	if mounts != 2 {
		t.Errorf("want exactly 2 mounts, got %d", mounts)
	}
}

func TestAgentCreateArgsScenario(t *testing.T) {
	plan := &schemas.RunPlan{Image: "img:1"}
	h := &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"}
	mounts := []string{"-v", "bench-mem-r_1:/memory", "-v", "bench-dir-r_1-0:/opt/skills:ro"}
	args := agentCreateArgs("bench-r_1-s1", nil, plan, h, "evalnet", "/tmp/envf", &launch{}, mounts, true)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-v bench-aux-r_1:/opt/bench -v bench-mem-r_1:/memory -v bench-dir-r_1-0:/opt/skills:ro --log-driver none") {
		t.Fatalf("mounts not after the aux volume: %s", joined)
	}
	if strings.Contains(joined, "--read-only") || strings.Contains(joined, "--tmpfs /tmp") || !strings.Contains(joined, "--tmpfs /home/agent:") {
		t.Fatalf("writable root: %s", joined)
	}
}

func TestCheckPlan(t *testing.T) {
	e := newDockerWorkspace(DockerWorkspaceConfig{EvalNetwork: "evalnet"})
	tests := []struct {
		name    string
		plan    schemas.RunPlan
		wantErr string
	}{
		{"default ok", schemas.RunPlan{Image: "i"}, ""},
		{"eval network ok", schemas.RunPlan{Image: "i", Network: "evalnet"}, ""},
		{"none ok", schemas.RunPlan{Image: "i", Network: "none"}, ""},
		{"no image", schemas.RunPlan{}, "no image"},
		{"root", schemas.RunPlan{Image: "i", User: "0"}, "root"},
		{"root name", schemas.RunPlan{Image: "i", User: "root:root"}, "root"},
		{"host network", schemas.RunPlan{Image: "i", Network: "host"}, "not allowed"},
		{"bridge", schemas.RunPlan{Image: "i", Network: "bridge"}, "not allowed"},
		{"container ns", schemas.RunPlan{Image: "i", Network: "container:x"}, "not allowed"},
		{"other network", schemas.RunPlan{Image: "i", Network: "internet"}, "not the evaluation network"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.checkPlan(&tt.plan, false)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestRootTask(t *testing.T) {
	e := newDockerWorkspace(DockerWorkspaceConfig{})
	if err := e.checkPlan(&schemas.RunPlan{Image: "i", User: rootUser}, true); err != nil {
		t.Fatalf("root task refused: %v", err)
	}
	plan := &schemas.RunPlan{Image: "img:1", User: rootUser}
	h := &schemas.EnvironmentHandle{Volume: "v", AuxVolume: "a"}
	joined := strings.Join(agentCreateArgs("bench-r_1", nil, plan, h, "evalnet", "/tmp/envf", &launch{}, nil, true), " ")
	for _, want := range []string{"--user 0:0 --cap-drop ALL --cap-add AUDIT_WRITE --cap-add CHOWN", "--cap-add SETUID --cap-add SYS_CHROOT --security-opt no-new-privileges", "--tmpfs /home/agent:exec,size=512m,mode=0700,uid=0,gid=0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q:\n%s", want, joined)
		}
	}
	for _, bad := range []string{"--privileged", "SYS_ADMIN", "NET_RAW", "docker.sock", "--read-only"} {
		if strings.Contains(joined, bad) {
			t.Errorf("args contain %q:\n%s", bad, joined)
		}
	}
	plain := strings.Join(agentCreateArgs("bench-r_1", nil, &schemas.RunPlan{Image: "img:1"}, h, "evalnet", "/tmp/envf", &launch{}, nil, false), " ")
	if strings.Contains(plain, "--cap-add") || !strings.Contains(plain, "--user 1000") {
		t.Fatalf("default task args: %s", plain)
	}
	img := &schemas.TaskEnvironment{Image: "tb/x:1", User: "root"}
	for _, tt := range []struct {
		task *schemas.Task
		want bool
	}{
		{&schemas.Task{Environment: img}, true},
		{&schemas.Task{Environment: &schemas.TaskEnvironment{WorkspacePath: "/app", User: "root"}}, false},
		{&schemas.Task{Environment: &schemas.TaskEnvironment{Image: "tb/x:1"}}, false},
		{&schemas.Task{}, false},
	} {
		if got := utils.TaskRunsAsRoot(tt.task); got != tt.want {
			t.Errorf("TaskRunsAsRoot(%+v) = %v", tt.task.Environment, got)
		}
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"tests/**", "tests/test_a.py", true},
		{"tests/**", "tests/sub/test_b.py", true},
		{"tests/**", "tests", true},
		{"tests/**", "ledger/tests.py", false},
		{"**/conftest.py", "conftest.py", true},
		{"**/conftest.py", "a/b/conftest.py", true},
		{"*.toml", "pyproject.toml", true},
		{"*.toml", "sub/pyproject.toml", false},
		{"ledger/*.py", "ledger/money.py", true},
		{"ledger/*.py", "ledger/sub/money.py", false},
		{"[", "x", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"|"+tt.name, func(t *testing.T) {
			if got := matchGlob(tt.pattern, tt.name); got != tt.want {
				t.Fatalf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

func TestProtectedHashes(t *testing.T) {
	repo, err := filepath.Abs("testdata/task/repo")
	if err != nil {
		t.Fatal(err)
	}
	got, err := protectedHashes(repo, []string{"tests/**"})
	if err != nil {
		t.Fatalf("protectedHashes: %v", err)
	}
	sum, err := utils.SHA256File(filepath.Join(repo, "tests/test_calc.py"))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"tests/test_calc.py": sum}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCleanNotes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "task", "fixtures"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"task/fixtures/notes.clean.md", "id_rsa"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../../id_rsa", filepath.Join(dir, "task", "fixtures", "leak.md")); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(dir, "task"))
	if err != nil {
		t.Fatal(err)
	}
	task := &schemas.Task{Dir: filepath.Join(dir, "task"), Notes: schemas.TaskNotes{Clean: "fixtures/notes.clean.md"}}
	leak := &schemas.Task{Dir: task.Dir, Notes: schemas.TaskNotes{Clean: "fixtures/leak.md"}}
	agent := &schemas.AgentConfig{Notes: schemas.AgentNotes{ProjectFile: "CLAUDE.md"}}
	overlay := &schemas.ProbeSpec{ID: "custom", Layer: schemas.LayerFixture,
		Apply: schemas.ProbeApply{Overlay: &schemas.OverlaySpec{From: "{{task.notes.stale}}", To: projectFileTemplate}}}
	tests := []struct {
		name     string
		rc       schemas.RunContext
		wantRel  string
		wantHost string
		wantErr  bool
	}{
		{"clean", schemas.RunContext{Task: task, Agent: agent, Slot: &schemas.Slot{Condition: "clean"}}, "CLAUDE.md", filepath.Join(real, "fixtures", "notes.clean.md"), false},
		{"notes symlink out of the task", schemas.RunContext{Task: leak, Agent: agent, Slot: &schemas.Slot{Condition: "clean"}}, "", "", true},
		{"stale-notes condition", schemas.RunContext{Task: task, Agent: agent, Slot: &schemas.Slot{Condition: "stale-notes"}}, "", "", false},
		{"overlay probe on project file", schemas.RunContext{Task: task, Agent: agent, Probe: overlay}, "", "", false},
		{"no agent notes", schemas.RunContext{Task: task, Agent: &schemas.AgentConfig{}}, "", "", false},
		{"escaping project file", schemas.RunContext{Task: task, Agent: &schemas.AgentConfig{Notes: schemas.AgentNotes{ProjectFile: "../x.md"}}}, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, host, err := cleanNotes(&tt.rc)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if rel != tt.wantRel || host != tt.wantHost {
				t.Fatalf("got %q %q, want %q %q", rel, host, tt.wantRel, tt.wantHost)
			}
		})
	}
}

func TestHomeTmpfsOpts(t *testing.T) {
	tests := map[string]string{
		"1000":      "exec,size=512m,mode=0700,uid=1000,gid=1000",
		"1001:2000": "exec,size=512m,mode=0700,uid=1001,gid=2000",
		"agent":     "exec,size=512m,mode=1777",
	}
	for user, want := range tests {
		t.Run(user, func(t *testing.T) {
			if got := homeTmpfsOpts(user); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestRunIDs(t *testing.T) {
	tests := []struct {
		name    string
		rc      *schemas.RunContext
		exp     string
		wantErr bool
	}{
		{"nil", nil, "", true},
		{"no run id", &schemas.RunContext{Run: &schemas.Run{}}, "", true},
		{"bad chars", &schemas.RunContext{Run: &schemas.Run{RunID: "r 1;x"}}, "", true},
		{"exp from run", &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_1"}}, "e_1", false},
		{"exp from slot", &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Slot: &schemas.Slot{ExpID: "e_2"}}, "e_2", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp, _, err := runIDs(tt.rc)
			if (err != nil) != tt.wantErr || exp != tt.exp {
				t.Fatalf("got %q, %v", exp, err)
			}
		})
	}
}

// TestCreateRefusesEscapingRepo checks that a repo folder that is a symlink
// out of the task is refused before docker is called: the docker binary
// here does not exist, so any docker call would fail differently.
func TestCreateRefusesEscapingRepo(t *testing.T) {
	task := t.TempDir()
	if err := os.Symlink("/etc", filepath.Join(task, "repo")); err != nil {
		t.Fatal(err)
	}
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: utils.Docker{Binary: filepath.Join(task, "no-docker")}})
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_1"}, Task: &schemas.Task{Dir: task, RepoDir: "repo"}}
	_, err := e.Create(t.Context(), rc)
	if err == nil || !strings.Contains(err.Error(), "repo: resolves outside the folder through a symlink") {
		t.Fatalf("Create = %v, want the escaping repo refused", err)
	}
}
