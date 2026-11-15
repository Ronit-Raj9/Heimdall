package environments

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
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
