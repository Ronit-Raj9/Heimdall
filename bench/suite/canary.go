package suite

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// StampResult is one instruction file `suite canary -stamp` looked at.
type StampResult struct {
	Task    string // task id
	Path    string // absolute path of the instruction file
	Changed bool   // the file was rewritten
}

// EnsureCanary makes sure the suite.yaml in suiteRoot carries a canary. A
// valid canary already there is kept and returned with created false; when
// there is none, a new one is written into suite.yaml (only the `canary:`
// line changes) and returned with created true. A value that is not a GUID
// is an error rather than silently replaced, since text may already have
// been stamped with it. Setting the canary changes no lock hash: suite.lock
// does not hash suite.yaml.
func EnsureCanary(suiteRoot string) (guid string, created bool, err error) {
	path := filepath.Join(suiteRoot, SuiteFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	if g := utils.SuiteYAMLCanary(data); g != "" {
		if !utils.ValidCanary(g) {
			return "", false, fmt.Errorf("%s: canary %q is not a lowercase GUID; fix or remove the line and run suite canary again", path, g)
		}
		return g, false, nil
	}
	guid = utils.NewCanary()
	out, _ := utils.SetSuiteYAMLCanary(data, guid)
	if err := writeKeepMode(path, out); err != nil {
		return "", false, err
	}
	return guid, true, nil
}

// InstructionFiles lists the instruction files of every task of s: the
// task's instruction and each session's instruction, as absolute paths in
// task order, each file once. A path that escapes its task folder is an
// error.
func InstructionFiles(s *schemas.Suite) ([]StampResult, error) {
	var out []StampResult
	seen := map[string]bool{}
	for _, t := range s.Tasks {
		rels := []string{t.Instruction}
		for _, sess := range t.Sessions {
			rels = append(rels, sess.Instruction)
		}
		for _, rel := range rels {
			if rel == "" {
				continue
			}
			p, err := resolveIn(t.Dir, rel)
			if err != nil {
				return nil, fmt.Errorf("task %s: instruction %s: %w", t.ID, rel, err)
			}
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, StampResult{Task: t.ID, Path: p})
		}
	}
	return out, nil
}

// StampCanary writes the canary line for guid into every instruction file of
// s (InstructionFiles) and reports what changed. It is idempotent: a file
// that already ends with the right line is left alone, and an older canary
// line of the same format is replaced. Stamping changes instruction hashes,
// so the suite must be locked and admitted again afterwards; callers say so.
func StampCanary(s *schemas.Suite, guid string) ([]StampResult, error) {
	return StampCanaryTasks(s, guid, nil)
}

// StampCanaryTasks is StampCanary limited to the tasks in only (every task
// when only is nil). Publishing held-out tasks into an existing benchmark
// stamps just the tasks it adds, so the benchmark's admitted tasks keep
// their instruction hashes and readiness records.
func StampCanaryTasks(s *schemas.Suite, guid string, only []string) ([]StampResult, error) {
	if !utils.ValidCanary(guid) {
		return nil, fmt.Errorf("suite %s: canary %q is not a lowercase GUID", s.ID, guid)
	}
	files, err := InstructionFiles(s)
	if err != nil {
		return nil, fmt.Errorf("suite %s: %w", s.ID, err)
	}
	var want map[string]bool
	if only != nil {
		want = map[string]bool{}
		for _, id := range only {
			want[id] = true
		}
	}
	for i, f := range files {
		if want != nil && !want[f.Task] {
			continue
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			return files[:i], fmt.Errorf("task %s: read instruction: %w", f.Task, err)
		}
		out, changed := StampText(filepath.Base(f.Path), string(data), guid)
		if !changed {
			continue
		}
		if err := writeKeepMode(f.Path, []byte(out)); err != nil {
			return files[:i], fmt.Errorf("task %s: %w", f.Task, err)
		}
		files[i].Changed = true
	}
	return files, nil
}

// StampText returns text with exactly one canary line for guid, in the
// comment form of name's extension (utils.CanaryLine), as its last line, and
// whether that differs from text. Every existing canary line is removed
// first, so a second run changes nothing and a new GUID replaces an old one.
// The line goes last so the task's own words stay first in the agent's
// prompt; a blank line separates it from the text. CRLF files keep CRLF. It
// is pure.
func StampText(name, text, guid string) (string, bool) {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	var body strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if _, ok := utils.CanaryLineGUID(line); ok {
			continue
		}
		body.WriteString(line)
	}
	out := body.String()
	switch {
	case out == "":
	case strings.HasSuffix(out, nl+nl):
	case strings.HasSuffix(out, nl):
		out += nl
	default:
		out += nl + nl
	}
	out += utils.CanaryLine(name, guid) + nl
	return out, out != text
}

// writeKeepMode replaces path with data through a temp file and a rename,
// keeping the file's permission bits.
func writeKeepMode(path string, data []byte) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(name, fi.Mode().Perm()); err != nil {
		os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
