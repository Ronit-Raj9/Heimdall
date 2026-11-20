package environments

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestBuildArgs(t *testing.T) {
	got, err := buildArgs(ImageBuild{Dockerfile: "/p/agents/images/x/Dockerfile", Context: "/p/agents/images/x", Tag: "acme/x:1-coding-core",
		Args: map[string]string{"BASE_IMAGE": "bench/coding-core-base:1", "A": "1"}, Labels: map[string]string{"bifrost-bench.agent": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "-f", "/p/agents/images/x/Dockerfile", "-t", "acme/x:1-coding-core",
		"--build-arg", "A=1", "--build-arg", "BASE_IMAGE=bench/coding-core-base:1", "--label", "bifrost-bench.agent=x", "/p/agents/images/x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args\n got %v\nwant %v", got, want)
	}
	for _, b := range []ImageBuild{{Dockerfile: "d", Context: "c"}, {Tag: "t", Context: "c"}, {Tag: "t", Dockerfile: "d"}} {
		if _, err := buildArgs(b); err == nil {
			t.Errorf("build %+v accepted", b)
		}
	}
}

func TestBuildEnv(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		want    []string
	}{
		{"adds the switch", []string{"PATH=/usr/bin", "HOME=/home/a"}, []string{"PATH=/usr/bin", "HOME=/home/a", "BUILDX_NO_DEFAULT_ATTESTATIONS=1"}},
		{"keeps the caller's value", []string{"BUILDX_NO_DEFAULT_ATTESTATIONS=0"}, []string{"BUILDX_NO_DEFAULT_ATTESTATIONS=0"}},
		{"empty environment", nil, []string{"BUILDX_NO_DEFAULT_ATTESTATIONS=1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := append([]string(nil), tt.environ...)
			if got := buildEnv(in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(in, tt.environ) {
				t.Fatalf("input changed to %v", in)
			}
		})
	}
}

func TestParseImageInspect(t *testing.T) {
	info, err := parseImageInspect([]byte(`{"Id":"sha256:abc","RepoDigests":["acme/x@sha256:def"],"Config":{"User":"agent"}}`))
	if err != nil || info.ID != "sha256:abc" || info.User != "agent" || len(info.RepoDigests) != 1 || !info.NonRoot() {
		t.Fatalf("info %+v err %v", info, err)
	}
	if _, err := parseImageInspect([]byte(`{"Config":{}}`)); err == nil {
		t.Error("no id accepted")
	}
	if _, err := parseImageInspect([]byte(`nope`)); err == nil {
		t.Error("bad json accepted")
	}
	for user, want := range map[string]bool{"": false, "root": false, "0": false, "0:0": false, "1000": true, "agent:agent": true} {
		if got := (ImageInfo{User: user}).NonRoot(); got != want {
			t.Errorf("NonRoot(%q) = %v, want %v", user, got, want)
		}
	}
}

func TestFrozenImageID(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_1", Arms: map[string]schemas.ArmManifest{"A": {ImageID: "sha256:a"}, "B": {}},
		Compatibility: &schemas.ArmManifest{ImageID: "sha256:c"}}
	tests := []struct {
		name string
		rc   *schemas.RunContext
		want string
	}{
		{"arm with id", &schemas.RunContext{Manifest: m, Slot: &schemas.Slot{Arm: "A"}}, "sha256:a"},
		{"arm without id", &schemas.RunContext{Manifest: m, Slot: &schemas.Slot{Arm: "B"}}, ""},
		{"compatibility arm", &schemas.RunContext{Manifest: m, Slot: &schemas.Slot{Arm: compatArm}}, "sha256:c"},
		{"no manifest", &schemas.RunContext{Slot: &schemas.Slot{Arm: "A"}}, ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := frozenImageID(tt.rc); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	// Without a recorded id nothing is inspected, so no docker is needed.
	e := newDockerWorkspace(DockerWorkspaceConfig{})
	if err := e.checkFrozenImage(t.Context(), &schemas.RunContext{Manifest: m, Slot: &schemas.Slot{Arm: "B"}, Agent: &schemas.AgentConfig{Image: "x:1"}}); err != nil {
		t.Fatal(err)
	}
	if s := short("sha256:0123456789abcdef"); s != "0123456789ab" || !strings.HasPrefix(short("abc"), "abc") {
		t.Errorf("short = %q", s)
	}
}

// A per-task agent image is not compared with the arm's frozen id: an agent
// built per task, or a scripted control the manifest placed in the task image.
func TestPerTaskAgent(t *testing.T) {
	task := &schemas.Task{ID: "t01", Environment: &schemas.TaskEnvironment{Image: "task:1"}}
	plain := &schemas.Task{ID: "t02"}
	m := &schemas.Manifest{
		Arms: map[string]schemas.ArmManifest{
			"A": {TaskImages: map[string]string{"t01": "task:1"}},
			"B": {},
		},
		Compatibility: &schemas.ArmManifest{TaskImages: map[string]string{"t01": "task:1"}},
	}
	baseArg := &schemas.AgentConfig{Build: &schemas.ImageSpec{Tag: "agent:1", BaseArg: "BASE"}}
	control := &schemas.AgentConfig{Adapter: "scripted", Image: "helper:1"}
	tests := []struct {
		name string
		rc   *schemas.RunContext
		want bool
	}{
		{"agent built per task", &schemas.RunContext{Task: task, Agent: baseArg}, true},
		{"control the manifest placed in the task image", &schemas.RunContext{Manifest: m, Slot: &schemas.Slot{Arm: "A"}, Task: task, Agent: control}, true},
		{"compatibility control", &schemas.RunContext{Manifest: m, Slot: &schemas.Slot{Arm: compatArm}, Task: task, Agent: control}, true},
		{"arm without a per-task image", &schemas.RunContext{Manifest: m, Slot: &schemas.Slot{Arm: "B"}, Task: task, Agent: control}, false},
		{"task without its own image", &schemas.RunContext{Manifest: m, Slot: &schemas.Slot{Arm: "A"}, Task: plain, Agent: baseArg}, false},
		{"no manifest", &schemas.RunContext{Task: task, Agent: control}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := perTaskAgent(tt.rc); got != tt.want {
				t.Errorf("perTaskAgent = %v, want %v", got, tt.want)
			}
		})
	}
}

// imageIDDocker is a docker stand-in whose `image inspect --format {{.Id}}
// <ref>` prints ids[ref] and reports any other ref as missing.
func imageIDDocker(t *testing.T, ids map[string]string) utils.Docker {
	t.Helper()
	dir := t.TempDir()
	cases := ""
	for ref, id := range ids {
		cases += fmt.Sprintf("  %q) echo %q; exit 0 ;;\n", ref, id)
	}
	script := "#!/bin/sh\ncase \"$5\" in\n" + cases + "esac\necho \"Error: No such image: $5\" >&2\nexit 1\n"
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}
}

