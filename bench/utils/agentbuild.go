package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/agents"
)

// AgentBuildPath resolves a build file or folder an agent config names
// (build.dockerfile, build.context) the way the agent's yaml resolves: the
// project's own copy first, then the built-in library root (suite.LibraryDir,
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

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

var builtinRoot struct {
	once sync.Once
	dir  string
	err  error
}

// BuiltinAgentsRoot is a folder holding agents/*.yaml and agents/images/**
// as shipped in this binary (agents.Files), written once per process under
// the temporary folder and named by a hash of the content, so a worker can
// build a built-in agent's image even when neither the organization's
// project nor the library holds its build files.
func BuiltinAgentsRoot() (string, error) {
	builtinRoot.once.Do(func() { builtinRoot.dir, builtinRoot.err = writeBuiltinAgents() })
	return builtinRoot.dir, builtinRoot.err
}

func writeBuiltinAgents() (string, error) {
	h := sha256.New()
	var files []string
	err := fs.WalkDir(agents.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := agents.Files.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write([]byte(p + "\x00"))
		h.Write(b)
		files = append(files, p)
		return nil
	})
	if err != nil {
		return "", err
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
	for _, p := range files {
		b, err := agents.Files.ReadFile(p)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(tmp, "agents", filepath.FromSlash(p))
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
