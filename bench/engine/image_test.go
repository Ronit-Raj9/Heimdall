package engine

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestResolveAgentImage(t *testing.T) {
	root := filepath.FromSlash("/bench")
	suite := &schemas.Suite{ID: "coding-core", BaseImage: schemas.ImageSpec{Tag: "bench/coding-core-base:1"}}
	build := func(mut func(*schemas.ImageSpec)) *schemas.AgentConfig {
		b := &schemas.ImageSpec{Dockerfile: "agents/images/x/Dockerfile", Tag: "acme/agent-x:2", BaseArg: "BASE_IMAGE"}
		if mut != nil {
			mut(b)
		}
		return &schemas.AgentConfig{ID: "x", Image: "acme/agent-x:2", Build: b}
	}
	tests := []struct {
		name    string
		cfg     *schemas.AgentConfig
		suite   *schemas.Suite
		want    AgentImage
		wantErr string
	}{
		{name: "no build keeps the image", cfg: &schemas.AgentConfig{ID: "x", Image: "bench/agent-codex:1"}, suite: suite,
			want: AgentImage{Tag: "bench/agent-codex:1"}},
		{name: "build without base_arg keeps the image for every suite",
			cfg:   &schemas.AgentConfig{ID: "x", Image: "bench/agent-x:1", Build: &schemas.ImageSpec{Dockerfile: "agents/images/x/Dockerfile", Tag: "bench/agent-x:1"}},
			suite: nil,
			want:  AgentImage{Tag: "bench/agent-x:1", Dockerfile: filepath.Join(root, "agents/images/x/Dockerfile"), Context: filepath.Join(root, "agents/images/x")}},
		{name: "base_arg builds per suite", cfg: build(nil), suite: suite,
			want: AgentImage{Tag: "acme/agent-x:2-coding-core", PerSuite: true, Dockerfile: filepath.Join(root, "agents/images/x/Dockerfile"),
				Context: filepath.Join(root, "agents/images/x"), BuildArgs: map[string]string{"BASE_IMAGE": "bench/coding-core-base:1"}}},
		{name: "context overrides the Dockerfile folder", cfg: build(func(b *schemas.ImageSpec) { b.Context = "." }), suite: suite,
			want: AgentImage{Tag: "acme/agent-x:2-coding-core", PerSuite: true, Dockerfile: filepath.Join(root, "agents/images/x/Dockerfile"),
				Context: root, BuildArgs: map[string]string{"BASE_IMAGE": "bench/coding-core-base:1"}}},
		{name: "registry port is not a tag", cfg: func() *schemas.AgentConfig {
			c := build(func(b *schemas.ImageSpec) { b.Tag = "registry:5000/agent" })
			c.Image = "registry:5000/agent"
			return c
		}(), suite: suite, wantErr: "explicit tag"},
		{name: "image must equal build.tag", cfg: func() *schemas.AgentConfig { c := build(nil); c.Image = "other:1"; return c }(), suite: suite, wantErr: "must equal build.tag"},
		{name: "base_arg needs a Dockerfile", cfg: build(func(b *schemas.ImageSpec) { b.Dockerfile = "" }), suite: suite, wantErr: "build.dockerfile is empty"},
		{name: "base_arg needs an ARG name", cfg: build(func(b *schemas.ImageSpec) { b.BaseArg = "1BAD" }), suite: suite, wantErr: "not a Dockerfile ARG"},
		{name: "base_arg needs a suite", cfg: build(nil), suite: nil, wantErr: "no suite"},
		{name: "base_arg needs a suite base image", cfg: build(nil), suite: &schemas.Suite{ID: "s"}, wantErr: "no base image"},
		{name: "derived tag must be a docker tag", cfg: build(nil), suite: &schemas.Suite{ID: "Bad Suite", BaseImage: schemas.ImageSpec{Tag: "b:1"}}, wantErr: "not a valid docker tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAgentImage(tt.cfg, tt.suite, root)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
	if _, err := ResolveAgentImage(nil, suite, root); err == nil {
		t.Error("nil config resolved")
	}
}

func TestHasTagPart(t *testing.T) {
	for ref, want := range map[string]bool{
		"bench/agent:1": true, "agent:latest": true, "agent": false, "registry:5000/agent": false,
		"registry:5000/agent:2": true, "agent@sha256:abc": false, "agent:": false,
	} {
		if got := hasTagPart(ref); got != want {
			t.Errorf("hasTagPart(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestResolveTaskAgentImage(t *testing.T) {
	root := filepath.FromSlash("/bench")
	suite := &schemas.Suite{ID: "tb", BaseImage: schemas.ImageSpec{Tag: "bench/tb-base:1"}}
	task := &schemas.Task{ID: "fix-git", Environment: &schemas.TaskEnvironment{Dockerfile: "environment/Dockerfile"}}
	based := &schemas.AgentConfig{ID: "x", Image: "acme/agent-x:2",
		Build: &schemas.ImageSpec{Dockerfile: "agents/images/x/Dockerfile", Tag: "acme/agent-x:2", BaseArg: "BASE_IMAGE"}}
	own := &schemas.AgentConfig{ID: "y", Image: "bench/agent-y:1"}
	tests := []struct {
		name      string
		cfg       *schemas.AgentConfig
		task      *schemas.Task
		taskImage string
		want      AgentImage
		wantErr   string
	}{
		{name: "no task image is the per-suite image", cfg: based, task: task, taskImage: "",
			want: AgentImage{Tag: "acme/agent-x:2-tb", PerSuite: true, Dockerfile: filepath.Join(root, "agents/images/x/Dockerfile"),
				Context: filepath.Join(root, "agents/images/x"), BuildArgs: map[string]string{"BASE_IMAGE": "bench/tb-base:1"}}},
		{name: "base_arg builds on the task image", cfg: based, task: task, taskImage: "bench/task-tb-fix-git:0123456789ab",
			want: AgentImage{Tag: "acme/agent-x:2-tb-fix-git", PerSuite: true, PerTask: true, Dockerfile: filepath.Join(root, "agents/images/x/Dockerfile"),
				Context: filepath.Join(root, "agents/images/x"), BuildArgs: map[string]string{"BASE_IMAGE": "bench/task-tb-fix-git:0123456789ab"}}},
		{name: "no base_arg keeps the agent's own image", cfg: own, task: task, taskImage: "bench/task-tb-fix-git:0123456789ab",
			want: AgentImage{Tag: "bench/agent-y:1", OwnImage: true}},
		{name: "task id must make a docker tag", cfg: based, task: &schemas.Task{ID: "Bad Task"}, taskImage: "t:1", wantErr: "not a valid docker tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTaskAgentImage(tt.cfg, suite, tt.task, tt.taskImage, root)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
