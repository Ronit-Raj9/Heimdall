package harbor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Offline reference solutions. Several Long-Horizon Terminal-Bench solutions
// end with `python -m pip install -e .` (sometimes with pinned extras) to
// install the task's own project, whose dependencies the image already has.
// Offline, pip's build isolation would still download the build backend. The
// importer bakes the build backend and the pinned extras into the image at
// build time and rewrites the line to install from what is there
// (`--no-index --no-build-isolation`), which installs the same project the
// same way with no network.

var editablePipRE = regexp.MustCompile(`^((?:python3?|/\S+/python3?)\s+-m\s+pip|pip3?)\s+install\s+(.+)$`)

// planSolveScript rewrites the editable pip installs of solve.sh. It returns
// the new script (empty when nothing changed), the build steps, the IMPORT.md
// lines, and the first line it cannot make offline (empty when none).
func planSolveScript(src string, buildRequires []string) (string, []string, []string, string) {
	phys := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var bake, changes []string
	changed := false
	for _, ll := range logicalLines(phys) {
		code := stripComment(ll.text)
		if code == "" || !solutionFetchRE.MatchString(code) {
			continue
		}
		m := editablePipRE.FindStringSubmatch(code)
		if m == nil || ll.first != ll.last || strings.ContainsAny(code, "$`;&|<>") {
			return "", nil, nil, short(code)
		}
		pip, args := m[1], strings.Fields(m[2])
		var extras []string
		editable := false
		for i := 0; i < len(args); i++ {
			switch a := args[i]; {
			case a == "-e" && i+1 < len(args) && args[i+1] == ".":
				editable = true
				i++
			case a == "-e." || a == "--editable=.":
				editable = true
			case strings.HasPrefix(a, "-"):
				return "", nil, nil, short(code) // an option the rewrite does not know
			default:
				if !strings.Contains(a, "==") {
					return "", nil, nil, short(code) // only pinned extras are baked
				}
				extras = append(extras, a)
			}
		}
		if !editable {
			return "", nil, nil, short(code)
		}
		step := pip + " install --no-cache-dir " + strings.Join(append(quoteAll(buildRequires), extras...), " ")
		bake = append(bake, step)
		line := strings.Replace(phys[ll.first], code, pip+" install --no-index --no-build-isolation "+m[2], 1)
		phys[ll.first] = line
		changed = true
		changes = append(changes, fmt.Sprintf("solve.sh `%s` installs offline: the build backend and pinned extras are baked at build (`%s`) and the line runs with `--no-index --no-build-isolation`", short(code), step))
	}
	if !changed {
		return "", nil, nil, ""
	}
	return strings.Join(phys, "\n"), bake, changes, ""
}

func quoteAll(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = shellQuote(s)
	}
	return out
}

// buildRequires is [build-system] requires of the project the image installs
// (the first pyproject.toml under environment/), else setuptools and wheel.
func buildRequires(envDir string) []string {
	var found []string
	_ = filepath.WalkDir(envDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != nil || d.IsDir() || d.Name() != "pyproject.toml" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		doc, err := parseTOML(string(b))
		if err != nil {
			return nil
		}
		for _, v := range tomlList(tomlTable(doc, "build-system"), "requires") {
			if s, ok := v.(string); ok {
				found = append(found, s)
			}
		}
		if found == nil {
			found = []string{}
		}
		return nil
	})
	if len(found) == 0 {
		return []string{"setuptools>=68", "wheel"}
	}
	return found
}
