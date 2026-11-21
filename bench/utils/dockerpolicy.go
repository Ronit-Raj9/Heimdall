package utils

// Docker policy (PROD2 N2, shared runner isolation): what a Docker value may
// start, pull and build when it acts for one organization on a shared host.
// Every docker CLI call of a Docker value with a policy goes through
// Policy.Rewrite: Run, RunEnv, MustRun, Stream, Pipe and Command. Without a
// policy (local mode, an org's own runner) nothing changes.
//
// The rules:
//
//   - every container (run, create) starts under Runtime (gVisor runsc on a
//     host that has it), so a shared host never runs org code under runc by
//     accident;
//   - a build whose Dockerfile or context lies outside the Trusted folders
//     (the runner's own checkout, the built-in library) is org-written: it
//     never runs on the host daemon. It runs on Builder, a separate BuildKit
//     container (`docker buildx build --builder <Builder> --load`) that holds
//     no host Docker socket, and its tags are namespaced under TagPrefix
//     (org-<id>/...). Without a Builder such a build is refused;
//   - a reference to an image this org built is rewritten to its namespaced
//     tag everywhere else (inspect, run, create, tag, rmi), so the org's
//     suite files keep their tags and no org can claim a global tag;
//   - an image in another org's namespace is refused outright;
//   - `docker pull` fetches only the org's approved images (digest pinned,
//     Assignment.Images) and the operator's platform images.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// OrgImagePrefix is the image namespace of one organization: org-<id>/.
// Org ids are [a-z0-9_]; "_" becomes "-" (docker allows only single
// separators), which stays unique because ids hold no "-".
func OrgImagePrefix(orgID string) string {
	return "org-" + strings.ReplaceAll(ImageName(orgID), "_", "-") + "/"
}

// ErrPolicy marks a docker call the policy refused.
var ErrPolicy = errors.New("refused on the shared worker")

// DockerPolicy confines one organization's docker calls on a shared host.
type DockerPolicy struct {
	// OrgID names the organization (messages only).
	OrgID string
	// Runtime is passed as --runtime to every run and create ("" keeps the
	// daemon's default).
	Runtime string
	// Builder is the buildx builder org-written images build on (a remote
	// BuildKit with no host socket). Empty refuses those builds.
	Builder string
	// TagPrefix namespaces the tags of org-written images (OrgImagePrefix).
	TagPrefix string
	// Trusted lists folders whose Dockerfiles may build on the host daemon:
	// the runner's own checkout and the built-in library. Never the org's
	// project.
	Trusted []string
	// Untrusted lists folders that are org content even inside a Trusted
	// one (the org's downloaded project).
	Untrusted []string
	// Approved lists the org's approved images (digest-pinned references).
	Approved []string
	// Platform lists images the operator allows every org to pull (exact
	// references, or prefixes ending in "/" or ":").
	Platform []string

	mu   sync.Mutex
	tags map[string]string // tag as the org wrote it -> namespaced tag
}

// SetApproved replaces the approved image list.
func (p *DockerPolicy) SetApproved(images []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Approved = append([]string(nil), images...)
}

// Namespaced returns the namespaced tag of an image the org built, and
// whether there is one.
func (p *DockerPolicy) Namespaced(tag string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.tags[tag]
	return v, ok
}

func (p *DockerPolicy) remember(orig, ns string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tags == nil {
		p.tags = map[string]string{}
	}
	p.tags[orig] = ns
	if !strings.Contains(lastPathPart(orig), ":") && !strings.Contains(orig, "@") {
		p.tags[orig+":latest"] = ns + ":latest"
	}
}

