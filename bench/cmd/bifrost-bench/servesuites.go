package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// serveSuites reads the benchmark packs for GET /suites through the suite
// package, so the page shows a pack exactly as `suite lint` loads it and a
// lock exactly as `suite lock` would rewrite it.
type serveSuites struct {
	root string            // benchmarks folder
	cfg  *lib.Config       // bench config for the helper and tool proxy image roles; nil leaves them out
	reg  *schemas.Registry // the graders whose versions a task hash covers; nil means task hashes are unavailable
	// library is the built-in library's benchmarks folder (suite/library.go):
	// its packs are listed beside root's and win by suite.PickSuiteDir. "" for none.
	library string
}

// TaskHash implements the Voice tab's task identity check: the hash a plan
// records on a slot, of the task as it loads now.
func (s serveSuites) TaskHash(st *schemas.Suite, t *schemas.Task) (string, error) {
	if s.reg == nil {
		return "", errors.New("no registry to hash tasks with")
	}
	return engine.NewPlanner(s.reg, "", nil).TaskHash(st, t)
}

// SuiteIDs lists the folders under the benchmarks root that hold a
// suite.yaml. A missing benchmarks folder is an empty list.
func (s serveSuites) SuiteIDs(context.Context) ([]string, error) {
	ids, err := suiteIDsIn(s.root)
	if err != nil || s.library == "" {
		return ids, err
	}
	lib, err := suiteIDsIn(s.library)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ids)+len(lib))
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range lib {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// SuiteBuiltin reports that suite id loads from the built-in library
// (GET /suites marks it builtin, source "library").
func (s serveSuites) SuiteBuiltin(id string) bool {
	_, lib := suite.PickSuiteDir(s.root, s.library, id)
	return lib
}

// suiteIDsIn lists the folders under root that hold a suite.yaml.
func suiteIDsIn(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read benchmarks folder %s: %w", root, err)
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		// "@" would be read as a pinned version by the loader.
		if !e.IsDir() || strings.HasPrefix(name, ".") || strings.Contains(name, "@") {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, name, suite.SuiteFile)); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		ids = append(ids, name)
	}
	sort.Strings(ids)
	return ids, nil
}

// LoadSuite loads one pack by id.
func (s serveSuites) LoadSuite(ctx context.Context, id string) (*schemas.Suite, error) {
	if s.library != "" {
		if dir, lib := suite.PickSuiteDir(s.root, s.library, id); lib {
			return s.loadLibraryCached(ctx, dir, id) // read-only and large: cached per stamp (servesuitecache.go)
		}
		return suite.Layered(s.root, s.library)[0].Load(ctx, id)
	}
	return loadSuite(ctx, s.root, id)
}

// LockDrift locks the loaded files afresh in memory and diffs that against
// suite.lock. Nothing is written. A fingerprinted lock is relocked with
// fingerprints, so drift names fingerprint fields too; a legacy lock is
// relocked without them, as before, so it never reads as stale only for
// lacking the section.
func (s serveSuites) LockDrift(ctx context.Context, st *schemas.Suite) ([]schemas.SuiteDrift, error) {
	var fresh *schemas.SuiteLock
	var err error
	if st.Lock != nil && st.Lock.Fingerprints != nil {
		fresh, err = suite.LockWith(st, time.Now().UTC(), s.fingerprintOptions(ctx, st))
	} else {
		fresh, err = suite.Lock(st, time.Now().UTC())
	}
	if err != nil {
		return nil, fmt.Errorf("hash suite %s: %w", st.ID, err)
	}
	changes := suite.Diff(st.Lock, fresh)
	out := make([]schemas.SuiteDrift, 0, len(changes))
	for _, c := range changes {
		out = append(out, schemas.SuiteDrift{
			Scope: c.Scope, ID: c.ID, Level: string(c.Level), Action: c.Action, Fields: append([]string{}, c.Fields...),
		})
	}
	return out, nil
}

// FingerprintDrift recomputes the lock fingerprints of st and lists what no
// longer matches suite.lock (docs/lock-fingerprints.md); the planner refuses
// while it is not empty. A legacy lock has none. Warnings (an image that
// cannot be inspected here) are not drift and are left out.
func (s serveSuites) FingerprintDrift(ctx context.Context, st *schemas.Suite) ([]schemas.FingerprintDrift, error) {
	drift, _, err := suite.CheckFingerprints(st, s.fingerprintOptions(ctx, st))
	if err != nil {
		return nil, fmt.Errorf("check fingerprints of suite %s: %w", st.ID, err)
	}
	return drift, nil
}

// fingerprintOptions binds what `suite lock` binds, pinning local image ids
// only when st's lock already does. Without a config no helper role is
// computed; a role missing on one side is only a warning, never drift.
func (s serveSuites) fingerprintOptions(ctx context.Context, st *schemas.Suite) suite.FingerprintOptions {
	return lockFingerprintOptions(ctx, s.cfg, utils.Docker{}, suite.LockPinsLocal(st.Lock))
}

// serveAgents lists agents/*.yaml for GET /agents with the planner's
// schema-checked decoder.
type serveAgents struct {
	root    string // bench root
	library string // built-in library root (suite/library.go): its agents/*.yaml are listed when root has no file of that name
}

// AgentConfigs reads every agents/*.yaml, sorted by file name. A file that
// fails to decode is returned with its error.
func (s serveAgents) AgentConfigs(context.Context) ([]handlers.AgentConfigFile, error) {
	files, err := filepath.Glob(filepath.Join(s.root, "agents", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list agent configs: %w", err)
	}
	if s.library != "" {
		lib, _ := filepath.Glob(filepath.Join(suite.LibraryAgents(s.library), "*.yaml"))
		for _, f := range lib {
			if _, err := os.Lstat(filepath.Join(s.root, "agents", filepath.Base(f))); errors.Is(err, os.ErrNotExist) {
				files = append(files, f)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return filepath.Base(files[i]) < filepath.Base(files[j]) })
	out := make([]handlers.AgentConfigFile, 0, len(files))
	for _, f := range files {
		// Library configs resolve as agents/<file> too (engine.WithAgentLibrary).
		rel := "agents/" + filepath.Base(f)
		var cfg schemas.AgentConfig
		if err := suite.DecodeYAMLFile(f, rel, suite.SchemaAgent, &cfg); err != nil {
			out = append(out, handlers.AgentConfigFile{Path: rel, Err: err})
			continue
		}
		out = append(out, handlers.AgentConfigFile{Path: rel, Config: &cfg})
	}
	return out, nil
}
