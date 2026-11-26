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
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
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
	err := eachPlatformSuite(benchDirs, func(s *schemas.Suite) {
		note := "benchmark " + s.ID
		for _, ref := range imagepin.SuiteImages(s, "") {
			add(ref, note)
		}
		for _, b := range ops.ServiceBuilds(s) {
			add(b.Tag, note)
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// platformBenchDirs are the benchmark folders of the checkout and the library.
func platformBenchDirs(checkoutRoot, libraryRoot string) []string {
	var dirs []string
	if checkoutRoot != "" {
		dirs = append(dirs, filepath.Join(checkoutRoot, "benchmarks"))
	}
	if libraryRoot != "" {
		dirs = append(dirs, suite.LibraryBenchmarks(libraryRoot))
	}
	return dirs
}

// eachPlatformSuite calls fn with every benchmark that loads from roots; a
// benchmark that fails to load is skipped with a warning.
func eachPlatformSuite(roots []string, fn func(*schemas.Suite)) error {
	ctx := context.Background()
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("platform images: %w", err)
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
			fn(s)
		}
	}
	return nil
}

// PlatformPins maps every platform image name to the pinned reference the
// platform's own suite locks give for it ("" for an image the platform
// builds or that no lock pins). It covers PlatformImages and every tag a
// platform lock pins (a service image such as postgres:16 too). Only the
// checkout and the library are read, never an organization's project.
func PlatformPins(checkoutRoot, libraryRoot string) (map[string]string, error) {
	set, err := PlatformImages(checkoutRoot, libraryRoot)
	if err != nil {
		return nil, err
	}
	pins := make(map[string]string, len(set))
	for ref := range set {
		pins[ref] = ""
	}
	addFP := func(fp schemas.Fingerprint) {
		for _, v := range fp.Images {
			if tag, pinned, ok := utils.LockedPin(v); ok {
				pins[tag] = pinned
			}
		}
	}
	err = eachPlatformSuite(platformBenchDirs(checkoutRoot, libraryRoot), func(s *schemas.Suite) {
		if s.Lock == nil || s.Lock.Fingerprints == nil {
			return
		}
		addFP(s.Lock.Fingerprints.Suite)
		for _, fp := range s.Lock.Fingerprints.Tasks {
			addFP(fp)
		}
	})
	if err != nil {
		return nil, err
	}
	return pins, nil
}
