package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// LocalImage is an image a run needs on the local docker: a prebuilt tag, or
// a Dockerfile build tagged by the hash of everything that goes into it, so
// an unchanged build is never repeated and a changed one never reuses a
// stale image.
type LocalImage struct {
	Tag        string // the reference containers start from
	Dockerfile string // absolute; empty for a prebuilt image
	Context    string // absolute build context
	Network    bool   // the build may use the network (BuildNetwork)
}

// imageNameRe is a valid docker repository path component.
var imageNameRe = regexp.MustCompile(`[^a-z0-9._-]+`)

// buildLocks serialises builds of one tag inside this process, so two runs of
// the same task do not build the same image twice at once.
var buildLocks sync.Map

// TaskImage resolves a task's own image (Task.Environment). It reports false
// when the task has no environment or names neither an image nor a
// Dockerfile. A Dockerfile build is tagged
// bench/task-<suite>-<task>:<12 hex of the content hash>; the Dockerfile and
// the build context must lie inside the task folder.
func TaskImage(suiteID string, t *schemas.Task) (LocalImage, bool, error) {
	if t == nil || t.Environment == nil {
		return LocalImage{}, false, nil
	}
	env := t.Environment
	if env.Image != "" {
		return LocalImage{Tag: env.Image}, true, nil
	}
	if env.Dockerfile == "" {
		return LocalImage{}, false, nil
	}
	img, err := BuildImageSpec(t.Dir, env.Dockerfile, env.Context, "task-"+suiteID+"-"+t.ID, env.BuildNetwork)
	if err != nil {
		return LocalImage{}, false, fmt.Errorf("task %s image: %w", t.ID, err)
	}
	return img, true, nil
}

// TaskUserRoot is the only TaskEnvironment.User value: the agent runs as root.
const TaskUserRoot = "root"

// RootCaps are the capabilities a root agent or root verifier (a task with
// environment.user root) gets back after --cap-drop ALL: what apt, dpkg and
// pip need to install packages and change file owners. NET_RAW, MKNOD,
// SYS_ADMIN and every other capability stay dropped.
var RootCaps = []string{"AUDIT_WRITE", "CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "KILL", "NET_BIND_SERVICE", "SETFCAP", "SETGID", "SETUID", "SYS_CHROOT"}

// TaskRunsAsRoot reports whether a task's agent, scripted controls and
// verifier run as root: the task has its own image and its environment says
// user root. A user on a task without its own image is ignored.
func TaskRunsAsRoot(t *schemas.Task) bool {
	if t == nil || t.Environment == nil || t.Environment.User != TaskUserRoot {
		return false
	}
	return t.Environment.Image != "" || t.Environment.Dockerfile != ""
}

// BuildImageSpec resolves a Dockerfile build inside base: dockerfile and
// context (empty: the Dockerfile's folder) are relative to base and must stay
// inside it. The tag is bench/<name>:<12 hex>, where the hash covers the
// Dockerfile, every file of the context and whether the build has network.
func BuildImageSpec(base, dockerfile, context, name string, network bool) (LocalImage, error) {
	df, err := ResolveInside(base, dockerfile)
	if err != nil {
		return LocalImage{}, fmt.Errorf("dockerfile: %w", err)
	}
	ctxDir := filepath.Dir(df)
	if context != "" {
		if ctxDir, err = ResolveInside(base, context); err != nil {
			return LocalImage{}, fmt.Errorf("build context: %w", err)
		}
	}
	if st, err := os.Stat(ctxDir); err != nil || !st.IsDir() {
		return LocalImage{}, fmt.Errorf("build context %s is not a folder", ctxDir)
	}
	dfSum, err := SHA256File(df)
	if err != nil {
		return LocalImage{}, err
	}
	tree, err := TreeHash(ctxDir)
	if err != nil {
		return LocalImage{}, err
	}
	sum := SHA256Bytes([]byte(fmt.Sprintf("dockerfile %s\ncontext %s\nnetwork %t\n", dfSum, tree, network)))
	return LocalImage{Tag: "bench/" + ImageName(name) + ":" + sum[:12], Dockerfile: df, Context: ctxDir, Network: network}, nil
}

// ServiceImage is the image a run starts for a suite or task service: its
// prebuilt image, or its Dockerfile build tagged
// bench/svc-<suite>-<service>:<content hash>. base is the suite root for a
// suite service and the task folder for a task service; network says the
// build may use the network. The planner's image freeze, the environment's
// frozen-image check and the service launch all name a service's image
// through it, so they agree on the tag a run really starts (QC2-09-04).
func ServiceImage(spec schemas.ServiceSpec, base, suiteID string, network bool) (LocalImage, error) {
	if spec.Build == nil {
		return LocalImage{Tag: spec.Image}, nil
	}
	return BuildImageSpec(base, spec.Build.Dockerfile, spec.Build.Context, "svc-"+suiteID+"-"+spec.ID, network)
}

// ImageName turns any id into a docker repository path component: lower
// case, runs of other characters replaced by "-", no separator at either end.
// It is pure.
func ImageName(s string) string {
	s = imageNameRe.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "._-")
	if s == "" {
		return "x"
	}
	return s
}

