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

// Names returns every embedded guide name (the file name without .md), sorted.
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
		out = append(out, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(out)
	return out
}

// Read returns the markdown of one guide by name (without .md). The name is
// looked up in the embedded set only, so no path can reach the filesystem.
func Read(name string) ([]byte, bool) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return nil, false
	}
	data, err := files.ReadFile(name + ".md")
	if err != nil || len(data) > MaxBytes {
		return nil, false
	}
	return data, true
}
