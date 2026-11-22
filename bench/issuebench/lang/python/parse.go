package python

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// ParseResults reads pytest output: a JUnit XML report, the -rA short test
// summary, or both (a log with the report appended). Ids are pytest node ids
// ("tests/test_x.py::TestA::test_b[p]"). Where both name a test, the JUnit
// report wins. XFAIL and XPASS count as pass, as SWE-bench grades them.
func (Python) ParseResults(output []byte) (map[string]issuebench.TestStatus, error) {
	i := junitStart(output)
	if i < 0 {
		return ParseSummary(output), nil
	}
	out := ParseSummary(output[:i])
	junit, err := ParseJUnit(output[i:])
	for k, v := range junit {
		out[k] = v
	}
	if err != nil && len(out) == 0 {
		return out, err
	}
	return out, nil
}

// junitStart is the offset of an XML report in output, or -1.
func junitStart(output []byte) int {
	for _, marker := range []string{"<?xml", "<testsuites", "<testsuite"} {
		if i := bytes.Index(output, []byte(marker)); i >= 0 {
			return i
		}
	}
	return -1
}

var (
	ansiRe    = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	summaryRe = regexp.MustCompile(`^(PASSED|FAILED|ERROR|SKIPPED|XFAIL|XPASS)\s+(.+)$`)
	verboseRe = regexp.MustCompile(`^(\S+::\S.*?)\s+(PASSED|FAILED|ERROR|SKIPPED|XFAIL|XPASS)(\s+\[\s*\d+%\])?$`)
)

func status(word string) issuebench.TestStatus {
	switch word {
	case "PASSED", "XFAIL", "XPASS":
		return issuebench.TestPass
	case "FAILED":
		return issuebench.TestFail
	case "ERROR":
		return issuebench.TestError
	default:
		return issuebench.TestSkip
	}
}

// ParseSummary reads the -rA short test summary lines (and pytest -v
// result lines). SKIPPED summary lines without a node id are ignored.
func ParseSummary(text []byte) map[string]issuebench.TestStatus {
	out := map[string]issuebench.TestStatus{}
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(ansiRe.ReplaceAllString(strings.TrimRight(line, "\r"), ""))
		if m := summaryRe.FindStringSubmatch(line); m != nil {
			id := m[2]
			if m[1] == "FAILED" || m[1] == "ERROR" || m[1] == "XFAIL" || m[1] == "SKIPPED" {
				if i := strings.Index(id, " - "); i >= 0 {
					id = id[:i]
				}
			}
			if !strings.Contains(id, "::") && !strings.HasSuffix(id, ".py") {
				continue // "SKIPPED [1] tests/x.py:3: reason"
			}
			set(out, NormalizeID(id), status(m[1]))
			continue
		}
		if m := verboseRe.FindStringSubmatch(line); m != nil {
			set(out, NormalizeID(m[1]), status(m[2]))
		}
	}
	return out
}

// set keeps the worst status for a test reported twice (a passing call
// with an erroring teardown).
func set(out map[string]issuebench.TestStatus, id string, s issuebench.TestStatus) {
	if id == "" {
		return
	}
	if old, ok := out[id]; ok && (old == issuebench.TestFail || old == issuebench.TestError) {
		return
	}
	out[id] = s
}

// NormalizeID turns a pytest node id into the form used everywhere: forward
// slashes, no leading "./", no surrounding space.
func NormalizeID(id string) string {
	id = strings.TrimSpace(strings.ReplaceAll(id, `\`, "/"))
	for strings.HasPrefix(id, "./") {
		id = id[2:]
	}
	return id
}

type junitCase struct {
	Classname string    `xml:"classname,attr"`
	Name      string    `xml:"name,attr"`
	File      string    `xml:"file,attr"`
	Failure   *struct{} `xml:"failure"`
	Error     *struct{} `xml:"error"`
	Skipped   *struct {
		Type    string `xml:"type,attr"`
		Message string `xml:"message,attr"`
	} `xml:"skipped"`
}

// ParseJUnit reads a JUnit XML report (pytest's, xunit1 or xunit2) into
// pytest node ids.
func ParseJUnit(data []byte) (map[string]issuebench.TestStatus, error) {
	out := map[string]issuebench.TestStatus{}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	seen := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if seen {
				break // trailing log text after the report
			}
			return out, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "testsuite", "testsuites":
			seen = true
		case "testcase":
			seen = true
			var c junitCase
			if err := dec.DecodeElement(&c, &se); err != nil {
				return out, err
			}
			s := issuebench.TestPass
			switch {
			case c.Failure != nil:
				s = issuebench.TestFail
			case c.Error != nil:
				s = issuebench.TestError
			case c.Skipped != nil:
				s = issuebench.TestSkip
				if strings.Contains(strings.ToLower(c.Skipped.Type+c.Skipped.Message), "xfail") {
					s = issuebench.TestPass
				}
			}
			set(out, JUnitID(c.Classname, c.Name, c.File), s)
		}
	}
	if !seen {
		return out, errors.New("no JUnit testsuite in output")
	}
	return out, nil
}

// JUnitID maps a JUnit testcase to a pytest node id. With a file attribute
// (xunit1) the path is exact; without one the module is the classname up to
// the first part that starts with an upper-case letter (a test class).
func JUnitID(classname, name, file string) string {
	parts := splitDots(classname)
	if file = NormalizeID(file); file != "" {
		mod := strings.TrimSuffix(strings.ReplaceAll(file, "/", "."), ".py")
		rest := parts
		if strings.HasPrefix(classname, mod) {
			rest = splitDots(strings.TrimPrefix(classname, mod))
		}
		return strings.Join(append(append([]string{file}, rest...), name), "::")
	}
	if len(parts) == 0 {
		return name
	}
	k := len(parts)
	for i, p := range parts {
		if i > 0 && p != "" && p[0] >= 'A' && p[0] <= 'Z' {
			k = i
			break
		}
	}
	mod := strings.Join(parts[:k], "/") + ".py"
	return strings.Join(append(append([]string{mod}, parts[k:]...), name), "::")
}

func splitDots(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ".") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
