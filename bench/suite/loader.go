// Package suite loads, lints, locks and admits benchmark packs.
//
// A pack is a folder benchmarks/<id>/ with suite.yaml, tasks/<id>/task.yaml
// and probes/<id>.yaml (format in benchmarks/coding-core/README.md). Every
// document is validated against the JSON Schemas in bench/spec before it is
// decoded, so a typo fails loudly with the file and field named. The lock
// (suite.lock) pins every task and probe by hash; DESIGN.md section 12 says
// which hash changes force a regrade or a rerun.
package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// LoaderLocal is the loader that reads packs from a folder on disk.
const LoaderLocal = "local"

const localLoaderVersion = "1.0.0"

// File names inside a pack.
const (
	SuiteFile = "suite.yaml"
	LockFile  = "suite.lock"
	TaskFile  = "task.yaml"
	tasksDir  = "tasks"
	probesDir = "probes"
)

// localLoader resolves refs against a benchmarks folder on disk.
type localLoader struct {
	root string // benchmarks folder holding one sub-folder per pack
}

func newLocalLoader(benchmarksRoot string) *localLoader {
	return &localLoader{root: benchmarksRoot}
}

// GetName implements schemas.Part.
func (l *localLoader) GetName() string { return LoaderLocal }

// GetVersion implements schemas.Part.
func (l *localLoader) GetVersion() string { return localLoaderVersion }

// Load resolves ref and reads the whole pack: suite.yaml, every task and
// probe it lists, and suite.lock when present. ref is a pack id
// ("coding-core"), a pinned id ("coding-core@1.0.0", which must match the
// version in suite.yaml) or a folder path (anything containing a path
// separator, or "."). Every problem found is returned, joined, each naming its
// file and field, so one run of `suite lint` shows them all.
func (l *localLoader) Load(ctx context.Context, ref string) (*schemas.Suite, error) {
	dir, wantID, wantVersion, err := l.resolve(ref)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve suite folder %s: %w", dir, err)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("suite %q: folder %s not found (%s)", ref, root, suitesBeside(filepath.Dir(root)))
	}
	d := displayer{base: filepath.Dir(root)}

	var s schemas.Suite
	suitePath := filepath.Join(root, SuiteFile)
	if err := DecodeYAMLFile(suitePath, d.name(suitePath), SchemaSuite, &s); err != nil {
		return nil, err
	}
	s.Root = root

	var errs []error
	if wantID != "" && s.ID != wantID {
		errs = append(errs, fmt.Errorf("%s: id: got %q, want %q (the folder name)", d.name(suitePath), s.ID, wantID))
	}
	if wantVersion != "" && s.Version != wantVersion {
		errs = append(errs, fmt.Errorf("%s: version: got %q, want %q (from ref %q)", d.name(suitePath), s.Version, wantVersion, ref))
	}
	if s.BaseImage.Dockerfile != "" {
		if err := checkFile(root, s.BaseImage.Dockerfile, false); err != nil {
			errs = append(errs, fmt.Errorf("%s: base_image.dockerfile: %w", d.name(suitePath), err))
		}
	}
	errs = append(errs, suiteSymlinks(&s, d)...)
	for i, sv := range s.Services {
		for _, dst := range sortedKeys(sv.Files) {
			if err := checkFile(root, sv.Files[dst], false); err != nil {
				errs = append(errs, fmt.Errorf("%s: services[%d].files[%s]: %w", d.name(suitePath), i, dst, err))
			}
		}
	}

	for _, id := range s.TaskIDs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("load suite %s: %w", s.ID, err)
		}
		t, err := loadTask(root, id, d)
		if err == nil {
			err = expandCompose(&s, t, d) // X2: a compose file becomes the task's environment and services (compose.go)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.Tasks = append(s.Tasks, t)
	}
	for _, id := range s.ProbeIDs {
		p, err := loadProbe(root, id, d)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.Probes = append(s.Probes, p)
	}
	if err := loadLock(&s, d); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("load suite %s: %w", ref, errors.Join(errs...))
	}
	return &s, nil
}

