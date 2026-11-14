package scaffold

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gitRepo reads a git repository with plumbing commands only. It never
// checks anything out, never takes optional locks and never writes to the
// repository. prefix is the folder inside the repository the task is cut
// from ("" for the whole repository, else "services/api/").
type gitRepo struct {
	top    string
	prefix string
}

// treeEntry is one blob, symlink or submodule of a commit's tree.
type treeEntry struct {
	Mode string // 100644, 100755, 120000 (symlink) or 160000 (submodule)
	Type string // blob or commit
	SHA  string
	Size int64  // -1 for a submodule
	Path string // relative to the prefix
}

// change is one path the reference commit changed, from git diff --name-status.
type change struct {
	Status string // A, M, D or T
	Path   string // relative to the prefix
}

// openRepo finds the repository holding dir. dir may be a sub-folder, which
// then limits the task to that folder.
func openRepo(ctx context.Context, dir string) (*gitRepo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("repo %s: %w", dir, err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("repo %s: not a folder", dir)
	}
	probe := &gitRepo{top: abs}
	out, err := probe.run(ctx, nil, "rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		return nil, fmt.Errorf("repo %s: not a git work tree: %w", dir, err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	r := &gitRepo{top: strings.TrimSpace(lines[0])}
	if len(lines) > 1 {
		r.prefix = strings.TrimSpace(lines[1])
	}
	return r, nil
}

// run runs one git command in the repository and returns its stdout. Global
// and system configuration still apply (the user's own repository), but
// every behaviour that could run code or change output is pinned by flags.
func (r *gitRepo) run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	full := append([]string{"--no-optional-locks", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-C", r.top}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C")
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// resolve returns the full id of the commit rev names.
func (r *gitRepo) resolve(ctx context.Context, rev string) (string, error) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return "", fmt.Errorf("commit %q: want a commit id, tag or branch", rev)
	}
	out, err := r.run(ctx, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("commit %q not found", rev)
	}
	return strings.TrimSpace(string(out)), nil
}

// isAncestor reports whether a is an ancestor of b.
func (r *gitRepo) isAncestor(ctx context.Context, a, b string) (bool, error) {
	full := []string{"-C", r.top, "merge-base", "--is-ancestor", a, b}
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	default:
		return false, fmt.Errorf("git merge-base: %w", err)
	}
}

// tree lists every entry of commit's tree under the prefix, with paths made
// relative to the prefix.
func (r *gitRepo) tree(ctx context.Context, commit string) ([]treeEntry, error) {
	args := []string{"ls-tree", "-r", "-l", "-z", "--full-tree", commit}
	if r.prefix != "" {
		args = append(args, "--", r.prefix)
	}
	out, err := r.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	var entries []treeEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, p, ok := strings.Cut(string(rec), "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			return nil, fmt.Errorf("git ls-tree: unexpected record %q", rec)
		}
		size := int64(-1)
		if f[3] != "-" {
			if size, err = strconv.ParseInt(f[3], 10, 64); err != nil {
				return nil, fmt.Errorf("git ls-tree: size of %s: %w", p, err)
			}
		}
		rel, inside := r.relative(p)
		if !inside {
			continue
		}
		entries = append(entries, treeEntry{Mode: f[0], Type: f[1], SHA: f[2], Size: size, Path: rel})
	}
	return entries, nil
}

// changes lists what changed from base to ref, renames split into a delete
// and an add. Paths outside the prefix come back separately, repo-relative.
func (r *gitRepo) changes(ctx context.Context, base, ref string) ([]change, []string, error) {
	out, err := r.run(ctx, nil, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", base, ref, "--")
	if err != nil {
		return nil, nil, err
	}
	fields := bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0})
	var inside []change
	var outside []string
	for i := 0; i+1 < len(fields); i += 2 {
		status, p := string(fields[i]), string(fields[i+1])
		rel, in := r.relative(p)
		if !in {
			outside = append(outside, p)
			continue
		}
		inside = append(inside, change{Status: status[:1], Path: rel})
	}
	return inside, outside, nil
}

