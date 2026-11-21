package scaffold

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/utils"
)

// Task init limits.
const (
	largeFileBytes = 5 << 20 // files over this size are reported
	maxListed      = 10      // paths named in one message
)

// TaskInitOptions configures InitTask. Repo, Base and Reference name the
// source; everything is read from commits, so the state of the work tree
// does not matter and nothing in the repository is written.
type TaskInitOptions struct {
	SuiteDir     string   // the suite folder holding suite.yaml
	ID           string   // task id and folder name
	Repo         string   // git work tree, or a folder inside one to cut the task from
	Base         string   // commit before the change: the agent's starting point
	Reference    string   // commit with the change: the reference solution and the hidden tests
	Tests        []string // globs of test files; empty uses the suite template's defaults
	License      string   // SPDX id or "proprietary"
	Capabilities []string
	Difficulty   string
	Kind         string // bugfix (default), feature, refactor or performance
	Force        bool   // replace an existing task folder
	// ExcludeDir is a folder to leave out of the snapshot: the bench project
	// itself when it lives inside the repository being cut.
	ExcludeDir string
}

// TaskInitResult says what InitTask wrote.
type TaskInitResult struct {
	Dir         string   // the task folder
	Template    string   // the suite template the task follows
	Hidden      []string // repository paths of the hidden test files
	Protected   []string // visible test files listed under protected
	Required    []string // required test ids written to task.yaml
	Warnings    []string
	SuiteEdited bool // the id was added to suite.yaml
	// From and EditMe are set by InitScenarioTask: the task whose shape was
	// copied, and the files that got an EDIT ME marker.
	From   string
	EditMe []string
}

// taskSource is everything read from git for one task.
type taskSource struct {
	repo      *gitRepo
	base, ref string
	baseTree  []treeEntry
	hidden    []treeEntry // reference versions of the hidden test files
	reference []byte      // the reference diff, test files excluded
	message   string
	exclude   string // prefix-relative folder left out of the snapshot
}

// InitTask turns a commit of a git repository into a task folder
// <suite>/tasks/<id>: repo/ is the tree at Base, verifier/tests/ the test
// files Reference added or changed (at their repository paths),
// solution/reference.diff the rest of the change, controls/ the empty diff
// and a generic exploit, instruction.md a draft from the commit message under
// the EDIT ME marker, and task.yaml with provenance. The id is then added to
// suite.yaml. It refuses a base that is not an ancestor of the reference, a
// change outside the chosen folder, a change with no test files or with only
// test files, symlinks that leave the repository, and .git folders in the
// tree. The task is written to a temporary folder and renamed into place, so
// a failure leaves nothing behind.
func InitTask(ctx context.Context, opts TaskInitOptions) (*TaskInitResult, error) {
	if !ValidID(opts.ID) {
		return nil, fmt.Errorf("task id %q: want lowercase letters, digits, '.', '_' or '-'", opts.ID)
	}
	suitePath := filepath.Join(opts.SuiteDir, "suite.yaml")
	suiteYAML, err := os.ReadFile(suitePath)
	if err != nil {
		return nil, fmt.Errorf("read suite: %w", err)
	}
	res := &TaskInitResult{Dir: filepath.Join(opts.SuiteDir, "tasks", opts.ID), Template: DetectTemplate(suiteYAML)}
	globs := opts.Tests
	if len(globs) == 0 {
		if globs = DefaultTestGlobs(res.Template); len(globs) == 0 {
			return nil, fmt.Errorf("%s: cannot tell the suite template; pass -tests with the globs of the test files", suitePath)
		}
	}
	if _, err := os.Stat(res.Dir); err == nil && !opts.Force {
		return nil, fmt.Errorf("task folder %s exists; pass -force to replace it", res.Dir)
	}
	src, warnings, err := readSource(ctx, opts, globs)
	if err != nil {
		return nil, err
	}
	res.Warnings = append(res.Warnings, warnings...)
	files, err := taskFiles(ctx, opts, res, src, globs)
	if err != nil {
		return nil, err
	}
	// The suite edit is validated before the task is published, and a
	// failed suite write takes the new task folder back, so a failure leaves
	// no unlisted task that blocks a retry (QC2-21-09).
	edited, changed, err := AddTaskID(suiteYAML, opts.ID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", suitePath, err)
	}
	if err := writeTask(ctx, res.Dir, src, files, opts.Force); err != nil {
		return nil, err
	}
	if changed {
		if err := os.WriteFile(suitePath, edited, 0o644); err != nil {
			return nil, errors.Join(fmt.Errorf("update %s: %w", suitePath, err), os.RemoveAll(res.Dir))
		}
		res.SuiteEdited = true
	}
	return res, nil
}

