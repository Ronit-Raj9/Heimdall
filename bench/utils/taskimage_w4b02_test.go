package utils

import (
	"context"
	"testing"
	"time"
)

// F1.1 part C: a platform image the shared worker builds is published to
// its registry in the background; other images never are.
func TestEnsureImagePublishesPlatformBuild(t *testing.T) {
	d, _ := fakeImageDocker(t)
	got := make(chan string, 4)
	d.Policy = &DockerPolicy{TagPrefix: "org-a/", Trusted: []string{"/t"},
		Publish: func(_ context.Context, ref string) (string, error) { got <- ref; return digestA, nil }}
	img := LocalImage{Tag: "bench/task-s-t:0123456789ab", Dockerfile: "/t/Dockerfile", Context: "/t"}
	if _, err := d.EnsureImage(context.Background(), img, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ref := <-got:
		if ref != img.Tag {
			t.Fatalf("published %q", ref)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the built platform image was not published")
	}
	d2, _ := fakeImageDocker(t)
	d2.Policy = &DockerPolicy{TagPrefix: "org-a/", Platform: []string{"postgres:"},
		Publish: func(_ context.Context, ref string) (string, error) { got <- ref; return digestA, nil }}
	if _, err := d2.EnsureImage(context.Background(), LocalImage{Tag: "postgres:16"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ref := <-got:
		t.Fatalf("published a non-bench image %q", ref)
	case <-time.After(200 * time.Millisecond):
	}
}
