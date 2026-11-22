package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ReadFile reads one file of owner/repo at ref (empty: the default branch)
// through the contents API (lane Z15: the committed .heimdall/agent.yaml).
// A missing file is a *GitHubAPIError with status 404; files over max bytes
// are refused.
func (c *GitHubRepoConnector) ReadFile(ctx context.Context, token, owner, repo, ref, path string, max int) ([]byte, error) {
	if !ValidRepo(owner, repo) {
		return nil, errors.New("invalid owner or repository name")
	}
	if ref != "" && !ValidBranch(ref) {
		return nil, errors.New("invalid branch or tag name")
	}
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return nil, errors.New("invalid file path")
	}
	p := repoPath(owner, repo) + "/contents/" + escapeRefPath(path)
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	var f struct {
		Type     string `json:"type"`
		Size     int    `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if _, err := c.api(ctx, token, http.MethodGet, p, nil, &f); err != nil {
		return nil, err
	}
	if f.Type != "file" {
		return nil, fmt.Errorf("%s is not a file in %s/%s", path, owner, repo)
	}
	if max > 0 && f.Size > max {
		return nil, fmt.Errorf("%s is %d bytes, over the %d byte limit", path, f.Size, max)
	}
	if f.Encoding != "base64" {
		return nil, fmt.Errorf("%s: unexpected encoding %q", path, f.Encoding)
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}
