package environments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	cmd := exec.CommandContext(ctx, dockerBinary(d), args...)
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

// checkFrozenImage refuses a run whose agent image tag no longer names the
// image the manifest froze for its arm, so a rebuilt tag never changes what a
// frozen experiment measures. A manifest without an image id (planned before
// ids were recorded, or without docker) is not checked.
func (e *dockerWorkspace) checkFrozenImage(ctx context.Context, rc *schemas.RunContext) error {
	want := frozenImageID(rc)
	if want == "" || rc.Agent == nil || rc.Agent.Image == "" {
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
