package codescan

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// maxDiffBytes caps how much diff text the scan reads, like the gates' own
// fence: the exporter already caps the workspace.
const maxDiffBytes = 64 << 20

// Line kinds in a hunk body.
const (
	kindContext = ' '
	kindAdded   = '+'
	kindRemoved = '-'
)

// fileChange is one file's section of a unified git diff.
type fileChange struct {
	OldPath string // without the a/ prefix; "" for /dev/null
	NewPath string // without the b/ prefix; "" for /dev/null
	Status  string // added | modified | deleted
	Binary  bool
	Hunks   []hunk
}

// path is the file's name in the post-run tree, or its old name when it was
// deleted.
func (f *fileChange) path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// hunk is one @@ block.
type hunk struct {
	Lines []hunkLine
}

// hunkLine is one body line of a hunk with its numbers: New is the line
// number in the post-run file (0 for a removed line), Old in the pre-run file
// (0 for an added line), Patch the 1-based line of the diff text.
type hunkLine struct {
	Kind  byte
	Text  string // without the leading ' ', '+' or '-'
	New   int
	Old   int
	Patch int
}

// parseDiff splits git diff text into per-file sections in diff order. Hunk
// bodies are consumed by their declared line counts, so a removed line that
// starts with "--" is never taken for a file header. It is pure.
func parseDiff(text []byte) ([]*fileChange, error) {
	var out []*fileChange
	var cur *fileChange
	var h *hunk
	oldLeft, newLeft, oldNo, newNo := 0, 0, 0, 0
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), maxDiffBytes)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if h != nil && (oldLeft > 0 || newLeft > 0) {
			hl := hunkLine{Patch: n}
			switch {
			case strings.HasPrefix(line, `\`): // "\ No newline at end of file"
				continue
			case strings.HasPrefix(line, "+"):
				hl.Kind, hl.Text, hl.New = kindAdded, line[1:], newNo
				newNo++
				newLeft--
			case strings.HasPrefix(line, "-"):
				hl.Kind, hl.Text, hl.Old = kindRemoved, line[1:], oldNo
				oldNo++
				oldLeft--
			default: // context; an empty line is a context line whose space was trimmed
				hl.Kind, hl.New, hl.Old = kindContext, newNo, oldNo
				if line != "" {
					hl.Text = line[1:]
				}
				newNo++
				oldNo++
				oldLeft--
				newLeft--
			}
			h.Lines = append(h.Lines, hl)
			continue
		}
		h = nil
		switch {
		case strings.HasPrefix(line, "diff --git "):
			cur = &fileChange{Status: "modified"}
			out = append(out, cur)
		case cur == nil:
			// Text before the first file header.
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = "deleted"
		case strings.HasPrefix(line, "Binary files ") || line == "GIT binary patch":
			cur.Binary = true
		case strings.HasPrefix(line, "--- "):
			cur.OldPath = diffPath(strings.TrimPrefix(line, "--- "), "a/")
		case strings.HasPrefix(line, "+++ "):
			cur.NewPath = diffPath(strings.TrimPrefix(line, "+++ "), "b/")
		case strings.HasPrefix(line, "@@ "):
			oldStart, oldCount, newStart, newCount, err := parseHunkHeader(line)
			if err != nil {
				return nil, fmt.Errorf("diff line %d: %w", n, err)
			}
			cur.Hunks = append(cur.Hunks, hunk{})
			h = &cur.Hunks[len(cur.Hunks)-1]
			oldLeft, newLeft, oldNo, newNo = oldCount, newCount, oldStart, newStart
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
	}
	for _, f := range out {
		if f.NewPath == "" && f.OldPath == "" {
			f.NewPath = gitHeaderPath(f)
		}
	}
	return out, nil
}

// gitHeaderPath is a placeholder name for a section with no ---/+++ lines (a
// binary or mode-only change); the scan skips such a section anyway.
func gitHeaderPath(f *fileChange) string {
	if f.Binary {
		return "(binary)"
	}
	return "(no content)"
}

// parseHunkHeader reads "@@ -a,b +c,d @@ ...". A missing count means 1.
func parseHunkHeader(line string) (int, int, int, int, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, 0, 0, fmt.Errorf("bad hunk header %q", line)
	}
	oldStart, oldCount, err := parseRange(fields[1][1:])
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("bad hunk header %q: %w", line, err)
	}
	newStart, newCount, err := parseRange(fields[2][1:])
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("bad hunk header %q: %w", line, err)
	}
	return oldStart, oldCount, newStart, newCount, nil
}

// parseRange reads "start,count" or "start".
func parseRange(s string) (int, int, error) {
	start, count, found := strings.Cut(s, ",")
	a, err := strconv.Atoi(start)
	if err != nil {
		return 0, 0, err
	}
	if !found {
		return a, 1, nil
	}
	b, err := strconv.Atoi(count)
	if err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

// diffPath strips the a/ or b/ prefix and unquotes git's C-style quoting.
// /dev/null becomes "".
func diffPath(s, prefix string) string {
	s = strings.TrimRight(s, "\t")
	if i := strings.IndexByte(s, '\t'); i >= 0 && !strings.HasPrefix(s, `"`) {
		s = s[:i]
	}
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			s = u
		}
	}
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, prefix)
}