func lastPathPart(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// Rewrite returns the arguments the docker CLI really runs for args, or an
// error wrapping ErrPolicy when the call is refused. A nil policy returns
// args unchanged.
func (p *DockerPolicy) Rewrite(args []string) ([]string, error) {
	if p == nil || len(args) == 0 {
		return args, nil
	}
	sub, rest := args[0], args[1:]
	prefix := []string{sub}
	if (sub == "container" || sub == "image" || sub == "buildx") && len(rest) > 0 {
		prefix = []string{sub, rest[0]}
		sub, rest = sub+" "+rest[0], rest[1:]
	}
	switch sub {
	case "build", "image build", "buildx build":
		return p.rewriteBuild(rest)
	case "pull", "image pull":
		return p.rewritePull(prefix, rest)
	}
	out := append([]string(nil), prefix...)
	if (sub == "run" || sub == "create" || sub == "container run" || sub == "container create") && p.Runtime != "" && !hasFlag(rest, "--runtime") {
		out = append(out, "--runtime", p.Runtime)
	}
	for _, a := range rest {
		if err := p.foreign(a); err != nil {
			return nil, err
		}
		if ns, ok := p.Namespaced(a); ok {
			a = ns
		}
		out = append(out, a)
	}
	return out, nil
}

// foreign refuses an argument naming another org's image.
func (p *DockerPolicy) foreign(a string) error {
	if p.TagPrefix == "" || !strings.HasPrefix(a, "org-") {
		return nil
	}
	slash := strings.Index(a, "/")
	if slash < 0 || strings.ContainsAny(a[:slash], "=: ") {
		return nil
	}
	if !strings.HasPrefix(a, p.TagPrefix) {
		return fmt.Errorf("%w: image %s belongs to another organization", ErrPolicy, a)
	}
	return nil
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

func (p *DockerPolicy) rewritePull(prefix, rest []string) ([]string, error) {
	var ref string
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") {
			ref = a
		}
	}
	if ref == "" {
		return nil, fmt.Errorf("%w: docker pull without an image", ErrPolicy)
	}
	if !p.pullAllowed(ref) {
		return nil, fmt.Errorf("%w: image %s is not approved for this organization; request it under Settings > Images (pinned by digest) or ask the operator to add it to the platform images", ErrPolicy, ref)
	}
	return append(append([]string(nil), prefix...), rest...), nil
}

func (p *DockerPolicy) pullAllowed(ref string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.Approved {
		if a == ref {
			return true
		}
	}
	for _, a := range p.Platform {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if a == ref || ((strings.HasSuffix(a, "/") || strings.HasSuffix(a, ":")) && strings.HasPrefix(ref, a)) {
			return true
		}
	}
	return false
}

// buildValueFlags take a value as the next argument.
var buildValueFlags = map[string]bool{
	"-f": true, "--file": true, "-t": true, "--tag": true, "--build-arg": true, "--label": true,
	"--network": true, "--target": true, "--platform": true, "--progress": true,
	"--cache-from": true, "--cache-to": true, "--secret": true, "--ssh": true, "--allow": true,
	"--build-context": true, "--add-host": true, "-o": true, "--output": true, "--iidfile": true,
	"--metadata-file": true, "--shm-size": true, "--ulimit": true, "--cgroup-parent": true,
	"--builder": true, "--attest": true, "--provenance": true, "--sbom": true, "--annotation": true,
}

// orgBuildFlags are the flags an org-written build may use; anything else
// (secrets, ssh, host network, extra contexts, outputs) is refused.
var orgBuildFlags = map[string]bool{
	"-f": true, "--file": true, "-t": true, "--tag": true, "--build-arg": true, "--label": true,
	"--network": true, "--target": true, "--platform": true, "--progress": true,
	"--pull": true, "--no-cache": true, "-q": true, "--quiet": true,
}

type buildFlag struct{ name, value string }

func parseBuild(rest []string) (flags []buildFlag, positional []string, err error) {
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		if name, val, ok := strings.Cut(a, "="); ok {
			flags = append(flags, buildFlag{name, val})
			continue
		}
		if buildValueFlags[a] {
			if i+1 >= len(rest) {
				return nil, nil, fmt.Errorf("%w: docker build flag %s has no value", ErrPolicy, a)
			}
			flags = append(flags, buildFlag{a, rest[i+1]})
			i++
			continue
		}
		flags = append(flags, buildFlag{a, ""})
	}
	return flags, positional, nil
}

