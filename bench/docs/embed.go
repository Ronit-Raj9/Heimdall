// Package docs embeds the user guides in this folder so the server can serve
// them in-app (GET /api/bench/docs/{name}). Only the top-level *.md files are
// embedded; the probes/ and results/ subfolders are reference data.
package docs

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.md
var files embed.FS

// MaxBytes caps one served guide; larger files are refused rather than streamed.
const MaxBytes = 1 << 20

// Internal reports whether name is an internal design or planning note
// (DESIGN, any *-design note, the spike log, the CLI and web parity
// checklist). They stay in the repository for contributors but are not
// user guides, so the in-app Help neither lists nor serves them.
func Internal(name string) bool {
	switch {
	case name == "DESIGN", name == "spike", name == "CLI_WEB_PARITY":
		return true
	case strings.HasSuffix(name, "-design"):
		return true
	}
	return false
}

// Names returns every embedded user guide name (the file name without .md),
// sorted. Internal notes (Internal) are left out.
func Names() []string {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if Internal(name) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Read returns the markdown of one guide by name (without .md). The name is
// looked up in the embedded set only, so no path can reach the filesystem.
// Internal notes are not served.
func Read(name string) ([]byte, bool) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") || Internal(name) {
		return nil, false
	}
	data, err := files.ReadFile(name + ".md")
	if err != nil || len(data) > MaxBytes {
		return nil, false
	}
	return data, true
}

// publicHidden are user guides the in-app Help lists but the public site
// (no sign-in, lane Y08) does not: the research source log behind DESIGN.
var publicHidden = map[string]bool{"sources": true}

// Public reports whether guide name is on the public site: a user guide
// (not Internal) that is not in publicHidden.
func Public(name string) bool { return !Internal(name) && !publicHidden[name] }

// PublicNames lists the guides the public site serves, sorted.
func PublicNames() []string {
	var out []string
	for _, n := range Names() {
		if Public(n) {
			out = append(out, n)
		}
	}
	return out
}

// ReadPublic is Read for the public site: guides outside Public are not served.
func ReadPublic(name string) ([]byte, bool) {
	if !Public(name) {
		return nil, false
	}
	return Read(name)
}
