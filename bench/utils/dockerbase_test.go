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

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var (
	digestA = "sha256:" + strings.Repeat("a", 64)
	digestB = "sha256:" + strings.Repeat("b", 64)
)

func TestPolicyBuildBaseContexts(t *testing.T) {
	org := writeDockerfile(t, "FROM bench/coding-core-base:1\nFROM ubuntu:24.04\nFROM python:3.12\n")
	manifest := writeManifest(t, "# heimdall registry manifest\nbench/coding-core-base:1 "+digestA+"\npython:3.12 "+digestB+"\n")
	var asked []string
	p := &DockerPolicy{
		TagPrefix: "org-a/", Builder: "heimdall-org-builder", Registry: "registry:5000", Platform: []string{"python:"},
		Pinned:      ManifestPins(manifest),
		RegistryHas: func(ref string) (bool, error) { asked = append(asked, ref); return true, nil },
	}
	out, err := p.Rewrite([]string{"build", "-t", "x:1", org})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(out, " ")
	for _, want := range []string{
		"--build-context bench/coding-core-base:1=docker-image://registry:5000/bench/coding-core-base:1@" + digestA,
		"--build-context python:3.12=docker-image://registry:5000/python:3.12@" + digestB,
		"-t org-a/x:1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "ubuntu") {
		t.Errorf("public image rewritten: %s", got)
	}
	// The registry is asked for the pinned digest, never the tag.
	if strings.Join(asked, " ") != "bench/coding-core-base:1@"+digestA+" python:3.12@"+digestB {
		t.Errorf("registry checks %v", asked)
	}
	// The org still may not pass build contexts of its own.
	if _, err := p.Rewrite([]string{"build", "--build-context", "bench/coding-core-base:1=/", "-t", "x", org}); !errors.Is(err, ErrPolicy) {
		t.Fatalf("org build context accepted: %v", err)
	}
	// A base image the manifest lacks fails before the build, naming it.
	if err := os.WriteFile(manifest, []byte("bench/coding-core-base:1 "+digestA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = p.Rewrite([]string{"build", "-t", "x:1", org})
	if !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "python:3.12") || !strings.Contains(err.Error(), "registry-sync.sh") {
		t.Fatalf("missing base image: %v", err)
	}
	// A pinned digest the registry no longer holds fails too.
	p.Pinned = func(string) (string, error) { return digestA, nil }
	p.RegistryHas = func(string) (bool, error) { return false, nil }
	if _, err = p.Rewrite([]string{"build", "-t", "x:1", org}); !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), digestA) {
		t.Fatalf("digest gone from the registry: %v", err)
	}
	// No manifest at all: never by tag.
	p.Pinned = nil
	if _, err = p.Rewrite([]string{"build", "-t", "x:1", org}); !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("no manifest: %v", err)
	}
	// Without a registry FROM lines stay as written.
	p.Registry = ""
	out, err = p.Rewrite([]string{"build", "-t", "x:1", org})
	if err != nil || strings.Contains(strings.Join(out, " "), "--build-context") {
		t.Fatalf("no registry: %v %v", out, err)
	}
}

func TestReadRegistryManifest(t *testing.T) {
	m, err := ReadRegistryManifest(writeManifest(t, "# c\n\nbench/a:1 "+digestA+"\nbench/a:1 "+digestB+"\npython:3 "+digestA+"\n"))
	if err != nil || m["bench/a:1"] != digestB || m["python:3"] != digestA || len(m) != 2 {
		t.Fatalf("manifest %v %v", m, err)
	}
	for _, bad := range []string{"bench/a:1\n", "bench/a:1 sha256:abc\n", "bench/a@" + digestA + " " + digestA + "\n", "a b c\n"} {
		if _, err := ReadRegistryManifest(writeManifest(t, bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := ManifestPins(filepath.Join(t.TempDir(), "none"))("bench/a:1"); err == nil || errors.Is(err, ErrNotPinned) {
		t.Fatalf("missing manifest file: %v", err)
	}
	if _, err := ManifestPins(writeManifest(t, ""))("bench/a:1"); !errors.Is(err, ErrNotPinned) {
		t.Fatalf("unknown image: %v", err)
	}
}

func TestReadRegistryCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pull.auth")
	os.WriteFile(path, []byte("heimdall-pull:s3cret:x\n"), 0o600)
	c, err := ReadRegistryCredentials(path)
	if err != nil || c.User != "heimdall-pull" || c.Password != "s3cret:x" {
		t.Fatalf("credentials %+v %v", c, err)
	}
	os.WriteFile(path, []byte("nopassword"), 0o600)
	if _, err := ReadRegistryCredentials(path); err == nil {
		t.Fatal("accepted a line without a password")
	}
}

func TestRegistryHas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pw, ok := r.BasicAuth()
		switch {
		case r.Method != http.MethodHead:
			w.WriteHeader(http.StatusMethodNotAllowed)
		case !ok || user != "pull" || pw != "pw":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/v2/bench/coding-core-base/manifests/1", r.URL.Path == "/v2/python/manifests/latest",
			r.URL.Path == "/v2/bench/coding-core-base/manifests/"+digestA:
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
	cred := &RegistryCredentials{User: "pull", Password: "pw"}
	for ref, want := range map[string]bool{
		"bench/coding-core-base:1": true, "python": true, "bench/coding-core-base:2": false,
		"bench/coding-core-base:1@" + digestA: true, "bench/coding-core-base@" + digestA: true,
		"bench/coding-core-base:1@" + digestB: false,
	} {
		got, err := RegistryHas(ctx, srv.Client(), host, ref, cred)
		if err != nil || got != want {
			t.Errorf("RegistryHas(%s) = %v %v, want %v", ref, got, err, want)
		}
	}
	if _, err := RegistryHas(ctx, srv.Client(), host, "broken", cred); err == nil {
		t.Error("server error not reported")
	}
	if _, err := RegistryHas(ctx, srv.Client(), host, "python", nil); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Errorf("refused credentials not reported: %v", err)
	}
}
