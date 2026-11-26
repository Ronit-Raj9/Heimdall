package taskguard

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/pii"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

const (
	wantPass = "pass"
	gotPass  = "pass"
	gotFail  = "fail"

	// maxFileBytes is how much of each file the leak and privacy checks read.
	maxFileBytes = 1 << 20
	// minLeakRunes is the shortest leak text the guard looks for, after fold.
	minLeakRunes = 12
	// minDiffLineRunes is the shortest added reference line the guard looks
	// for in the instruction and workspace of an issue task.
	minDiffLineRunes = 20
	// maxWorkspaceFiles caps the visible workspace files read per task.
	maxWorkspaceFiles = 5000
)

var (
	sha256Source = regexp.MustCompile(`sha256:[0-9a-f]{64}`)
	hexCommit    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	mutantCheck  = regexp.MustCompile(`^mutant_\d+$`)
	refCheck     = regexp.MustCompile(`^reference_\d+$`)
)

type checker struct {
	s       *schemas.Suite
	t       *schemas.Task
	rec     *schemas.AdmissionRecord
	g       *Record
	builder string
}

func result(name string, ok bool, detail string) schemas.AdmissionCheck {
	c := schemas.AdmissionCheck{Name: name, Want: wantPass, Got: gotPass, Passed: ok}
	if !ok {
		c.Got = gotFail
		c.Detail = detail
	}
	return c
}

func (c *checker) noop() schemas.AdmissionCheck {
	seen, ok := 0, true
	for _, ch := range c.rec.Checks {
		switch ch.Name {
		case "empty_patch", "null_noop", "null_refuse":
			seen++
			if !ch.Passed {
				ok = false
			}
		}
	}
	return result(CheckNoop, seen > 0 && ok, "An agent that does nothing passes this task.")
}

func (c *checker) control() schemas.AdmissionCheck {
	seen, ok := 0, true
	for _, ch := range c.rec.Checks {
		if ch.Name == "exploit" || mutantCheck.MatchString(ch.Name) {
			seen++
			if !ch.Passed {
				ok = false
			}
		}
	}
	return result(CheckControl, seen > 0 && ok, "No near miss is caught: the checks may not test what went wrong.")
}

func (c *checker) leak() schemas.AdmissionCheck {
	var needles []string
	for _, l := range c.g.LeakTexts {
		if f := fold(l); len([]rune(f)) >= minLeakRunes {
			needles = append(needles, f)
		}
	}
	if c.builder == BuilderIssue {
		needles = append(needles, referenceAddedLines(c.t)...)
	}
	if len(needles) == 0 {
		return result(CheckLeak, true, "")
	}
	for _, rel := range c.visibleFiles() {
		data := readCapped(filepath.Join(c.t.Dir, rel))
		if data == nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			fl := fold(line)
			if fl == "" {
				continue
			}
			for _, n := range needles {
				if strings.Contains(fl, n) {
					return result(CheckLeak, false, fmt.Sprintf("The instruction or the workspace holds the answer: %s, line %d.", filepath.ToSlash(rel), i+1))
				}
			}
		}
	}
	return result(CheckLeak, true, "")
}

func (c *checker) flaky() schemas.AdmissionCheck {
	n, k := 0, 0
	for _, ch := range c.rec.Checks {
		if refCheck.MatchString(ch.Name) {
			n++
			if ch.Passed {
				k++
			}
		}
	}
	if k < n {
		return result(CheckFlaky, false, fmt.Sprintf("The reference does not pass every time (%d of %d).", k, n))
	}
	if len(c.g.FlakyTests) > 0 {
		return result(CheckFlaky, false, "Tests that flip between runs: "+strings.Join(c.g.FlakyTests, ", ")+".")
	}
	if c.s != nil && c.s.Root != "" {
		if h, err := suite.ReadHealthFile(c.s.Root); err == nil {
			if _, q := h.Quarantined[c.t.ID]; q {
				return result(CheckFlaky, false, "The task is set aside as flaky in this benchmark.")
			}
		}
	}
	return result(CheckFlaky, true, "")
}

func (c *checker) determinism() schemas.AdmissionCheck {
	rs := c.rec.ReferenceResults
	for _, r := range rs {
		if r != rs[0] {
			return result(CheckDeterminism, false, "The reference got different scores on the same code: "+strings.Join(rs, ", ")+".")
		}
	}
	return result(CheckDeterminism, true, "")
}

