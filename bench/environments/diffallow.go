package environments

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Allowlist reject reasons, as recorded in schemas.DiffReject.
const (
	RejectSymlink   = "symlink"
	RejectDevice    = "device"
	RejectSubmodule = "submodule"
	RejectOutside   = "outside_workspace"
	RejectCache     = "cache"
)

// Diff file statuses, as recorded in schemas.DiffFile.
const (
	StatusAdded    = "added"
	StatusModified = "modified"
	StatusDeleted  = "deleted"
)

// filteredDiff is the result of applying the path allowlist to a raw diff.
type filteredDiff struct {
	Kept     []byte
	Files    []schemas.DiffFile
	Rejected []schemas.DiffReject
}

// diffSection is one `diff --git` block of a git patch.
type diffSection struct {
	path   string
	status string
	modes  []string
	raw    []byte
	body   bool // X2: a hunk or binary patch has started; no more header lines
}

var diffHeader = []byte("diff --git ")

// filterDiff splits a `git diff --binary --no-renames` patch into per-file
// sections and keeps only the sections the allowlist accepts. Cache paths are
// dropped with reason cache; symlinks, submodules, unknown file modes and
// paths that leave the workspace are dropped with their own reason. The kept
// sections are returned byte for byte, so the result still applies with
// `git apply`. It is pure.
func filterDiff(raw []byte) (*filteredDiff, error) {
	sections, err := splitDiff(raw)
	if err != nil {
		return nil, err
	}
	out := &filteredDiff{Kept: []byte{}}
	for _, s := range sections {
		if reason := rejectReason(s); reason != "" {
			out.Rejected = append(out.Rejected, schemas.DiffReject{Path: s.path, Reason: reason})
			continue
		}
		out.Kept = append(out.Kept, s.raw...)
		out.Files = append(out.Files, schemas.DiffFile{Path: s.path, Status: s.status})
	}
	return out, nil
}

// splitDiff cuts raw into sections at every line that starts with
// "diff --git ". Content lines inside a hunk always start with ' ', '+', '-'
// or '\', and base85 lines of a binary patch never contain a space, so the
// header cannot appear inside a section.
func splitDiff(raw []byte) ([]diffSection, error) {
	var sections []diffSection
	var cur *diffSection
	rest := raw
	for len(rest) > 0 {
		line := rest
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line = rest[:i+1]
		}
		rest = rest[len(line):]
		if bytes.HasPrefix(line, diffHeader) {
			if cur != nil {
				sections = append(sections, *cur)
			}
			path, err := headerPath(string(trimEOL(line[len(diffHeader):])))
			if err != nil {
				return nil, err
			}
			cur = &diffSection{path: path, status: StatusModified}
		} else if cur == nil {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			return nil, fmt.Errorf("parse diff: text before the first diff header: %q", truncate(string(line), 80))
		}
		cur.raw = append(cur.raw, line...)
		if err := cur.readExtendedHeader(string(trimEOL(line))); err != nil {
			return nil, err
		}
	}
	if cur != nil {
		sections = append(sections, *cur)
	}
	return sections, nil
}

// readExtendedHeader records the mode, status and path a header line carries.
// Lines that are not header lines change nothing.
func (s *diffSection) readExtendedHeader(line string) error {
	// X2: after the first hunk (or binary patch) a line is content: a removed
	// line "-- note" (an SQL or Lua comment) reads "--- note" and is no marker.
	if s.body || strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "GIT binary patch") {
		s.body = true
		return nil
	}
	switch {
	case strings.HasPrefix(line, "new file mode "):
		s.status = StatusAdded
		s.modes = append(s.modes, strings.TrimPrefix(line, "new file mode "))
	case strings.HasPrefix(line, "deleted file mode "):
		s.status = StatusDeleted
		s.modes = append(s.modes, strings.TrimPrefix(line, "deleted file mode "))
	case strings.HasPrefix(line, "old mode "):
		s.modes = append(s.modes, strings.TrimPrefix(line, "old mode "))
	case strings.HasPrefix(line, "new mode "):
		s.modes = append(s.modes, strings.TrimPrefix(line, "new mode "))
	case strings.HasPrefix(line, "index "):
		if f := strings.Fields(line); len(f) == 3 {
			s.modes = append(s.modes, f[2])
		}
	case strings.HasPrefix(line, "--- ") && line != "--- /dev/null":
		p, err := markerPath(strings.TrimPrefix(line, "--- "), "a/")
		if err != nil {
			return err
		}
		s.path = p
	case strings.HasPrefix(line, "+++ ") && line != "+++ /dev/null":
		p, err := markerPath(strings.TrimPrefix(line, "+++ "), "b/")
		if err != nil {
			return err
		}
		s.path = p
	}
	return nil
}

