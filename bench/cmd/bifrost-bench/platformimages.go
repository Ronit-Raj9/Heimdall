package main

import "context"

// platformImagesReady reports whether org's sandbox platform name can pull
// Heimdall's images: an image registry is saved on its card (fix wave 3,
// N216). Lane H06 replaces it in the init() of platformregistry_hosted.go;
// until then no platform can, so its runs are refused before launch.
var platformImagesReady = func(ctx context.Context, env *hostedEnv, org, name string) bool { return false }
