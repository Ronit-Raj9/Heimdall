package handlers

// Agent image tags of a versions evaluation (M1). A version stores only its
// YAML (agentversions, vN.yaml); the agent's images folder is shared. The
// image tag is derived from build.tag (plus "-<suite>" and "-<task>" for a
// base_arg build, engine.ResolveAgentImageIn and ResolveTaskAgentImage), so
// two versions of one agent made from a template both carry the builder's
// "bench/agent-<id>:1". That is right when only settings or run time files
// differ: they run the same image, built once. When their build inputs
// differ (build.dockerfile, build.context, build.base_arg), one tag would
// hold whichever image was built last and one arm would run the other's
// container: the later arm gets "<tag>-v<N>" instead.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/schemas"
)

// armBuildInputs is what an image built from build.dockerfile depends on
// besides the shared files themselves.
type armBuildInputs struct{ dockerfile, context, baseArg string }

// versionArmTags gives each arm of a versions evaluation whose build inputs
// differ from an earlier arm with the same build.tag a tag of its own. root
// is the project root; arms are the experiment arms by spec arm label, with
// the task image variant already merged in.
func versionArmTags(spec schemas.EvaluationSpec, arms map[string]schemas.ArmSpec, root string) map[string]schemas.ArmSpec {
	if spec.Mode != schemas.EvaluationModeVersions || root == "" {
		return arms
	}
	seen := map[string]armBuildInputs{} // build tag -> inputs of the first arm using it
	for i, a := range spec.Arms {
		arm, ok := arms[a.Label]
		if !ok {
			continue
		}
		doc, err := armAgentDoc(root, a)
		if err != nil {
			continue // the planner reports a missing file or version
		}
		cfg, err := decodeAgent(doc, "agents/"+a.Agent+".yaml")
		if err != nil {
			continue
		}
		applyBuildSet(cfg, arm.Set)
		if cfg.Build == nil || cfg.Build.Dockerfile == "" || cfg.Build.Tag == "" {
			continue
		}
		in := armBuildInputs{cfg.Build.Dockerfile, cfg.Build.Context, cfg.Build.BaseArg}
		first, used := seen[cfg.Build.Tag]
		if !used {
			seen[cfg.Build.Tag] = in
			continue
		}
		if first == in {
			continue // same build: one image for both arms
		}
		n := a.Version
		if n <= 0 {
			n = i + 1
		}
		tag := fmt.Sprintf("%s-v%d", cfg.Build.Tag, n)
		set := map[string]any{"build.tag": tag}
		if cfg.Image == "" || cfg.Image == cfg.Build.Tag {
			set["image"] = tag
		}
		seen[tag] = in
		arms[a.Label] = withSet(arm, set)
	}
	return arms
}

// armAgentDoc is the YAML an arm runs: the frozen snapshot of a pinned
// version, else the current agents/<id>.yaml (evalArms' rule).
func armAgentDoc(root string, a schemas.EvaluationArm) ([]byte, error) {
	if a.Version > 0 && !a.LatestAtLaunch {
		doc, _, err := agentversions.Read(root, a.Agent, a.Version)
		return doc, err
	}
	return os.ReadFile(filepath.Join(root, "agents", a.Agent+".yaml"))
}

// applyBuildSet applies the arm overrides that change the image or its
// build (the task image variant's keys) to cfg, as the planner's overrides
// would (engine.ResolveAgentConfig; handlers cannot import engine).
func applyBuildSet(cfg *schemas.AgentConfig, set map[string]any) {
	for k, v := range set {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if k == "image" {
			cfg.Image = s
			continue
		}
		field, ok := strings.CutPrefix(k, "build.")
		if !ok {
			continue
		}
		if cfg.Build == nil {
			cfg.Build = &schemas.ImageSpec{}
		}
		switch field {
		case "dockerfile":
			cfg.Build.Dockerfile = s
		case "tag":
			cfg.Build.Tag = s
		case "base_arg":
			cfg.Build.BaseArg = s
		case "context":
			cfg.Build.Context = s
		}
	}
}
