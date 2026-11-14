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

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// serveSuites reads the benchmark packs for GET /suites through the suite
// package, so the page shows a pack exactly as `suite lint` loads it and a
// lock exactly as `suite lock` would rewrite it.
type serveSuites struct {
	root string // benchmarks folder
}

// SuiteIDs lists the folders under the benchmarks root that hold a
// suite.yaml. A missing benchmarks folder is an empty list.
func (s serveSuites) SuiteIDs(context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read benchmarks folder %s: %w", s.root, err)
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		// "@" would be read as a pinned version by the loader.
		if !e.IsDir() || strings.HasPrefix(name, ".") || strings.Contains(name, "@") {
			continue
		}
		if fi, err := os.Stat(filepath.Join(s.root, name, suite.SuiteFile)); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		ids = append(ids, name)
	}
	sort.Strings(ids)
	return ids, nil
}

// LoadSuite loads one pack by id.
func (s serveSuites) LoadSuite(ctx context.Context, id string) (*schemas.Suite, error) {
	return loadSuite(ctx, s.root, id)
}

// LockDrift locks the loaded files afresh in memory and diffs that against
// suite.lock. Nothing is written.
func (s serveSuites) LockDrift(_ context.Context, st *schemas.Suite) ([]schemas.SuiteDrift, error) {
	fresh, err := suite.Lock(st, time.Now().UTC())
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

// serveAgents lists agents/*.yaml for GET /agents with the planner's
// schema-checked decoder.
type serveAgents struct {
	root string // bench root
}

// AgentConfigs reads every agents/*.yaml, sorted by file name. A file that
// fails to decode is returned with its error.
func (s serveAgents) AgentConfigs(context.Context) ([]handlers.AgentConfigFile, error) {
	files, err := filepath.Glob(filepath.Join(s.root, "agents", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list agent configs: %w", err)
	}
	sort.Strings(files)
	out := make([]handlers.AgentConfigFile, 0, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(s.root, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		rel = filepath.ToSlash(rel)
		var cfg schemas.AgentConfig
		if err := suite.DecodeYAMLFile(f, rel, suite.SchemaAgent, &cfg); err != nil {
			out = append(out, handlers.AgentConfigFile{Path: rel, Err: err})
			continue
		}
		out = append(out, handlers.AgentConfigFile{Path: rel, Config: &cfg})
	}
	return out, nil
}
