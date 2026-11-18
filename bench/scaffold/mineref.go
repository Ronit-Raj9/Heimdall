package scaffold

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
)

// First workflow: `compare --baseline <ref> --candidate <ref>` reads the
// agent's own files (notes, skills, prompts, configs) at two commits. They
// are copied out with read-only plumbing (ls-tree and cat-file), never with
// a checkout or a worktree, so the repository is not touched.

// DefaultAgentPaths are the files and folders an agent's behaviour lives in.
var DefaultAgentPaths = []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md", ".claude/skills", "skills", "prompts", ".heimdall/agents"}

// ExportedRef is what ExportAgentFiles copied for one ref.
type ExportedRef struct {
	Ref    string   // as given
	Commit string   // full commit id
	Files  []string // repository paths written, sorted
	Hash   string   // sha256 over the paths and blob ids: equal hashes mean the same files
}

// ExportAgentFiles writes every blob under paths at ref into dst (at its
// repository path) and reports what it wrote. A path that does not exist at
// ref is skipped. Symlinks are written only when they stay inside dst.
func ExportAgentFiles(ctx context.Context, repoDir, ref string, paths []string, dst string) (*ExportedRef, error) {
	repo, err := openRepo(ctx, repoDir)
	if err != nil {
		return nil, err
	}
	commit, err := repo.resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	whole := &gitRepo{top: repo.top} // agent files are at the repository root
	tree, err := whole.tree(ctx, commit)
	if err != nil {
		return nil, err
	}
	var picked []treeEntry
	for _, e := range tree {
		if e.Type != "blob" || !underAny(e.Path, paths) {
			continue
		}
		if err := checkRel(e.Path); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Path, err)
		}
		picked = append(picked, e)
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].Path < picked[j].Path })
	if err := streamTree(ctx, whole, dst, picked); err != nil {
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

func underAny(p string, roots []string) bool {
	for _, r := range roots {
		r = strings.Trim(path.Clean(strings.ReplaceAll(r, "\\", "/")), "/")
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}
