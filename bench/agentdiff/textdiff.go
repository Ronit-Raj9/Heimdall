package agentdiff

import (
	"fmt"
	"strings"
)

// contextLines is how many unchanged lines a hunk keeps around a change.
const contextLines = 3

// maxDiffCells caps the line-pair table of one diff. Two files of 64 KiB
// with short lines stay under it; beyond it the diff is skipped rather than
// spend seconds on one request.
const maxDiffCells = 4_000_000

// op is one line of an edit script.
type op struct {
	kind byte // ' ', '-' or '+'
	text string
}

// unifiedDiff returns a unified diff of old and new under the names a/<name>
// and b/<name>, at most maxLines lines after the headers, and whether it was
// cut. ok is false when the inputs are too large to diff. Equal inputs give
// "". It is pure.
func unifiedDiff(name, old, new string, maxLines int) (diff string, truncated, ok bool) {
	if old == new {
		return "", false, true
	}
	a, b := splitLines(old), splitLines(new)
	if len(a)*len(b) > maxDiffCells {
		return "", false, false
	}
	script := editScript(a, b)
	var sb strings.Builder
	clean := strings.TrimPrefix(name, "/")
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", clean, clean)
	lines := 0
	for _, h := range hunks(script) {
		if lines >= maxLines {
			truncated = true
			break
		}
		fmt.Fprintf(&sb, "@@ -%s +%s @@\n", hunkRange(h.oldStart, h.oldLen), hunkRange(h.newStart, h.newLen))
		for _, o := range h.ops {
			if lines >= maxLines {
				truncated = true
				break
			}
			sb.WriteByte(o.kind)
			sb.WriteString(o.text)
			sb.WriteByte('\n')
			lines++
		}
	}
	return sb.String(), truncated, true
}

// splitLines splits text into lines without their newline. A trailing
// newline does not add an empty last line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// editScript is the longest-common-subsequence edit script from a to b.
func editScript(a, b []string) []op {
	n, m := len(a), len(b)
	// lcs[i][j] is the LCS length of a[i:] and b[j:], kept in one slice.
	lcs := make([]int32, (n+1)*(m+1))
	at := func(i, j int) int32 { return lcs[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			v := max(at(i+1, j), at(i, j+1))
			if a[i] == b[j] {
				v = at(i+1, j+1) + 1
			}
			lcs[i*(m+1)+j] = v
		}
	}
	out := make([]op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, op{' ', a[i]})
			i, j = i+1, j+1
		case at(i+1, j) >= at(i, j+1):
			out = append(out, op{'-', a[i]})
			i++
		default:
			out = append(out, op{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, op{'-', a[i]})
	}
	for ; j < m; j++ {
		out = append(out, op{'+', b[j]})
	}
	return out
}

// hunk is one run of changes with its context.
type hunk struct {
	oldStart, oldLen, newStart, newLen int
	ops                                []op
}

// hunks groups an edit script into hunks with contextLines of context,
// merging hunks whose context would overlap.
func hunks(script []op) []hunk {
	var out []hunk
	n := len(script)
	i := 0
	oldLine, newLine := 1, 1 // 1-based line numbers at script[i]
	for i < n {
		if script[i].kind == ' ' {
			i, oldLine, newLine = i+1, oldLine+1, newLine+1
			continue
		}
		start := max(0, i-contextLines)
		h := hunk{oldStart: oldLine - (i - start), newStart: newLine - (i - start)}
		end := i
		for end < n {
			if script[end].kind != ' ' {
				end++
				continue
			}
			k := end
			for k < n && script[k].kind == ' ' {
				k++
			}
			if k == n || k-end > 2*contextLines {
				end = min(n, end+contextLines)
				break
			}
			end = k
		}
		h.ops = script[start:end]
		for _, o := range h.ops {
			if o.kind != '+' {
				h.oldLen++
			}
			if o.kind != '-' {
				h.newLen++
			}
		}
		for ; i < end; i++ {
			if script[i].kind != '+' {
				oldLine++
			}
			if script[i].kind != '-' {
				newLine++
			}
		}
		out = append(out, h)
	}
	return out
}

// hunkRange formats a hunk side as unified diff does: "start,len", with an
// empty side starting one line earlier.
func hunkRange(start, length int) string {
	if length == 0 {
		return fmt.Sprintf("%d,0", start-1)
	}
	if length == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, length)
}
