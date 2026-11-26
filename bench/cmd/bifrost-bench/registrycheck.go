package main

// `registry check` (wave 4, F1.1 part D): what the shared worker's registry
// still lacks, and any platform-named image on this host that was pulled
// from outside the platform. The upgrade runs it and only warns.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/imagepub"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

func runRegistryCheck(args []string) int {
	fs := flag.NewFlagSet("registry check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	manifest := fs.String("manifest", os.Getenv(registryManifestEnv), "registry manifest (digest pins)")
	docker := fs.String("docker", "docker", "docker binary")
	strict := fs.Bool("strict", false, "exit 1 when anything is missing")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "registry check: %v\n%s\n", err, registryUsage)
		return exitUsage
	}
	set, err := imagepub.PlatformImages(os.Getenv("HEIMDALL_BENCH_DIR"), suite.LibraryDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry check: %v\n", err)
		return 1
	}
	pins := map[string]string{}
	if *manifest != "" {
		if p, err := utils.ReadRegistryManifest(*manifest); err == nil {
			pins = p
		} else {
			fmt.Fprintf(os.Stderr, "registry check: %v\n", err)
		}
	}
	lines := registryCheckLines(context.Background(), utils.Docker{Binary: *docker}, set, pins, sharedSplitList(os.Getenv(sharedPlatformImagesEnv)))
	for _, l := range lines {
		fmt.Println(l)
	}
	if *strict && len(lines) > 0 {
		return 1
	}
	return exitOK
}

// registryCheckLines are the warnings for a platform set and the manifest's
// pins: each agent base missing, each benchmark with images missing, then
// each platform-named image on the host pulled from outside the platform.
func registryCheckLines(ctx context.Context, d utils.Docker, set, pins map[string]string, platform []string) []string {
	var agents, outside []string
	type count struct{ missing, total int }
	benches := map[string]*count{}
	for ref, note := range set {
		_, pinned := pins[ref]
		if name, ok := strings.CutPrefix(note, "benchmark "); ok {
			c := benches[name]
			if c == nil {
				c = &count{}
				benches[name] = c
			}
			c.total++
			if !pinned {
				c.missing++
			}
			continue
		}
		if !pinned {
			agents = append(agents, fmt.Sprintf("Agent base image %s is not in the shared worker's registry. Runs of agents built on it wait while it is copied.", ref))
		}
	}
	sort.Strings(agents)
	names := make([]string, 0, len(benches))
	for n, c := range benches {
		if c.missing > 0 {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	lines := agents
	for _, n := range names {
		c := benches[n]
		lines = append(lines, fmt.Sprintf("Benchmark %s: %d of %d container images are not in the shared worker's registry yet. They are copied the first time a run needs them.", n, c.missing, c.total))
	}
	// The SEC-C leftover check: Qualify rule 4 on every platform-named
	// image this host holds.
	res, err := d.Run(ctx, "image", "ls", "--format", "{{.Repository}}:{{.Tag}}")
	if err == nil && res.ExitCode == 0 {
		for _, ref := range strings.Fields(string(res.Stdout)) {
			if _, ok := set[ref]; !ok {
				continue
			}
			ins, err := d.Run(ctx, "image", "inspect", "--format", "{{json .RepoDigests}}", ref)
			if err != nil || ins.ExitCode != 0 {
				continue
			}
			var digests []string
			if json.Unmarshal(bytes.TrimSpace(ins.Stdout), &digests) != nil {
				continue
			}
			if repo := imagepub.OutsideRepo(ref, digests, platform); repo != "" {
				outside = append(outside, fmt.Sprintf("Image %s on this host was pulled from %s, outside the platform. It is never copied. Remove it and let the platform build it again.", ref, repo))
			}
		}
	}
	sort.Strings(outside)
	return append(lines, outside...)
}