// readSource reads the commits, classifies the change and runs every refusal
// check before anything is written.
func readSource(ctx context.Context, opts TaskInitOptions, globs []string) (*taskSource, []string, error) {
	repo, err := openRepo(ctx, opts.Repo)
	if err != nil {
		return nil, nil, err
	}
	src := &taskSource{repo: repo, exclude: excludedFolder(repo, opts.ExcludeDir)}
	if src.base, err = repo.resolve(ctx, opts.Base); err != nil {
		return nil, nil, fmt.Errorf("-base: %w", err)
	}
	if src.ref, err = repo.resolve(ctx, opts.Reference); err != nil {
		return nil, nil, fmt.Errorf("-reference: %w", err)
	}
	if src.base == src.ref {
		return nil, nil, errors.New("-base and -reference are the same commit")
	}
	if ok, err := repo.isAncestor(ctx, src.base, src.ref); err != nil {
		return nil, nil, err
	} else if !ok {
		return nil, nil, fmt.Errorf("-base %s is not an ancestor of -reference %s", shortSHA(src.base), shortSHA(src.ref))
	}
	changes, outside, err := repo.changes(ctx, src.base, src.ref)
	if err != nil {
		return nil, nil, err
	}
	if len(outside) > 0 {
		return nil, nil, fmt.Errorf("the reference commit changes %d paths outside %s: %s; cut the task from the repository root or pick a commit that stays inside the folder",
			len(outside), repo.prefix, listSome(outside))
	}
	var warnings []string
	hiddenPaths, nonTest, w := classify(changes, globs, src.exclude)
	warnings = append(warnings, w...)
	if len(hiddenPaths) == 0 {
		return nil, nil, fmt.Errorf("the reference commit adds or changes no test files matching %s; pass -tests with the globs of its test files, or write verifier/tests by hand (docs/tasks-from-your-repo.md)",
			strings.Join(globs, ","))
	}
	if len(nonTest) == 0 {
		return nil, nil, errors.New("the reference commit changes only test files, so there is no reference solution")
	}
	w, err = src.load(ctx, hiddenPaths, nonTest)
	if err != nil {
		return nil, nil, err
	}
	return src, append(warnings, w...), nil
}

// load reads both trees, the reference diff and the message, and checks
// every entry that will be written.
func (s *taskSource) load(ctx context.Context, hiddenPaths, nonTest []string) ([]string, error) {
	var warnings []string
	var err error
	if s.baseTree, err = s.repo.tree(ctx, s.base); err != nil {
		return nil, err
	}
	kept := s.baseTree[:0]
	for _, e := range s.baseTree {
		if s.exclude != "" && (e.Path == s.exclude || strings.HasPrefix(e.Path, s.exclude+"/")) {
			continue
		}
		w, keep, err := checkEntry(e)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, w...)
		if keep {
			kept = append(kept, e)
		}
	}
	s.baseTree = kept
	refTree, err := s.repo.tree(ctx, s.ref)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]treeEntry, len(refTree))
	for _, e := range refTree {
		byPath[e.Path] = e
	}
	for _, p := range hiddenPaths {
		e, ok := byPath[p]
		if !ok {
			return nil, fmt.Errorf("hidden test %s is missing from the reference tree", p)
		}
		w, keep, err := checkEntry(e)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, w...)
		if keep {
			s.hidden = append(s.hidden, e)
		}
	}
	bins, err := s.repo.binaries(ctx, s.base, s.ref, nonTest)
	if err != nil {
		return nil, err
	}
	if len(bins) > 0 {
		warnings = append(warnings, fmt.Sprintf("the reference diff changes %d binary files (%s); check that it applies and that the grader can read them", len(bins), listSome(bins)))
	}
	if s.reference, err = s.repo.diff(ctx, s.base, s.ref, nonTest); err != nil {
		return nil, err
	}
	if s.message, err = s.repo.message(ctx, s.ref); err != nil {
		return nil, err
	}
	return warnings, nil
}