// warnLines records Warn calls.
type warnLines struct {
	schemas.NoopLogger
	mu    sync.Mutex
	lines []string
}

func (l *warnLines) Warn(msg string, kv ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(append([]any{msg, " "}, kv...)...))
}

func (l *warnLines) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func TestImageRoles(t *testing.T) {
	got := newDockerWorkspace(DockerWorkspaceConfig{HelperImage: "bench/helper:1", ToolProxyImage: "bench/bifrost-bench:dev"}).ImageRoles()
	want := map[string]string{"helper": "bench/helper:1", "toolproxy": "bench/bifrost-bench:dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := newDockerWorkspace(DockerWorkspaceConfig{}).ImageRoles(); len(got) != 0 {
		t.Fatalf("empty config got %v", got)
	}
}

func TestCheckFrozenSuiteImages(t *testing.T) {
	docker := imageIDDocker(t, map[string]string{
		"bench/base:1": "sha256:base0000000000000", "bench/helper:1": "sha256:helper000000000",
		"bench/tp:dev": "sha256:tp00000000000000", "bench/db:1": "sha256:db00000000000000", "bench/mcp:1": "sha256:mcp0000000000000",
	})
	suite := &schemas.Suite{ID: "s", BaseImage: schemas.ImageSpec{Tag: "bench/base:1"},
		Services: []schemas.ServiceSpec{{ID: "db", Image: "bench/db:1"}}}
	task := &schemas.Task{ID: "t01", Services: []schemas.ServiceSpec{{ID: "mcp", Build: &schemas.ImageSpec{Tag: "bench/mcp:1"}}}}
	rc := func(images map[string]string) *schemas.RunContext {
		return &schemas.RunContext{Manifest: &schemas.Manifest{ExpID: "e_1", SuiteImages: images}, Suite: suite, Task: task}
	}
	tests := []struct {
		name    string
		images  map[string]string
		wantErr string
		warn    string
	}{
		{"no frozen images", nil, "", ""},
		{"every id matches", map[string]string{"base": "sha256:base0000000000000", "helper": "sha256:helper000000000",
			"toolproxy": "sha256:tp00000000000000", "service/db": "sha256:db00000000000000", "service/mcp": "sha256:mcp0000000000000"}, "", ""},
		{"rebuilt base refused", map[string]string{"base": "sha256:old0000000000000"},
			"base image bench/base:1 is now base00000000 but experiment e_1 was planned with old000000000; restore that image or plan a new experiment", ""},
		{"rebuilt helper refused", map[string]string{"helper": "sha256:old0000000000000"}, "helper image bench/helper:1 is now", ""},
		{"rebuilt task service refused", map[string]string{"service/mcp": "sha256:old0000000000000"}, "service/mcp image bench/mcp:1 is now", ""},
		{"task-scoped service matches", map[string]string{"task/t01/service/mcp": "sha256:mcp0000000000000"}, "", ""},
		{"rebuilt task-scoped service refused", map[string]string{"task/t01/service/mcp": "sha256:old0000000000000"}, "task/t01/service/mcp image bench/mcp:1 is now", ""},
		{"another task's service is skipped silently", map[string]string{"task/t02/service/mcp": "sha256:old0000000000000"}, "", ""},
		{"role this run does not name warns", map[string]string{"service/gone": "sha256:x"}, "", "frozen image not checked: this run does not name it"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &warnLines{}
			e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "bench/helper:1", ToolProxyImage: "bench/tp:dev", Logger: log})
			err := e.checkFrozenSuiteImages(t.Context(), rc(tt.images))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err %v, want %q", err, tt.wantErr)
			}
			if tt.warn == "" && log.joined() != "" {
				t.Fatalf("unexpected warning %q", log.joined())
			}
			if tt.warn != "" && !strings.Contains(log.joined(), tt.warn) {
				t.Fatalf("warnings %q, want %q", log.joined(), tt.warn)
			}
		})
	}

	t.Run("missing image warns, never refuses", func(t *testing.T) {
		log := &warnLines{}
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: imageIDDocker(t, nil), HelperImage: "bench/helper:1", Logger: log})
		if err := e.checkFrozenSuiteImages(t.Context(), rc(map[string]string{"base": "sha256:a", "helper": "sha256:b"})); err != nil {
			t.Fatalf("refused: %v", err)
		}
		if got := log.joined(); strings.Count(got, "frozen image not checked: it cannot be inspected") != 2 {
			t.Fatalf("warnings %q", got)
		}
	})
	t.Run("no docker warns, never refuses", func(t *testing.T) {
		log := &warnLines{}
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: utils.Docker{Binary: filepath.Join(t.TempDir(), "no-docker")}, Logger: log})
		if err := e.checkFrozenSuiteImages(t.Context(), rc(map[string]string{"base": "sha256:a"})); err != nil {
			t.Fatalf("refused: %v", err)
		}
		if !strings.Contains(log.joined(), "cannot be inspected") {
			t.Fatalf("warnings %q", log.joined())
		}
	})
	t.Run("checkFrozenImage runs it", func(t *testing.T) {
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker})
		err := e.checkFrozenImage(t.Context(), rc(map[string]string{"base": "sha256:old0000000000000"}))
		if err == nil || !strings.Contains(err.Error(), "base image bench/base:1 is now") {
			t.Fatalf("err %v", err)
		}
	})
}
