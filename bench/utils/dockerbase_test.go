package utils

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDockerfile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDockerfileBases(t *testing.T) {
	dir := writeDockerfile(t, strings.Join([]string{
		"# syntax=docker/dockerfile:1",
		"ARG BASE=bench/coding-core-base:1",
		"ARG PY",
		"FROM ${BASE} AS build",
		"RUN make",
		"FROM --platform=linux/amd64 python:$PY",
		"FROM build AS again",
		"FROM scratch",
		"FROM \\",
		"  bench/coding-core-base:1",
		"FROM $UNSET",
		"FROM ghcr.io/acme/tool:2",
	}, "\n"))
	got, err := DockerfileBases(filepath.Join(dir, "Dockerfile"), map[string]string{"PY": "3.12"})
	if err != nil {
		t.Fatal(err)
	}
	want := "bench/coding-core-base:1 python:3.12 ghcr.io/acme/tool:2"
	if strings.Join(got, " ") != want {
		t.Fatalf("bases %v, want %s", got, want)
	}
	// A build argument overrides the ARG default.
	got, _ = DockerfileBases(filepath.Join(dir, "Dockerfile"), map[string]string{"BASE": "bench/other:2"})
	if got[0] != "bench/other:2" {
		t.Fatalf("build arg ignored: %v", got)
	}
}

func TestFromRegistry(t *testing.T) {
	p := &DockerPolicy{Registry: "registry:5000", Platform: []string{"python:", "redis:7", "ghcr.io/heimdall/"}}
	for ref, want := range map[string]bool{
		"bench/coding-core-base:1": true,
		"python:3.12":              true,
		"redis:7":                  true,
		"redis:6":                  false,
		"ubuntu:24.04":             false,
		"ghcr.io/heimdall/x:1":     false, // has a registry host: BuildKit pulls it itself
		"localhost/bench/x:1":      false,
		"bench/x@sha256:" + strings.Repeat("a", 64): false,
	} {
		if got := p.fromRegistry(ref); got != want {
			t.Errorf("fromRegistry(%s) = %v, want %v", ref, got, want)
		}
	}
	if (&DockerPolicy{}).fromRegistry("bench/coding-core-base:1") {
		t.Error("no registry configured but bench image rewritten")
	}
}

func TestPolicyBuildBaseContexts(t *testing.T) {
	org := writeDockerfile(t, "FROM bench/coding-core-base:1\nFROM ubuntu:24.04\nFROM python:3.12\n")
	have := map[string]bool{"bench/coding-core-base:1": true, "python:3.12": true}
	p := &DockerPolicy{
		TagPrefix: "org-a/", Builder: "heimdall-org-builder", Registry: "registry:5000", Platform: []string{"python:"},
		RegistryHas: func(ref string) (bool, error) { return have[ref], nil },
	}
	out, err := p.Rewrite([]string{"build", "-t", "x:1", org})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(out, " ")
	for _, want := range []string{
		"--build-context bench/coding-core-base:1=docker-image://registry:5000/bench/coding-core-base:1",
		"--build-context python:3.12=docker-image://registry:5000/python:3.12",
		"-t org-a/x:1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "ubuntu") {
		t.Errorf("public image rewritten: %s", got)
	}
	// The org still may not pass build contexts of its own.
	if _, err := p.Rewrite([]string{"build", "--build-context", "bench/coding-core-base:1=/", "-t", "x", org}); !errors.Is(err, ErrPolicy) {
		t.Fatalf("org build context accepted: %v", err)
	}
	// A base image the registry lacks fails before the build, naming it.
	delete(have, "python:3.12")
	_, err = p.Rewrite([]string{"build", "-t", "x:1", org})
	if !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "python:3.12") || !strings.Contains(err.Error(), "registry-sync.sh") {
		t.Fatalf("missing base image: %v", err)
	}
	// Without a registry FROM lines stay as written.
	p.Registry = ""
	out, err = p.Rewrite([]string{"build", "-t", "x:1", org})
	if err != nil || strings.Contains(strings.Join(out, " "), "--build-context") {
		t.Fatalf("no registry: %v %v", out, err)
	}
}

func TestRegistryHas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodHead:
			w.WriteHeader(http.StatusMethodNotAllowed)
		case r.URL.Path == "/v2/bench/coding-core-base/manifests/1", r.URL.Path == "/v2/python/manifests/latest":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v2/broken/manifests/latest":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	ctx := context.Background()
	for ref, want := range map[string]bool{"bench/coding-core-base:1": true, "python": true, "bench/coding-core-base:2": false} {
		got, err := RegistryHas(ctx, srv.Client(), host, ref)
		if err != nil || got != want {
			t.Errorf("RegistryHas(%s) = %v %v, want %v", ref, got, err, want)
		}
	}
	if _, err := RegistryHas(ctx, srv.Client(), host, "broken"); err == nil {
		t.Error("server error not reported")
	}
}
