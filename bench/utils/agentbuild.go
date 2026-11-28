package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/agents"
	"github.com/maximhq/bifrost/bench/agents/history"
	"github.com/maximhq/bifrost/bench/examples/frameworks"
)

// AgentBuildPath resolves a build file or folder an agent config names
// (build.dockerfile, build.context) the way the agent's yaml resolves: the
// project's own copy first (unless it is an unedited older copy of a
// built-in agent's files, BuiltinUpgrade, which follows the current ones), then the built-in library root (suite.LibraryDir,
// the same layer the planner reads agents/<file> from), then the build files
// shipped in this binary (agents/images/**). Only agents/ paths are layered;
// a path that is in none of them, an absolute path and any other path
// resolve against project, unchanged. found reports that one of the layers
// holds it; a project path that cannot be checked (permission) counts as
// found, so the caller keeps the benefit of the doubt only there.
func AgentBuildPath(project, library, rel string) (abs string, found bool) {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel), exists(rel)
	}
	own := filepath.Join(project, filepath.FromSlash(rel))
	if _, err := os.Lstat(own); !errors.Is(err, fs.ErrNotExist) {
		if p, ok := BuiltinUpgrade(project, rel); ok {
			return p, true
		}
		return own, true // the project's own file (or link) wins, even a broken one
	}
	clean := path.Clean(filepath.ToSlash(rel))
	if !strings.HasPrefix(clean, "agents/") || strings.Contains(clean, "..") {
		return own, false
	}
	if library != "" {
		if p := filepath.Join(library, filepath.FromSlash(clean)); exists(p) {
			return p, true
		}
	}
	if root, err := BuiltinAgentsRoot(); err == nil {
		if p := filepath.Join(root, filepath.FromSlash(clean)); exists(p) {
			return p, true
		}
	}
	return own, false
}

// BuiltinUpgrade is the current built-in copy of a project's agent file
// rel when the project holds an older built-in version that the
// organization never edited, decided at read time by content hash
// (agents/history): an agents/<id>.yaml seeded verbatim, or a file or
// folder under agents/images/<id>/ whose every file is a shipped version of
// the same built-in folder (agents/images/<x>/ or examples/frameworks/<x>/,
// the agent builder copies those). Any file with another hash, a link, or a
// copy equal to the current built-in keeps the project's own; nothing in
// the project is written.
func BuiltinUpgrade(project, rel string) (string, bool) {
	clean := path.Clean(filepath.ToSlash(rel))
	if filepath.IsAbs(rel) || strings.Contains(clean, "..") || !strings.HasPrefix(clean, "agents/") {
		return "", false
	}
	root, err := BuiltinAgentsRoot()
	if err != nil {
		return "", false
	}
	own := filepath.Join(project, filepath.FromSlash(clean))
	fi, err := os.Lstat(own)
	if err != nil {
		return "", false
	}
	parts := strings.Split(clean, "/")
	if len(parts) == 2 && strings.HasSuffix(clean, ".yaml") && fi.Mode().IsRegular() {
		sum, err := fileSum(own)
		if err != nil || !hasPath(history.Paths(sum), clean) {
			return "", false
		}
		cur := filepath.Join(root, filepath.FromSlash(clean))
		if cs, err := fileSum(cur); err != nil || cs == sum {
			return "", false
		}
		return cur, true
	}
	if len(parts) < 3 || parts[1] != "images" {
		return "", false
	}
	dir, name := path.Join(parts[:3]...), path.Join(parts[3:]...)
	folder, ok := builtinFolder(filepath.Join(project, filepath.FromSlash(dir)), root)
	if !ok {
		return "", false
	}
	return filepath.Join(root, filepath.FromSlash(folder), filepath.FromSlash(name)), true
}

// builtinFolder is the built-in folder whose shipped versions every file of
// the project folder dir is, when at least one file differs from its
// current version there.
func builtinFolder(dir, root string) (string, bool) {
	var cands map[string]bool
	changed := map[string]bool{}
	ok := true
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			ok = false
			return fs.SkipAll
		}
		name, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		sum, err := fileSum(p)
		if err != nil {
			return err
		}
		next := map[string]bool{}
		for _, hp := range history.Paths(sum) {
			if f, cut := strings.CutSuffix(hp, "/"+name); cut && (cands == nil || cands[f]) {
				next[f] = true
				if cs, err := fileSum(filepath.Join(root, filepath.FromSlash(hp))); err != nil || cs != sum {
					changed[f] = true
				}
			}
		}
		cands = next
		if len(cands) == 0 {
			ok = false
			return fs.SkipAll
		}
		return nil
	})
	if err != nil || !ok {
		return "", false
	}
	best := ""
	for f := range cands {
		if changed[f] && (best == "" || f < best) {
			best = f
		}
	}
	return best, best != ""
}

func hasPath(ps []string, p string) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}

func fileSum(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

var builtinRoot struct {
	once sync.Once
	dir  string
	err  error
}

// BuiltinAgentsRoot is a folder holding agents/*.yaml, agents/images/**
// and examples/frameworks/** as shipped in this binary, written once per process under
// the temporary folder and named by a hash of the content, so a worker can
// build a built-in agent's image even when neither the organization's
// project nor the library holds its build files.
func BuiltinAgentsRoot() (string, error) {
	builtinRoot.once.Do(func() { builtinRoot.dir, builtinRoot.err = writeBuiltinAgents() })
	return builtinRoot.dir, builtinRoot.err
}

func writeBuiltinAgents() (string, error) {
	h := sha256.New()
	files := map[string][]byte{}
	for _, src := range []struct {
		fsys   fs.FS
		prefix string
	}{{agents.Files, "agents"}, {frameworks.Files, "examples/frameworks"}} {
		err := fs.WalkDir(src.fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(src.fsys, p)
			if err != nil {
				return err
			}
			files[path.Join(src.prefix, p)] = b
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	names := make([]string, 0, len(files))
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		h.Write([]byte(p + "\x00"))
		h.Write(files[p])
	}
	dir := filepath.Join(os.TempDir(), "heimdall-builtin-agents-"+hex.EncodeToString(h.Sum(nil))[:16])
	if _, err := os.Stat(filepath.Join(dir, ".complete")); err == nil {
		return dir, nil
	}
	tmp, err := os.MkdirTemp(os.TempDir(), "heimdall-builtin-agents-tmp-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, p := range names {
		b := files[p]
		dst := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, ".complete"), nil, 0o644); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if _, serr := os.Stat(filepath.Join(dir, ".complete")); serr == nil {
			return dir, nil // another process wrote it first
		}
		return "", err
	}
	return dir, nil
}
