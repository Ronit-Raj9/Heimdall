package k8s

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// A kubelet reports a container's image by its config digest
// (sha256:<config>) when the image has no registry digest on the node, which
// is how `kind load` leaves a locally built image. Docker with the
// containerd image store gives the image its index digest as its id, which
// is what an experiment freezes. configDigest reads the config digest of a
// local image for one platform from `docker save`, so the two can be
// compared: the node runs the frozen image when the local image still has
// the frozen id and its config digest is the one the node reports.

var configDigests sync.Map // image id + "|" + platform -> digest

// configDigest returns the config digest of ref for platform (os/arch). id
// is the immutable image id ref named when it was checked; the cache is keyed
// by it, not by the mutable tag, so a retagged ref never reuses a stale
// digest.
func (d *Driver) configDigest(ctx context.Context, ref, id, platform string) (string, error) {
	key := id + "|" + platform
	if v, ok := configDigests.Load(key); ok {
		return v.(string), nil
	}
	cmd := exec.CommandContext(ctx, dockerBin(d.cfg.Docker), "save", "--platform", platform, ref)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	digest, perr := configFromSave(out)
	_, _ = io.Copy(io.Discard, out)
	werr := cmd.Wait()
	if perr != nil {
		return "", perr
	}
	if werr != nil {
		return "", fmt.Errorf("docker save %s: %v: %s", ref, werr, strings.TrimSpace(stderr.String()))
	}
	configDigests.Store(key, digest)
	return digest, nil
}

// configFromSave reads an OCI image tar (docker save) and returns the config
// digest of the manifest its index names, following one nested index. Only
// small JSON members are kept. It is pure apart from reading r.
func configFromSave(r io.Reader) (string, error) {
	tr := tar.NewReader(r)
	blobs := map[string][]byte{}
	var index []byte
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read docker save: %w", err)
		}
		if h.Size > 1<<20 {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return "", err
		}
		switch {
		case h.Name == "index.json":
			index = b
		case strings.HasPrefix(h.Name, "blobs/sha256/"):
			blobs["sha256:"+strings.TrimPrefix(h.Name, "blobs/sha256/")] = b
		}
	}
	if index == nil {
		return "", errors.New("docker save output has no index.json")
	}
	type desc struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
	}
	var doc struct {
		Manifests []desc `json:"manifests"`
		Config    desc   `json:"config"`
	}
	raw := index
	for depth := 0; depth < 4; depth++ {
		doc.Manifests, doc.Config = nil, desc{}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return "", fmt.Errorf("parse image index: %w", err)
		}
		if doc.Config.Digest != "" {
			return doc.Config.Digest, nil
		}
		if len(doc.Manifests) == 0 {
			return "", errors.New("image index lists no manifest")
		}
		next, ok := blobs[doc.Manifests[0].Digest]
		if !ok {
			return "", fmt.Errorf("docker save output lacks %s", doc.Manifests[0].Digest)
		}
		raw = next
	}
	return "", errors.New("image index nests too deep")
}
