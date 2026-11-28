package utils

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHost is the shared host's docker images (namespaced tag to image ID)
// and the registry pushes of org images.
type fakeHost struct {
	images map[string]string
	pushed [][2]string
}

func (h *fakeHost) own(prefix string) OwnImageFunc {
	return func(_ context.Context, ref string) (string, bool, error) {
		if !strings.HasPrefix(ref, prefix) {
			return "", false, nil
		}
		id, ok := h.images[ref]
		return id, ok, nil
	}
}

func (h *fakeHost) publishOwn(_ context.Context, hostRef, pushRef string) (string, error) {
	h.pushed = append(h.pushed, [2]string{hostRef, pushRef})
	return digestB, nil
}

var (
	idA = "sha256:" + strings.Repeat("1", 64)
	idP = "sha256:" + strings.Repeat("2", 64)
)

func orgPolicyU1(h *fakeHost, org string, refused *[]string) *DockerPolicy {
	prefix := OrgImagePrefix(org)
	return &DockerPolicy{
		TagPrefix: prefix, Builder: "b", Registry: "registry:5000",
		PlatformPin: func(ref string) (string, bool) { return "", ref == "bench/coding-core-base:1" },
		Pinned: func(ref string) (string, error) {
			if ref == "bench/coding-core-base:1" {
				return digestA, nil
			}
			return "", ErrNotPinned
		},
		Publish: func(_ context.Context, ref string) (string, error) {
			*refused = append(*refused, ref)
			return "", errors.New("bench/x: not a platform image")
		},
		OwnImage: h.own(prefix), PublishOwn: h.publishOwn,
	}
}

const agentDockerfile = "ARG BASE_IMAGE=bench/coding-core-base:1\nFROM ${BASE_IMAGE}\n"

// The live defect: bare-model built for a user-made benchmark on its own
// base image bench/s-base:1 (built as org-a/bench/s-base:1 on the host).
func TestAgentBuildOnOwnSuiteBase(t *testing.T) {
	h := &fakeHost{images: map[string]string{"org-a/bench/s-base:1": idA}}
	var refused []string
	p := orgPolicyU1(h, "a", &refused)
	dir := writeDockerfile(t, agentDockerfile)
	out, err := p.Rewrite([]string{"build", "--build-arg", "BASE_IMAGE=bench/s-base:1", "-t", "bench/agent-bare-model:1-s", dir})
	if err != nil {
		t.Fatal(err)
	}
	push := "org-a/bench/s-base:" + strings.Repeat("1", 64)
	want := "--build-context bench/s-base:1=docker-image://registry:5000/" + push + "@" + digestB
	if !strings.Contains(strings.Join(out, " "), want) {
		t.Fatalf("missing %q in %v", want, out)
	}
	if len(h.pushed) != 1 || h.pushed[0] != [2]string{"org-a/bench/s-base:1", push} || len(refused) != 0 {
		t.Fatalf("pushed %v refused %v", h.pushed, refused)
	}
	// A second build reuses the digest without another push.
	if _, err := p.Rewrite([]string{"build", "--build-arg", "BASE_IMAGE=bench/s-base:1", "-t", "y:1", dir}); err != nil || len(h.pushed) != 1 {
		t.Fatalf("second: %v %v", err, h.pushed)
	}
}

// Another org writing the same name gets its own namespace only: never the
// first org's image.
func TestOwnBaseOtherOrgNotReachable(t *testing.T) {
	h := &fakeHost{images: map[string]string{"org-a/bench/s-base:1": idA}}
	var refused []string
	p := orgPolicyU1(h, "b", &refused)
	dir := writeDockerfile(t, agentDockerfile)
	out, err := p.Rewrite([]string{"build", "--build-arg", "BASE_IMAGE=bench/s-base:1", "-t", "x:1", dir})
	var nr *ImageNotReady
	if !errors.As(err, &nr) || nr.Own || strings.Contains(strings.Join(out, " "), "org-a") || len(h.pushed) != 0 {
		t.Fatalf("out %v err %v pushed %v", out, err, h.pushed)
	}
	if !strings.Contains(err.Error(), "build the benchmark's base image first") {
		t.Fatalf("text %q", err.Error())
	}
	for _, base := range []string{"org-a/bench/s-base:1", "registry:5000/org-a/bench/s-base:" + strings.Repeat("1", 64)} {
		_, err := p.Rewrite([]string{"build", "--build-arg", "BASE_IMAGE=" + base, "-t", "x:1", dir})
		if !errors.Is(err, ErrPolicy) || len(h.pushed) != 0 {
			t.Fatalf("%s: err %v pushed %v", base, err, h.pushed)
		}
	}
}

