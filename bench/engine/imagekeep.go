package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ImageKeep is the set of images runner image GC must never remove: every
// image a suite under the benchmarks roots needs, whether its tasks are
// admitted or draft. Copied library images (SWE-bench, Terminal-Bench, ...)
// can sit unused for days and are expensive to bring back.
type ImageKeep struct {
	Refs     map[string]bool // exact references (repo:tag) and ids
	Prefixes []string        // repositories of a suite's built task and service images, any tag
}

// Keeps reports whether ref is in the keep set.
func (k *ImageKeep) Keeps(ref string) bool {
	if k == nil {
		return false
	}
	if k.Refs[ref] {
		return true
	}
	repo := ref
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		repo = ref[:i]
	}
	for _, p := range k.Prefixes {
		if strings.HasPrefix(repo, p) {
			return true
		}
	}
	return false
}

// SuiteImageKeeper builds the keep set from the suite folders under the
// benchmarks roots Roots returns (a missing root is skipped; a Roots error fails the set), caching each
// suite by the stamp (size and mtime) of its suite.yaml, suite.lock and
// every task.yaml.
type SuiteImageKeeper struct {
	Roots func() ([]string, error)
	Extra []string // references always kept (the helper image, pins)

	mu    sync.Mutex
	cache map[string]suiteKeep // suite folder -> its images
}

type suiteKeep struct {
	stamp    string
	refs     []string
	prefixes []string
}

// KeepSet implements the hygiene's keep set. Any read error other than a
// missing file fails it: image GC then removes nothing that sweep.
func (k *SuiteImageKeeper) KeepSet(ctx context.Context) (*ImageKeep, error) {
	out := &ImageKeep{Refs: map[string]bool{}}
	for _, r := range k.Extra {
		addRef(out.Refs, r)
	}
	var roots []string
	if k.Roots != nil {
		var err error
		if roots, err = k.Roots(); err != nil {
			return nil, err
		}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cache == nil {
		k.cache = map[string]suiteKeep{}
	}
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read benchmarks %s: %w", root, err)
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			dir := filepath.Join(root, e.Name())
			if !isDirEntry(dir, e) {
				continue
			}
			sk, err := k.suite(dir)
			if err != nil {
				return nil, err
			}
			seen[dir] = true
			for _, r := range sk.refs {
				addRef(out.Refs, r)
			}
			out.Prefixes = append(out.Prefixes, sk.prefixes...)
		}
	}
	for dir := range k.cache {
		if !seen[dir] {
			delete(k.cache, dir)
		}
	}
	return out, nil
}

// isDirEntry follows a symlinked suite folder.
func isDirEntry(p string, e fs.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink != 0 {
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir()
	}
	return false
}

// suite returns one suite folder's images, from the cache when its stamp is
// unchanged. A folder with neither suite.yaml nor suite.lock is not a suite.
func (k *SuiteImageKeeper) suite(dir string) (suiteKeep, error) {
	stamp, tasks, ok, err := suiteStamp(dir)
	if err != nil || !ok {
		return suiteKeep{}, err
	}
	if c, hit := k.cache[dir]; hit && c.stamp == stamp {
		return c, nil
	}
	sk, err := readSuiteImages(dir, tasks)
	if err != nil {
		return suiteKeep{}, err
	}
	sk.stamp = stamp
	k.cache[dir] = sk
	return sk, nil
}

// suiteStamp stats suite.yaml, suite.lock and tasks/*/task.yaml.
func suiteStamp(dir string) (stamp string, tasks []string, ok bool, err error) {
	var b strings.Builder
	for _, name := range []string{"suite.yaml", "suite.lock"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			b.WriteString(name + ":-;")
			continue
		case err != nil:
			return "", nil, false, fmt.Errorf("stat %s: %w", filepath.Join(dir, name), err)
		}
		ok = true
		fmt.Fprintf(&b, "%s:%d:%d;", name, fi.Size(), fi.ModTime().UnixNano())
	}
	if !ok {
		return "", nil, false, nil
	}
	entries, err := os.ReadDir(filepath.Join(dir, "tasks"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", nil, false, fmt.Errorf("read tasks of %s: %w", dir, err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, "tasks", e.Name(), "task.yaml")
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", nil, false, fmt.Errorf("stat %s: %w", p, err)
		}
		tasks = append(tasks, e.Name())
		fmt.Fprintf(&b, "%s:%d:%d;", e.Name(), fi.Size(), fi.ModTime().UnixNano())
	}
	return b.String(), tasks, true, nil
}

