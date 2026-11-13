package gates

import (
	"fmt"
	"path"
	"strings"
)

// matchGlob reports whether the slash-separated path name matches pattern.
// Segments match with path.Match; a "**" segment matches zero or more whole
// segments, so "tests/**" covers every file below tests/ at any depth. Both
// sides are normalized first, so "./tests/a.py" and "tests/a.py" are the same
// path. A malformed pattern is an error rather than a silent miss, because a
// typo in a task's protected list must not switch protection off.
func matchGlob(pattern, name string) (bool, error) {
	pattern = normPath(pattern)
	name = normPath(name)
	if pattern == "" {
		return false, nil
	}
	ok, err := matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
	if err != nil {
		return false, fmt.Errorf("glob %q: %w", pattern, err)
	}
	return ok, nil
}

// underPath reports whether name is root itself or lies below it. It is used
// for restore lists, which hold plain workspace paths like "tests" or
// "conftest.py" rather than globs; a root that contains glob metacharacters
// is also tried as a glob, with and without a trailing "/**".
func underPath(root, name string) (bool, error) {
	root = normPath(root)
	name = normPath(name)
	if root == "" || name == "" {
		return false, nil
	}
	if name == root || strings.HasPrefix(name, root+"/") {
		return true, nil
	}
	if !strings.ContainsAny(root, "*?[") {
		return false, nil
	}
	if ok, err := matchGlob(root, name); err != nil || ok {
		return ok, err
	}
	return matchGlob(root+"/**", name)
}

// normPath cleans a workspace-relative path: forward slashes, no leading "./"
// or "/", no trailing "/". An empty or "." path becomes "".
func normPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if p == "" {
		return ""
	}
	p = path.Clean("/" + p)
	p = strings.TrimPrefix(p, "/")
	return p
}

// matchSegments matches pattern segments against name segments, expanding
// "**" by backtracking. Paths are short, so the worst case stays tiny.
func matchSegments(pat, name []string) (bool, error) {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(name); i++ {
				ok, err := matchSegments(rest, name[i:])
				if err != nil || ok {
					return ok, err
				}
			}
			return false, nil
		}
		if len(name) == 0 {
			return false, nil
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0, nil
}
