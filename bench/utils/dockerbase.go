package utils

// Base images of org-written builds on the shared worker (FIX_BRIEF
// decision 2, shared worker). The isolated BuildKit has no host Docker
// socket, so it cannot see images that exist only on the host, such as the
// bench's own base images (bench/coding-core-base:1) or the operator's
// platform images. The operator copies those into a private registry beside
// the builder (deploy/hosted/registry-sync.sh), and the policy points every
// such FROM at it with a named build context:
//
//	--build-context bench/coding-core-base:1=docker-image://registry:5000/bench/coding-core-base:1
//
// so an organization's Dockerfile stays unchanged. A base image missing from
// the registry fails the build before it starts, naming the image.

import (
	"bufio"
	"context"
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

// RegistryHas reports whether the registry at host (host:port, plain HTTP
// on the builder network) holds ref (name:tag, the tag defaults to latest).
func RegistryHas(ctx context.Context, client *http.Client, host, ref string) (bool, error) {
	name, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		name, tag = ref[:i], ref[i+1:]
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "http://"+host+"/v2/"+name+"/manifests/"+tag, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json", "application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
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
	}
	return false, fmt.Errorf("image registry %s: %s for %s", host, resp.Status, ref)
}

// baseContexts are the named build contexts that point an org build's
// bench and platform base images at the registry, or an error wrapping
// ErrPolicy that names a base image the registry lacks.
func (p *DockerPolicy) baseContexts(dockerfile string, buildArgs map[string]string) ([]string, error) {
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
		if p.RegistryHas != nil {
			ok, err := p.RegistryHas(ref)
			if err != nil {
				return nil, fmt.Errorf("%w: base image %s: %v", ErrPolicy, ref, err)
			}
			if !ok {
				return nil, fmt.Errorf("%w: base image %s is not in the shared worker's image registry; ask the operator to copy it there (deploy/hosted/registry-sync.sh %s)", ErrPolicy, ref, ref)
			}
		}
		out = append(out, "--build-context", ref+"=docker-image://"+p.Registry+"/"+ref)
	}
	return out, nil
}