// A platform name always means the platform image, even when the org has
// an image of that name.
func TestOwnBasePlatformNameWins(t *testing.T) {
	h := &fakeHost{images: map[string]string{"org-a/bench/coding-core-base:1": idP}}
	var refused []string
	p := orgPolicyU1(h, "a", &refused)
	out, err := p.Rewrite([]string{"build", "-t", "x:1", writeDockerfile(t, agentDockerfile)})
	if err != nil {
		t.Fatal(err)
	}
	want := "--build-context bench/coding-core-base:1=docker-image://registry:5000/bench/coding-core-base:1@" + digestA
	if !strings.Contains(strings.Join(out, " "), want) || len(h.pushed) != 0 {
		t.Fatalf("out %v pushed %v", out, h.pushed)
	}
}

// A missing own base is built first (the suite base build runs before the
// agent build), then the agent build starts from it.
func TestOwnBaseBuiltFirstThenUsed(t *testing.T) {
	h := &fakeHost{images: map[string]string{}}
	var refused []string
	p := orgPolicyU1(h, "a", &refused)
	agent := writeDockerfile(t, agentDockerfile)
	args := []string{"build", "--build-arg", "BASE_IMAGE=bench/s-base:1", "-t", "x:1", agent}
	if _, err := p.Rewrite(args); err == nil {
		t.Fatal("no error without the base")
	}
	base := writeDockerfile(t, "FROM python:3.12-slim\n")
	out, err := p.Rewrite([]string{"build", "-t", "bench/s-base:1", base})
	if err != nil || !strings.Contains(strings.Join(out, " "), "-t org-a/bench/s-base:1") {
		t.Fatalf("base build %v %v", out, err)
	}
	h.images["org-a/bench/s-base:1"] = idA // what --load leaves on the host
	out, err = p.Rewrite(args)
	if err != nil || !strings.Contains(strings.Join(out, " "), "bench/s-base:1=docker-image://registry:5000/org-a/bench/s-base:") {
		t.Fatalf("agent build %v %v", out, err)
	}
}

// A platform agent Dockerfile on the org's own base builds on the isolated
// builder, never on the host daemon.
func TestTrustedAgentOnOwnBaseIsolated(t *testing.T) {
	h := &fakeHost{images: map[string]string{"org-a/bench/s-base:1": idA}}
	var refused []string
	p := orgPolicyU1(h, "a", &refused)
	lib := t.TempDir()
	if err := os.WriteFile(filepath.Join(lib, "Dockerfile"), []byte(agentDockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Trusted = []string{lib}
	out, err := p.Rewrite([]string{"build", "--build-arg", "BASE_IMAGE=bench/s-base:1", "-t", "x:1", lib})
	if err != nil || len(out) < 2 || out[0] != "buildx" || !strings.Contains(strings.Join(out, " "), "docker-image://registry:5000/org-a/") {
		t.Fatalf("out %v err %v", out, err)
	}
	// On a platform base it still builds on the host daemon.
	out, err = p.Rewrite([]string{"build", "-t", "x:1", lib})
	if err != nil || out[0] != "build" {
		t.Fatalf("platform base: %v %v", out, err)
	}
}

func TestOwnBaseFailureNamesImage(t *testing.T) {
	h := &fakeHost{images: map[string]string{"org-a/bench/s-base:1": idA}}
	var refused []string
	p := orgPolicyU1(h, "a", &refused)
	p.PublishOwn = func(context.Context, string, string) (string, error) { return "", errors.New("connection refused") }
	_, err := p.Rewrite([]string{"build", "--build-arg", "BASE_IMAGE=bench/s-base:1", "-t", "x:1", writeDockerfile(t, agentDockerfile)})
	if !errors.Is(err, ErrPolicy) || !strings.HasPrefix(err.Error(), "The base image bench/s-base:1 that this benchmark built could not be used") {
		t.Fatalf("err %v", err)
	}
}
