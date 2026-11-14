package utils

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ResolveInside returns the real absolute path of rel inside root, with
// every symlink on the way resolved. It refuses a rel that is absolute, that
// leaves root lexically, that does not exist, or whose real path lies
// outside root's real path. A lexical check alone is not enough: os.Open,
// Docker bind mounts and docker build contexts all follow symlinks, so a
// task file or folder that is a link (or sits under one) could hand any host
// file to the agent. Callers mount or read the returned path and resolve
// again right before they do, so a path swapped after the suite was loaded
// is caught too; docs/task-safety.md describes the race that remains.
func ResolveInside(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty path")
	}
	native := filepath.FromSlash(rel)
	if filepath.IsAbs(native) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%s: want a path relative to the folder", rel)
	}
	if !filepath.IsLocal(native) {
		return "", fmt.Errorf("%s: path leaves the folder", rel)
	}
	realRoot, err := realPath(root)
	if err != nil {
		return "", err
	}
	// Joined without cleaning, so a ".." after a symlink climbs from the
	// link's target, as the kernel does, instead of cancelling the link.
	real, err := filepath.EvalSymlinks(realRoot + string(filepath.Separator) + native)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s not found", rel)
	}
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", rel, err)
	}
	if !within(realRoot, real) {
		return "", fmt.Errorf("%s: resolves outside the folder through a symlink", rel)
	}
	return real, nil
}

// ReadFileInside reads rel inside root through an os.Root, which follows
// symlinks only while they stay inside root, so a link swapped in after
// ResolveInside checked the path still cannot read a file outside it.
func ReadFileInside(root, rel string) ([]byte, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", root, err)
	}
	defer r.Close()
	b, err := r.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	return b, nil
}

// Symlink is one symbolic link found under a folder.
type Symlink struct {
	Path    string // slash-separated, relative to the folder walked
	Target  string // the link text
	Escapes bool   // resolves outside the root, is absolute, or cannot be resolved
}

// FindSymlinks walks dir without following symlinks and lists every link in
// it, marking the ones whose real target lies outside root. An absolute
// target always escapes: it names a host path that means something else on
// another machine and inside a container. A link that cannot be resolved (a
// loop) escapes too. A dangling link is judged by where it would point. A
// missing dir has no links.
func FindSymlinks(root, dir string) ([]Symlink, error) {
	realRoot, err := realPath(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var out []Symlink
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		r, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, Symlink{Path: filepath.ToSlash(r), Target: target, Escapes: !linkInside(realRoot, p, target)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan symlinks in %s: %w", dir, err)
	}
	return out, nil
}

// realPath is the absolute path of dir with every symlink resolved.
func realPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	return real, nil
}

// linkInside reports whether the link at path link, holding target, resolves
// inside realRoot. For a dangling link it resolves the longest existing
// prefix of the target and appends the missing rest.
func linkInside(realRoot, link, target string) bool {
	if target == "" || filepath.IsAbs(target) {
		return false
	}
	real, err := filepath.EvalSymlinks(link)
	if err == nil {
		return within(realRoot, real)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(link))
	if err != nil {
		return false
	}
	sep := string(filepath.Separator)
	parts := strings.Split(filepath.FromSlash(target), sep)
	for k := len(parts); k >= 0; k-- {
		prefix, err := filepath.EvalSymlinks(parent + sep + strings.Join(parts[:k], sep))
		if err != nil {
			continue
		}
		return within(realRoot, filepath.Join(append([]string{prefix}, parts[k:]...)...))
	}
	return false
}

// within reports whether p is root or lies under it. Both are clean
// absolute paths.
func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && (r == "." || filepath.IsLocal(r))
}
