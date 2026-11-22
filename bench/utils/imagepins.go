package utils

// Library registry (INFRA_I4 item 4, bench/imagepin): a suite lock that
// records an image as `<tag>=<repo>@sha256:<hex>` pins it by registry
// digest. A worker that lacks the image pulls exactly that digest and tags
// it, instead of building its own copy (which would get another image id).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

var lockedPinRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)

// LockedPin splits a lock image identity of the registry digest form,
// "<tag>=<repo>@sha256:<hex>", into the tag and the pinned reference. Any
// other form (tag only, a local id) reports false.
func LockedPin(identity string) (tag, pinned string, ok bool) {
	tag, pinned, ok = strings.Cut(identity, "=")
	if !ok || tag == "" || !lockedPinRe.MatchString(pinned) {
		return "", "", false
	}
	return tag, pinned, true
}

// SuiteLockedPin returns the registry reference the suite lock pins for an
// image role: the suite's own roles (base, helper) when taskID is empty,
// else the task's (task, compose services). Empty when the lock records the
// role by tag only, or records another tag than want.
func SuiteLockedPin(s *schemas.Suite, taskID, role, want string) string {
	if s == nil || s.Lock == nil || s.Lock.Fingerprints == nil {
		return ""
	}
	fp := s.Lock.Fingerprints.Suite
	if taskID != "" {
		var ok bool
		if fp, ok = s.Lock.Fingerprints.Tasks[taskID]; !ok {
			return ""
		}
	}
	tag, pinned, ok := LockedPin(fp.Images[role])
	if !ok || (want != "" && tag != want) {
		return ""
	}
	return pinned
}

// RepoDigests returns the registry digests (repo@sha256:...) docker reports
// for ref, and whether the image exists.
func (d Docker) RepoDigests(ctx context.Context, ref string) ([]string, bool, error) {
	res, err := d.Run(ctx, "image", "inspect", "--format", "{{json .RepoDigests}}", ref)
	if err != nil {
		return nil, false, fmt.Errorf("inspect image %s: %w", ref, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if strings.Contains(strings.ToLower(msg), "no such image") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("inspect image %s: exit %d: %s", ref, res.ExitCode, msg)
	}
	var out []string
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return nil, true, fmt.Errorf("inspect image %s: %w", ref, err)
	}
	return out, true, nil
}

// HasPinned reports whether the local image tag is the pinned digest:
// pinned is among its registry digests, or (Docker's containerd store,
// where an image id is its manifest or index digest) its id is the pinned
// digest.
func (d Docker) HasPinned(ctx context.Context, tag, pinned string) (present, same bool, err error) {
	digests, ok, err := d.RepoDigests(ctx, tag)
	if err != nil || !ok {
		return ok, false, err
	}
	if slices.Contains(digests, pinned) {
		return true, true, nil
	}
	id, ok, err := d.ImageID(ctx, tag)
	if err != nil || !ok {
		return ok, false, err
	}
	_, digest, _ := strings.Cut(pinned, "@")
	return true, id == digest, nil
}

// PullPinned pulls pinned (repo@sha256:...) and tags it as tag, with env
// layered over the docker CLI's environment (DOCKER_CONFIG for a private
// credential folder). Docker's own output goes to out.
func (d Docker) PullPinned(ctx context.Context, env []string, tag, pinned string, out io.Writer) error {
	if !lockedPinRe.MatchString(pinned) {
		return fmt.Errorf("pull %s: not <repo>@sha256:<hex>", pinned)
	}
	if out == nil {
		out = io.Discard
	}
	res, err := d.RunEnv(ctx, env, "pull", "-q", pinned)
	if err != nil {
		return fmt.Errorf("docker pull %s: %w", pinned, err)
	}
	out.Write(res.Stdout)
	if res.ExitCode != 0 {
		return fmt.Errorf("docker pull %s (pinned by the suite lock): %s; log this worker in to the library registry (bifrost-bench images login) or ask the operator to sync it (bifrost-bench images sync)", pinned, strings.TrimSpace(string(res.Stderr)))
	}
	res, err = d.Run(ctx, "tag", pinned, tag)
	if err != nil {
		return fmt.Errorf("docker tag %s %s: %w", pinned, tag, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("docker tag %s %s: %s", pinned, tag, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}
