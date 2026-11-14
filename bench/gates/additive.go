package gates

import (
	"regexp"
	"strings"
)

// testDisablingLine matches an added line that would switch tests off or end
// the test process early. Adding tests is good engineering; adding one of
// these to a protected test file is not.
var testDisablingLine = regexp.MustCompile(`pytest\.skip|pytest\.mark\.(skip|skipif|xfail)|pytest\.exit|unittest\.skip|SkipTest|sys\.exit|os\._exit|pytest_collection_modifyitems|pytest_runtest_`)

// modificationVerdict says whether a modified file's diff only adds lines.
//
// A protected or restored test file that only gains lines (new tests) is not
// tampering: the clean regrade restores those files anyway, and an agent that
// writes extra tests should not be punished. A file that loses or changes any
// existing line, or gains a test-disabling line, is still a violation. When
// the diff text has no section for the file, the change is treated as a
// violation, never waved through.
func modificationVerdict(files map[string]*fileDiff, path string) (additiveOnly bool, reason string) {
	fd, ok := files[normPath(path)]
	if !ok {
		fd, ok = files[path]
	}
	if !ok || fd == nil {
		return false, "no diff section"
	}
	for _, h := range fd.Hunks {
		for _, l := range h.Lines {
			switch {
			case strings.HasPrefix(l, "-"):
				return false, "existing lines removed or changed"
			case strings.HasPrefix(l, "+") && testDisablingLine.MatchString(l):
				return false, "added line disables or short-circuits tests: " + strings.TrimSpace(strings.TrimPrefix(l, "+"))
			}
		}
	}
	return true, "only new lines added"
}

// loadDiffFiles parses the exported diff text, or returns nil when there is
// none to read.
func loadDiffFiles(diffPath string) map[string]*fileDiff {
	if diffPath == "" {
		return nil
	}
	b, err := readDiffText(diffPath)
	if err != nil {
		return nil
	}
	files, err := parseUnifiedDiff(b)
	if err != nil {
		return nil
	}
	return files
}