// classify splits the change into hidden test files (added or changed test
// files) and the rest. Deleted test files stay visible at their base version.
func classify(changes []change, globs []string, exclude string) (hidden, rest, warnings []string) {
	for _, c := range changes {
		if exclude != "" && (c.Path == exclude || strings.HasPrefix(c.Path, exclude+"/")) {
			warnings = append(warnings, fmt.Sprintf("%s is in the bench project folder and is left out", c.Path))
			continue
		}
		test := MatchAny(globs, c.Path)
		switch {
		case test && c.Status == "D":
			warnings = append(warnings, fmt.Sprintf("the reference commit deletes test file %s; it stays visible in repo/ at its base version", c.Path))
		case test:
			hidden = append(hidden, c.Path)
		default:
			rest = append(rest, c.Path)
		}
	}
	sort.Strings(hidden)
	sort.Strings(rest)
	return hidden, rest, warnings
}

// checkEntry refuses unsafe tree entries and reports odd ones. Submodules
// are left out (keep false).
func checkEntry(e treeEntry) ([]string, bool, error) {
	if err := checkRel(e.Path); err != nil {
		return nil, false, fmt.Errorf("tree path %q: %w", e.Path, err)
	}
	for _, seg := range strings.Split(e.Path, "/") {
		if strings.EqualFold(seg, ".git") {
			return nil, false, fmt.Errorf("tree path %s holds a .git folder; a task repo must not carry git metadata", e.Path)
		}
	}
	var warnings []string
	switch {
	case e.Type == "commit" || e.Mode == "160000":
		return []string{fmt.Sprintf("submodule %s is left out of repo/", e.Path)}, false, nil
	case e.Size > largeFileBytes:
		warnings = append(warnings, fmt.Sprintf("%s is %.1f MB; large files slow every run", e.Path, float64(e.Size)/(1<<20)))
	}
	return warnings, true, nil
}

