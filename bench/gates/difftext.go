package gates

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// maxDiffBytes caps how much diff text a gate reads. The diff exporter already
// caps the workspace; this is a second fence so a gate never loads an
// unbounded file into memory.
const maxDiffBytes = 64 << 20

// fileDiff is one file's section of a unified diff.
type fileDiff struct {
	OldPath string // without the a/ prefix; "" for /dev/null
	NewPath string // without the b/ prefix; "" for /dev/null
	Hunks   []hunk
}

// hunk is one @@ block. Lines keep their leading ' ', '+' or '-'.
type hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []string
}

// readDiffText reads the exported diff text from the run folder. The diff was
// written by the trusted exporter; the host only reads it as data.
func readDiffText(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open diff %s: %w", p, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxDiffBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read diff %s: %w", p, err)
	}
	if len(b) > maxDiffBytes {
		return nil, fmt.Errorf("diff %s exceeds %d bytes", p, maxDiffBytes)
	}
	return b, nil
}

// parseUnifiedDiff splits git diff text into per-file sections keyed by path
// (the new path, or the old path for deletions). Hunk bodies are consumed by
// their declared line counts, so a removed line that starts with "--" is never
// mistaken for a file header. It is pure: no I/O, no logging.
func parseUnifiedDiff(text []byte) (map[string]*fileDiff, error) {
	out := map[string]*fileDiff{}
	var cur *fileDiff
	var h *hunk
	oldLeft, newLeft := 0, 0
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), maxDiffBytes)
	for sc.Scan() {
		line := sc.Text()
		if h != nil && (oldLeft > 0 || newLeft > 0) {
			oldLeft, newLeft = consumeHunkLine(h, line, oldLeft, newLeft)
			continue
		}
		h = nil
		switch {
		case strings.HasPrefix(line, "diff --git "):
			cur = &fileDiff{}
		case strings.HasPrefix(line, "--- "):
			if cur == nil {
				cur = &fileDiff{}
			}
			cur.OldPath = diffPath(strings.TrimPrefix(line, "--- "), "a/")
		case strings.HasPrefix(line, "+++ "):
			if cur == nil {
				cur = &fileDiff{}
			}
			cur.NewPath = diffPath(strings.TrimPrefix(line, "+++ "), "b/")
			key := cur.NewPath
			if key == "" {
				key = cur.OldPath
			}
			out[key] = cur
		case strings.HasPrefix(line, "@@ "):
			if cur == nil {
				return nil, fmt.Errorf("hunk before any file header: %q", line)
			}
			parsed, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			cur.Hunks = append(cur.Hunks, parsed)
			h = &cur.Hunks[len(cur.Hunks)-1]
			oldLeft, newLeft = parsed.OldCount, parsed.NewCount
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
	}
	return out, nil
}

// consumeHunkLine appends one body line to h and returns the remaining old and
// new line counts.
func consumeHunkLine(h *hunk, line string, oldLeft, newLeft int) (int, int) {
	switch {
	case strings.HasPrefix(line, `\`): // "\ No newline at end of file"
		return oldLeft, newLeft
	case strings.HasPrefix(line, "+"):
		newLeft--
	case strings.HasPrefix(line, "-"):
		oldLeft--
	default: // context; an empty line is a context line whose space was trimmed
		if line == "" {
			line = " "
		}
		oldLeft--
		newLeft--
	}
	h.Lines = append(h.Lines, line)
	return oldLeft, newLeft
}

// parseHunkHeader reads "@@ -a,b +c,d @@ ...". A missing count means 1.
func parseHunkHeader(line string) (hunk, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return hunk{}, fmt.Errorf("bad hunk header %q", line)
	}
	oldStart, oldCount, err := parseRange(fields[1][1:])
	if err != nil {
		return hunk{}, fmt.Errorf("bad hunk header %q: %w", line, err)
	}
	newStart, newCount, err := parseRange(fields[2][1:])
	if err != nil {
		return hunk{}, fmt.Errorf("bad hunk header %q: %w", line, err)
	}
	return hunk{OldStart: oldStart, OldCount: oldCount, NewStart: newStart, NewCount: newCount}, nil
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
	return normPath(strings.TrimPrefix(s, prefix))
}
