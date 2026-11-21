package scaffold

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// RemoteEntry is one tree entry of a commit read from a hosted git service
// (the GitHub trees API), for ExportRemoteTree.
type RemoteEntry struct {
	Path string // repository path, slash-separated
	Mode string // 100644, 100755, 120000 (symlink) or 160000 (submodule)
	Type string // blob, tree or commit
	SHA  string // blob id
}

// MaxRemoteExportFiles caps the files ExportRemoteTree fetches for one ref.
const MaxRemoteExportFiles = 2000

// ExportRemoteTree is ExportAgentFiles for a repository that is not on
// disk: tree is the commit's recursive tree and fetch returns one blob's
// bytes. It writes the same files under dst and computes the same Hash as
// ExportAgentFiles would for a local clone of that commit.
func ExportRemoteTree(ctx context.Context, ref, commit string, tree []RemoteEntry, paths []string, dst string,
	fetch func(ctx context.Context, sha string) ([]byte, error)) (*ExportedRef, error) {
	var picked []RemoteEntry
	for _, e := range tree {
		if e.Type != "blob" || !underAny(e.Path, paths) {
			continue
		}
		if err := checkRel(e.Path); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Path, err)
		}
		picked = append(picked, e)
	}
	if len(picked) > MaxRemoteExportFiles {
		return nil, fmt.Errorf("%d agent files at %s, more than the %d this export reads; narrow the paths", len(picked), ref, MaxRemoteExportFiles)
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].Path < picked[j].Path })
	cache := map[string][]byte{}
	var files []File
	for _, e := range picked {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, ok := cache[e.SHA]
		if !ok {
			var err error
			if data, err = fetch(ctx, e.SHA); err != nil {
				return nil, fmt.Errorf("read %s at %s: %w", e.Path, ref, err)
			}
			cache[e.SHA] = data
		}
		f := File{Path: e.Path, Data: data, Mode: 0o644}
		switch e.Mode {
		case "100755":
			f.Mode = 0o755
		case "120000":
			target := string(data)
			if err := checkLink(e.Path, target); err != nil {
				return nil, err
			}
			f = File{Path: e.Path, Link: target}
		}
		files = append(files, f)
	}
	if err := WriteFiles(dst, files, false); err != nil {
		return nil, err
	}
	if err := refuseEscapingLinks(dst); err != nil {
		return nil, err
	}
	out := &ExportedRef{Ref: ref, Commit: commit}
	var sum strings.Builder
	for _, e := range picked {
		out.Files = append(out.Files, e.Path)
		fmt.Fprintf(&sum, "%s %s %s\n", e.Mode, e.SHA, e.Path)
	}
	h := sha256.Sum256([]byte(sum.String()))
	out.Hash = hex.EncodeToString(h[:])
	return out, nil
}
