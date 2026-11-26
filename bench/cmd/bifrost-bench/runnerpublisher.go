package main

// The shared runner's image publisher client (wave 4, F1.1 part B): a
// missing platform base image is published on demand through the
// publisher service instead of refused. Off unless
// HEIMDALL_IMAGE_PUBLISHER_URL is set.

import (
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