// rejectReason returns why the allowlist drops s, or "" to keep it. Security
// reasons win over cache, so a symlink inside __pycache__ is still reported as
// a symlink.
func rejectReason(s diffSection) string {
	if outsideWorkspace(s.path) {
		return RejectOutside
	}
	for _, m := range s.modes {
		switch m {
		case "160000":
			return RejectSubmodule
		case "120000":
			return RejectSymlink
		case "100644", "100755":
		default:
			return RejectDevice
		}
	}
	if cachePath(s.path) {
		return RejectCache
	}
	return ""
}

// outsideWorkspace reports paths that are empty, absolute or climb out with "..".
func outsideWorkspace(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return true
	}
	for _, c := range strings.Split(p, "/") {
		if c == ".." {
			return true
		}
	}
	return false
}

// cachePath reports interpreter and VCS caches that never belong in a patch.
func cachePath(p string) bool {
	for _, c := range strings.Split(p, "/") {
		switch strings.ToLower(c) {
		case ".git", "__pycache__", ".pytest_cache":
			return true
		}
	}
	return strings.HasSuffix(p, ".pyc")
}

// headerPath extracts the path from the part of a `diff --git` line after the
// command. With --no-renames both sides name the same path, which makes an
// unquoted header with spaces unambiguous.
func headerPath(rest string) (string, error) {
	if strings.HasPrefix(rest, `"`) {
		a, _, err := unquoteC(rest)
		if err != nil {
			return "", fmt.Errorf("parse diff header %q: %w", truncate(rest, 80), err)
		}
		if !strings.HasPrefix(a, "a/") {
			return "", fmt.Errorf("parse diff header %q: missing a/ prefix", truncate(rest, 80))
		}
		return a[2:], nil
	}
	if strings.HasPrefix(rest, "a/") && (len(rest)-5)%2 == 0 && len(rest) >= 5 {
		n := (len(rest) - 5) / 2
		left, sep, right := rest[2:2+n], rest[2+n:5+n], rest[5+n:]
		if sep == " b/" && left == right {
			return left, nil
		}
	}
	return "", fmt.Errorf("parse diff header %q: cannot find the path", truncate(rest, 80))
}

// markerPath parses the path of a ---/+++ line. Git appends a tab when the
// name contains a space.
func markerPath(v, prefix string) (string, error) {
	if strings.HasPrefix(v, `"`) {
		p, _, err := unquoteC(v)
		if err != nil {
			return "", fmt.Errorf("parse diff marker %q: %w", truncate(v, 80), err)
		}
		v = p
	} else {
		v = strings.TrimSuffix(v, "\t")
	}
	if !strings.HasPrefix(v, prefix) {
		return "", fmt.Errorf("parse diff marker %q: missing %s prefix", truncate(v, 80), prefix)
	}
	return v[len(prefix):], nil
}

// unquoteC decodes one C-style quoted string as git writes it (core.quotePath)
// from the start of s and returns it with the unparsed remainder.
func unquoteC(s string) (string, string, error) {
	if !strings.HasPrefix(s, `"`) {
		return "", s, errors.New("not quoted")
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			return b.String(), s[i+1:], nil
		case c != '\\':
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch e := s[i]; e {
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'v':
			b.WriteByte('\v')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case '"', '\\':
			b.WriteByte(e)
		default:
			if i+2 >= len(s) || !isOctal(e) || !isOctal(s[i+1]) || !isOctal(s[i+2]) {
				return "", s, fmt.Errorf("bad escape at byte %d", i)
			}
			b.WriteByte((e-'0')<<6 | (s[i+1]-'0')<<3 | (s[i+2] - '0'))
			i += 2
		}
	}
	return "", s, errors.New("unterminated quote")
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

func trimEOL(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
