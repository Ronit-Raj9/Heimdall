package main

import "github.com/maximhq/bifrost/bench/utils"

// sharedImagePublisher returns the shared worker's image publisher (wave
// 4, F1.1). Package B1 replaces it in the init() of runnerpublisher.go;
// until then nil, and a missing base image is refused as before.
var sharedImagePublisher = func(h *sharedHost) utils.PublishFunc { return nil }
