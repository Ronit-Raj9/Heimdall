package utils

import "context"

// PublishFunc publishes a platform image (ref, a bench/ tag on the shared
// host) to the shared worker's registry and returns its digest (wave 4,
// F1.1). Set on DockerPolicy.Publish by the shared runner.
type PublishFunc func(ctx context.Context, ref string) (digest string, err error)

type imageProgressKey struct{}

// WithImageProgress returns ctx carrying f, which receives one plain line
// each time a build waits for an image to be prepared.
func WithImageProgress(ctx context.Context, f func(msg string)) context.Context {
	return context.WithValue(ctx, imageProgressKey{}, f)
}

// ImageProgress returns the progress function of ctx, or one that does
// nothing.
func ImageProgress(ctx context.Context) func(msg string) {
	if f, ok := ctx.Value(imageProgressKey{}).(func(msg string)); ok && f != nil {
		return f
	}
	return func(string) {}
}

// OwnImageFunc returns the host image ID of ref, an image in the org's own
// namespace (org-<id>/...) on the shared host, and whether it exists. The
// shared runner inspects the host without a policy.
type OwnImageFunc func(ctx context.Context, ref string) (id string, ok bool, err error)

// PublishOwnFunc copies the org's own host image hostRef to the shared
// worker's registry as pushRef (in the same org namespace) and returns the
// pushed digest.
type PublishOwnFunc func(ctx context.Context, hostRef, pushRef string) (digest string, err error)