// LoadTask reads suite.yaml, suite.lock and the one task taskID of the suite
// ref names (resolved like Load against benchmarksRoot), so a task can be
// validated while other tasks of the suite are still being written. The
// suite's Tasks holds only that task and no probe is loaded. A task missing
// from the suite's tasks list still loads; Lint reports it.
func LoadTask(benchmarksRoot, ref, taskID string) (*schemas.Suite, *schemas.Task, error) {
	dir, wantID, _, err := newLocalLoader(benchmarksRoot).resolve(ref)
	if err != nil {
		return nil, nil, err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve suite folder %s: %w", dir, err)
	}
	d := displayer{base: filepath.Dir(root)}
	var s schemas.Suite
	suitePath := filepath.Join(root, SuiteFile)
	if err := DecodeYAMLFile(suitePath, d.name(suitePath), SchemaSuite, &s); err != nil {
		return nil, nil, err
	}
	s.Root = root
	if wantID != "" && s.ID != wantID {
		return nil, nil, fmt.Errorf("%s: id: got %q, want %q (the folder name)", d.name(suitePath), s.ID, wantID)
	}
	t, err := loadTask(root, taskID, d)
	if err == nil {
		err = expandCompose(&s, t, d) // X2: a compose file becomes the task's environment and services (compose.go)
	}
	if err != nil {
		return nil, nil, err
	}
	s.Tasks = []*schemas.Task{t}
	if err := loadLock(&s, d); err != nil {
		return nil, nil, err
	}
	return &s, t, nil
}

