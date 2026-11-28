package main

// The shared runner's image publisher client (wave 4, F1.1 part B): a
// missing platform base image is published on demand through the
// publisher service instead of refused. Off unless
// HEIMDALL_IMAGE_PUBLISHER_URL is set.

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/imagepub"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	imagePublisherURLEnv   = "HEIMDALL_IMAGE_PUBLISHER_URL"
	imagePublisherTokenEnv = "HEIMDALL_IMAGE_PUBLISHER_TOKEN"
)

// publisherClients holds one client per shared host, so concurrent leases
// share its coalescing of publishes.
var publisherClients sync.Map // *sharedHost -> *imagepub.Client

func init() {
	sharedImagePublisher = func(h *sharedHost) utils.PublishFunc {
		url := strings.TrimSpace(os.Getenv(imagePublisherURLEnv))
		if url == "" || h == nil || h.base == nil {
			return nil
		}
		if c, ok := publisherClients.Load(h); ok {
			return c.(*imagepub.Client).Ensure
		}
		root := ""
		if h.base.cfg != nil {
			root = h.base.cfg.Root
		}
		c := &imagepub.Client{
			URL:       url,
			TokenFile: os.Getenv(imagePublisherTokenEnv),
			// No policy: the client inspects and saves platform images on
			// the host itself, never on an organization's behalf.
			Docker:   utils.Docker{Binary: h.base.docker.Binary},
			Manifest: h.manifest,
			Set:      cachedPlatformImages(root, suite.LibraryDir()),
			Platform: h.platform,
		}
		actual, _ := publisherClients.LoadOrStore(h, c)
		return actual.(*imagepub.Client).Ensure
	}
}

// sharedOwnPublisher returns the publisher's copy of an org's own image to
// the registry (its suite base image, for an agent built on it), or nil
// without a publisher.
func sharedOwnPublisher(h *sharedHost) utils.PublishOwnFunc {
	if sharedImagePublisher(h) == nil {
		return nil
	}
	c, ok := publisherClients.Load(h)
	if !ok {
		return nil
	}
	return c.(*imagepub.Client).PublishOwn
}

// hostOwnImage returns the host lookup of an org's own image: only a ref in
// its namespace prefix is inspected, on the host daemon without a policy.
func hostOwnImage(bin, prefix string) utils.OwnImageFunc {
	return func(ctx context.Context, ref string) (string, bool, error) {
		if prefix == "" || !strings.HasPrefix(ref, prefix) {
			return "", false, nil
		}
		id, ok, err := utils.Docker{Binary: bin}.ImageID(ctx, ref)
		return id, ok, err
	}
}
