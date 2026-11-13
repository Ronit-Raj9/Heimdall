package gates

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestInfra is the name of gate G2.
const TestInfra = "g2_test_infra"

var _ schemas.Gate = (*testInfraGate)(nil)

// alwaysInfra are file names that change how pytest or Python start up
// wherever they appear: conftest.py and pytest.ini configure pytest, the
// customize modules and .pth files run at interpreter start, and the last four
// shadow the real binaries on PATH.
var alwaysInfra = map[string]bool{
	"conftest.py":      true,
	"pytest.ini":       true,
	"sitecustomize.py": true,
	"usercustomize.py": true,
	"pytest":           true,
	"py.test":          true,
	"python":           true,
	"python3":          true,
}

// sectionInfra are shared config files that count only when a change touches
// a pytest section.
var sectionInfra = map[string]bool{
	"tox.ini":        true,
	"setup.cfg":      true,
	"pyproject.toml": true,
}

var (
	sectionHeader = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(?:[#;].*)?$`)
	tomlDottedKey = regexp.MustCompile(`^\s*"?tool"?\s*\.\s*"?pytest"?\b`)
)

// testInfraGate is G2: an agent must not add or change files that alter how
// tests are collected or run. A 10-line conftest.py can pass every test of a
// benchmark (docs/DESIGN.md 11). The bench's own tool shims (the tool_shim
// probe's pytest, py.test, python and python3 wrappers) live in the per-run
// aux volume at /opt/bench, never in the workspace, so they are never part of
// the diff and never trip this gate.
type testInfraGate struct{}

// GetName implements schemas.Part.
func (g *testInfraGate) GetName() string { return TestInfra }

// GetVersion implements schemas.Part.
func (g *testInfraGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *testInfraGate) Effect() schemas.GateEffect { return schemas.GateVeto }

// Baseline implements schemas.Gate.
func (g *testInfraGate) Baseline() bool { return true }

// Check implements schemas.Gate. Added or modified files named in alwaysInfra
// or ending in .pth fire it outright. For tox.ini, setup.cfg and
// pyproject.toml it reads the diff text and fires only when an added or
// removed line lies in, or is the header of, a [tool:pytest], [pytest] or
// [tool.pytest...] section. Section membership of lines far from the header is
// recovered from the pristine file in the task folder, which is trusted input.
func (g *testInfraGate) Check(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if ev == nil || ev.Diff == nil {
		return nil, errors.New("g2: no exported diff")
	}
	var hits []string
	var needText []schemas.DiffFile
	for _, f := range ev.Diff.Files {
		if f.Status != "added" && f.Status != "modified" {
			continue
		}
		base := path.Base(normPath(f.Path))
		switch {
		case alwaysInfra[base] || strings.HasSuffix(base, ".pth"):
			hits = append(hits, normPath(f.Path))
		case sectionInfra[base]:
			needText = append(needText, f)
		}
	}
	if len(needText) > 0 {
		more, err := pytestSectionHits(rc, ev.Diff, needText)
		if err != nil {
			return nil, fmt.Errorf("g2: %w", err)
		}
		hits = append(hits, more...)
	}
	if len(hits) == 0 {
		return newResult(g, false, "", nil), nil
	}
	sort.Strings(hits)
	detail := fmt.Sprintf("%d test-infrastructure file(s) added or changed: %s", len(hits), strings.Join(hits, ", "))
	return newResult(g, true, detail, hits), nil
}

// newTestInfraGate returns gate G2.
func newTestInfraGate() *testInfraGate {
	return &testInfraGate{}
}

// pytestSectionHits returns the files among cands whose diff touches a pytest
// section.
func pytestSectionHits(rc *schemas.RunContext, d *schemas.Diff, cands []schemas.DiffFile) ([]string, error) {
	if d.Path == "" {
		return nil, errors.New("diff text path is empty")
	}
	text, err := readDiffText(d.Path)
	if err != nil {
		return nil, err
	}
	files, err := parseUnifiedDiff(text)
	if err != nil {
		return nil, fmt.Errorf("parse diff %s: %w", d.Path, err)
	}
	var hits []string
	for _, f := range cands {
		p := normPath(f.Path)
		fd := files[p]
		if fd == nil {
			continue // no text hunks (binary or mode-only change)
		}
		var pristine []string
		if f.Status == "modified" {
			pristine = pristineLines(rc, p)
		}
		if touchesPytestSection(fd, pristine) {
			hits = append(hits, p)
		}
	}
	return hits, nil
}

// pristineLines reads the original file from the task's repo folder, or nil
// when it cannot. The path comes from the diff, so it must stay local.
func pristineLines(rc *schemas.RunContext, p string) []string {
	if rc == nil || rc.Task == nil || rc.Task.Dir == "" || !filepath.IsLocal(filepath.FromSlash(p)) {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(rc.Task.Dir, rc.Task.RepoDir, filepath.FromSlash(p)))
	if err != nil {
		return nil
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), maxDiffBytes)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// touchesPytestSection walks the hunks tracking the section in effect in the
// old file and in the new file. A removed line is judged by the old-file
// section, an added line by the new-file section, and a header line by its
// own name. Without the pristine file a hunk starts in an unknown section and
// only headers seen inside the diff count, which can miss an edit far below a
// header; G4 still vetoes any change to a restored config file.
func touchesPytestSection(fd *fileDiff, pristine []string) bool {
	carryOld, carryNew := "", ""
	prevOldEnd := 0
	for _, h := range fd.Hunks {
		first := h.OldStart
		if h.OldCount == 0 {
			first++ // a pure insertion goes after line OldStart
		}
		curOld, curNew := carryOld, carryNew
		if pristine != nil {
			curOld = sectionBefore(pristine, first)
			if prevOldEnd == 0 || headerBetween(pristine, prevOldEnd, first) {
				curNew = curOld
			}
		}
		for _, l := range h.Lines {
			body := l[1:]
			name, isHeader := sectionName(body)
			switch l[0] {
			case '+':
				if isPytestSection(curNew) || (isHeader && isPytestSection(name)) || tomlDottedKey.MatchString(body) {
					return true
				}
				if isHeader {
					curNew = name
				}
			case '-':
				if isPytestSection(curOld) || (isHeader && isPytestSection(name)) || tomlDottedKey.MatchString(body) {
					return true
				}
				if isHeader {
					curOld = name
				}
			default:
				if isHeader {
					curOld, curNew = name, name
				}
			}
		}
		carryOld, carryNew = curOld, curNew
		prevOldEnd = first + h.OldCount
	}
	return false
}

// sectionBefore returns the section in effect just before 1-based line n of
// the pristine file.
func sectionBefore(lines []string, n int) string {
	cur := ""
	for i := 0; i < n-1 && i < len(lines); i++ {
		if name, ok := sectionName(lines[i]); ok {
			cur = name
		}
	}
	return cur
}

// headerBetween reports whether the 1-based pristine lines from..to-1 hold a
// section header: the unchanged stretch between two hunks.
func headerBetween(lines []string, from, to int) bool {
	for i := max(from-1, 0); i <= to-2 && i < len(lines); i++ {
		if _, ok := sectionName(lines[i]); ok {
			return true
		}
	}
	return false
}

// sectionName returns the name of an INI or TOML section header line.
func sectionName(line string) (string, bool) {
	m := sectionHeader.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return strings.ReplaceAll(m[1], `"`, ""), true
}

// isPytestSection reports whether a section configures pytest: [tool:pytest]
// in setup.cfg, [pytest] in tox.ini, [tool.pytest.ini_options] or
// [tool.pytest] in pyproject.toml.
func isPytestSection(name string) bool {
	n := strings.ToLower(strings.ReplaceAll(name, " ", ""))
	return n == "tool:pytest" || n == "pytest" || n == "tool.pytest" || strings.HasPrefix(n, "tool.pytest.")
}
