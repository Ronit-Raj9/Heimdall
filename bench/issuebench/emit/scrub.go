package emit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// scrubEnv makes the fresh commit the same for the same tree: fixed author,
// committer and dates, no user or system git config.
var scrubEnv = []string{
	"GIT_AUTHOR_NAME=heimdall", "GIT_AUTHOR_EMAIL=heimdall@localhost",
	"GIT_COMMITTER_NAME=heimdall", "GIT_COMMITTER_EMAIL=heimdall@localhost",
	"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
	"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
}

// ScrubGit replaces the history of the checkout at dir (the repository at
// the base commit) with one fresh commit of the same files, so the fix,
// later commits, branches, tags, reflogs and remotes cannot be read from
// inside the task. It returns the new commit.
func ScrubGit(ctx context.Context, dir string) (string, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a folder", dir)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		return "", fmt.Errorf("remove history: %w", err)
	}
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A", "-f"},
		{"commit", "-q", "--no-verify", "--no-gpg-sign", "--allow-empty", "-m", "base"},
	}
	for _, s := range steps {
		if _, err := git(ctx, dir, s...); err != nil {
			return "", err
		}
	}
	sha, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if err := CheckScrubbed(ctx, dir); err != nil {
		return "", err
	}
	return sha, nil
}

// CheckScrubbed proves dir holds exactly one commit, one branch, no tags
// and no remotes.
func CheckScrubbed(ctx context.Context, dir string) error {
	n, err := git(ctx, dir, "rev-list", "--all", "--count")
	if err != nil {
		return err
	}
	if n != "1" {
		return fmt.Errorf("history not scrubbed: %s commits", n)
	}
	refs, err := git(ctx, dir, "for-each-ref", "--format=%(refname)")
	if err != nil {
		return err
	}
	if refs != "refs/heads/main" {
		return fmt.Errorf("history not scrubbed: refs %q", refs)
	}
	if rem, _ := git(ctx, dir, "remote"); rem != "" {
		return errors.New("history not scrubbed: remote " + rem)
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "safe.directory=*", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), scrubEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
