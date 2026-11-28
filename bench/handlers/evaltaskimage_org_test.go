package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// orgWithOwnCodex is an organization project that holds only its own
// agents: agents/codex.yaml's build folder, no -task files (28 Nov: every
// organization but the test one).
func orgWithOwnCodex(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	df := filepath.Join(root, "agents", "images", "codex", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(df), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(df, []byte("FROM bench/coding-core-base:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// applySet applies the derived arm overrides the planner applies.
func applySet(cfg *schemas.AgentConfig, set map[string]any) schemas.AgentConfig {
	out := *cfg
	b := *cfg.Build
	for k, v := range set {
		s, _ := v.(string)
		switch k {
		case "build.dockerfile":
			b.Dockerfile = s
		case "build.tag":
			b.Tag = s
		case "build.base_arg":
			b.BaseArg = s
		case "build.context":
			b.Context = s
		case "image":
			out.Image = s
		}
	}
	out.Build = &b
	return out
}

func TestTaskImageCodexResolvesInOrgWithoutTaskFiles(t *testing.T) {
	t.Setenv(suite.LibraryEnv, "")
	root := orgWithOwnCodex(t)
	st := taskImageSuite()
	st.BaseImage = schemas.ImageSpec{Tag: "bench/tb-base:1"}
	v := deriveTaskImageAgent(codexLike(), st, nil, map[string]*schemas.AgentConfig{}, root)
	if v.Skip != "" || v.Set["build.dockerfile"] != "agents/images/codex-task/Dockerfile" || v.Set["image"] != "bench/agent-codex-task:1" {
		t.Fatalf("variant %+v", v)
	}
	cfg := applySet(codexLike(), v.Set)
	cfg.Image = cfg.Build.Tag
	img, err := engine.ResolveTaskAgentImage(&cfg, st, st.TaskByID("t01"), "harbor/t01:1", root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !img.PerTask || img.Tag != "bench/agent-codex-task:1-hd-harbor-tb2-t01" || img.BuildArgs["BASE_IMAGE"] != "harbor/t01:1" {
		t.Fatalf("image %+v", img)
	}
	b, err := os.ReadFile(img.Dockerfile)
	if err != nil {
		t.Fatalf("worker cannot read the Dockerfile: %v", err)
	}
	if !strings.Contains(string(b), "FROM ${BASE_IMAGE}") || img.Context != filepath.Dir(img.Dockerfile) {
		t.Fatalf("Dockerfile %s context %s:\n%s", img.Dockerfile, img.Context, b)
	}
}

// The library's older codex-task.yaml (agents/live-l10-images/...) still
// works: its Dockerfile resolves in the library, not the project.
func TestTaskImageLibraryVariantResolvesInLibrary(t *testing.T) {
	root, lib := orgWithOwnCodex(t), t.TempDir()
	df := filepath.Join(lib, "agents", "live-l10-images", "codex-task", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(df), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(df, []byte("ARG BASE_IMAGE\nFROM ${BASE_IMAGE}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(suite.LibraryEnv, lib)
	old := &schemas.AgentConfig{ID: "codex-task", Adapter: "command", Image: "bench/agent-codex-task:1",
		Build: &schemas.ImageSpec{Dockerfile: "agents/live-l10-images/codex-task/Dockerfile", Tag: "bench/agent-codex-task:1", BaseArg: "BASE_IMAGE"}}
	st := taskImageSuite()
	st.BaseImage = schemas.ImageSpec{Tag: "bench/tb-base:1"}
	v := deriveTaskImageAgent(codexLike(), st, nil, map[string]*schemas.AgentConfig{"agents/codex-task.yaml": old}, root)
	if v.Skip != "" {
		t.Fatalf("variant %+v", v)
	}
	cfg := applySet(codexLike(), v.Set)
	img, err := engine.ResolveTaskAgentImage(&cfg, st, st.TaskByID("t01"), "harbor/t01:1", root, suite.LibraryDir())
	if err != nil || img.Dockerfile != df {
		t.Fatalf("image %+v err %v", img, err)
	}
	// Removed from the library too: refused at the estimate and the launch,
	// with the agent and the missing file named.
	if err := os.RemoveAll(filepath.Join(lib, "agents", "live-l10-images")); err != nil {
		t.Fatal(err)
	}
	v = deriveTaskImageAgent(codexLike(), st, nil, map[string]*schemas.AgentConfig{"agents/codex-task.yaml": old}, root)
	if !strings.Contains(v.Skip, "agent codex") || !strings.Contains(v.Skip, "agents/live-l10-images/codex-task/Dockerfile") {
		t.Fatalf("missing file: %+v", v)
	}
}

// Every built-in agent's build files under agents/ ship in the binary, so a
// worker finds them without a copy in the organization's project.
func TestBuiltinAgentBuildFilesShip(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "agents", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v %d", err, len(files))
	}
	empty := t.TempDir()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var cfg schemas.AgentConfig
		if err := yamlUnmarshalAgent(b, &cfg); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if cfg.Build == nil || cfg.Build.Dockerfile == "" || !strings.HasPrefix(cfg.Build.Dockerfile, "agents/") {
			continue
		}
		if _, found := utils.AgentBuildPath(empty, "", cfg.Build.Dockerfile); !found {
			t.Errorf("%s: build.dockerfile %s is not shipped with the binary", f, cfg.Build.Dockerfile)
		}
	}
}

func yamlUnmarshalAgent(b []byte, cfg *schemas.AgentConfig) error { return yaml.Unmarshal(b, cfg) }