// keepSuiteFile is the part of suite.yaml and task.yaml image GC reads.
type keepSuiteFile struct {
	ID          string                   `yaml:"id"`
	BaseImage   schemas.ImageSpec        `yaml:"base_image"`
	Services    []schemas.ServiceSpec    `yaml:"services"`
	Environment *schemas.TaskEnvironment `yaml:"environment"`
}

// readSuiteImages reads one suite's images: the lock's base image and
// fingerprinted image identities, suite.yaml's base and service images,
// each task's own and service images, and the repositories of the suite's
// built task and service images (bench/task-<suite>-..., bench/svc-<suite>-...).
func readSuiteImages(dir string, tasks []string) (suiteKeep, error) {
	var sk suiteKeep
	ids := map[string]bool{filepath.Base(dir): true}
	raw, err := os.ReadFile(filepath.Join(dir, "suite.lock"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return sk, fmt.Errorf("read %s: %w", filepath.Join(dir, "suite.lock"), err)
	default:
		var lock schemas.SuiteLock
		if err := json.Unmarshal(raw, &lock); err != nil {
			return sk, fmt.Errorf("parse %s: %w", filepath.Join(dir, "suite.lock"), err)
		}
		if lock.Suite != "" {
			ids[lock.Suite] = true
		}
		sk.refs = append(sk.refs, lock.BaseImage.Tag)
		if fp := lock.Fingerprints; fp != nil {
			sk.refs = append(sk.refs, fingerprintRefs(fp.Suite)...)
			for _, f := range fp.Tasks {
				sk.refs = append(sk.refs, fingerprintRefs(f)...)
			}
			for _, f := range fp.Probes {
				sk.refs = append(sk.refs, fingerprintRefs(f)...)
			}
		}
	}
	var s keepSuiteFile
	if err := readKeepYAML(filepath.Join(dir, "suite.yaml"), &s); err != nil {
		return sk, err
	}
	if s.ID != "" {
		ids[s.ID] = true
	}
	sk.refs = append(sk.refs, s.BaseImage.Tag)
	sk.refs = append(sk.refs, serviceRefs(s.Services)...)
	for _, id := range tasks {
		var t keepSuiteFile
		if err := readKeepYAML(filepath.Join(dir, "tasks", id, "task.yaml"), &t); err != nil {
			return sk, err
		}
		if t.Environment != nil {
			sk.refs = append(sk.refs, t.Environment.Image)
		}
		sk.refs = append(sk.refs, serviceRefs(t.Services)...)
	}
	for id := range ids {
		sk.prefixes = append(sk.prefixes, "bench/"+utils.ImageName("task-"+id+"-"), "bench/"+utils.ImageName("svc-"+id+"-"))
	}
	return sk, nil
}

func readKeepYAML(p string, v any) error {
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	if err := yaml.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("parse %s: %w", p, err)
	}
	return nil
}

func serviceRefs(svcs []schemas.ServiceSpec) []string {
	var out []string
	for _, sv := range svcs {
		out = append(out, sv.Image)
		if sv.Build != nil {
			out = append(out, sv.Build.Tag)
		}
	}
	return out
}

// fingerprintRefs reads the references in a fingerprint's image identities:
// "tag only: <tag>", "<tag>=<repo>@sha256:..." or "<tag>@<id>".
func fingerprintRefs(f schemas.Fingerprint) []string {
	var out []string
	for _, v := range f.Images {
		v = strings.TrimPrefix(v, "tag only: ")
		if tag, digest, ok := strings.Cut(v, "="); ok {
			out = append(out, tag, digest)
			continue
		}
		if tag, id, ok := strings.Cut(v, "@"); ok {
			out = append(out, tag, id)
			continue
		}
		out = append(out, v)
	}
	return out
}

// addRef keeps ref, and ref:latest when it names no tag.
func addRef(m map[string]bool, ref string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return
	}
	m[ref] = true
	if !strings.HasPrefix(ref, "sha256:") && !strings.Contains(refTail(ref), ":") && !strings.Contains(ref, "@") {
		m[ref+":latest"] = true
	}
}
