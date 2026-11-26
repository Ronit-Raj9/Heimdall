package imagepub

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/imagepin"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/suite"
)

// CoreBase is the platform's own base image, always a platform image.
const CoreBase = "bench/coding-core-base:1"

// agentImages is the part of an agent file that names its image.
type agentImages struct {
	ID    string `yaml:"id"`
	Image string `yaml:"image"`
	Build struct {
		Tag string `yaml:"tag"`
	} `yaml:"build"`
}

// PlatformImages lists every image the platform itself builds, mapped to a
// plain note of where it comes from ("agent codex", "benchmark x"). It reads
// only the checkout and the library (both read-only to every organization):
// the core base, the image and build tag of each agents/*.yaml, and for each
// benchmark folder its base image, task images and service builds. Only
// values that pass ValidRef are kept. A benchmark that fails to load is
// skipped with a warning; an empty root is skipped.
func PlatformImages(checkoutRoot, libraryRoot string) (map[string]string, error) {
	out := map[string]string{CoreBase: "platform base"}
	add := func(ref, note string) {
		if ValidRef(ref) {
			if _, ok := out[ref]; !ok {
				out[ref] = note
			}
		}
	}
	var agentDirs, benchDirs []string
	if checkoutRoot != "" {
		agentDirs = append(agentDirs, filepath.Join(checkoutRoot, "agents"))
		benchDirs = append(benchDirs, filepath.Join(checkoutRoot, "benchmarks"))
	}
	if libraryRoot != "" {
		agentDirs = append(agentDirs, suite.LibraryAgents(libraryRoot))
		benchDirs = append(benchDirs, suite.LibraryBenchmarks(libraryRoot))
	}
	for _, dir := range agentDirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				return nil, fmt.Errorf("platform images: %w", err)
			}
			var a agentImages
			if err := yaml.Unmarshal(raw, &a); err != nil {
				slog.Warn("platform images: agent file skipped", "file", f, "err", err)
				continue
			}
			id := a.ID
			if id == "" {
				id = filepath.Base(f[:len(f)-len(".yaml")])
			}
			add(a.Build.Tag, "agent "+id)
			add(a.Image, "agent "+id)
		}
	}
	ctx := context.Background()
	for _, root := range benchDirs {
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("platform images: %w", err)
		}
		loader := suite.Builtin(root)[0]
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			folder := filepath.Join(root, e.Name())
			if _, err := os.Stat(filepath.Join(folder, suite.SuiteFile)); err != nil {
				continue
			}
			s, err := loader.Load(ctx, folder)
			if err != nil {
				slog.Warn("platform images: benchmark skipped", "folder", folder, "err", err)
				continue
			}
			note := "benchmark " + s.ID
			for _, ref := range imagepin.SuiteImages(s, "") {
				add(ref, note)
			}
			for _, b := range ops.ServiceBuilds(s) {
				add(b.Tag, note)
			}
		}
	}
	return out, nil
}
