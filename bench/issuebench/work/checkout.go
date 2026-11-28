package work

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// Git runs `git args...` with the extra env and returns the combined output.
// Tests swap it to serve a local repository.
type Git func(ctx context.Context, env []string, args ...string) ([]byte, error)

// ExecGit runs the real git binary with a clean environment: no inherited
// GIT_* variables, no prompts, no system or user config (HOME is a fresh
// temp folder), plus env.
func ExecGit(ctx context.Context, env []string, args ...string) ([]byte, error) {
	home, err := os.MkdirTemp("", "ibgit-home-")
	if err != nil {
		return nil, fmt.Errorf("could not make a home folder for git: %w", err)
	}
	defer func() { _ = os.RemoveAll(home) }()
	base := make([]string, 0, len(os.Environ())+8+len(env))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			continue
		}
		base = append(base, kv)
	}
	base = append(base,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+home,
		"XDG_CONFIG_HOME="+home,
	)
	base = append(base, env...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = base
	return cmd.CombinedOutput()
}

// TokenEnv is the env that hands a GitHub token to git as an HTTP header,
// never through argv or a file. An empty token gives nil.
func TokenEnv(token string) []string {
	if token == "" {
		return nil
	}
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + auth,
	}
}

// Checkouter fetches one commit of a GitHub repository into a fresh folder.
type Checkouter struct{ Git Git }

var (
	checkoutRepoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	checkoutSHARE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func checkRepo(repo string) error {
	if !checkoutRepoRE.MatchString(repo) || strings.Contains(repo, "..") {
		return fmt.Errorf("repository %q is not owner/name", repo)
	}
	return nil
}

func repoURL(repo string) string { return "https://github.com/" + repo + ".git" }

// outputTail is the last part of git's output with the token removed.
func outputTail(out []byte, token string) string {
	s := strings.TrimSpace(string(out))
	if len(s) > 400 {
		s = s[len(s)-400:]
	}
	s = issuebench.RedactValues(s, map[string]string{issuebench.SecretGitHubToken: token})
	if token != "" {
		// A token shorter than RedactValues' floor is still never shown.
		s = strings.ReplaceAll(s, token, "[redacted]")
	}
	return s
}

// Checkout makes dir a git work tree at sha with a shallow fetch of that one
// commit. No remote is configured, so the token never lands in .git/config.
func (c Checkouter) Checkout(ctx context.Context, repo, sha, dir, token string) error {
	if err := checkRepo(repo); err != nil {
		return err
	}
	if !checkoutSHARE.MatchString(sha) {
		return fmt.Errorf("commit %q is not a full commit id (40 hex characters)", sha)
	}
	if dir == "" {
		return errors.New("checkout folder is empty")
	}
	git := c.Git
	if git == nil {
		git = ExecGit
	}
	if out, err := git(ctx, nil, "init", "-q", dir); err != nil {
		return fmt.Errorf("could not make a git folder for %s: %s", repo, outputTail(out, token))
	}
	if out, err := git(ctx, TokenEnv(token), "-C", dir, "fetch", "-q", "--depth", "1", "--no-tags", repoURL(repo), sha); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("fetching commit %s of %s stopped: %w", sha, repo, ctx.Err())
		}
		return fmt.Errorf("could not fetch commit %s of %s (check the repository exists, the commit is on GitHub, and the GitHub token can read it): %s", sha, repo, outputTail(out, token))
	}
	if out, err := git(ctx, nil, "-C", dir, "checkout", "-q", "--detach", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("could not check out commit %s of %s: %s", sha, repo, outputTail(out, token))
	}
	out, err := git(ctx, nil, "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("could not read the checked out commit of %s: %s", repo, outputTail(out, token))
	}
	if got := strings.TrimSpace(string(out)); got != sha {
		return fmt.Errorf("checked out commit %s of %s, wanted %s", outputTail([]byte(got), token), repo, sha)
	}
	return nil
}

// RepoPublic says whether GitHub serves repo without a token. Any failure,
// including a bad name or a timeout, counts as not public.
func RepoPublic(ctx context.Context, git Git, repo string) bool {
	if checkRepo(repo) != nil {
		return false
	}
	if git == nil {
		git = ExecGit
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := git(ctx, nil, "ls-remote", "--exit-code", repoURL(repo), "HEAD")
	return err == nil
}
