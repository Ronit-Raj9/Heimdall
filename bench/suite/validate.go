package suite

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Size limits of task validate: a file over largeFileBytes or a repo with
// more than maxRepoFiles files is a warning (every run copies them); a task
// folder over maxTaskBytes is an error.
const (
	largeFileBytes = 5 << 20
	maxRepoFiles   = 5000
	maxTaskBytes   = 200 << 20
	maxLargeListed = 10
)

// StaticValidation is what admission runs before anything starts in Docker:
// the suite-level part of Lint and ValidateTask for every task of s, or only
// the task only when it is set. Admission refuses on any error problem.
// Problems are sorted by path.
func StaticValidation(ctx context.Context, s *schemas.Suite, only string) []Problem {
	out := lintSuite(s)
	for _, t := range s.Tasks {
		if only != "" && t.ID != only {
			continue
		}
		out = append(out, ValidateTask(ctx, s, t)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Errors counts the error problems in ps.
func Errors(ps []Problem) int {
	n := 0
	for _, p := range ps {
		if p.Severity == SeverityError {
			n++
		}
	}
	return n
}

// ValidateTask runs every check `task validate` reports for one loaded task
// of s, without Docker: LintTask (which includes the EDIT ME marker, hidden
// tests visible in the repo and symlinks), the task listed in suite.yaml, no
// .git folder in the repo, file and folder sizes, a secret scan of the whole
// task folder (findings name file and line and mask the value), a warning to
// review hidden tests cut from a reference commit, and that the reference
// diff and every mutant apply cleanly to a temporary copy of the repo (a
// warning instead when git is not installed: admission's reference runs
// still apply it). Problems are sorted by path.
func ValidateTask(ctx context.Context, s *schemas.Suite, t *schemas.Task) []Problem {
	out := LintTask(s, t)
	taskYAML := rel(s.Root, filepath.Join(t.Dir, TaskFile))
	add := func(path, sev, format string, args ...any) {
		out = append(out, Problem{Path: path, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	if !slices.Contains(s.TaskIDs, t.ID) {
		add(SuiteFile, SeverityError, "tasks: %q is not listed; add it so the suite runs it", t.ID)
	}
	repo := filepath.Join(t.Dir, filepath.FromSlash(t.RepoDir))
	if gitDirs := findGitDirs(repo); len(gitDirs) > 0 {
		add(rel(s.Root, repo), SeverityError, "holds git metadata (%s); a task repo is a plain snapshot", strings.Join(gitDirs, ", "))
	}
	sizes := lintSizes(s.Root, t)
	out = append(out, sizes...)
	findings, err := ScanSecrets(t.Dir)
	if err != nil {
		add(taskYAML, SeverityError, "%v", err)
	}
	for _, f := range findings {
		add(rel(s.Root, filepath.Join(t.Dir, filepath.FromSlash(f.Path))), SeverityError,
			"line %d: possible %s (%s); remove it, or replace it with a fake that matches no secret pattern", f.Line, f.Kind, f.Masked)
	}
	if t.Provenance != nil && t.Provenance.ReferenceCommit != "" {
		add(rel(s.Root, filepath.Join(t.Dir, filepath.FromSlash(t.Verifier.Tests))), SeverityWarning,
			"these tests come from reference commit %s; review them: tests written beside the fix can encode the implementation too closely",
			short(t.Provenance.ReferenceCommit))
	}
	patches := append([]string{t.Controls.Reference}, t.Controls.Mutants...)
	_, gitErr := exec.LookPath("git")
	switch {
	case utils.ControlKind(s, t) != utils.ControlPatch:
		// Script and answer controls are not patches; admission runs them.
	case Errors(sizes) > 0:
		// Too large to copy for the apply check; the size error already refuses it.
	case gitErr != nil:
		add(taskYAML, SeverityWarning, "git is not installed, so the reference diff and the mutants were not checked to apply; admission's reference runs still apply them")
	default:
		for _, p := range patches {
			if err := ApplyCheck(ctx, repo, filepath.Join(t.Dir, filepath.FromSlash(p))); err != nil {
				add(rel(s.Root, filepath.Join(t.Dir, filepath.FromSlash(p))), SeverityError, "does not apply cleanly to %s: %v", t.RepoDir, err)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// lintSizes warns about files over 5 MB in the task folder and about a repo
// with more than 5,000 files, and refuses a task folder over 200 MB. Symlinks
// are not followed and count by their own size.
func lintSizes(root string, t *schemas.Task) []Problem {
	var out []Problem
	var total int64
	var large []string
	repo := filepath.Join(t.Dir, filepath.FromSlash(t.RepoDir))
	repoFiles := 0
	err := filepath.WalkDir(t.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if strings.HasPrefix(path, repo+string(filepath.Separator)) {
			repoFiles++
		}
		if info.Mode().IsRegular() && info.Size() > largeFileBytes {
			large = append(large, path)
			if len(large) <= maxLargeListed {
				out = append(out, Problem{Path: rel(root, path), Severity: SeverityWarning,
					Message: fmt.Sprintf("%.1f MB, over 5 MB; large files slow every run, leave them out of the task if the agent does not need them", mb(info.Size()))})
			}
		}
		return nil
	})
	if err != nil {
		return append(out, Problem{Path: rel(root, t.Dir), Severity: SeverityError, Message: fmt.Sprintf("measure task folder: %v", err)})
	}
	if n := len(large) - maxLargeListed; n > 0 {
		out = append(out, Problem{Path: rel(root, t.Dir), Severity: SeverityWarning, Message: fmt.Sprintf("and %d more files over 5 MB", n)})
	}
	if repoFiles > maxRepoFiles {
		out = append(out, Problem{Path: rel(root, repo), Severity: SeverityWarning,
			Message: fmt.Sprintf("%d files, over 5,000; every run copies the whole repo, so a smaller snapshot runs faster", repoFiles)})
	}
	if total > maxTaskBytes {
		out = append(out, Problem{Path: rel(root, t.Dir), Severity: SeverityError,
			Message: fmt.Sprintf("task folder is %.1f MB, over the 200 MB limit; cut the repo down or leave large assets out", mb(total))})
	}
	return out
}

func mb(n int64) float64 { return float64(n) / (1 << 20) }

// ApplyCheck runs `git apply --check -p1` for patch against a temporary copy
// of repo, outside any git repository, so neither the task folder nor a
// repository around it is read or written. An empty patch applies.
func ApplyCheck(ctx context.Context, repo, patch string) error {
	fi, err := os.Stat(patch)
	if err != nil {
		return fmt.Errorf("read patch: %w", err)
	}
	if fi.Size() == 0 {
		return nil
	}
	tmp, err := os.MkdirTemp("", "bench-apply-")
	if err != nil {
		return fmt.Errorf("apply check: %w", err)
	}
	defer os.RemoveAll(tmp)
	work := filepath.Join(tmp, "repo")
	if err := copyTree(repo, work); err != nil {
		return fmt.Errorf("apply check: %w", err)
	}
	abs, err := filepath.Abs(patch)
	if err != nil {
		return fmt.Errorf("apply check: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "apply", "--check", "-p1", "--whitespace=nowarn", abs)
	cmd.Dir = work
	cmd.Env = append(cleanGitEnv(os.Environ()), "GIT_CEILING_DIRECTORIES="+tmp, "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// cleanGitEnv drops variables that would point git at another repository.
func cleanGitEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// copyTree copies regular files, folders and symlinks from src to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, r)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			return copyFile(path, target)
		}
		return nil
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// findGitDirs lists .git entries (folders or files) under dir.
func findGitDirs(dir string) []string {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.EqualFold(d.Name(), ".git") {
			out = append(out, rel(dir, path))
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return append(out, err.Error())
	}
	return out
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// SuiteProblems is the suite-level part of StaticValidation (no task
// checks), so a caller validating a few tasks runs it once.
func SuiteProblems(s *schemas.Suite) []Problem { return lintSuite(s) }
