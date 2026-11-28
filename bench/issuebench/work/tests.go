package work

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/builder"
	"github.com/maximhq/bifrost/bench/issuebench/validate"
)

// PatchFailed is the marker the test script prints when a patch does not
// apply (the same marker the server's local runner uses).
const PatchFailed = "HEIMDALL_PATCH_FAILED"

const maxImageRef = 128

var (
	tagRepoRE = regexp.MustCompile(`[^a-z0-9]+`)
	tagPartRE = regexp.MustCompile(`[^a-z0-9_.-]+`)
)

// EnvImageTag is the tag of the job's environment image:
// heimdall-issuebench/<repo>:<version>-<job id>, lower case, at most 128
// characters.
func EnvImageTag(req issuebench.WorkRequest) string {
	repo := strings.Trim(tagRepoRE.ReplaceAllString(strings.ToLower(req.Repo), "-"), "-")
	tag := tagPartRE.ReplaceAllString(strings.ToLower(req.Version)+"-"+strings.ToLower(req.JobID), "-")
	tag = strings.TrimLeft(tag, ".-")
	if tag == "" {
		tag = "job"
	}
	ref := "heimdall-issuebench/" + repo + ":" + tag
	if len(ref) > maxImageRef {
		ref = strings.TrimRight(ref[:maxImageRef], ".-")
	}
	return ref
}

// BuildEnvImage writes the environment's Dockerfile to scratch and builds
// the image with the checkout as the context. The Dockerfile is always a
// real file (never -f -) so a shared worker's policy can check it.
func BuildEnvImage(ctx context.Context, docker issuebench.DockerFunc, env issuebench.EnvSpec, repoDir, scratch, tag string) error {
	if strings.TrimSpace(env.Dockerfile) == "" {
		return errors.New("the environment has no Dockerfile")
	}
	if names, err := issuebench.WriteSupportFiles(repoDir, issuebench.LanguageFor(env.Language)); err != nil {
		return fmt.Errorf("the environment image did not build: %w", err)
	} else if len(names) > 0 {
		defer func() { _ = os.RemoveAll(filepath.Join(repoDir, issuebench.SupportFolder)) }()
	}
	path := filepath.Join(scratch, "Dockerfile.env")
	if err := os.WriteFile(path, []byte(env.Dockerfile), 0o600); err != nil {
		return fmt.Errorf("write the environment Dockerfile: %w", err)
	}
	if out, err := docker(ctx, nil, "build", "--quiet", "-t", tag, "-f", path, repoDir); err != nil {
		return fmt.Errorf("the environment image did not build: %s", tailText(out, err))
	}
	return nil
}

// RemoveImage removes the job's environment image, also after the job's
// context ended. A missing image is not an error.
func RemoveImage(ctx context.Context, docker issuebench.DockerFunc, tag string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	_, _ = docker(rctx, nil, "image", "rm", "-f", tag)
}

// TestRunner runs one test command in a fresh offline container of the
// job's environment image. It implements validate.Runner.
type TestRunner struct {
	Docker  issuebench.DockerFunc
	Image   string
	Runtime string // "" leaves the daemon default (the shared policy adds runsc)
	JobID   string
	Timeout time.Duration // 0: issuebench.DefaultTestTimeoutSeconds
}

var _ validate.Runner = TestRunner{}

// Run applies the patches the state needs and runs the command. A failing
// test command is a result, not an error.
func (r TestRunner) Run(ctx context.Context, env *issuebench.EnvSpec, c *issuebench.Candidate, run validate.Run) ([]byte, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = issuebench.DefaultTestTimeoutSeconds * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"run", "--rm", "-i", "--network", "none", "--memory", "4g", "--cpus", "2",
		"--pids-limit", "2048", "--security-opt", "no-new-privileges", "-w", builder.Workdir,
		"--label", issuebench.LabelWork + "=validate", "--label", issuebench.LabelJob + "=" + r.JobID}
	if r.Runtime != "" {
		args = append(args, "--runtime", r.Runtime)
	}
	args = append(args, r.Image, "bash", "-s")
	out, err := r.Docker(cctx, []byte(Script(c, run)), args...)
	switch {
	case bytes.Contains(out, []byte(PatchFailed)):
		return out, fmt.Errorf("%w: %s", validate.ErrPatchApply, tail(out))
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		return out, validate.ErrTimeout
	case err != nil && ctx.Err() != nil:
		return out, ctx.Err()
	case err != nil && len(out) == 0:
		return out, fmt.Errorf("docker run: %w", err)
	}
	return out, nil
}

// Script applies the patches the run's state needs, then runs the command.
// Patches travel base64 encoded inside the script on stdin.
func Script(c *issuebench.Candidate, run validate.Run) string {
	var b strings.Builder
	apply := func(name, patch string) {
		if strings.TrimSpace(patch) == "" {
			return
		}
		fmt.Fprintf(&b, "echo %s | base64 -d > /tmp/%s.patch\n", base64.StdEncoding.EncodeToString([]byte(patch)), name)
		fmt.Fprintf(&b, "git apply --whitespace=nowarn /tmp/%s.patch || { echo %s %s; exit 97; }\n", name, PatchFailed, name)
	}
	switch run.State {
	case validate.StateTests:
		apply("tests", c.TestPatch)
	case validate.StateGold:
		apply("tests", c.TestPatch)
		apply("fix", c.FixPatch)
	}
	b.WriteString(run.Command + "\n")
	return b.String()
}

// ValidateTests runs the candidate's tests at every state in the job's
// environment image. The model checks stay on the server, so Checks is zero.
func ValidateTests(ctx context.Context, r TestRunner, env issuebench.EnvSpec, c issuebench.Candidate) (issuebench.Validation, error) {
	v, err := (&validate.Validator{Runner: r}).Validate(ctx, &env, c, validate.Options{})
	if err != nil {
		return issuebench.Validation{}, err
	}
	return *v, nil
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 2000 {
		s = s[len(s)-2000:]
	}
	return s
}

func tailText(out []byte, err error) string {
	if s := tail(out); s != "" {
		return s
	}
	return err.Error()
}
