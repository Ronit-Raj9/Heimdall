// Package importers converts tasks of public benchmarks into bench suites.
//
// Each format lives in its own subpackage and registers itself from init().
// An importer reads a local copy of the upstream benchmark (a clone or a
// downloaded dataset), never the network, and writes an ordinary suite under
// benchmarks/<suite>: after import the suite is linted, locked, admitted and
// run like a hand-written one. Upstream task data is never committed to this
// repository; Finish writes a .gitignore into the suite folder.
package importers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Info describes an importer to `bifrost-bench import list` and the UI.
type Info struct {
	Name       string   `json:"name"`       // format name, e.g. harbor
	Benchmarks []string `json:"benchmarks"` // upstream benchmarks in this format, e.g. terminal-bench@2.1
	Source     string   `json:"source"`     // where to get the data (a clone or download command)
	License    string   `json:"license"`    // license as upstream states it; "varies" when per task
	Track      string   `json:"track"`      // schemas.Track* the imported suite gets
	Grading    string   `json:"grading"`    // schemas.GradingVerified or GradingJudged
	Needs      []string `json:"needs"`      // what a run needs beyond the default stack, in plain words
	Summary    string   `json:"summary"`
}

// Options are the inputs of one import.
type Options struct {
	BenchRoot string // the project folder (holds benchmarks/)
	// BenchmarksDir is the resolved benchmarks_root of the project config.
	// Empty means BenchRoot/benchmarks.
	BenchmarksDir string
	From          string            // local path of the upstream data
	SuiteID       string            // suite to create or extend
	IDs           []string          // upstream task ids to import; empty means all, up to Limit
	Limit         int               // at most this many tasks; 0 means no limit
	Force         bool              // replace task folders that already exist
	Extra         map[string]string // importer-specific settings from repeated -set key=value
	Logger        schemas.Logger
}

// Skip is an upstream task the importer left out, with the reason.
type Skip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Result is what one import wrote.
type Result struct {
	Suite    string   `json:"suite"`
	SuiteDir string   `json:"suite_dir"`
	Tasks    []string `json:"tasks"`   // task ids written
	Skipped  []Skip   `json:"skipped"` // upstream tasks left out
	Notes    []string `json:"notes"`   // what the user must do next, in order
}

// Importer converts one upstream format.
type Importer interface {
	Info() Info
	Import(ctx context.Context, opts Options) (*Result, error)
}

var (
	mu       sync.RWMutex
	registry = map[string]Importer{}
)

// Register adds an importer. It panics on a duplicate name: that is a
// programming error caught at start.
func Register(im Importer) {
	mu.Lock()
	defer mu.Unlock()
	name := im.Info().Name
	if _, dup := registry[name]; dup {
		panic("importers: duplicate importer " + name)
	}
	registry[name] = im
}

// Get returns the importer named name.
func Get(name string) (Importer, bool) {
	mu.RLock()
	defer mu.RUnlock()
	im, ok := registry[name]
	return im, ok
}

// All returns every importer's Info, sorted by name.
func All() []Info {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Info, 0, len(registry))
	for _, im := range registry {
		out = append(out, im.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SuiteDir is where this import writes its suite: under the configured
// benchmarks root when one is set, so the suite commands that read
// benchmarks_root find it (finding 7-C-09).
func (o Options) SuiteDir() string {
	if o.BenchmarksDir != "" {
		return filepath.Join(o.BenchmarksDir, o.SuiteID)
	}
	return SuiteDir(o.BenchRoot, o.SuiteID)
}

// SuiteDir is where an imported suite is written under the default
// benchmarks root.
func SuiteDir(benchRoot, suiteID string) string {
	return filepath.Join(benchRoot, "benchmarks", suiteID)
}

// Finish writes the .gitignore that keeps imported upstream data out of git.
// Importers call it last.
func Finish(suiteDir string) error {
	body := "# Imported benchmark data: fetched with `bifrost-bench import`, never committed.\n*\n"
	if err := os.WriteFile(filepath.Join(suiteDir, ".gitignore"), []byte(body), 0o644); err != nil {
		return fmt.Errorf("importers: write .gitignore: %w", err)
	}
	return nil
}

// NoTaskError is the error of an import that wrote no task: it names the
// first three skipped tasks with their reasons, so the reason reaches the
// person even where the skip list is not shown (fix wave 3, N263).
func NoTaskError(name string, skipped []Skip) error {
	msg := name + ": no task was imported"
	if len(skipped) == 0 {
		return errors.New(msg)
	}
	parts := make([]string, 0, 4)
	for i, s := range skipped {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more (listed in the job log)", len(skipped)-3))
			break
		}
		parts = append(parts, s.ID+": "+s.Reason)
	}
	return errors.New(msg + ". " + strings.Join(parts, "; "))
}
