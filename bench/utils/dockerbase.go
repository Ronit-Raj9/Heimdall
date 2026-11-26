package utils

// Base images of org-written builds on the shared worker (FIX_BRIEF
// decision 2, shared worker). The isolated BuildKit has no host Docker
// socket, so it cannot see images that exist only on the host, such as the
// bench's own base images (bench/coding-core-base:1) or the operator's
// platform images. The operator copies those into a private registry beside
// the builder (deploy/hosted/registry-sync.sh), and the policy points every
// such FROM at it with a named build context, pinned by the digest the sync
// recorded in the registry manifest (G3 B2):
//
//	--build-context bench/coding-core-base:1=docker-image://registry:5000/bench/coding-core-base:1@sha256:<digest>
//
// so an organization's Dockerfile stays unchanged, and a tag moved in the
// registry (by anyone) cannot change what another org's build starts from.
// A base image missing from the manifest or the registry fails the build
// before it starts, naming the image.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// LocalBaseImagePrefix starts the images the bench builds on the host
// (bench/coding-core-base:1, bench/agent-*): always served from the
// registry to org builds.
const LocalBaseImagePrefix = "bench/"

var argRef = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// DockerfileBases lists the base images of a Dockerfile's FROM lines, in
// order and without repeats: ARG defaults before the first FROM and
// buildArgs (name to value, from --build-arg) are substituted; stage names,
// scratch and references that still hold a variable are left out.
func DockerfileBases(dockerfile string, buildArgs map[string]string) ([]string, error) {
	f, err := os.Open(dockerfile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	args := map[string]string{}
	stages := map[string]bool{}
	seen := map[string]bool{}
	var out []string
	sawFrom := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var line string
	for sc.Scan() {
		part := strings.TrimSpace(sc.Text())
		if strings.HasSuffix(part, "\\") {
			line += strings.TrimSuffix(part, "\\") + " "
			continue
		}
		line += part
		fields := strings.Fields(line)
		line = ""
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "ARG":
			if sawFrom || len(fields) < 2 {
				continue
			}
			name, val, _ := strings.Cut(fields[1], "=")
			args[name] = strings.Trim(val, `"'`)
		case "FROM":
			sawFrom = true
			rest := fields[1:]
			for len(rest) > 0 && strings.HasPrefix(rest[0], "--") {
				rest = rest[1:]
			}
			if len(rest) == 0 {
				continue
			}
			ref := argRef.ReplaceAllStringFunc(rest[0], func(m string) string {
				name := argRef.FindStringSubmatch(m)[1]
				if v, ok := buildArgs[name]; ok {
					return v
				}
				if v, ok := args[name]; ok {
					return v
				}
				return m
			})
			stage := ""
			if len(rest) >= 3 && strings.EqualFold(rest[1], "AS") {
				stage = strings.ToLower(rest[2])
			}
			skip := ref == "scratch" || strings.Contains(ref, "$") || stages[strings.ToLower(ref)] || seen[ref]
			if stage != "" {
				stages[stage] = true
			}
			if skip {
				continue
			}
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out, sc.Err()
}

// hasRegistryHost reports whether ref names a registry host (a first path
// part with a dot or a port, or localhost).
func hasRegistryHost(ref string) bool {
	first, _, ok := strings.Cut(ref, "/")
	return ok && (strings.ContainsAny(first, ".:") || first == "localhost")
}

// fromRegistry reports whether an org build takes base image ref from the
// shared worker's registry: a bench image, or a platform image of the
// operator (exact, or a prefix ending in "/" or ":").
func (p *DockerPolicy) fromRegistry(ref string) bool {
	if p.Registry == "" || hasRegistryHost(ref) || strings.Contains(ref, "@") {
		return false
	}
	if strings.HasPrefix(ref, LocalBaseImagePrefix) {
		return true
	}
	for _, a := range p.Platform {
		a = strings.TrimSpace(a)
		if a != "" && (a == ref || ((strings.HasSuffix(a, "/") || strings.HasSuffix(a, ":")) && strings.HasPrefix(ref, a))) {
			return true
		}
	}
	return false
}

// RegistryCredentials are the read-only pull credentials of the shared
// worker's registry (deploy/hosted/registry-sync.sh writes them).
type RegistryCredentials struct {
	User, Password string
}

// ReadRegistryCredentials reads a "user:password" file (one line).
func ReadRegistryCredentials(path string) (*RegistryCredentials, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("registry credentials: %w", err)
	}
	user, pw, ok := strings.Cut(strings.TrimSpace(string(raw)), ":")
	if !ok || user == "" || pw == "" {
		return nil, fmt.Errorf("registry credentials %s: want one line user:password", path)
	}
	return &RegistryCredentials{User: user, Password: pw}, nil
}

// splitRef splits name:tag, name@digest or name:tag@digest into the
// repository name and the manifest reference (digest first, then tag, then
// latest).
func splitRef(ref string) (name, reference string) {
	if n, d, ok := strings.Cut(ref, "@"); ok {
		name, reference = n, d
	} else {
		name, reference = ref, "latest"
		if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
			name, reference = ref[:i], ref[i+1:]
		}
		return name, reference
	}
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i]
	}
	return name, reference
}

