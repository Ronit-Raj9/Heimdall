package agentdojo

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// upstream is the local AgentDojo clone the import reads.
type upstream struct {
	root     string
	version  string // package version from pyproject.toml
	license  string // SPDX id read from LICENSE
	revision string // commit of the clone; empty when it is not a git clone
}

// packageFiles are the files of the clone the image installs from, besides
// src/agentdojo. README.md is the package's readme, which the build reads.
var packageFiles = []string{"pyproject.toml", "README.md", "LICENSE"}

var (
	pyprojectVersion = regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`)
	pyprojectName    = regexp.MustCompile(`(?m)^name\s*=\s*"agentdojo"`)
	commitRE         = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// openUpstream checks that root is an AgentDojo clone holding the suite and
// reads its version, license and commit.
func openUpstream(root, suite string) (*upstream, error) {
	b, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if err != nil {
		return nil, fmt.Errorf("%s is not an AgentDojo clone: %w", root, err)
	}
	if !pyprojectName.Match(b) {
		return nil, fmt.Errorf("%s/pyproject.toml is not the agentdojo package", root)
	}
	m := pyprojectVersion.FindSubmatch(b)
	if m == nil {
		return nil, fmt.Errorf("%s/pyproject.toml names no version", root)
	}
	up := &upstream{root: root, version: string(m[1])}
	for _, f := range packageFiles {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			return nil, fmt.Errorf("clone is missing %s: %w", f, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(root, "src", "agentdojo", "data", "suites", suite)); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("clone has no suite %q under src/agentdojo/data/suites", suite)
	}
	if up.license, err = readLicense(filepath.Join(root, "LICENSE")); err != nil {
		return nil, err
	}
	if up.revision, err = gitHead(root); err != nil {
		return nil, err
	}
	return up, nil
}

// readLicense returns the SPDX id of the license file's first line. Only
// MIT, the license upstream ships, is recognized; anything else is reported
// rather than guessed.
func readLicense(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() && strings.TrimSpace(sc.Text()) == "MIT License" {
		return "MIT", nil
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return "", fmt.Errorf("%s is not the MIT license upstream ships; check the clone", path)
}

// gitHead returns the commit the clone has checked out, read from .git
// without running git. It is empty when root is not a git clone.
func gitHead(root string) (string, error) {
	head, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	h := strings.TrimSpace(string(head))
	ref, isRef := strings.CutPrefix(h, "ref: ")
	if !isRef {
		if commitRE.MatchString(h) {
			return h, nil
		}
		return "", fmt.Errorf("unreadable .git/HEAD %q", h)
	}
	if b, err := os.ReadFile(filepath.Join(root, ".git", filepath.FromSlash(ref))); err == nil {
		if c := strings.TrimSpace(string(b)); commitRE.MatchString(c) {
			return c, nil
		}
	}
	packed, err := os.ReadFile(filepath.Join(root, ".git", "packed-refs"))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	for _, line := range strings.Split(string(packed), "\n") {
		if c, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref && commitRE.MatchString(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("resolve %s: not in .git", ref)
}

// packageTree returns the files the image installs from, as slash paths
// relative to the clone root mapped to their content: packageFiles and
// every regular file under src/agentdojo except Python caches. Symlinks are
// refused: a clone is untrusted input and a link could point anywhere.
func (u *upstream) packageTree() (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, f := range packageFiles {
		b, err := os.ReadFile(filepath.Join(u.root, f))
		if err != nil {
			return nil, err
		}
		out[f] = b
	}
	src := filepath.Join(u.root, "src", "agentdojo")
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", path)
		}
		if !d.Type().IsRegular() || strings.HasSuffix(d.Name(), ".pyc") {
			return nil
		}
		rel, err := filepath.Rel(u.root, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read package sources: %w", err)
	}
	return out, nil
}
