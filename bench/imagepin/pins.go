// Package imagepin is the library image registry (INFRA_I4 item 4): images
// pushed once to a registry and pinned by digest, so every worker on every
// platform runs the same bytes instead of building its own copy.
//
//   - Sync (sync.go) pushes a suite's images (base, helper, task images) to
//     the registry and writes the digest of each push to the suite's pins
//     file (PinsFile). `suite lock` reads that file and records each image as
//     `<tag>=<repo>@sha256:<hex>` (suite/fingerprint.go), the identity that
//     is the same on every machine.
//   - A worker that lacks a pinned image pulls it by digest and tags it
//     (utils.Docker.EnsureImage), and Prefetch (prefetch.go) pulls the
//     pinned images of an experiment's suite before its slots need them.
//   - The registry trusts short-lived pull tokens (token.go) that the
//     Heimdall server issues per org (handlers/registrytoken.go): every org
//     pulls the open library, an access file limits licence-restricted
//     suites to the orgs allowed to use them, and only the sync identity
//     pushes.
//   - Layers (layers.go) measures how much an image family shares, from the
//     registry's own manifests.
package imagepin

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PinsFile is the pins file in a suite folder. Its ".suite." prefix keeps it
// out of the suite's files fingerprint (suite/fingerprint.go
// expandSuiteRoot): the lock binds the digests themselves.
const PinsFile = ".suite.image-pins"

// Pins maps a local image tag (as the suite and its tasks name it) to the
// registry reference it was pushed as: <registry>/<repo>@sha256:<hex>.
type Pins map[string]string

// digestRe is a sha256 content digest.
var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var pinnedRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)

// ValidPinned reports whether ref is <repo>@sha256:<64 hex>.
func ValidPinned(ref string) bool { return pinnedRe.MatchString(ref) }

// PinsPath is the pins file of the suite folder root.
func PinsPath(root string) string { return filepath.Join(root, PinsFile) }

// ReadPins reads a pins file: one "<tag> <repo>@sha256:<hex>" per line, #
// comments and blank lines ignored, a later line for the same tag winning.
// A missing file is no pins and no error.
func ReadPins(path string) (Pins, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Pins{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("image pins: %w", err)
	}
	defer f.Close()
	out := Pins{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.Contains(fields[0], "@") || !ValidPinned(fields[1]) {
			return nil, fmt.Errorf("image pins %s line %d: want \"<tag> <repo>@sha256:<64 hex>\"", path, n)
		}
		out[fields[0]] = fields[1]
	}
	return out, sc.Err()
}

// WritePins writes p to path atomically, sorted by tag, with a header saying
// what wrote it.
func WritePins(path string, p Pins) error {
	tags := make([]string, 0, len(p))
	for t := range p {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	var b strings.Builder
	b.WriteString("# Library registry pins: the digest each image was pushed as.\n")
	b.WriteString("# Written by `bifrost-bench images sync`; `suite lock` records them.\n")
	for _, t := range tags {
		if !ValidPinned(p[t]) {
			return fmt.Errorf("image pins: %s: %q is not <repo>@sha256:<hex>", t, p[t])
		}
		fmt.Fprintf(&b, "%s %s\n", t, p[t])
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), PinsFile+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// DefaultNamespace is the registry path every library image lives under.
const DefaultNamespace = "library"

var repoPartRe = regexp.MustCompile(`[^a-z0-9._/-]+`)

// RepoFor maps a local tag of suite to the repository and tag it is pushed
// as: <namespace>/<suite>/<image name>:<tag>. The suite is the second path
// part, which is what the token issuer gates licence-restricted suites by.
// A registry host in the local tag is kept as a path part (ghcr.io/x/y
// becomes <namespace>/<suite>/ghcr.io/x/y); docker.io/ and library/ prefixes
// Docker adds implicitly are dropped.
func RepoFor(namespace, suite, tag string) (repo, ref string) {
	if namespace == "" {
		namespace = DefaultNamespace
	}
	name, ref := tag, "latest"
	if i := strings.LastIndex(tag, ":"); i > strings.LastIndex(tag, "/") {
		name, ref = tag[:i], tag[i+1:]
	}
	name = strings.TrimPrefix(name, "docker.io/")
	name = strings.TrimPrefix(name, "library/")
	name = strings.Trim(repoPartRe.ReplaceAllString(strings.ToLower(name), "-"), "/.-_")
	return namespace + "/" + suite + "/" + name, ref
}
