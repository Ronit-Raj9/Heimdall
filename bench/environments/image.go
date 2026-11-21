package environments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// compatArm mirrors engine.CompatArm, which this package does not import:
// the compatibility agent's image id lives on Manifest.Compatibility.
const compatArm = "compat"

// ErrImageNotFound is returned, wrapped, when the local docker has no image
// for a reference.
var ErrImageNotFound = errors.New("image not found")

// ImageInfo is what the bench reads about a local image.
type ImageInfo struct {
	ID          string   // local image id, sha256:...
	User        string   // the image's USER; "" means root
	RepoDigests []string // registry digests, empty for an image never pushed or pulled
}

// NonRoot reports whether the image's USER is set and is not root.
func (i ImageInfo) NonRoot() bool {
	u, _, _ := strings.Cut(i.User, ":")
	return u != "" && u != "0" && u != "root"
}

// ImageBuild is one docker build of an agent image.
type ImageBuild struct {
	Dockerfile string
	Context    string
	Tag        string
	Args       map[string]string // --build-arg
	Labels     map[string]string // --label
}

// InspectImage reads one local image. A missing image is ErrImageNotFound.
func InspectImage(ctx context.Context, d utils.Docker, ref string) (ImageInfo, error) {
	res, err := d.Run(ctx, "image", "inspect", "--format", "{{json .}}", ref)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("inspect image %s: %w", ref, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if strings.Contains(strings.ToLower(msg), "no such image") {
			return ImageInfo{}, fmt.Errorf("inspect image %s: %w", ref, ErrImageNotFound)
		}
		return ImageInfo{}, fmt.Errorf("inspect image %s: exit %d: %s", ref, res.ExitCode, msg)
	}
	return parseImageInspect(res.Stdout)
}

// BuildImage runs `docker build` for b, streaming the build output to out,
// and returns the built image. The host runs only the docker CLI; every
// build step runs inside docker.
func BuildImage(ctx context.Context, d utils.Docker, b ImageBuild, out io.Writer) (ImageInfo, error) {
	args, err := buildArgs(b)
	if err != nil {
		return ImageInfo{}, err
	}
	cmd := d.Command(ctx, args...)
	cmd.Env = buildEnv(os.Environ())
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return ImageInfo{}, fmt.Errorf("docker build %s: %w", b.Tag, err)
	}
	return InspectImage(ctx, d, b.Tag)
}

// buildEnv is the environment of `docker build`: the caller's, with buildx's
// default provenance attestation switched off. On Docker's containerd image
// store the image id is the digest of an index that holds the attestation,
// and the attestation records the build time, so every rebuild of an
// unchanged agent got a new id. A frozen experiment compares that id before
// each run, so `agent check` (which rebuilds) after `plan` refused every
// remaining run of the experiment. Without the attestation an unchanged
// build keeps its id. The legacy builder ignores the variable. A value the
// caller set is kept.
func buildEnv(environ []string) []string {
	for _, kv := range environ {
		if strings.HasPrefix(kv, noDefaultAttestations+"=") {
			return environ
		}
	}
	return append(append([]string(nil), environ...), noDefaultAttestations+"=1")
}

// noDefaultAttestations is the buildx variable that turns off the
// attestations it adds by default.
const noDefaultAttestations = "BUILDX_NO_DEFAULT_ATTESTATIONS"

