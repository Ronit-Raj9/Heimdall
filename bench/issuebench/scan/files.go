package scan

import (
	"path"
	"regexp"
	"strings"
)

// HeuristicTestFile is the path rule used when no Language is registered
// for the repository: test directories and test_/_test/.test./.spec. names.
func HeuristicTestFile(p string) bool {
	l := strings.ToLower(p)
	for _, seg := range strings.Split(path.Dir(l), "/") {
		switch seg {
		case "test", "tests", "testing", "__tests__", "spec", "specs", "testdata":
			return true
		}
	}
	b := path.Base(l)
	stem := strings.TrimSuffix(b, path.Ext(b))
	return strings.HasPrefix(b, "test_") || strings.HasSuffix(stem, "_test") || strings.HasSuffix(stem, "_tests") ||
		strings.Contains(b, ".test.") || strings.Contains(b, ".spec.") || b == "conftest.py"
}

var binaryExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true, ".webp": true, ".tiff": true,
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".tar": true, ".whl": true, ".egg": true,
	".so": true, ".dll": true, ".dylib": true, ".exe": true, ".bin": true, ".o": true, ".a": true, ".pyc": true, ".class": true, ".jar": true,
	".npy": true, ".npz": true, ".pkl": true, ".pickle": true, ".h5": true, ".hdf5": true, ".parquet": true, ".db": true, ".sqlite": true,
	".mp3": true, ".mp4": true, ".wav": true, ".ogg": true, ".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
}

func binaryPath(p string) bool { return binaryExt[strings.ToLower(path.Ext(p))] }

var nonSourceExt = map[string]bool{".md": true, ".rst": true, ".txt": true, ".adoc": true, ".toml": true, ".cfg": true, ".ini": true, ".yml": true, ".yaml": true, ".json": true, ".lock": true}

// sourcePath is a file that can carry the fix (not docs, changelog, CI or
// packaging metadata).
func sourcePath(p string) bool {
	l := strings.ToLower(p)
	if strings.HasPrefix(l, "docs/") || strings.HasPrefix(l, "doc/") || strings.HasPrefix(l, ".github/") || strings.HasPrefix(l, "changelog") || strings.HasPrefix(l, "changes/") || strings.HasPrefix(l, "news/") {
		return false
	}
	return !nonSourceExt[path.Ext(l)]
}

func anySource(ps []string) bool {
	for _, p := range ps {
		if sourcePath(p) {
			return true
		}
	}
	return false
}

// fileChunks splits a unified diff into per-file chunks with their path
// (the b/ side; the a/ side for a deletion).
func fileChunks(diff string) (paths []string, chunks []string) {
	if diff == "" {
		return nil, nil
	}
	parts := strings.Split("\n"+diff, "\ndiff --git ")
	for _, p := range parts[1:] {
		chunk := "diff --git " + p
		if !strings.HasSuffix(chunk, "\n") {
			chunk += "\n"
		}
		paths = append(paths, chunkPath(chunk))
		chunks = append(chunks, chunk)
	}
	return paths, chunks
}

func chunkPath(chunk string) string {
	var minus, plus string
	for _, ln := range strings.SplitN(chunk, "\n", 12) {
		switch {
		case strings.HasPrefix(ln, "+++ b/"):
			plus = strings.TrimPrefix(ln, "+++ b/")
		case strings.HasPrefix(ln, "--- a/"):
			minus = strings.TrimPrefix(ln, "--- a/")
		case strings.HasPrefix(ln, "rename to "):
			plus = strings.TrimPrefix(ln, "rename to ")
		}
	}
	if plus != "" {
		return plus
	}
	if minus != "" {
		return minus
	}
	// No ---/+++ (mode change or empty file): "diff --git a/x b/x".
	first := strings.SplitN(chunk, "\n", 2)[0]
	if i := strings.LastIndex(first, " b/"); i >= 0 {
		return first[i+3:]
	}
	return ""
}