func (c *checker) clarity() schemas.AdmissionCheck {
	if p := c.t.Provenance; p != nil && p.InstructionReviewed != nil && *p.InstructionReviewed {
		return result(CheckClarity, true, "")
	}
	cl := c.g.Clarity
	if cl != nil && cl.Verdict == ClarityClear {
		return result(CheckClarity, true, "")
	}
	first := "no check was run"
	if cl != nil && len(cl.Problems) > 0 && cl.Problems[0].Quote != "" {
		first = cl.Problems[0].Quote
	}
	return result(CheckClarity, false, fmt.Sprintf("The instruction may be unclear: %s. A person must approve or edit it.", first))
}

func (c *checker) provenance() schemas.AdmissionCheck {
	p := c.t.Provenance
	ok := false
	switch c.builder {
	case BuilderTrace:
		ok = sha256Source.MatchString(p.Source)
	case BuilderIssue:
		ok = hexCommit.MatchString(p.BaseCommit) && hexCommit.MatchString(p.ReferenceCommit) && strings.HasPrefix(p.Source, "https://github.com/")
	case BuilderAPI, BuilderPolicy:
		ok = strings.TrimSpace(p.Source) != ""
	}
	return result(CheckProvenance, ok, "Where this task came from is not recorded.")
}

func (c *checker) privacy() schemas.AdmissionCheck {
	sum := pii.Summary{}
	files := c.visibleFiles()
	for _, name := range []string{"scenario.yaml", "scenario.yml"} {
		if _, err := os.Stat(filepath.Join(c.t.Dir, name)); err == nil {
			files = append(files, name)
		}
	}
	for _, rel := range files {
		if data := readCapped(filepath.Join(c.t.Dir, rel)); data != nil {
			sum.Add(pii.Scan(string(data), pii.Policy{}))
		}
	}
	if sum.Total() == 0 {
		return result(CheckPrivacy, true, "")
	}
	return result(CheckPrivacy, false, "Personal data found: "+sum.String()+". Remove them first.")
}

// visibleFiles is the instruction and every file of the visible workspace,
// relative to the task folder.
func (c *checker) visibleFiles() []string {
	instr := c.t.Instruction
	if instr == "" {
		instr = "instruction.md"
	}
	out := []string{instr}
	if c.t.RepoDir == "" {
		return out
	}
	root := filepath.Join(c.t.Dir, c.t.RepoDir)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || len(out) >= maxWorkspaceFiles {
			return nil
		}
		if rel, err := filepath.Rel(c.t.Dir, p); err == nil {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// readCapped reads up to maxFileBytes of a text file; nil for a missing or
// binary file.
func readCapped(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes))
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return nil
	}
	return data
}

// referenceAddedLines are the folded added lines of an issue task's reference
// diff of 20 characters or more.
func referenceAddedLines(t *schemas.Task) []string {
	ref := t.Controls.Reference
	if !strings.HasSuffix(ref, ".diff") {
		ref = filepath.Join("solution", "reference.diff")
	}
	data := readCapped(filepath.Join(t.Dir, ref))
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		if f := fold(line[1:]); len([]rune(f)) >= minDiffLineRunes {
			out = append(out, f)
		}
	}
	return out
}

// derivedLeakTexts are the leak texts of a task without guard.json: the
// expected answers of its answer file and the argument values of the calls
// its checks.yaml expects, of 12 characters or more.
func derivedLeakTexts(t *schemas.Task) []string {
	var out []string
	answer := t.Verifier.Answer
	if answer == "" {
		answer = filepath.Join("verifier", "answer.yaml")
	}
	if data := readCapped(filepath.Join(t.Dir, answer)); data != nil {
		var spec schemas.AnswerSpec
		if yaml.Unmarshal(data, &spec) == nil {
			out = append(out, spec.Expected...)
		}
	}
	tests := t.Verifier.Tests
	if tests == "" {
		tests = "verifier"
	}
	if data := readCapped(filepath.Join(t.Dir, tests, "checks.yaml")); data != nil {
		var doc struct {
			Checks []struct {
				ToolCalled *struct {
					Args map[string]any `yaml:"args"`
				} `yaml:"tool_called"`
			} `yaml:"checks"`
		}
		if yaml.Unmarshal(data, &doc) == nil {
			for _, ch := range doc.Checks {
				if ch.ToolCalled != nil {
					out = append(out, argStrings(ch.ToolCalled.Args)...)
				}
			}
		}
	}
	var kept []string
	for _, s := range out {
		if len([]rune(fold(s))) >= minLeakRunes {
			kept = append(kept, s)
		}
	}
	return kept
}

func argStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case map[string]any:
		var out []string
		for _, e := range x {
			out = append(out, argStrings(e)...)
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, argStrings(e)...)
		}
		return out
	}
	return nil
}