// binaries lists which of paths changed as binary files from base to ref.
func (r *gitRepo) binaries(ctx context.Context, base, ref string, paths []string) ([]string, error) {
	args := append([]string{"diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", base, ref, "--"}, r.pathspecs(paths)...)
	out, err := r.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	var bins []string
	for _, rec := range bytes.Split(out, []byte{0}) {
		f := strings.SplitN(string(rec), "\t", 3)
		if len(f) == 3 && f[0] == "-" && f[1] == "-" {
			rel, _ := r.relative(f[2])
			bins = append(bins, rel)
		}
	}
	return bins, nil
}

// diff returns `git diff base ref` limited to paths, as a patch that applies
// with `git apply -p1` inside the prefix folder.
func (r *gitRepo) diff(ctx context.Context, base, ref string, paths []string) ([]byte, error) {
	args := []string{"diff", "--binary", "--full-index", "--no-renames", "--no-color", "--no-ext-diff", "--no-textconv",
		"--src-prefix=a/", "--dst-prefix=b/"}
	if r.prefix != "" {
		args = append(args, "--relative="+r.prefix)
	}
	args = append(append(args, base, ref, "--"), r.pathspecs(paths)...)
	return r.run(ctx, nil, args...)
}

// message returns a commit's message, read from the raw commit object.
func (r *gitRepo) message(ctx context.Context, commit string) (string, error) {
	out, err := r.run(ctx, nil, "cat-file", "commit", commit)
	if err != nil {
		return "", err
	}
	_, msg, _ := strings.Cut(string(out), "\n\n")
	return msg, nil
}

// remoteURL returns the origin remote, or "".
func (r *gitRepo) remoteURL(ctx context.Context) string {
	out, err := r.run(ctx, nil, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// catBlobs streams the content of each blob through fn, in order, with one
// `git cat-file --batch` process. Content is raw: no filters or textconv run.
func (r *gitRepo) catBlobs(ctx context.Context, shas []string, fn func(sha string, data []byte) error) error {
	if len(shas) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", r.top, "cat-file", "--batch")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	in := strings.Join(shas, "\n") + "\n"
	cmd.Stdin = strings.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git cat-file: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git cat-file: %w", err)
	}
	rerr := readBatch(bufio.NewReaderSize(stdout, 1<<20), shas, fn)
	if rerr != nil {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("git cat-file: %w (kill: %v)", rerr, err)
		}
	}
	if _, err := io.Copy(io.Discard, stdout); err != nil && rerr == nil {
		rerr = err
	}
	werr := cmd.Wait()
	if rerr != nil {
		return rerr
	}
	if werr != nil {
		return fmt.Errorf("git cat-file: %w: %s", werr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// readBatch parses `git cat-file --batch` output: "<sha> <type> <size>\n",
// the content and a newline, per requested object.
func readBatch(br *bufio.Reader, shas []string, fn func(string, []byte) error) error {
	for _, want := range shas {
		header, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("git cat-file: read header for %s: %w", want, err)
		}
		f := strings.Fields(header)
		if len(f) != 3 {
			return fmt.Errorf("git cat-file: object %s: %s", want, strings.TrimSpace(header))
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			return fmt.Errorf("git cat-file: size of %s: %w", want, err)
		}
		data := make([]byte, size+1)
		if _, err := io.ReadFull(br, data); err != nil {
			return fmt.Errorf("git cat-file: read %s: %w", want, err)
		}
		if err := fn(f[0], data[:size]); err != nil {
			return err
		}
	}
	return nil
}

// relative strips the prefix from a repo-relative path.
func (r *gitRepo) relative(p string) (string, bool) {
	if r.prefix == "" {
		return p, true
	}
	if !strings.HasPrefix(p, r.prefix) {
		return "", false
	}
	return strings.TrimPrefix(p, r.prefix), true
}

// pathspecs turns prefix-relative paths into literal, repo-relative
// pathspecs, so no path is read as a glob.
func (r *gitRepo) pathspecs(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ":(top,literal)" + r.prefix + p
	}
	return out
}
