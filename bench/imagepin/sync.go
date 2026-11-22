package imagepin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// SuiteImages lists the image tags of a suite the library registry holds:
// the base image, the helper (the project's images.helper) and every
// task's own image, each once, sorted.
func SuiteImages(s *schemas.Suite, helper string) []string {
	seen := map[string]bool{}
	add := func(t string) {
		if t != "" {
			seen[t] = true
		}
	}
	add(s.BaseImage.Tag)
	add(helper)
	for _, t := range s.Tasks {
		if img, ok, err := utils.TaskImage(s.ID, t); err == nil && ok {
			add(img.Tag)
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Credentials of a registry identity.
type Credentials struct{ User, Password string }

// ReadCredentials reads a "user:password" file (one line).
func ReadCredentials(path string) (Credentials, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, fmt.Errorf("registry credentials: %w", err)
	}
	u, p, ok := strings.Cut(strings.TrimSpace(string(raw)), ":")
	if !ok || u == "" || p == "" {
		return Credentials{}, fmt.Errorf("registry credentials %s: want one line user:password", path)
	}
	return Credentials{User: u, Password: p}, nil
}

// DockerConfig writes a private docker client config folder holding only
// the credentials for host, so a push or pull never reads or changes the
// operator's own ~/.docker. It returns the DOCKER_CONFIG env entry and a
// cleanup that removes the folder.
func DockerConfig(host string, c Credentials) ([]string, func(), error) {
	dir, err := os.MkdirTemp("", "heimdall-registry-*")
	if err != nil {
		return nil, nil, err
	}
	done := func() { os.RemoveAll(dir) }
	cfg := map[string]any{"auths": map[string]any{host: map[string]string{
		"auth": base64.StdEncoding.EncodeToString([]byte(c.User + ":" + c.Password)),
	}}}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		done()
		return nil, nil, err
	}
	return []string{"DOCKER_CONFIG=" + dir}, done, nil
}

// SyncOptions configure Sync.
type SyncOptions struct {
	Registry  string // host[:port] of the library registry
	Namespace string // default DefaultNamespace
	Suite     string
	Tags      []string // local image tags to push (SuiteImages)
	Push      Credentials
	PlainHTTP bool // the registry speaks plain HTTP (a local registry)
	Docker    utils.Docker
	Log       io.Writer
}

// SyncResult is one image of a sync.
type SyncResult struct {
	Tag    string `json:"tag"`
	Pinned string `json:"pinned,omitempty"` // <registry>/<repo>@sha256:<hex>
	Error  string `json:"error,omitempty"`
	// Missing: the image is not on this machine; build or pull it first.
	Missing bool `json:"missing,omitempty"`
}

// Sync pushes every tag to the registry under Namespace/Suite and returns
// the digest each push produced. Docker runs with a private client config
// holding only the push credentials. A tag the machine does not have is
// reported missing, never built here: CI or the operator builds the
// library images, the registry holds them.
func Sync(ctx context.Context, o SyncOptions) ([]SyncResult, error) {
	if o.Registry == "" || o.Suite == "" {
		return nil, errors.New("sync: registry and suite are required")
	}
	log := o.Log
	if log == nil {
		log = io.Discard
	}
	env, done, err := DockerConfig(o.Registry, o.Push)
	if err != nil {
		return nil, err
	}
	defer done()
	cl := &Client{Host: o.Registry, PlainHTTP: o.PlainHTTP, User: o.Push.User, Password: o.Push.Password}
	var out []SyncResult
	for _, tag := range o.Tags {
		r := SyncResult{Tag: tag}
		if _, ok, err := o.Docker.ImageID(ctx, tag); err != nil {
			r.Error = err.Error()
			out = append(out, r)
			continue
		} else if !ok {
			r.Missing = true
			fmt.Fprintf(log, "  skip  %s: not on this machine; build or pull it first\n", tag)
			out = append(out, r)
			continue
		}
		repo, ref := RepoFor(o.Namespace, o.Suite, tag)
		remote := o.Registry + "/" + repo + ":" + ref
		pinned, err := pushOne(ctx, o.Docker, env, cl, tag, remote, repo, ref)
		if err != nil {
			r.Error = err.Error()
			fmt.Fprintf(log, "  FAIL  %s: %v\n", tag, err)
		} else {
			r.Pinned = pinned
			fmt.Fprintf(log, "  ok    %s -> %s\n", tag, pinned)
		}
		out = append(out, r)
	}
	return out, nil
}

func pushOne(ctx context.Context, d utils.Docker, env []string, cl *Client, tag, remote, repo, ref string) (string, error) {
	if res, err := d.Run(ctx, "tag", tag, remote); err != nil {
		return "", err
	} else if res.ExitCode != 0 {
		return "", fmt.Errorf("docker tag: %s", strings.TrimSpace(string(res.Stderr)))
	}
	// The registry tag stays: docker lists a registry digest for an image
	// only while a reference of that repository exists, and the lock check
	// on this machine compares the pin against those digests.
	res, err := d.RunEnv(ctx, env, "push", "-q", remote)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("docker push: %s", strings.TrimSpace(string(res.Stderr)))
	}
	digest, err := cl.Digest(ctx, repo, ref)
	if err != nil {
		return "", err
	}
	pinned := cl.Host + "/" + repo + "@" + digest
	// The local image must be what the registry now holds: docker lists the
	// pushed digest among the image's registry digests.
	if present, same, err := d.HasPinned(ctx, tag, pinned); err != nil {
		return "", err
	} else if !present || !same {
		return "", fmt.Errorf("the registry holds %s, which docker does not report for the local %s (was it rebuilt during the push?)", digest, tag)
	}
	return pinned, nil
}

// MergePins records the pushed digests of results into the pins file at
// path, keeping pins of other tags. It returns how many it recorded.
func MergePins(path string, results []SyncResult) (int, error) {
	p, err := ReadPins(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range results {
		if r.Pinned != "" {
			p[r.Tag] = r.Pinned
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, WritePins(path, p)
}
