package utils

import (
	"context"
	"testing"
	"time"
)

// TestEnsureImageNeverPublishesOrgTag (SEC-C): an image built under an
// organization's tag prefix is never sent to the image publisher, so an
// organization's image can never reach the shared registry.
func TestEnsureImageNeverPublishesOrgTag(t *testing.T) {
	got := make(chan string, 4)
	for _, tag := range []string{"org-a/bench/task-s-t:0123456789ab", "org-a/python:3.12"} {
		d, _ := fakeImageDocker(t)
		d.Policy = &DockerPolicy{TagPrefix: "org-a/", Trusted: []string{"/t"},
			Publish: func(_ context.Context, ref string) (string, error) { got <- ref; return digestA, nil }}
		if _, err := d.EnsureImage(context.Background(), LocalImage{Tag: tag, Dockerfile: "/t/Dockerfile", Context: "/t"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case ref := <-got:
		t.Fatalf("published the org image %q", ref)
	case <-time.After(300 * time.Millisecond):
	}
}
