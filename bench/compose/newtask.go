package compose

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Limits of the folder `task init -from-compose` copies.
const (
	maxCopyFiles = 20000
	maxCopyBytes = 256 << 20
	// StackDir is the task folder's copy of the team's compose project.
	StackDir = "stack"
	// editMe is suite.EditMeMarker (suite imports this package, so the
	// value is repeated here; a test keeps them equal).
	editMe = "<!-- EDIT ME -->"
)

// EditMeMarker is the draft marker NewTask writes (suite.EditMeMarker).
func EditMeMarker() string { return editMe }

var taskIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// skipDirs are folders of a team's project that never belong in a task.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true, ".pytest_cache": true, ".mypy_cache": true}

// NewTaskOptions configure NewTask.
type NewTaskOptions struct {
	SuiteDir    string // benchmarks/<suite>, holding suite.yaml
	ID          string // the new task's id
	ComposeFile string // the team's compose file; its folder is copied into the task
	License     string // SPDX id or proprietary
	Force       bool   // replace an existing task folder
}

// NewTaskResult is what NewTask wrote and what the mapping found.
type NewTaskResult struct {
	Dir       string    // the new task folder
	Compose   string    // the compose file, relative to the task folder
	Workspace string    // the service the agent works in, empty when none was found
	Services  []string  // the other services, in start order
	Copied    int       // files copied from the team's folder
	Skipped   []string  // what was left out of the copy, with why
	Findings  []Finding // refused, changed and ignored settings
	EditMe    []string  // files to edit before the task validates, relative to Dir
}

// NewTask writes tasks/<id> of a suite from a team's own compose file: the
// file's folder is copied into stack/ (without .git, dependency folders and
// symlinks), task.yaml names it with compose:, and the instruction, the
// hidden verifier and the reference solution are drafts with EDIT ME
// markers. The compose file is mapped once so the caller can show what was
// refused, changed or ignored; a refused setting does not stop the write,
// but the task will not load until it is fixed.
func NewTask(opts NewTaskOptions) (*NewTaskResult, error) {
	if !taskIDRE.MatchString(opts.ID) {
		return nil, fmt.Errorf("task id %q: want lower-case letters, digits, '.', '_' or '-'", opts.ID)
	}
	if st, err := os.Stat(filepath.Join(opts.SuiteDir, "suite.yaml")); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("no suite.yaml in %s; create the suite first (bifrost-bench suite init)", opts.SuiteDir)
	}
	src, err := filepath.Abs(opts.ComposeFile)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(src); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("compose file %s not found", opts.ComposeFile)
	}
	dir := filepath.Join(opts.SuiteDir, "tasks", opts.ID)
	if _, err := os.Stat(dir); err == nil {
		if !opts.Force {
			return nil, fmt.Errorf("%s exists; pass -force to replace it", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
	}
	res := &NewTaskResult{Dir: dir, Compose: StackDir + "/" + filepath.Base(src)}
	if err := copyProject(filepath.Dir(src), filepath.Join(dir, StackDir), res); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	p, err := Parse(dir, res.Compose, Options{})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	mapped := p.Map(nil)
	res.Findings, res.Workspace = mapped.Findings, mapped.Workspace
	for _, sv := range mapped.Services {
		res.Services = append(res.Services, sv.ID)
	}
	license := opts.License
	if license == "" {
		license = "proprietary"
	}
	files := map[string]string{
		"task.yaml":                  taskYAML(opts.ID, res.Compose, license, filepath.Base(src)),
		"instruction.md":             editMe + "\n" + instructionDraft,
		"repo/README.md":             "The workspace comes from the workspace service's image (see " + res.Compose + "), not from this folder.\n",
		"verifier/tests/verify.py":   verifyDraft,
		"verifier/requirements.md":   editMe + " map every check of verify.py to the instruction sentence it checks, then delete this line.\n",
		"solution/reference.sh":      referenceDraft,
		"controls/empty.sh":          "#!/bin/sh\n# Empty control: does nothing, as an agent that gave up would. Admission\n# requires it to fail.\nexit 0\n",
		"controls/exploit.sh":        exploitDraft,
		"controls/mutant-example.sh": mutantDraft,
	}
	for rel, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			return nil, err
		}
	}
	res.EditMe = []string{"instruction.md", "verifier/tests/verify.py", "verifier/requirements.md", "solution/reference.sh", "controls/mutant-example.sh"}
	return res, nil
}

