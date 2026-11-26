package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

const w4b01Hex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// w4b01Docker is a docker stand-in: "image ls" lists the host's tags and
// "image inspect" answers the RepoDigests of bench/task-b1:1 as pulled from
// outside the platform, every other image as a local build.
func w4b01Docker(t *testing.T) utils.Docker {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "docker")
	script := `#!/bin/sh
case "$2" in
  ls) printf 'bench/task-b1:1\nbench/agent-codex:1\nalpine:3\n' ;;
  inspect)
    for a; do last=$a; done
    if [ "$last" = bench/task-b1:1 ]; then printf '["docker.io/evil/x@sha256:` + w4b01Hex + `"]\n'; else printf '[]\n'; fi ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}
}

func TestRegistryCheckListsMissingBenchmarkImages(t *testing.T) {
	set := map[string]string{
		"bench/coding-core-base:1": "platform base",
		"bench/agent-codex:1":      "agent codex",
		"bench/agent-claude:1":     "agent claude",
		"bench/task-b1:1":          "benchmark bfcl",
		"bench/task-b2:1":          "benchmark bfcl",
		"bench/task-b3:1":          "benchmark bfcl",
		"bench/task-s1:1":          "benchmark swe",
	}
	pins := map[string]string{
		"bench/coding-core-base:1": "sha256:" + w4b01Hex,
		"bench/agent-claude:1":     "sha256:" + w4b01Hex,
		"bench/task-b3:1":          "sha256:" + w4b01Hex,
		"bench/task-s1:1":          "sha256:" + w4b01Hex,
	}
	lines := registryCheckLines(context.Background(), w4b01Docker(t), set, pins, nil)
	want := []string{
		"Agent base image bench/agent-codex:1 is not in the shared worker's registry. Runs of agents built on it wait while it is copied.",
		"Benchmark bfcl: 2 of 3 container images are not in the shared worker's registry yet. They are copied the first time a run needs them.",
		"Image bench/task-b1:1 on this host was pulled from docker.io/evil/x, outside the platform. It is never copied. Remove it and let the platform build it again.",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestRegistryCheckStrictExit(t *testing.T) {
	dir := t.TempDir()
	m := filepath.Join(dir, "manifest")
	if err := os.WriteFile(m, []byte("bench/coding-core-base:1 sha256:"+w4b01Hex+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEIMDALL_BENCH_DIR", "")
	t.Setenv("HEIMDALL_LIBRARY_DIR", "")
	d := w4b01Docker(t)
	if rc := runRegistryCheck([]string{"-manifest", m, "-docker", d.Binary, "-strict"}); rc != exitOK {
		t.Fatalf("nothing missing: rc %d", rc)
	}
	if rc := runRegistryCheck([]string{"-manifest", filepath.Join(dir, "none"), "-docker", d.Binary, "-strict"}); rc == exitOK {
		t.Fatal("strict with the core base missing exited 0")
	}
	if rc := runRegistryCheck([]string{"-manifest", filepath.Join(dir, "none"), "-docker", d.Binary}); rc != exitOK {
		t.Fatalf("warn only: rc %d", rc)
	}
}

func TestRunRegistryRoutesSubcommands(t *testing.T) {
	if rc := runRegistry([]string{"nope"}); rc != exitUsage {
		t.Fatalf("unknown: rc %d", rc)
	}
	if !strings.Contains(registryUsage, "registry publisher") || !strings.Contains(registryUsage, "registry check") || !strings.Contains(registryUsage, "registry platform-images") {
		t.Fatalf("usage lacks the new subcommands:\n%s", registryUsage)
	}
	t.Setenv("HEIMDALL_BENCH_DIR", "")
	t.Setenv("HEIMDALL_LIBRARY_DIR", "")
	if rc := runRegistry([]string{"platform-images"}); rc != exitOK {
		t.Fatalf("platform-images: rc %d", rc)
	}
}
