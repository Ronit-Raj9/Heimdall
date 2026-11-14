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