// resolve maps ref to a folder and, for id refs, the id and version the pack
// must declare.
func (l *localLoader) resolve(ref string) (dir, id, version string, err error) {
	if ref == "" {
		return "", "", "", errors.New("empty suite ref")
	}
	if ref == "." || ref == ".." || strings.ContainsAny(ref, `/\`) {
		return ref, "", "", nil
	}
	id, version, _ = strings.Cut(ref, "@")
	if id == "" {
		return "", "", "", fmt.Errorf("suite ref %q: missing id", ref)
	}
	if strings.Contains(ref, "@") && version == "" {
		return "", "", "", fmt.Errorf("suite ref %q: missing version after @", ref)
	}
	return filepath.Join(l.root, id), id, version, nil
}

// loadTask reads tasks/<id>/task.yaml and checks that every file it names
// exists inside the task folder.
func loadTask(root, id string, d displayer) (*schemas.Task, error) {
	dir := filepath.Join(root, tasksDir, id)
	path := filepath.Join(dir, TaskFile)
	var t schemas.Task
	if err := DecodeYAMLFile(path, d.name(path), SchemaTask, &t); err != nil {
		return nil, err
	}
	t.Dir = dir
	file := d.name(path)
	var errs []error
	if t.ID != id {
		errs = append(errs, fmt.Errorf("%s: id: got %q, want %q (the folder name and the suite.yaml tasks entry)", file, t.ID, id))
	}
	if _, err := utils.ResolveInside(root, filepath.Join(tasksDir, id)); err != nil {
		return nil, fmt.Errorf("%s: task folder: %w", file, err)
	}
	links, err := utils.FindSymlinks(dir, dir)
	if err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", file, err))
	}
	for _, l := range links {
		if l.Escapes {
			errs = append(errs, fmt.Errorf("%s: symlink -> %s resolves outside the task folder; replace it with the file it points to, or remove it",
				d.name(filepath.Join(dir, filepath.FromSlash(l.Path))), l.Target))
		}
	}
	check := func(field, rel string, isDir bool) {
		if err := checkFile(dir, rel, isDir); err != nil {
			errs = append(errs, fmt.Errorf("%s: %s: %w", file, field, err))
		}
	}
	check("instruction", t.Instruction, false)
	check("repo_dir", t.RepoDir, true)
	if t.Notes.Clean != "" {
		check("notes.clean", t.Notes.Clean, false)
	}
	if t.Notes.Stale != "" {
		check("notes.stale", t.Notes.Stale, false)
	}
	check("verifier.tests", t.Verifier.Tests, true)
	check("controls.reference", t.Controls.Reference, false)
	check("controls.empty", t.Controls.Empty, false)
	for i, m := range t.Controls.Mutants {
		check(fmt.Sprintf("controls.mutants[%d]", i), m, false)
	}
	check("controls.exploit", t.Controls.Exploit, false)
	errs = append(errs, scenarioLoadErrors(&t, file)...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &t, nil
}

// loadProbe reads probes/<id>.yaml.
func loadProbe(root, id string, d displayer) (*schemas.ProbeSpec, error) {
	path := filepath.Join(root, probesDir, id+".yaml")
	var p schemas.ProbeSpec
	if err := DecodeYAMLFile(path, d.name(path), SchemaProbe, &p); err != nil {
		return nil, err
	}
	if p.ID != id {
		return nil, fmt.Errorf("%s: id: got %q, want %q (the file name and the suite.yaml probes entry)", d.name(path), p.ID, id)
	}
	p.Path = path
	return &p, nil
}

// loadLock reads suite.lock when it exists. LockHash is the sha256 of the
// exact file bytes, so any edit to the lock changes the hash manifests record.
func loadLock(s *schemas.Suite, d displayer) error {
	path := filepath.Join(s.Root, LockFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: read: %w", d.name(path), err)
	}
	var lock schemas.SuiteLock
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lock); err != nil {
		return fmt.Errorf("%s: decode: %w", d.name(path), err)
	}
	if lock.Suite != s.ID {
		return fmt.Errorf("%s: suite: got %q, want %q", d.name(path), lock.Suite, s.ID)
	}
	s.Lock = &lock
	s.LockHash = utils.SHA256Bytes(data)
	return nil
}

// suiteSymlinks reports symlinks in the suite folders the bench reads on
// the host or sends to docker build (probes/, fixtures/ and the folder of the
// base Dockerfile) that resolve outside the suite folder.
func suiteSymlinks(s *schemas.Suite, d displayer) []error {
	dirs := []string{probesDir, "fixtures"}
	if s.BaseImage.Dockerfile != "" {
		dirs = append(dirs, filepath.Dir(filepath.FromSlash(s.BaseImage.Dockerfile)))
	}
	var errs []error
	seen := map[string]bool{}
	for _, rel := range dirs {
		dir := filepath.Join(s.Root, rel)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		links, err := utils.FindSymlinks(s.Root, dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, l := range links {
			if l.Escapes {
				errs = append(errs, fmt.Errorf("%s: symlink -> %s resolves outside the suite folder; replace it with the file it points to, or remove it",
					d.name(filepath.Join(dir, filepath.FromSlash(l.Path))), l.Target))
			}
		}
	}
	return errs
}

// checkFile verifies that rel, relative to base, stays inside base, also
// after resolving symlinks, and exists as a directory (isDir) or a regular
// file.
func checkFile(base, rel string, isDir bool) error {
	abs, err := resolveIn(base, rel)
	if err != nil {
		return err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("%s not found", rel)
	}
	if isDir && !fi.IsDir() {
		return fmt.Errorf("%s is not a folder", rel)
	}
	if !isDir && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", rel)
	}
	return nil
}

// resolveIn returns the real path of rel inside base and refuses absolute
// paths, paths that leave base, and paths that leave it through a symlink,
// so a task can never point the grader or the agent at files outside its
// folder (utils.ResolveInside).
func resolveIn(base, rel string) (string, error) {
	return utils.ResolveInside(base, rel)
}

// displayer shortens absolute paths in messages to "<pack>/tasks/t01/task.yaml".
type displayer struct {
	base string // parent of the pack folder
}

func (d displayer) name(path string) string {
	rel, err := filepath.Rel(d.base, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// suitesBeside names the suites in dir (folders holding a suite.yaml), so a
// wrong or placeholder suite id ("my-suite" in a fresh smoke experiment)
// shows what the project has. It is for messages only.
func suitesBeside(dir string) string {
	files, err := filepath.Glob(filepath.Join(dir, "*", SuiteFile))
	if err != nil || len(files) == 0 {
		return "no suite in " + dir + "; create one with bifrost-bench suite init <id> -template <name>"
	}
	ids := make([]string, 0, len(files))
	for _, f := range files {
		ids = append(ids, filepath.Base(filepath.Dir(f)))
	}
	sort.Strings(ids)
	return "suites here: " + strings.Join(ids, ", ")
}