// RegistryHas reports whether the registry at host (host:port, plain HTTP
// on the build network) holds ref (name:tag, name@digest or
// name:tag@digest; the tag defaults to latest). cred, when set, is sent as
// basic authentication.
func RegistryHas(ctx context.Context, client *http.Client, host, ref string, cred *RegistryCredentials) (bool, error) {
	name, reference := splitRef(ref)
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "http://"+host+"/v2/"+name+"/manifests/"+reference, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json", "application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	if cred != nil {
		req.SetBasicAuth(cred.User, cred.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("image registry %s: %w", host, err)
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, fmt.Errorf("image registry %s: %s for %s: the worker's pull credentials were refused (HEIMDALL_SHARED_REGISTRY_AUTH; registry-sync.sh writes them)", host, resp.Status, ref)
	}
	return false, fmt.Errorf("image registry %s: %s for %s", host, resp.Status, ref)
}

// digestRe is a sha256 image digest.
var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ErrNotPinned marks a base image the registry manifest has no digest for.
var ErrNotPinned = errors.New("not in the registry manifest")

// ReadRegistryManifest reads the registry manifest registry-sync.sh writes:
// one "<ref> <sha256 digest>" per line, # comments and blank lines ignored,
// a later line for the same ref winning. It maps each ref (as org
// Dockerfiles write it) to the digest the sync pushed.
func ReadRegistryManifest(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("registry manifest: %w", err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !digestRe.MatchString(fields[1]) || strings.Contains(fields[0], "@") {
			return nil, fmt.Errorf("registry manifest %s line %d: want \"<image> sha256:<64 hex>\"", path, n)
		}
		out[fields[0]] = fields[1]
	}
	return out, sc.Err()
}

// ManifestPins returns a DockerPolicy.Pinned function over the manifest at
// path, read again on every call so a sync takes effect without a restart.
func ManifestPins(path string) func(ref string) (string, error) {
	return func(ref string) (string, error) {
		m, err := ReadRegistryManifest(path)
		if err != nil {
			return "", err
		}
		d, ok := m[ref]
		if !ok {
			return "", ErrNotPinned
		}
		return d, nil
	}
}

// baseContexts are the named build contexts that point an org build's
// bench and platform base images at the registry, each pinned by the
// digest of the registry manifest, or an error wrapping ErrPolicy that
// names a base image the manifest or the registry lacks.
func (p *DockerPolicy) baseContexts(ctx context.Context, dockerfile string, buildArgs map[string]string) ([]string, error) {
	if p.Registry == "" {
		return nil, nil
	}
	bases, err := DockerfileBases(dockerfile, buildArgs)
	if err != nil {
		return nil, fmt.Errorf("%w: read Dockerfile %s: %v", ErrPolicy, dockerfile, err)
	}
	var out []string
	for _, ref := range bases {
		if !p.fromRegistry(ref) {
			continue
		}
		if p.Pinned == nil {
			return nil, &ImageNotReady{Ref: ref, Reason: ErrNotPinned}
		}
		digest, err := p.Pinned(ref)
		missing := errors.Is(err, ErrNotPinned)
		if err != nil && !missing {
			return nil, fmt.Errorf("%w: base image %s: %v", ErrPolicy, ref, err)
		}
		if !missing && p.RegistryHas != nil {
			ok, err := p.RegistryHas(ref + "@" + digest)
			if err != nil {
				return nil, fmt.Errorf("%w: base image %s: %v", ErrPolicy, ref, err)
			}
			missing = !ok
		}
		if missing {
			if p.Publish == nil {
				return nil, &ImageNotReady{Ref: ref, Reason: ErrNotPinned}
			}
			ImageProgress(ctx)("Preparing the container image " + ref)
			d, err := p.Publish(ctx, ref)
			if err != nil {
				return nil, &ImageNotReady{Ref: ref, Plain: publishReason(err), Reason: err}
			}
			digest = d
		}
		pinned := ref + "@" + digest
		out = append(out, "--build-context", ref+"=docker-image://"+p.Registry+"/"+pinned)
	}
	return out, nil
}

// ImageNotReady is the refusal of an org build whose base image is not in
// the shared worker's registry and could not be published there. It wraps
// ErrPolicy, so the lease treats it as a permanent refusal, while its text
// stays plain and never shows the internal image tag (wave 4, F1.1).
type ImageNotReady struct {
	Ref    string
	Plain  string // the plain reason the publish failed; empty without a publisher
	Reason error
}

func (e *ImageNotReady) Error() string {
	if errors.Is(e.Reason, ErrNotPinned) && e.Plain == "" {
		return "The container image this run starts from is not in the shared worker's image store yet. Tell the server admin to copy it there."
	}
	return "The container image this run starts from is not ready on the shared worker, and Heimdall could not prepare it: " + e.Plain + ". Try again in a few minutes. If it keeps failing, tell the server admin."
}

func (e *ImageNotReady) Unwrap() []error { return []error{ErrPolicy, e.Reason} }

// publishReason maps a publish error to its plain reason. utils cannot
// import imagepub, so it matches the texts of imagepub.ErrNotPlatform and
// imagepub.ErrMissing.
func publishReason(err error) string {
	switch msg := err.Error(); {
	case strings.Contains(msg, "not a platform image"):
		return "it is not an image the platform built"
	case strings.Contains(msg, "not on this host"):
		return "it is not on the shared worker"
	}
	return "the image store did not answer"
}