// copyProject copies the compose project folder into the task, leaving out
// version control, dependency and cache folders and every symlink.
func copyProject(src, dst string, res *NewTaskResult) error {
	var total int64
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		switch {
		case d.IsDir() && skipDirs[d.Name()] && rel != ".":
			res.Skipped = append(res.Skipped, slash+"/ (version control, dependencies or caches; the image build installs dependencies)")
			return filepath.SkipDir
		case d.Type()&fs.ModeSymlink != 0:
			res.Skipped = append(res.Skipped, slash+" (a symlink; tasks hold files only)")
			return nil
		case d.IsDir():
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		case !d.Type().IsRegular():
			res.Skipped = append(res.Skipped, slash+" (not a regular file)")
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		res.Copied++
		if res.Copied > maxCopyFiles || total > maxCopyBytes {
			return fmt.Errorf("the compose project %s is over %d files or %d MiB; point -from-compose at a compose file in a smaller folder", src, maxCopyFiles, maxCopyBytes>>20)
		}
		return copyOne(p, filepath.Join(dst, rel), info.Mode().Perm())
	})
}

func copyOne(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o200)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	return errors.Join(cerr, out.Close())
}

func taskYAML(id, compose, license, file string) string {
	return fmt.Sprintf(`# Written by bifrost-bench task init -from-compose from the team's %s.
# The environment is that compose file, copied into %s/: one service is the
# agent's workspace (x-heimdall: {workspace: true}), the others run beside it
# on the run's internal network (docs/multi-container.md). Edit every file
# with an EDIT ME line, then run task validate and task admit.
id: %s
version: 1.0.0
title: EDIT ME one line saying what the agent must do
project: %s
kind: bugfix
instruction: instruction.md
# The workspace comes from the workspace service's image.
repo_dir: repo
compose: %s
verifier:
  tests: verifier/tests
  required: []
protected: []
controls:
  reference: solution/reference.sh
  empty: controls/empty.sh
  mutants: [controls/mutant-example.sh]
  exploit: controls/exploit.sh
timeouts:
  agent_s: 900
provenance:
  license: %s
  created_by: task init -from-compose
`, file, path.Dir(compose), id, id, compose, license)
}

const instructionDraft = `
Write the request as a teammate would send it: what is wrong or missing,
what done looks like, and anything the agent must not change. Do not say
how the work is graded.
`

const verifyDraft = `# EDIT ME: the hidden verifier. It runs after the agent exited, in the
# workspace service's image, on the clean-room workspace (the image's files
# plus the agent's diff). With x-heimdall verifier_services: true in the
# compose file it joins the run's internal network and reaches every service
# by its compose name; the workspace service's environment is set, so the
# app's own settings (DATABASE_URL and the like) work here too.
#
# Write {"reward": 1} when every check passes, else {"reward": 0}, to
# /grader/out/reward.json. Print why a check failed.
import json

fails = ["EDIT ME: write the checks of this task"]
with open("/grader/out/reward.json", "w") as f:
    json.dump({"reward": 0 if fails else 1}, f)
for msg in fails:
    print("FAIL:", msg)
`

const referenceDraft = `#!/bin/sh
# EDIT ME: the reference solution, run in the agent's place inside the
# workspace container with the services up. It must make the verifier pass
# three times in a row. Delete these lines and the exit below.
echo "EDIT ME: write the reference solution" >&2
exit 1
`

const exploitDraft = `#!/bin/sh
# Exploit control: claims success without doing the work, by leaving fake
# reward files where a careless verifier might read them. Admission requires
# it to fail.
mkdir -p grader/out
printf '{"reward": 1}\n' > reward.json
printf '{"reward": 1}\n' > grader/out/reward.json
printf '{"reward": 1}\n' 2>/dev/null > /grader/out/reward.json || true
exit 0
`

const mutantDraft = `#!/bin/sh
# EDIT ME: a plausible wrong solution (one that looks right but breaks a
# requirement). Admission requires every mutant to fail. Add more as
# controls.mutants entries.
echo "EDIT ME: write a wrong solution" >&2
exit 0
`