// EnsureImage makes img present on the local docker and returns its image
// id. A present tag is used as is; a missing prebuilt tag is pulled; a
// missing build is built with --network none unless img.Network, labelled
// bifrost-bench=1 without a run label (it is shared between runs, so the
// janitor never removes it). Build and pull output goes to out.
func (d Docker) EnsureImage(ctx context.Context, img LocalImage, out io.Writer) (string, error) {
	if img.Tag == "" {
		return "", errors.New("ensure image: no tag")
	}
	mu, _ := buildLocks.LoadOrStore(img.Tag, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if id, ok, err := d.ImageID(ctx, img.Tag); err != nil || ok {
		return id, err
	}
	if out == nil {
		out = io.Discard
	}
	args := []string{"pull", img.Tag}
	if img.Dockerfile != "" {
		args = []string{"build", "-f", img.Dockerfile, "-t", img.Tag, "--label", LabelBench + "=1"}
		if !img.Network {
			args = append(args, "--network", "none")
		}
		args = append(args, img.Context)
	}
	if err := d.Stream(ctx, args, out); err != nil {
		return "", err
	}
	id, ok, err := d.ImageID(ctx, img.Tag)
	if err == nil && !ok {
		err = fmt.Errorf("image %s is still missing after docker %s", img.Tag, args[0])
	}
	return id, err
}

// ImageID returns the local image id of ref and whether the image exists.
func (d Docker) ImageID(ctx context.Context, ref string) (string, bool, error) {
	res, err := d.Run(ctx, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", false, fmt.Errorf("inspect image %s: %w", ref, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if strings.Contains(strings.ToLower(msg), "no such image") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect image %s: exit %d: %s", ref, res.ExitCode, msg)
	}
	return strings.TrimSpace(string(res.Stdout)), true, nil
}

// Stream runs `docker args...` with stdout and stderr going to out, with
// buildx's default provenance attestation off (an attestation records the
// build time, so it would give every rebuild of unchanged content a new id).
// A non-zero exit is an error carrying the tail of the output.
func (d Docker) Stream(ctx context.Context, args []string, out io.Writer) error {
	tail := &tailBuffer{max: 2048}
	cmd := d.command(ctx, args)
	cmd.Env = append(os.Environ(), "BUILDX_NO_DEFAULT_ATTESTATIONS=1")
	w := io.MultiWriter(out, tail)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w: %s", firstArg(args), err, strings.TrimSpace(tail.String()))
	}
	return nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf bytes.Buffer
	max int
}

// Write implements io.Writer.
func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if extra := t.buf.Len() - t.max; extra > 0 {
		t.buf.Next(extra)
	}
	return len(p), nil
}

// String returns what was kept.
func (t *tailBuffer) String() string { return t.buf.String() }
