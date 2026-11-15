package agentdiff

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RootContents returns a ContentSource that reads files below root, the
// bench root a config's notes.user_file is relative to. A path that is
// absolute, climbs out of root, or names a file larger than MaxFileBytes
// reads as absent; so does any read error. An empty root reads nothing.
func RootContents(root string) ContentSource {
	if root == "" {
		return nil
	}
	return func(rel string) ([]byte, bool) {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, false
		}
		f, err := os.Open(filepath.Join(root, clean))
		if err != nil {
			return nil, false
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		if err != nil || len(data) > MaxFileBytes {
			return nil, false
		}
		return data, true
	}
}