// inside reports whether path resolves inside one of roots.
func inside(path string, roots []string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = filepath.Clean(path)
	}
	real, _ = filepath.Abs(real)
	for _, r := range roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		rr, err := filepath.EvalSymlinks(r)
		if err != nil {
			rr = filepath.Clean(r)
		}
		rr, _ = filepath.Abs(rr)
		if real == rr || strings.HasPrefix(real, rr+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (p *DockerPolicy) rewriteBuild(rest []string) ([]string, error) {
	flags, pos, err := parseBuild(rest)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, fmt.Errorf("%w: docker build needs exactly one local context folder", ErrPolicy)
	}
	ctxDir := pos[0]
	if strings.Contains(ctxDir, "://") || strings.HasPrefix(ctxDir, "git@") {
		return nil, fmt.Errorf("%w: docker build from a remote context", ErrPolicy)
	}
	dockerfile := filepath.Join(ctxDir, "Dockerfile")
	for _, f := range flags {
		if f.name == "-f" || f.name == "--file" {
			dockerfile = f.value
		}
	}
	if inside(dockerfile, p.Trusted) && inside(ctxDir, p.Trusted) && !inside(dockerfile, p.Untrusted) && !inside(ctxDir, p.Untrusted) {
		// Platform content (the runner's checkout, the built-in library):
		// built on the host daemon as before.
		return append([]string{"build"}, rest...), nil
	}
	if p.Builder == "" {
		return nil, fmt.Errorf("%w: images from your own Dockerfiles need the isolated image builder, which this worker does not have; ask the operator to start it (HEIMDALL_SHARED_BUILDER) or request a prebuilt image pinned by digest under Settings > Images", ErrPolicy)
	}
	out := []string{"buildx", "build", "--builder", p.Builder, "--load"}
	for _, f := range flags {
		if !orgBuildFlags[f.name] {
			return nil, fmt.Errorf("%w: docker build flag %s is not allowed for organization images", ErrPolicy, f.name)
		}
		switch f.name {
		case "-t", "--tag":
			if err := p.foreign(f.value); err != nil {
				return nil, err
			}
			ns := f.value
			if !strings.HasPrefix(ns, p.TagPrefix) {
				ns = p.TagPrefix + f.value
				p.remember(f.value, ns)
			}
			out = append(out, "-t", ns)
			continue
		case "--network":
			if f.value != "none" && f.value != "default" {
				return nil, fmt.Errorf("%w: docker build --network %s is not allowed for organization images", ErrPolicy, f.value)
			}
		}
		if f.value == "" && !buildValueFlags[f.name] {
			out = append(out, f.name)
		} else {
			out = append(out, f.name, f.value)
		}
	}
	return append(out, ctxDir), nil
}

// command builds the exec.Cmd of `docker args...` under d's policy. A
// refused call yields a command whose Start returns the refusal.
func (d Docker) command(ctx context.Context, args []string) *exec.Cmd {
	rewritten, err := d.Policy.Rewrite(args)
	var cmd *exec.Cmd
	if ctx != nil {
		cmd = exec.CommandContext(ctx, d.bin(), rewritten...)
	} else {
		cmd = exec.Command(d.bin(), rewritten...)
	}
	if err != nil {
		cmd.Err = err
	}
	return cmd
}

// Command is exec.CommandContext for `docker args...` with d's policy
// applied (ctx may be nil for a command without one). Code that runs the
// docker CLI itself (streaming builds, observers) uses it instead of
// exec.Command so a shared runner's policy covers it.
func (d Docker) Command(ctx context.Context, args ...string) *exec.Cmd {
	return d.command(ctx, args)
}
