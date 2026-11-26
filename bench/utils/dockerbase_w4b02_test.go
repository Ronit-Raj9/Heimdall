package utils

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// F1.1 part B: a base image missing from the registry is published on
// demand, with a progress line, and the build uses the published digest.
func TestBaseContextsPublishesMissingBase(t *testing.T) {
	org := writeDockerfile(t, "FROM bench/task-a:1\n")
	var published []string
	p := &DockerPolicy{
		TagPrefix: "org-a/", Builder: "heimdall-org-builder", Registry: "registry:5000",
		Pinned: func(string) (string, error) { return "", ErrNotPinned },
		Publish: func(_ context.Context, ref string) (string, error) {
			published = append(published, ref)
			return digestA, nil
		},
	}
	var progress []string
	ctx := WithImageProgress(context.Background(), func(msg string) { progress = append(progress, msg) })
	out, err := p.RewriteContext(ctx, []string{"build", "-t", "x:1", org})
	if err != nil {
		t.Fatal(err)
	}
	want := "--build-context bench/task-a:1=docker-image://registry:5000/bench/task-a:1@" + digestA
	if !strings.Contains(strings.Join(out, " "), want) {
		t.Fatalf("missing %q in %v", want, out)
	}
	if strings.Join(published, ",") != "bench/task-a:1" {
		t.Fatalf("published %v", published)
	}
	if strings.Join(progress, ",") != "Preparing the container image bench/task-a:1" {
		t.Fatalf("progress %v", progress)
	}

	// A digest the registry lost is published again too.
	published = nil
	p.Pinned = func(string) (string, error) { return digestB, nil }
	p.RegistryHas = func(ref string) (bool, error) { return ref == "bench/task-a:1@"+digestA, nil }
	out, err = p.RewriteContext(ctx, []string{"build", "-t", "x:1", org})
	if err != nil || !strings.Contains(strings.Join(out, " "), want) || len(published) != 1 {
		t.Fatalf("lost digest: %v %v %v", out, err, published)
	}
}

func TestBaseContextsPublishErrorIsPlain(t *testing.T) {
	org := writeDockerfile(t, "FROM bench/task-a:1\n")
	for reason, want := range map[string]string{
		"not a platform image": "it is not an image the platform built",
		"not on this host":     "it is not on the shared worker",
		"connection refused":   "the image store did not answer",
	} {
		p := &DockerPolicy{
			TagPrefix: "org-a/", Builder: "b", Registry: "registry:5000",
			Pinned:  func(string) (string, error) { return "", ErrNotPinned },
			Publish: func(context.Context, string) (string, error) { return "", errors.New(reason) },
		}
		_, err := p.Rewrite([]string{"build", "-t", "x:1", org})
		var nr *ImageNotReady
		if !errors.Is(err, ErrPolicy) || !errors.As(err, &nr) || nr.Ref != "bench/task-a:1" {
			t.Fatalf("%s: err %v", reason, err)
		}
		text := "The container image this run starts from is not ready on the shared worker, and Heimdall could not prepare it: " + want + ". Try again in a few minutes. If it keeps failing, tell the server admin."
		if err.Error() != text {
			t.Fatalf("%s: text %q", reason, err.Error())
		}
	}
}

func TestBaseContextsRefusalIsPlain(t *testing.T) {
	org := writeDockerfile(t, "FROM bench/task-a:1\n")
	for name, p := range map[string]*DockerPolicy{
		"not pinned":  {Builder: "b", Registry: "registry:5000", Pinned: func(string) (string, error) { return "", ErrNotPinned }},
		"lost digest": {Builder: "b", Registry: "registry:5000", Pinned: func(string) (string, error) { return digestA, nil }, RegistryHas: func(string) (bool, error) { return false, nil }},
		"no manifest": {Builder: "b", Registry: "registry:5000"},
	} {
		_, err := p.Rewrite([]string{"build", "-t", "x:1", org})
		if !errors.Is(err, ErrPolicy) {
			t.Fatalf("%s: err %v", name, err)
		}
		for _, bad := range []string{"registry-sync", "deploy/", "HEIMDALL_", "bench/task-a"} {
			if strings.Contains(err.Error(), bad) {
				t.Fatalf("%s: text %q holds %q", name, err.Error(), bad)
			}
		}
		if err.Error() != "The container image this run starts from is not in the shared worker's image store yet. Tell the server admin to copy it there." {
			t.Fatalf("%s: text %q", name, err.Error())
		}
	}
}
