package validate

import (
	"regexp"
	"sort"
	"strings"
)

// filePatch is one file's part of a unified diff.
type filePatch struct {
	Path    string   // the b/ path, or the a/ path for a deleted file
	Header  string   // the lines before the first hunk
	Hunks   []string // each hunk with its @@ line
	Created bool     // the file does not exist before the patch
	Deleted bool     // the file does not exist after the patch
}

var diffGitRe = regexp.MustCompile(`^diff --git a/(\S+) b/(\S+)`)

// splitPatch reads a git unified diff into its files and hunks. Text before
// the first "diff --git" line is ignored.
func splitPatch(patch string) []filePatch {
	var out []filePatch
	var cur *filePatch
	var hunk *strings.Builder
	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, hunk.String())
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.SplitAfter(patch, "\n") {
		if line == "" {
			continue
		}
		bare := strings.TrimRight(line, "\r\n")
		if m := diffGitRe.FindStringSubmatch(bare); m != nil {
			flushFile()
			cur = &filePatch{Path: m[2], Header: line}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(bare, "@@") {
			flushHunk()
			hunk = &strings.Builder{}
			hunk.WriteString(line)
			continue
		}
		if hunk != nil {
			hunk.WriteString(line)
			continue
		}
		cur.Header += line
		switch {
		case strings.HasPrefix(bare, "new file mode"), bare == "--- /dev/null":
			cur.Created = true
		case strings.HasPrefix(bare, "deleted file mode"), bare == "+++ /dev/null":
			cur.Deleted = true
		}
	}
	flushFile()
	for i := range out {
		if out[i].Deleted {
			if m := diffGitRe.FindStringSubmatch(strings.TrimRight(out[i].Header, "\r\n")); m != nil {
				out[i].Path = m[1]
			}
		}
	}
	return out
}

// createdFiles lists the files a patch creates, sorted.
func createdFiles(patch string) map[string]bool {
	out := map[string]bool{}
	for _, f := range splitPatch(patch) {
		if f.Created {
			out[f.Path] = true
		}
	}
	return out
}

// patchFiles lists every path a patch touches, sorted.
func patchFiles(patch string) []string {
	var out []string
	for _, f := range splitPatch(patch) {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

// hunkCount is the number of hunks in a patch.
func hunkCount(patch string) int {
	n := 0
	for _, f := range splitPatch(patch) {
		n += len(f.Hunks)
	}
	return n
}

// hunkAblations returns patches that each leave out one hunk of a patch
// with two or more hunks, at most limit of them. A wrong patch built this
// way that still passes means the hunk it drops is not tested. A file whose
// only hunk is dropped is left out of that ablation entirely, unless the
// patch creates it (then the empty file would be a different change).
func hunkAblations(patch string, limit int) []string {
	files := splitPatch(patch)
	type ref struct{ file, hunk int }
	var refs []ref
	for fi, f := range files {
		for hi := range f.Hunks {
			refs = append(refs, ref{fi, hi})
		}
	}
	if len(refs) < 2 || limit <= 0 {
		return nil
	}
	var out []string
	for _, drop := range refs {
		if len(out) >= limit {
			break
		}
		if files[drop.file].Created || files[drop.file].Deleted {
			continue // a half created or half deleted file does not apply
		}
		var b strings.Builder
		for fi, f := range files {
			var kept []string
			for hi, h := range f.Hunks {
				if fi == drop.file && hi == drop.hunk {
					continue
				}
				kept = append(kept, h)
			}
			if len(kept) == 0 {
				continue
			}
			b.WriteString(f.Header)
			for _, h := range kept {
				b.WriteString(h)
			}
		}
		if s := b.String(); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// addedLines are the "+" lines of a patch, without the plus sign.
func addedLines(patch string) []string {
	var out []string
	for _, f := range splitPatch(patch) {
		for _, h := range f.Hunks {
			for _, line := range strings.Split(h, "\n") {
				if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
					out = append(out, line[1:])
				}
			}
		}
	}
	return out
}

var (
	pyDefRe   = regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)?`)
	pyClassRe = regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	identRe   = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// newSymbols lists names the fix defines (def or class on an added line)
// that the test patch's added lines use, with the signature as written, in
// order of first definition. These are the names tests need that the base
// lacks: the interface an error to pass task has to state.
func newSymbols(fixPatch, testPatch string) []string {
	used := map[string]bool{}
	for _, line := range addedLines(testPatch) {
		for _, id := range identRe.FindAllString(line, -1) {
			used[id] = true
		}
	}
	removed := map[string]bool{}
	for _, f := range splitPatch(fixPatch) {
		for _, h := range f.Hunks {
			for _, line := range strings.Split(h, "\n") {
				if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
					if m := pyDefRe.FindStringSubmatch(line[1:]); m != nil {
						removed[m[1]] = true
					} else if m := pyClassRe.FindStringSubmatch(line[1:]); m != nil {
						removed[m[1]] = true
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range addedLines(fixPatch) {
		var name, sig string
		if m := pyDefRe.FindStringSubmatch(line); m != nil {
			name, sig = m[1], "def "+m[1]+"("+strings.TrimSpace(m[2])+")"
		} else if m := pyClassRe.FindStringSubmatch(line); m != nil {
			name, sig = m[1], "class "+m[1]
		} else {
			continue
		}
		if !used[name] || seen[name] || removed[name] {
			continue
		}
		seen[name] = true
		out = append(out, sig)
	}
	return out
}

// leakScore is the deterministic leak pre-screen: the share of the fix's
// substantial added lines (12 or more characters once trimmed, not a
// comment) that appear verbatim in the issue text, and how many did.
func leakScore(issue, fixPatch string) (float64, int) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	text := norm(issue)
	total, hit := 0, 0
	for _, line := range addedLines(fixPatch) {
		l := norm(line)
		if len(l) < 12 || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "//") || strings.HasPrefix(l, `"""`) {
			continue
		}
		total++
		if strings.Contains(text, l) {
			hit++
		}
	}
	if total == 0 {
		return 0, 0
	}
	return float64(hit) / float64(total), hit
}
