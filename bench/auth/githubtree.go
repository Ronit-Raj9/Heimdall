package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// GitHubTreeEntry is one entry of a commit's recursive tree.
type GitHubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

var (
	gitRefRE = regexp.MustCompile(`^[A-Za-z0-9._/@^~+-]{1,200}$`)
	blobSHA  = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
)

// ValidGitRef reports whether ref is a safe branch, tag or commit name.
func ValidGitRef(ref string) bool {
	return gitRefRE.MatchString(ref) && !strings.Contains(ref, "..") && !strings.HasPrefix(ref, "-") &&
		!strings.HasPrefix(ref, "/") && !strings.HasSuffix(ref, "/")
}

// Tree resolves ref to its commit and returns the commit's recursive tree.
// A tree GitHub truncated is refused, so no file is silently left out.
func (c *GitHubRepoConnector) Tree(ctx context.Context, token, owner, repo, ref string) (string, []GitHubTreeEntry, error) {
	if !ValidRepo(owner, repo) || !ValidGitRef(ref) {
		return "", nil, &GitHubAPIError{Status: http.StatusBadRequest, Msg: "invalid repository or ref name"}
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if _, err := c.api(ctx, token, http.MethodGet, repoPath(owner, repo)+"/commits/"+escapeRefPath(ref), nil, &commit); err != nil {
		return "", nil, fmt.Errorf("resolve %s: %w", ref, err)
	}
	if !blobSHA.MatchString(commit.SHA) {
		return "", nil, fmt.Errorf("resolve %s: GitHub returned no commit id", ref)
	}
	var tree struct {
		Tree      []GitHubTreeEntry `json:"tree"`
		Truncated bool              `json:"truncated"`
	}
	if _, err := c.api(ctx, token, http.MethodGet, repoPath(owner, repo)+"/git/trees/"+commit.SHA+"?recursive=1", nil, &tree); err != nil {
		return "", nil, fmt.Errorf("tree of %s: %w", ref, err)
	}
	if tree.Truncated {
		return "", nil, fmt.Errorf("the tree of %s is too large for the GitHub API (truncated); compare from a local clone with the CLI", ref)
	}
	return commit.SHA, tree.Tree, nil
}

// Blob returns the bytes of one blob of owner/repo.
func (c *GitHubRepoConnector) Blob(ctx context.Context, token, owner, repo, sha string) ([]byte, error) {
	if !ValidRepo(owner, repo) || !blobSHA.MatchString(sha) {
		return nil, &GitHubAPIError{Status: http.StatusBadRequest, Msg: "invalid repository or blob id"}
	}
	var b struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if _, err := c.api(ctx, token, http.MethodGet, repoPath(owner, repo)+"/git/blobs/"+url.PathEscape(sha), nil, &b); err != nil {
		return nil, err
	}
	if b.Encoding != "base64" {
		return []byte(b.Content), nil
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(b.Content, "\n", ""))
}
