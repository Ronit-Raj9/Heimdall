package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxTagLength is the longest tag part docker accepts after the colon.
const maxTagLength = 128

var (
	// tagRe is docker's rule for the tag part of an image reference.
	tagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	// argRe is a Dockerfile ARG name the build can set.
	argRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// AgentImage is the image one agent config runs in one suite, and how to
// build it. ResolveAgentImage is the only place it is derived, so the
// manifest, the config hash, the run plan, `agent build`, `agent check` and
// doctor all name the same tag.
type AgentImage struct {
	Tag        string            // the tag runs start: cfg.Image, or "<build.tag>-<suite id>" with build.base_arg
	PerSuite   bool              // build.base_arg is set: one image per suite, on the suite's base image
	Dockerfile string            // absolute; empty when the config has no build section
	Context    string            // absolute build context: build.context, else the Dockerfile's folder
	BuildArgs  map[string]string // build.base_arg -> the suite's base image tag; empty without base_arg
}

// ImageInspector is implemented by an environment that can say which local
// image a tag names. The planner records the id per arm (ArmManifest.ImageID)
// so a frozen experiment never runs a rebuilt image by accident.
type ImageInspector interface {
	ImageID(ctx context.Context, ref string) (string, error)
}

// ResolveAgentImage derives the image cfg runs in suite. Without
// build.base_arg the tag is cfg.Image for every suite, unchanged. With it the
// agent's Dockerfile declares that ARG and starts FROM it: the image is built
// once per suite with the suite's base image as the argument and tagged
// "<build.tag>-<suite id>", so one agent config runs inside every suite's
// toolchain. build.dockerfile and build.context are relative to benchRoot.
//
// A per-suite image needs a build section whose tag has an explicit tag part
// (name:tag) and equals image, and a suite with a base image, because the
// derived tag is appended to that tag part.
func ResolveAgentImage(cfg *schemas.AgentConfig, suite *schemas.Suite, benchRoot string) (AgentImage, error) {
	if cfg == nil {
		return AgentImage{}, errors.New("resolve agent image: no agent config")
	}
	out := AgentImage{Tag: cfg.Image}
	b := cfg.Build
	if b == nil {
		return out, nil
	}
	if b.Dockerfile != "" {
		out.Dockerfile = benchPath(benchRoot, b.Dockerfile)
		out.Context = filepath.Dir(out.Dockerfile)
	}
	if b.Context != "" {
		out.Context = benchPath(benchRoot, b.Context)
	}
	if b.BaseArg == "" {
		return out, nil
	}
	tag, err := perSuiteTag(cfg, suite)
	if err != nil {
		return AgentImage{}, fmt.Errorf("agent %s: %w", cfg.ID, err)
	}
	out.Tag, out.PerSuite = tag, true
	out.BuildArgs = map[string]string{b.BaseArg: suite.BaseImage.Tag}
	return out, nil
}

// perSuiteTag validates a base_arg build and returns "<build.tag>-<suite id>".
func perSuiteTag(cfg *schemas.AgentConfig, suite *schemas.Suite) (string, error) {
	b := cfg.Build
	if !argRe.MatchString(b.BaseArg) {
		return "", fmt.Errorf("build.base_arg %q is not a Dockerfile ARG name", b.BaseArg)
	}
	if b.Dockerfile == "" {
		return "", errors.New("build.base_arg is set but build.dockerfile is empty")
	}
	if cfg.Image != "" && cfg.Image != b.Tag {
		return "", fmt.Errorf("build.base_arg is set, so image %q must equal build.tag %q", cfg.Image, b.Tag)
	}
	if !hasTagPart(b.Tag) {
		return "", fmt.Errorf("build.tag %q needs an explicit tag (name:tag) when build.base_arg is set", b.Tag)
	}
	if suite == nil || suite.ID == "" {
		return "", errors.New("build.base_arg is set but no suite was given")
	}
	if suite.BaseImage.Tag == "" {
		return "", fmt.Errorf("build.base_arg is set but suite %s has no base image", suite.ID)
	}
	tag := b.Tag + "-" + suite.ID
	_, part, _ := strings.Cut(tag[strings.LastIndex(tag, "/")+1:], ":")
	if !tagRe.MatchString(part) || len(part) > maxTagLength {
		return "", fmt.Errorf("per-suite tag %q is not a valid docker tag", tag)
	}
	return tag, nil
}

// hasTagPart reports whether an image reference names a tag after the last
// path segment, as in "bench/agent:1" (a registry port is not a tag).
func hasTagPart(ref string) bool {
	if strings.Contains(ref, "@") {
		return false
	}
	last := ref[strings.LastIndex(ref, "/")+1:]
	_, tag, ok := strings.Cut(last, ":")
	return ok && tag != ""
}

// benchPath resolves a bench-relative path.
func benchPath(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, filepath.FromSlash(p))
}
