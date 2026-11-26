package imagepub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/utils"
)

// ErrNotPlatform marks a ref that is not a platform image, or an image on
// the host that was pulled from outside the platform. Its text is matched by
// utils (which cannot import this package): keep it exactly.
var ErrNotPlatform = errors.New("not a platform image")

// ErrMissing marks a platform image that is not on this host. Its text is
// matched by utils: keep it exactly.
var ErrMissing = errors.New("not on this host")

// Qualify checks that ref may be published from this host: it is a valid
// plain bench/ tag, it is in the platform set, the image is on the host, and
// every RepoDigests entry names either ref's own repository (a local build)
// or a repository matched by an entry of platform (the operator's
// HEIMDALL_SHARED_PLATFORM_IMAGES, exact or by a prefix ending in "/" or
// ":"). Any other repository means the image was pulled from outside the
// platform, for example by an organization that tagged its own image with a
// platform name. d must carry no organization policy.
func Qualify(ctx context.Context, d utils.Docker, ref string, set map[string]string, platform []string) error {
	if !ValidRef(ref) {
		return fmt.Errorf("%s: %w", ref, ErrNotPlatform)
	}
	if _, ok := set[ref]; !ok {
		return fmt.Errorf("%s: %w", ref, ErrNotPlatform)
	}
	res, err := d.Run(ctx, "image", "inspect", "--format", "{{json .RepoDigests}}", ref)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", ref, err)
	}
	if res.ExitCode != 0 {
		if strings.Contains(strings.ToLower(string(res.Stderr)), "no such image") {
			return fmt.Errorf("%s: %w", ref, ErrMissing)
		}
		return fmt.Errorf("inspect %s: %s", ref, strings.TrimSpace(string(res.Stderr)))
	}
	var digests []string
	if err := json.Unmarshal(bytes.TrimSpace(res.Stdout), &digests); err != nil {
		return fmt.Errorf("inspect %s: %w", ref, err)
	}
	if repo := OutsideRepo(ref, digests, platform); repo != "" {
		return fmt.Errorf("%s was pulled from %s: %w", ref, repo, ErrNotPlatform)
	}
	return nil
}

// OutsideRepo returns the first RepoDigests repository of ref that is
// neither ref's own repository nor matched by platform, or "" when there is
// none.
func OutsideRepo(ref string, repoDigests, platform []string) string {
	own := ref[:strings.LastIndex(ref, ":")]
	for _, rd := range repoDigests {
		repo, _, ok := strings.Cut(rd, "@")
		if !ok || repo == "" {
			return rd
		}
		if repo == own || platformMatch(repo, platform) {
			continue
		}
		return repo
	}
	return ""
}

// platformMatch is the exact-or-prefix rule of the policy's pullAllowed
// (utils/dockerpolicy.go): an entry equal to repo, or an entry ending in
// "/" or ":" that repo starts with.
func platformMatch(repo string, platform []string) bool {
	for _, a := range platform {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if a == repo || ((strings.HasSuffix(a, "/") || strings.HasSuffix(a, ":")) && strings.HasPrefix(repo, a)) {
			return true
		}
	}
	return false
}