func diffFiles(diff string) []string {
	ps, _ := fileChunks(diff)
	return ps
}

// splitDiff separates the test files' chunks from the rest.
func splitDiff(diff string, isTest func(string) bool) (testPatch, fixPatch string) {
	ps, cs := fileChunks(diff)
	var t, f strings.Builder
	for i, p := range ps {
		if isTest(p) {
			t.WriteString(cs[i])
		} else {
			f.WriteString(cs[i])
		}
	}
	return t.String(), f.String()
}

// addedLines returns the + lines of a diff without the marker.
func addedLines(diff string) []string {
	var out []string
	for _, ln := range strings.Split(diff, "\n") {
		if strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++") {
			out = append(out, ln[1:])
		}
	}
	return out
}

func otherLines(diff string) []string {
	var out []string
	for _, ln := range strings.Split(diff, "\n") {
		if (strings.HasPrefix(ln, "-") && !strings.HasPrefix(ln, "---")) || strings.HasPrefix(ln, " ") {
			out = append(out, ln[1:])
		}
	}
	return out
}

var (
	networkRE = regexp.MustCompile(`(?i)@pytest\.mark\.(?:network|online|internet|remote_data)|\brequests\.(?:get|post|put|head)\(\s*["']https?://|urlopen\(\s*["']https?://|socket\.create_connection|httpx\.(?:get|post)\(\s*["']https?://`)
	secretRE  = regexp.MustCompile(`(?i)os\.environ\[\s*["'][A-Z0-9_]*(?:TOKEN|SECRET|API_KEY|PASSWORD|ACCESS_KEY)["']|getenv\(\s*["'][A-Z0-9_]*(?:TOKEN|SECRET|API_KEY|PASSWORD|ACCESS_KEY)["']|-----BEGIN [A-Z ]*PRIVATE KEY-----|\bAKIA[0-9A-Z]{16}\b|\bghp_[A-Za-z0-9]{36}\b`)
)

// envReason flags tests that reach the internet or need a secret, from the
// lines the PR adds.
func envReason(testPatch, fixPatch string) string {
	added := strings.Join(addedLines(testPatch), "\n")
	if secretRE.MatchString(added) || secretRE.MatchString(strings.Join(addedLines(fixPatch), "\n")) {
		return ReasonSecret
	}
	if networkRE.MatchString(added) {
		return ReasonNetwork
	}
	return ""
}

var identRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// distinctive is a name unlikely to appear by chance in prose: 6+ chars
// with an underscore or an inner capital.
func distinctive(id string) bool {
	if len(id) < 6 {
		return false
	}
	id = strings.Trim(id, "_")
	if strings.Contains(id, "_") {
		return true
	}
	for i := 1; i < len(id); i++ {
		if id[i] >= 'A' && id[i] <= 'Z' && id[i-1] >= 'a' && id[i-1] <= 'z' {
			return true
		}
	}
	return false
}

// Leaks is the cheap leak pre-check: the issue text already holds the fix,
// either a whole added code line (20+ chars) or a distinctive identifier
// the fix introduces (absent from the removed and context lines).
func Leaks(issueText, fixPatch string) bool {
	old := map[string]bool{}
	for _, ln := range otherLines(fixPatch) {
		for _, id := range identRE.FindAllString(ln, -1) {
			old[id] = true
		}
	}
	words := map[string]bool{}
	for _, id := range identRE.FindAllString(issueText, -1) {
		words[id] = true
	}
	flat := strings.Join(strings.Fields(issueText), " ")
	for _, ln := range addedLines(fixPatch) {
		t := strings.Join(strings.Fields(ln), " ")
		if len(t) >= 20 && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "\"\"\"") && strings.Contains(flat, t) {
			return true
		}
		for _, id := range identRE.FindAllString(ln, -1) {
			if !old[id] && words[id] && distinctive(id) {
				return true
			}
		}
	}
	return false
}
