// Package ops holds the logic of CLI commands that the server also runs as
// background jobs (bench/jobs): suite lint, lock and admit, the results
// index rebuild and regate. The CLI (cmd/bifrost-bench) and the job kinds
// call the same functions; each returns a report and writes the text the CLI
// prints to an io.Writer (stdout for the CLI, the job's log for a job).
//
// Jobs area (P4), docs/platform-design.md.
package ops

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/utils"
)

// short returns the first 12 characters of a hash for display.
func short(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// out returns w, or io.Discard for nil.
func out(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidID checks an id a job names (suite, task, experiment, regrade): one
// path segment of letters, digits, dot, dash and underscore, never "." or
// "..", so it can not leave the folder it is looked up in.
func ValidID(what, id string) error {
	if !idPattern.MatchString(id) || id == "." || id == ".." || strings.Contains(id, "..") {
		return fmt.Errorf("%s %q: want letters, digits, '.', '-' or '_' (one path segment)", what, id)
	}
	return nil
}

// Confine resolves rel inside root (the organization's project folder): it
// must be relative, stay inside root after every symlink, and exist. It
// returns the real absolute path.
func Confine(root, what, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("%s: empty path", what)
	}
	p, err := utils.ResolveInside(root, filepath.ToSlash(rel))
	if err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	return p, nil
}