// taskFiles builds every generated file of the task (the snapshot and the
// hidden tests are streamed separately by writeTask).
func taskFiles(ctx context.Context, opts TaskInitOptions, res *TaskInitResult, src *taskSource, globs []string) ([]File, error) {
	hiddenData := map[string][]byte{}
	if err := src.repo.catBlobs(ctx, shasOf(src.hidden), func(sha string, data []byte) error {
		for _, e := range src.hidden {
			if e.SHA == sha {
				hiddenData[e.Path] = data
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, e := range src.hidden {
		res.Hidden = append(res.Hidden, e.Path)
	}
	for _, e := range src.baseTree {
		if MatchAny(globs, e.Path) {
			res.Protected = append(res.Protected, e.Path)
		}
	}
	files, err := TaskControls(templateOr(res.Template))
	if err != nil {
		return nil, err
	}
	extra, required, warnings, err := supportFiles(ctx, res.Template, src, hiddenData)
	if err != nil {
		return nil, err
	}
	res.Required = required
	res.Warnings = append(res.Warnings, warnings...)
	files = mergeSupport(files, extra, hiddenData)
	subject, body := SplitMessage(src.message)
	instr, err := Instruction(subject, body, src.ref)
	if err != nil {
		return nil, err
	}
	source := SanitizeSource(src.repo.remoteURL(ctx))
	if source == "" {
		source = src.repo.top
	}
	if src.repo.prefix != "" {
		source += "//" + strings.TrimSuffix(src.repo.prefix, "/")
	}
	meta, err := TaskYAML(TaskMeta{
		ID: opts.ID, Title: subject, Project: projectName(src.repo), Kind: opts.Kind,
		Required: required, Protected: res.Protected, Capabilities: opts.Capabilities, Difficulty: opts.Difficulty,
		Source: source, License: opts.License, Base: src.base, Reference: src.ref,
	})
	if err != nil {
		return nil, err
	}
	files = append(files,
		File{Path: "task.yaml", Data: meta, Mode: 0o644},
		File{Path: "instruction.md", Data: instr, Mode: 0o644},
		File{Path: "solution/reference.diff", Data: src.reference, Mode: 0o644})
	sortFiles(files)
	return files, nil
}

// templateOr falls back to the generic controls for a suite whose template
// is unknown (task init was given -tests).
func templateOr(tmpl string) string {
	if tmpl == "" {
		return TemplateGenericReward
	}
	return tmpl
}

// mergeSupport drops a generated support file when a hidden file already
// sits at the same path, and adds the template-specific extras.
func mergeSupport(files, extra []File, hidden map[string][]byte) []File {
	out := files[:0]
	for _, f := range files {
		if rel, ok := strings.CutPrefix(f.Path, "verifier/tests/"); ok {
			if _, clash := hidden[rel]; clash {
				continue
			}
		}
		out = append(out, f)
	}
	return append(out, extra...)
}

// supportFiles derives the required test ids and, for Python, copies the
// base conftest.py files the hidden tests may rely on: pytest runs from
// /grader, so a fixture defined in a visible conftest.py would be missing.
func supportFiles(ctx context.Context, tmpl string, src *taskSource, hidden map[string][]byte) ([]File, []string, []string, error) {
	var required, warnings []string
	var extra []File
	switch tmpl {
	case TemplatePythonPytest:
		for _, e := range src.hidden {
			if IsTestFile(tmpl, e.Path) {
				required = append(required, PythonTestIDs(e.Path, hidden[e.Path])...)
			}
		}
		confs, err := baseConftests(ctx, src, hidden)
		if err != nil {
			return nil, nil, nil, err
		}
		extra = confs
		if _, ok := hidden["conftest.py"]; ok {
			warnings = append(warnings, "the hidden root conftest.py replaces the generated bootstrap; it must put /workspace on sys.path itself")
		}
	case TemplateGoTest:
		mods, err := goModules(ctx, src)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, e := range src.hidden {
			if !IsTestFile(tmpl, e.Path) {
				continue
			}
			imp, ok := goImportPath(mods, e.Path)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("no go.mod above %s; add its required test ids by hand", e.Path))
				continue
			}
			required = append(required, GoTestIDs(imp, hidden[e.Path])...)
		}
	}
	return extra, required, warnings, nil
}

// baseConftests returns the base conftest.py of every folder on the way to a
// hidden Python test, under verifier/tests. The repository root's one becomes
// repo_conftest.py, which the generated bootstrap conftest.py imports.
func baseConftests(ctx context.Context, src *taskSource, hidden map[string][]byte) ([]File, error) {
	want := map[string]string{} // base path -> verifier/tests path
	for _, e := range src.hidden {
		for dir := path.Dir(e.Path); ; dir = path.Dir(dir) {
			p := path.Join(dir, "conftest.py")
			if _, isHidden := hidden[p]; !isHidden {
				dst := p
				if dir == "." {
					dst = "repo_conftest.py"
				}
				want[p] = dst
			}
			if dir == "." {
				break
			}
		}
	}
	var entries []treeEntry
	for _, e := range src.baseTree {
		if _, ok := want[e.Path]; ok && e.Mode != "120000" {
			entries = append(entries, e)
		}
	}
	var out []File
	err := src.repo.catBlobs(ctx, shasOf(entries), func(sha string, data []byte) error {
		for _, e := range entries {
			if e.SHA == sha && !slices.ContainsFunc(out, func(f File) bool { return f.Path == "verifier/tests/"+want[e.Path] }) {
				out = append(out, File{Path: "verifier/tests/" + want[e.Path], Data: data, Mode: 0o644})
			}
		}
		return nil
	})
	return out, err
}

// goModules maps each folder holding a go.mod (at the reference) to its
// module path.
func goModules(ctx context.Context, src *taskSource) (map[string]string, error) {
	tree, err := src.repo.tree(ctx, src.ref)
	if err != nil {
		return nil, err
	}
	var entries []treeEntry
	for _, e := range tree {
		if path.Base(e.Path) == "go.mod" && e.Type == "blob" {
			entries = append(entries, e)
		}
	}
	mods := map[string]string{}
	err = src.repo.catBlobs(ctx, shasOf(entries), func(sha string, data []byte) error {
		for _, e := range entries {
			if e.SHA == sha {
				if mp := GoModulePath(data); mp != "" {
					mods[path.Dir(e.Path)] = mp
				}
			}
		}
		return nil
	})
	return mods, err
}

// goImportPath is the import path of the package holding file p, from the
// nearest go.mod above it.
func goImportPath(mods map[string]string, p string) (string, bool) {
	pkg := path.Dir(p)
	for dir := pkg; ; dir = path.Dir(dir) {
		if mp, ok := mods[dir]; ok {
			if dir == pkg {
				return mp, true
			}
			sub := strings.TrimPrefix(pkg, dir+"/")
			if dir == "." {
				sub = pkg
			}
			return mp + "/" + sub, true
		}
		if dir == "." {
			return "", false
		}
	}
}

// writeTask writes the task into a temporary sibling folder, streaming the
// base tree into repo/ and the hidden tests into verifier/tests/, then
// renames it into place.
func writeTask(ctx context.Context, dir string, src *taskSource, files []File, force bool) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".tmp-")
	if err != nil {
		return fmt.Errorf("create task folder: %w", err)
	}
	defer os.RemoveAll(tmp)
	// MkdirTemp creates 0700; the grader's containers read the task folder
	// as another user.
	if err := os.Chmod(tmp, 0o755); err != nil {
		return fmt.Errorf("create task folder: %w", err)
	}
	if err := streamTree(ctx, src.repo, filepath.Join(tmp, "repo"), src.baseTree); err != nil {
		return err
	}
	if err := streamTree(ctx, src.repo, filepath.Join(tmp, "verifier", "tests"), src.hidden); err != nil {
		return err
	}
	for _, tree := range []string{filepath.Join(tmp, "repo"), filepath.Join(tmp, "verifier", "tests")} {
		if err := refuseEscapingLinks(tree); err != nil {
			return err
		}
	}
	if err := WriteFiles(tmp, files, true); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(tmp, "repo"), 0o755); err != nil {
		return fmt.Errorf("create repo folder: %w", err)
	}
	if force {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("replace %s: %w", dir, err)
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("move task into place: %w", err)
	}
	return nil
}

