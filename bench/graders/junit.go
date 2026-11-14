package graders

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// junitReport is the part of a JUnit XML file the grader reads.
type junitReport struct {
	Tests    int
	Failures int
	Errors   int
	Skipped  int
	Cases    []junitCase
}

// junitCase is one testcase element.
type junitCase struct {
	Classname string
	Name      string
	Failed    bool
	Errored   bool
	Skipped   bool
}

// ID names the case as classname::name.
func (c junitCase) ID() string {
	if c.Classname == "" {
		return c.Name
	}
	return c.Classname + "::" + c.Name
}

type xmlSuite struct {
	XMLName  xml.Name
	Tests    *string    `xml:"tests,attr"`
	Failures *string    `xml:"failures,attr"`
	Errors   *string    `xml:"errors,attr"`
	Skipped  *string    `xml:"skipped,attr"`
	Suites   []xmlSuite `xml:"testsuite"`
	Cases    []xmlCase  `xml:"testcase"`
}

type xmlCase struct {
	Classname string     `xml:"classname,attr"`
	Name      string     `xml:"name,attr"`
	Failures  []struct{} `xml:"failure"`
	Errors    []struct{} `xml:"error"`
	Skipped   []struct{} `xml:"skipped"`
}

// parseJUnit reads a JUnit XML report whose root is <testsuites> or
// <testsuite>, summing counts over every <testsuite>. A count is never taken
// below what the suite's testcase elements show, so a missing or understated
// failures attribute cannot hide a failed case. Testcase elements directly
// under <testsuites>, which Node's junit reporter writes for top-level tests,
// count from the elements alone; otherwise a failing top-level test beside a
// passing describe block would vanish. It is pure.
func parseJUnit(data []byte) (*junitReport, error) {
	var root xmlSuite
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse junit: %w", err)
	}
	r := &junitReport{}
	switch root.XMLName.Local {
	case "testsuites":
		top := tallyCases(root.Cases)
		r.Tests, r.Failures, r.Errors, r.Skipped = top.Tests, top.Failures, top.Errors, top.Skipped
		r.Cases = top.Cases
		for _, s := range root.Suites {
			if err := r.addSuite(s); err != nil {
				return nil, err
			}
		}
	case "testsuite":
		if err := r.addSuite(root); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("parse junit: unexpected root element %q", root.XMLName.Local)
	}
	return r, nil
}

// addSuite adds one suite, and any nested suites, to r.
func (r *junitReport) addSuite(s xmlSuite) error {
	cases := tallyCases(s.Cases)
	r.Cases = append(r.Cases, cases.Cases...)
	counts := []struct {
		attr     *string
		fallback int
		dst      *int
		name     string
	}{
		{s.Tests, cases.Tests, &r.Tests, "tests"},
		{s.Failures, cases.Failures, &r.Failures, "failures"},
		{s.Errors, cases.Errors, &r.Errors, "errors"},
		{s.Skipped, cases.Skipped, &r.Skipped, "skipped"},
	}
	for _, c := range counts {
		n := c.fallback
		if c.attr != nil {
			v, err := strconv.Atoi(strings.TrimSpace(*c.attr))
			if err != nil || v < 0 {
				return fmt.Errorf("parse junit: bad %s count %q", c.name, *c.attr)
			}
			n = max(v, c.fallback)
		}
		*c.dst += n
	}
	for _, nested := range s.Suites {
		if err := r.addSuite(nested); err != nil {
			return err
		}
	}
	return nil
}

// tallyCases turns testcase elements into cases and counts them.
func tallyCases(xs []xmlCase) junitReport {
	var t junitReport
	for _, c := range xs {
		jc := junitCase{
			Classname: c.Classname,
			Name:      c.Name,
			Failed:    len(c.Failures) > 0,
			Errored:   len(c.Errors) > 0,
			Skipped:   len(c.Skipped) > 0,
		}
		t.Tests++
		t.Failures += boolInt(jc.Failed)
		t.Errors += boolInt(jc.Errored)
		t.Skipped += boolInt(jc.Skipped)
		t.Cases = append(t.Cases, jc)
	}
	return t
}

// requiredResult says, for each required id, whether it ran and passed.
type requiredResult struct {
	Missing []string // required ids with no matching testcase, or only skipped ones
	Failed  []string // required ids that ran and failed or errored
}

// checkRequired matches required ids of the form "path/file.py::test" or
// "file.py::Class::test" against testcases. pytest writes the module as a
// dotted classname relative to its rootdir (for example
// "tests.test_file"), so the module part matches as a dotted suffix, and a
// parametrized "test[param]" counts as an instance of "test". Classnames
// with slashes, as Go reporters write package paths ("example.com/calc"),
// match as if dotted, so "example.com/calc::TestAdd" and "calc::TestAdd"
// both work; pytest never writes a slash, so its matching is unchanged. A
// required id passes only when at least one instance ran and none failed,
// errored or was skipped. It is pure.
func checkRequired(r *junitReport, required []string) (requiredResult, error) {
	var out requiredResult
	for _, id := range required {
		cls, name, err := splitTestID(id)
		if err != nil {
			return out, err
		}
		var ran, bad, skipped bool
		for _, c := range r.Cases {
			if !classMatches(c.Classname, cls) || !(c.Name == name || strings.HasPrefix(c.Name, name+"[")) {
				continue
			}
			switch {
			case c.Failed || c.Errored:
				bad = true
			case c.Skipped:
				skipped = true
			default:
				ran = true
			}
		}
		switch {
		case bad:
			out.Failed = append(out.Failed, id)
		case skipped || !ran:
			out.Missing = append(out.Missing, id)
		}
	}
	return out, nil
}

// splitTestID turns "dir/file.py::Class::test" into the dotted class
// "dir.file.Class" and the test name "test".
func splitTestID(id string) (string, string, error) {
	parts := strings.Split(id, "::")
	if len(parts) < 2 || parts[0] == "" || parts[len(parts)-1] == "" {
		return "", "", fmt.Errorf("required test id %q is not file.py::test", id)
	}
	mod := strings.TrimSuffix(parts[0], ".py")
	mod = strings.ReplaceAll(strings.Trim(mod, "/"), "/", ".")
	cls := strings.Join(append([]string{mod}, parts[1:len(parts)-1]...), ".")
	if cls == "" {
		return "", "", errors.New("required test id has an empty module")
	}
	return cls, parts[len(parts)-1], nil
}

func classMatches(classname, want string) bool {
	classname = strings.ReplaceAll(classname, "/", ".")
	return classname == want || strings.HasSuffix(classname, "."+want)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
