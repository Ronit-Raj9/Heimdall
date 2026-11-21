package engine

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// imageRoleEnv is the inspecting fake environment plus engine.ImageRoler.
type imageRoleEnv struct {
	inspectingEnv
	roles map[string]string
}

func (e imageRoleEnv) ImageRoles() map[string]string { return e.roles }

func TestSuiteImages(t *testing.T) {
	ids := map[string]string{
		"bench/base:1": "sha256:base", "bench/helper:1": "sha256:helper", "bench/tp:dev": "sha256:tp",
		"bench/db:1": "sha256:db", "bench/built:1": "sha256:built", "bench/mcp:1": "sha256:mcp",
	}
	roles := map[string]string{"helper": "bench/helper:1", "toolproxy": "bench/tp:dev"}
	withServices := &schemas.Suite{ID: "s", Environment: "docker_workspace", BaseImage: schemas.ImageSpec{Tag: "bench/base:1"},
		Services: []schemas.ServiceSpec{{ID: "db", Image: "bench/db:1"}, {ID: "api", Image: "ignored", Build: &schemas.ImageSpec{Tag: "bench/built:1"}}}}
	coding := &schemas.Suite{ID: "c", Environment: "docker_workspace", BaseImage: schemas.ImageSpec{Tag: "bench/base:1"}}
	taskService := &schemas.Suite{ID: "t", Environment: "docker_workspace", BaseImage: schemas.ImageSpec{Tag: "bench/base:1"},
		Tasks: []*schemas.Task{{ID: "t01", Services: []schemas.ServiceSpec{{ID: "mcp", Image: "bench/mcp:1"}}}}}
	missingBase := &schemas.Suite{ID: "m", Environment: "docker_workspace", BaseImage: schemas.ImageSpec{Tag: "bench/gone:1"}}

	tests := []struct {
		name  string
		env   schemas.Environment
		suite *schemas.Suite
		want  map[string]string
		warn  string
	}{
		{"every role with services", imageRoleEnv{inspectingEnv{fakeEnv{part{"docker_workspace"}, nil}, ids}, roles}, withServices,
			map[string]string{"base": "sha256:base", "helper": "sha256:helper", "toolproxy": "sha256:tp",
				"service/db": "sha256:db"}, ""}, // QC2-09-04: api's launch builds a content-derived tag from a Dockerfile it lacks, never build.tag, so nothing is frozen for it
		{"no tool proxy without services", imageRoleEnv{inspectingEnv{fakeEnv{part{"docker_workspace"}, nil}, ids}, roles}, coding,
			map[string]string{"base": "sha256:base", "helper": "sha256:helper"}, ""},
		{"task service needs the tool proxy", imageRoleEnv{inspectingEnv{fakeEnv{part{"docker_workspace"}, nil}, ids}, roles}, taskService,
			map[string]string{"base": "sha256:base", "helper": "sha256:helper", "toolproxy": "sha256:tp", "task/t01/service/mcp": "sha256:mcp"}, ""},
		{"inspector without roles", inspectingEnv{fakeEnv{part{"docker_workspace"}, nil}, ids}, coding,
			map[string]string{"base": "sha256:base"}, ""},
		{"missing image warns and is left out", inspectingEnv{fakeEnv{part{"docker_workspace"}, nil}, ids}, missingBase,
			nil, "image not inspected; the manifest records no id for it"},
		{"no inspector", fakeEnv{part{"docker_workspace"}, nil}, coding, nil, ""},
		{"environment not registered", inspectingEnv{fakeEnv{part{"other"}, nil}, ids}, coding, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &warnLog{}
			p := NewPlanner(&schemas.Registry{Environments: []schemas.Environment{tt.env}}, t.TempDir(), log)
			got := p.suiteImages(t.Context(), tt.suite)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if tt.warn == "" && log.joined() != "" {
				t.Fatalf("unexpected warning %q", log.joined())
			}
			if tt.warn != "" && !strings.Contains(log.joined(), tt.warn) {
				t.Fatalf("warnings %q, want %q", log.joined(), tt.warn)
			}
		})
	}
	if got := NewPlanner(nil, t.TempDir(), nil).suiteImages(t.Context(), coding); got != nil {
		t.Fatalf("nil registry got %v", got)
	}
}
