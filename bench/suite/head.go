package suite

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// LoadHead reads the cheap part of the pack in dir: suite.yaml and
// suite.lock. No task file, probe or fixture is opened, so it costs the same
// for a 5-task pack and a 5,000-task pack with gigabytes of audio. The
// returned suite has no Tasks or Probes; its TaskIDs come from suite.yaml or,
// when suite.yaml lists none, from the folders under tasks/. The suite list
// serves this while the full load (Load) runs in the background.
func LoadHead(dir string) (*schemas.Suite, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve suite folder %s: %w", dir, err)
	}
	d := displayer{base: filepath.Dir(root)}
	var s schemas.Suite
	suitePath := filepath.Join(root, SuiteFile)
	if err := DecodeYAMLFile(suitePath, d.name(suitePath), SchemaSuite, &s); err != nil {
		return nil, err
	}
	s.Root = root
	if len(s.TaskIDs) == 0 {
		if entries, err := os.ReadDir(filepath.Join(root, tasksDir)); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					s.TaskIDs = append(s.TaskIDs, e.Name())
				}
			}
		}
	}
	if err := loadLock(&s, d); err != nil {
		return nil, err
	}
	return &s, nil
}

// Stamp is a cache key of the pack in dir that changes when suite.yaml,
// suite.lock, the quarantine sidecar or the tasks folder listing changes (path plus their sizes and
// modification times). perTask also folds in every tasks/<id>/ folder and
// its task.yaml, so an in-place task edit changes it too; it is meant for an
// organization's own packs, which are small and edited on the web. A
// library pack is read-only and skips it.
func Stamp(dir string, perTask bool) string {
	var b strings.Builder
	b.WriteString(filepath.Clean(dir))
	add := func(path string) {
		fi, err := os.Stat(path)
		if err != nil {
			b.WriteString("|-")
			return
		}
		b.WriteString("|")
		b.WriteString(strconv.FormatInt(fi.ModTime().UnixNano(), 36))
		b.WriteString(":")
		b.WriteString(strconv.FormatInt(fi.Size(), 36))
	}
	add(filepath.Join(dir, SuiteFile))
	add(filepath.Join(dir, LockFile))
	add(filepath.Join(dir, schemas.SuiteHealthFileName)) // quarantine sidecar
	add(filepath.Join(dir, schemas.TaskReviewFileName))  // review sidecar of generated tasks
	tasks := filepath.Join(dir, tasksDir)
	add(tasks)
	if !perTask {
		return b.String()
	}
	entries, err := os.ReadDir(tasks)
	if err != nil {
		return b.String()
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b.WriteString("|" + e.Name())
		add(filepath.Join(tasks, e.Name()))
		add(filepath.Join(tasks, e.Name(), TaskFile))
	}
	return b.String()
}

// LoadCatalogHead reads what the catalog lists of pack ref under
// benchmarksRoot (with the built-in library layered in, as Load resolves
// it): suite.yaml, every probe it names and suite.lock, but no task file.
// TaskIDs are exactly the ones suite.yaml lists, as after a full Load. It
// costs the same for a 5-task and a 5,000-task pack, so building the
// catalog over every library pack no longer loads every task of each.
func LoadCatalogHead(benchmarksRoot, ref string) (*schemas.Suite, error) {
	dir, wantID, wantVersion, err := newLocalLoader(benchmarksRoot).resolve(ref)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve suite folder %s: %w", dir, err)
	}
	d := displayer{base: filepath.Dir(root)}
	var s schemas.Suite
	suitePath := filepath.Join(root, SuiteFile)
	if err := DecodeYAMLFile(suitePath, d.name(suitePath), SchemaSuite, &s); err != nil {
		return nil, err
	}
	s.Root = root
	if wantID != "" && s.ID != wantID {
		return nil, fmt.Errorf("%s: id: got %q, want %q (the folder name)", d.name(suitePath), s.ID, wantID)
	}
	if wantVersion != "" && s.Version != wantVersion {
		return nil, fmt.Errorf("%s: version: got %q, want %q (from ref %q)", d.name(suitePath), s.Version, wantVersion, ref)
	}
	for _, id := range s.ProbeIDs {
		p, err := loadProbe(root, id, d)
		if err != nil {
			return nil, err
		}
		s.Probes = append(s.Probes, p)
	}
	if err := loadLock(&s, d); err != nil {
		return nil, err
	}
	return &s, nil
}
