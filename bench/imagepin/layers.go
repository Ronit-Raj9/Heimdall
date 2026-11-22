package imagepin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Layer is one layer blob of an image.
type Layer struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// ImageLayers is one pinned image and its layers (linux/amd64 for an index).
type ImageLayers struct {
	Tag    string  `json:"tag"`
	Pinned string  `json:"pinned"`
	Bytes  int64   `json:"bytes"`
	Layers []Layer `json:"layers"`
	Error  string  `json:"error,omitempty"`
}

// SharedLayer is a layer more than one image of the set uses.
type SharedLayer struct {
	Layer
	Images int `json:"images"`
}

// LayerReport is how much a set of images shares: what pulling every image
// on its own would move (TotalBytes) against the distinct blobs the
// registry stores and a worker pulls once (UniqueBytes). A family built on
// shared base and environment layers (SWE-style instance images) has a
// small UniqueBytes per extra image; a family built as independent images
// does not, which is the case for restructuring its Dockerfiles.
type LayerReport struct {
	Images       []ImageLayers `json:"images"`
	TotalBytes   int64         `json:"total_bytes"`
	UniqueBytes  int64         `json:"unique_bytes"`
	SharedBytes  int64         `json:"shared_bytes"` // bytes of the layers more than one image uses, counted once
	SharedLayers []SharedLayer `json:"shared_layers"`
}

// Layers reads the manifests of wants from the registry c points at (a
// want on another host is reported with an error) and sums their layers.
func Layers(ctx context.Context, c *Client, wants []Want) *LayerReport {
	rep := &LayerReport{}
	uses := map[string]*SharedLayer{}
	for _, w := range wants {
		il := ImageLayers{Tag: w.Tag, Pinned: w.Pinned}
		layers, err := c.imageLayers(ctx, w.Pinned)
		if err != nil {
			il.Error = err.Error()
		}
		seen := map[string]bool{}
		for _, l := range layers {
			il.Bytes += l.Size
			il.Layers = append(il.Layers, l)
			if seen[l.Digest] {
				continue
			}
			seen[l.Digest] = true
			if u := uses[l.Digest]; u != nil {
				u.Images++
			} else {
				uses[l.Digest] = &SharedLayer{Layer: l, Images: 1}
			}
		}
		rep.TotalBytes += il.Bytes
		rep.Images = append(rep.Images, il)
	}
	for _, u := range uses {
		rep.UniqueBytes += u.Size
		if u.Images > 1 {
			rep.SharedBytes += u.Size
			rep.SharedLayers = append(rep.SharedLayers, *u)
		}
	}
	sort.Slice(rep.SharedLayers, func(i, j int) bool {
		a, b := rep.SharedLayers[i], rep.SharedLayers[j]
		if a.Size*int64(a.Images) != b.Size*int64(b.Images) {
			return a.Size*int64(a.Images) > b.Size*int64(b.Images)
		}
		return a.Digest < b.Digest
	})
	return rep
}

type manifestDoc struct {
	MediaType string  `json:"mediaType"`
	Layers    []Layer `json:"layers"`
	Manifests []struct {
		Digest      string            `json:"digest"`
		MediaType   string            `json:"mediaType"`
		Annotations map[string]string `json:"annotations"`
		Platform    *struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
}

// imageLayers returns the layers of pinned (<host>/<repo>@<digest>); for an
// index, those of its linux/amd64 image (attestation manifests skipped).
func (c *Client) imageLayers(ctx context.Context, pinned string) ([]Layer, error) {
	host, rest, ok := strings.Cut(pinned, "/")
	if !ok || host != c.Host {
		return nil, fmt.Errorf("%s is not on registry %s", pinned, c.Host)
	}
	repo, digest, ok := strings.Cut(rest, "@")
	if !ok {
		return nil, fmt.Errorf("%s is not pinned by digest", pinned)
	}
	for depth := 0; depth < 2; depth++ {
		_, body, err := c.Manifest(ctx, repo, digest)
		if err != nil {
			return nil, err
		}
		var m manifestDoc
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("manifest of %s: %w", pinned, err)
		}
		if len(m.Manifests) == 0 {
			return m.Layers, nil
		}
		next := ""
		for _, d := range m.Manifests {
			if d.Annotations["vnd.docker.reference.type"] != "" || d.Platform == nil {
				continue
			}
			if d.Platform.OS == "linux" && d.Platform.Architecture == "amd64" {
				next = d.Digest
				break
			}
			if next == "" && d.Platform.OS != "unknown" {
				next = d.Digest
			}
		}
		if next == "" {
			return nil, fmt.Errorf("index %s has no image manifest", pinned)
		}
		digest = next
	}
	return nil, fmt.Errorf("index %s nests too deep", pinned)
}
