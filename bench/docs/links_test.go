package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// markdownLink matches [label](target) and captures the target.
var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// fence opens or closes a fenced code block.
var fence = regexp.MustCompile("^\\s*(```|~~~)")

// inlineCode is a code span; links inside one are samples.
var inlineCode = regexp.MustCompile("`[^`]*`")

// scheme starts an absolute URL (https:, mailto:).
var scheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// TestDocsRelativeLinksExist keeps the guides' relative links honest (fix
// wave 2, N137): the docs viewer sends every relative link that is not a
// guide to the file on GitHub, so each one must name a file of this
// repository. Links inside fenced code blocks are samples and are skipped.
func TestDocsRelativeLinksExist(t *testing.T) {
	guides, err := filepath.Glob("*.md")
	if err != nil || len(guides) == 0 {
		t.Fatalf("no guides found: %v", err)
	}
	checked := 0
	for _, guide := range guides {
		data, err := os.ReadFile(guide)
		if err != nil {
			t.Fatal(err)
		}
		inFence := false
		for i, line := range strings.Split(string(data), "\n") {
			if fence.MatchString(line) {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			line = inlineCode.ReplaceAllString(line, "")
			for _, m := range markdownLink.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if !relativeLink(target) {
					continue
				}
				path := strings.SplitN(target, "#", 2)[0]
				if path == "" {
					continue
				}
				checked++
				if _, err := os.Stat(filepath.FromSlash(path)); err != nil {
					t.Errorf("%s:%d links to %s, which does not exist (resolved from bench/docs)", guide, i+1, target)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no relative links checked; the link pattern is broken")
	}
}

// relativeLink reports whether a link target is a path in this repository:
// no scheme, not an in-page anchor, not an absolute site path.
func relativeLink(target string) bool {
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") {
		return false
	}
	return !scheme.MatchString(target)
}