// ImageID implements engine.ImageInspector: the local image id of ref.
func (e *dockerWorkspace) ImageID(ctx context.Context, ref string) (string, error) {
	info, err := InspectImage(ctx, e.cfg.Docker, ref)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

// ImageRepoDigests implements engine.ImageDigester: the registry digests of
// ref, empty for an image built locally.
func (e *dockerWorkspace) ImageRepoDigests(ctx context.Context, ref string) ([]string, error) {
	info, err := InspectImage(ctx, e.cfg.Docker, ref)
	if err != nil {
		return nil, err
	}
	return info.RepoDigests, nil
}

// checkFrozenImage refuses a run whose agent image tag no longer names the
// image the manifest froze for its arm, so a rebuilt tag never changes what a
// frozen experiment measures. A manifest without an image id (planned before
// ids were recorded, or without docker) is not checked.
func (e *dockerWorkspace) checkFrozenImage(ctx context.Context, rc *schemas.RunContext) error {
	if err := e.checkFrozenTaskImage(ctx, rc); err != nil {
		return err
	}
	if err := e.checkFrozenSuiteImages(ctx, rc); err != nil { // Lock fingerprints: frozen suite image ids (docs/lock-fingerprints.md)
		return err
	}
	want := frozenImageID(rc)
	if want == "" || rc.Agent == nil || rc.Agent.Image == "" || perTaskAgent(rc) {
		return nil
	}
	got, err := e.ImageID(ctx, rc.Agent.Image)
	if err != nil {
		return fmt.Errorf("agent image: %w", err)
	}
	if got != want {
		return fmt.Errorf("agent image %s is now %s but experiment %s was planned with %s; restore that image or plan a new experiment",
			rc.Agent.Image, short(got), rc.Manifest.ExpID, short(want))
	}
	return nil
}

// checkFrozenTaskImage refuses a run whose task image tag no longer names
// the image id the manifest recorded for the task (Manifest.TaskImages). A
// task without its own image, or a manifest without the id, is not checked;
// a missing image is left to Create, which builds it.
func (e *dockerWorkspace) checkFrozenTaskImage(ctx context.Context, rc *schemas.RunContext) error {
	if rc.Manifest == nil || rc.Task == nil || rc.Manifest.TaskImages[rc.Task.ID] == "" {
		return nil
	}
	suiteID := ""
	if rc.Suite != nil {
		suiteID = rc.Suite.ID
	}
	img, ok, err := utils.TaskImage(suiteID, rc.Task)
	if err != nil || !ok {
		return err
	}
	got, found, err := e.cfg.Docker.ImageID(ctx, img.Tag)
	if err != nil || !found {
		return err
	}
	if want := rc.Manifest.TaskImages[rc.Task.ID]; got != want {
		return fmt.Errorf("task image %s is now %s but experiment %s was planned with %s; restore that image or plan a new experiment",
			img.Tag, short(got), rc.Manifest.ExpID, short(want))
	}
	return nil
}

// ImageRoles implements engine.ImageRoler: the images this environment
// starts beside the suite's own, by role. Lock fingerprints bind them and a
// plan records their ids (Manifest.SuiteImages).
func (e *dockerWorkspace) ImageRoles() map[string]string {
	out := map[string]string{}
	if e.cfg.HelperImage != "" {
		out[roleHelper] = e.cfg.HelperImage
	}
	if e.cfg.ToolProxyImage != "" {
		out[roleToolProxy] = e.cfg.ToolProxyImage
	}
	return out
}

// Image roles of Manifest.SuiteImages; services use "service/<id>". They
// mirror suite.ImageBase, ImageHelper and ImageToolProxy, which this package
// does not import.
const (
	roleBase      = "base"
	roleHelper    = "helper"
	roleToolProxy = "toolproxy"
	roleService   = "service/"
	roleTask      = "task/" // task/<task id>/service/<service id>: a task's own service
)

// checkFrozenSuiteImages refuses a run when a tag the manifest froze by id
// (Manifest.SuiteImages: base, helper, tool proxy, services) now names
// another image, so a rebuilt toolchain, helper, proxy or tool server never
// changes what a frozen experiment measures. A manifest without the map
// checks nothing; a role whose tag is unknown here, or whose image is
// missing or cannot be inspected, is not checked and logs a warning, never a
// refusal, because CI and fresh clones have no images and Create builds or
// pulls what is missing.
func (e *dockerWorkspace) checkFrozenSuiteImages(ctx context.Context, rc *schemas.RunContext) error {
	if rc == nil || rc.Manifest == nil || len(rc.Manifest.SuiteImages) == 0 {
		return nil
	}
	roles := make([]string, 0, len(rc.Manifest.SuiteImages))
	for role := range rc.Manifest.SuiteImages {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		want := rc.Manifest.SuiteImages[role]
		if otherTaskRole(rc, role) {
			continue // another task's service; its own runs check it
		}
		tag := e.suiteImageTag(rc, role)
		if tag == "" || want == "" {
			e.cfg.Logger.Warn("frozen image not checked: this run does not name it", "experiment", rc.Manifest.ExpID, "role", role)
			continue
		}
		got, found, err := e.cfg.Docker.ImageID(ctx, tag)
		if err != nil || !found {
			msg := "image missing"
			if err != nil {
				msg = err.Error()
			}
			e.cfg.Logger.Warn("frozen image not checked: it cannot be inspected", "experiment", rc.Manifest.ExpID, "role", role, "image", tag, "error", msg)
			continue
		}
		if got != want {
			return fmt.Errorf("%s image %s is now %s but experiment %s was planned with %s; restore that image or plan a new experiment",
				role, tag, short(got), rc.Manifest.ExpID, short(want))
		}
	}
	return nil
}

// suiteImageTag is the tag a run uses for an image role: the suite's base
// image, the configured helper and tool proxy images, or a service image of
// the suite (first) or the task. Empty when the run does not name it.
func (e *dockerWorkspace) suiteImageTag(rc *schemas.RunContext, role string) string {
	switch role {
	case roleBase:
		if rc.Suite != nil {
			return rc.Suite.BaseImage.Tag
		}
		return ""
	case roleHelper:
		return e.cfg.HelperImage
	case roleToolProxy:
		return e.cfg.ToolProxyImage
	}
	if rest, ok := strings.CutPrefix(role, roleTask); ok {
		taskID, id, ok := strings.Cut(rest, "/"+roleService)
		if !ok || rc.Task == nil || rc.Task.ID != taskID {
			return ""
		}
		for _, sv := range rc.Task.Services {
			if sv.ID != id {
				continue
			}
			return serviceTag(rc, sv, rc.Task.Dir, rc.Task.Environment != nil && rc.Task.Environment.BuildNetwork)
		}
		return ""
	}
	id, ok := strings.CutPrefix(role, roleService)
	if !ok || id == "" {
		return ""
	}
	if rc.Suite != nil {
		for _, sv := range rc.Suite.Services {
			if sv.ID == id {
				return serviceTag(rc, sv, rc.Suite.Root, false)
			}
		}
	}
	if rc.Task != nil {
		for _, sv := range rc.Task.Services {
			if sv.ID == id {
				return serviceTag(rc, sv, rc.Task.Dir, rc.Task.Environment != nil && rc.Task.Environment.BuildNetwork)
			}
		}
	}
	return ""
}

// serviceTag is the tag a run starts for a service (utils.ServiceImage, as
// the launch computes it), or "" when its build cannot be resolved.
func serviceTag(rc *schemas.RunContext, sv schemas.ServiceSpec, base string, network bool) string {
	img, err := serviceImage(rc, runService{spec: sv, base: base, network: network})
	if err != nil {
		return ""
	}
	return img.Tag
}

// otherTaskRole reports whether role is a task service role of a task other
// than the run's: each run checks only its own task's services.
func otherTaskRole(rc *schemas.RunContext, role string) bool {
	rest, ok := strings.CutPrefix(role, roleTask)
	if !ok {
		return false
	}
	taskID, _, _ := strings.Cut(rest, "/")
	return rc.Task == nil || rc.Task.ID != taskID
}

// perTaskAgent reports whether the run's agent image is chosen per task: the
// task has its own image and either the agent build declares base_arg, or the
// manifest recorded a per-task image for the run's arm (a scripted control
// runs in the task image itself). The arm's frozen id is the per-suite
// image's, so it is not compared; the task image is (checkFrozenTaskImage),
// and the engine compares the per-task image with the id it recorded.
func perTaskAgent(rc *schemas.RunContext) bool {
	if rc.Task == nil || rc.Task.Environment == nil || rc.Agent == nil {
		return false
	}
	if rc.Agent.Build != nil && rc.Agent.Build.BaseArg != "" {
		return true
	}
	return armTaskImage(rc) != ""
}

// armTaskImage is the per-task image the manifest recorded for the run's arm
// and task, or empty.
func armTaskImage(rc *schemas.RunContext) string {
	if rc.Manifest == nil || rc.Slot == nil {
		return ""
	}
	if rc.Slot.Arm == compatArm {
		if rc.Manifest.Compatibility == nil {
			return ""
		}
		return rc.Manifest.Compatibility.TaskImages[rc.Task.ID]
	}
	return rc.Manifest.Arms[rc.Slot.Arm].TaskImages[rc.Task.ID]
}

// frozenImageID is the image id the manifest recorded for the run's arm.
func frozenImageID(rc *schemas.RunContext) string {
	if rc == nil || rc.Manifest == nil || rc.Slot == nil {
		return ""
	}
	if rc.Slot.Arm == compatArm {
		if rc.Manifest.Compatibility == nil {
			return ""
		}
		return rc.Manifest.Compatibility.ImageID
	}
	return rc.Manifest.Arms[rc.Slot.Arm].ImageID
}

// buildArgs is the `docker build` command line, with build args and labels
// in sorted order so the same build always runs the same command. It is pure.
func buildArgs(b ImageBuild) ([]string, error) {
	switch {
	case b.Tag == "":
		return nil, errors.New("docker build: no tag")
	case b.Dockerfile == "":
		return nil, fmt.Errorf("docker build %s: no Dockerfile", b.Tag)
	case b.Context == "":
		return nil, fmt.Errorf("docker build %s: no build context", b.Tag)
	}
	args := []string{"build", "-f", b.Dockerfile, "-t", b.Tag}
	for _, k := range sortedKeys(b.Args) {
		args = append(args, "--build-arg", k+"="+b.Args[k])
	}
	for _, k := range sortedKeys(b.Labels) {
		args = append(args, "--label", k+"="+b.Labels[k])
	}
	return append(args, b.Context), nil
}

// parseImageInspect decodes `docker image inspect --format '{{json .}}'`.
func parseImageInspect(raw []byte) (ImageInfo, error) {
	var v struct {
		ID          string   `json:"Id"`
		RepoDigests []string `json:"RepoDigests"`
		Config      struct {
			User string `json:"User"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ImageInfo{}, fmt.Errorf("parse image inspect: %w", err)
	}
	if v.ID == "" {
		return ImageInfo{}, errors.New("parse image inspect: no image id")
	}
	return ImageInfo{ID: v.ID, User: v.Config.User, RepoDigests: v.RepoDigests}, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// short trims an image id for a message.
func short(id string) string {
	s := strings.TrimPrefix(id, "sha256:")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