// streamTree writes tree entries under dir from one cat-file stream. A
// symlink is written only when its target stays inside the repository.
func streamTree(ctx context.Context, repo *gitRepo, dir string, entries []treeEntry) error {
	bySHA := map[string][]treeEntry{}
	var shas []string
	for _, e := range entries {
		if _, seen := bySHA[e.SHA]; !seen {
			shas = append(shas, e.SHA)
		}
		bySHA[e.SHA] = append(bySHA[e.SHA], e)
	}
	return repo.catBlobs(ctx, shas, func(sha string, data []byte) error {
		var files []File
		for _, e := range bySHA[sha] {
			f := File{Path: e.Path, Data: data, Mode: 0o644}
			switch e.Mode {
			case "100755":
				f.Mode = 0o755
			case "120000":
				target := string(data)
				if err := checkLink(e.Path, target); err != nil {
					return err
				}
				f = File{Path: e.Path, Link: target}
			}
			files = append(files, f)
		}
		return WriteFiles(dir, files, false)
	})
}

// refuseEscapingLinks refuses a written tree holding a symlink whose real
// target leaves it. checkLink judges each link alone; a chain of links that
// each stay inside on paper (a/l -> .., a/m -> l/..) can still climb out
// once the first one is resolved, so the written tree is checked again with
// every link resolved.
func refuseEscapingLinks(tree string) error {
	links, err := utils.FindSymlinks(tree, tree)
	if err != nil {
		return err
	}
	for _, l := range links {
		if l.Escapes {
			return fmt.Errorf("symlink %s points outside the repository (%s, through another symlink); refusing", l.Path, l.Target)
		}
	}
	return nil
}

// checkLink refuses a symlink whose target is absolute or leaves the tree.
func checkLink(p, target string) error {
	if target == "" || path.IsAbs(target) {
		return fmt.Errorf("symlink %s points outside the repository (%s); refusing", p, target)
	}
	joined := path.Join(path.Dir(p), target)
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return fmt.Errorf("symlink %s points outside the repository (%s); refusing", p, target)
	}
	return nil
}

// excludedFolder returns dir relative to the repository folder the task is
// cut from, or "" when dir is not inside it.
func excludedFolder(repo *gitRepo, dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	top := filepath.Join(repo.top, filepath.FromSlash(repo.prefix))
	rel, err := filepath.Rel(top, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func projectName(repo *gitRepo) string {
	if repo.prefix != "" {
		return path.Base(strings.TrimSuffix(repo.prefix, "/"))
	}
	return filepath.Base(repo.top)
}

func shasOf(entries []treeEntry) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if !seen[e.SHA] {
			seen[e.SHA] = true
			out = append(out, e.SHA)
		}
	}
	return out
}

func listSome(paths []string) string {
	if len(paths) <= maxListed {
		return strings.Join(paths, ", ")
	}
	return strings.Join(paths[:maxListed], ", ") + fmt.Sprintf(" and %d more", len(paths)-maxListed)
}
