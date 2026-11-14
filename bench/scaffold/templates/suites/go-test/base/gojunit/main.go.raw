// Command go-junit turns `go test -json` output on stdin into a JUnit XML
// report on stdout, for the Heimdall go-test suite template.
//
// Each test is one testcase with classname = the package import path and
// name = the test name, as gotestsum writes them, so a required test id
// "example.com/mod/pkg::TestName" matches it. A package that fails without
// reporting a failed test (a build error, a panic in init, a TestMain that
// exits early) becomes one errored testcase named after the package, and a
// test that never finished is an error too, so a broken run can never read
// as tests passed. Lines that are not JSON (go's own errors on stderr) are
// kept and reported when no package reported anything.
package main

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

const maxOutput = 64 << 10

type event struct {
	Action     string
	Package    string
	ImportPath string
	Test       string
	Output     string
	Elapsed    float64
}

type testResult struct {
	action  string
	elapsed float64
	output  strings.Builder
}

type pkgResult struct {
	action string
	tests  map[string]*testResult
	order  []string
	output strings.Builder
}

type message struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type testcase struct {
	XMLName   xml.Name `xml:"testcase"`
	Classname string   `xml:"classname,attr"`
	Name      string   `xml:"name,attr"`
	Time      string   `xml:"time,attr"`
	Failure   *message `xml:"failure,omitempty"`
	Error     *message `xml:"error,omitempty"`
	Skipped   *message `xml:"skipped,omitempty"`
}

type testsuite struct {
	XMLName  xml.Name   `xml:"testsuite"`
	Name     string     `xml:"name,attr"`
	Tests    int        `xml:"tests,attr"`
	Failures int        `xml:"failures,attr"`
	Errors   int        `xml:"errors,attr"`
	Skipped  int        `xml:"skipped,attr"`
	Cases    []testcase `xml:"testcase"`
}

type testsuites struct {
	XMLName xml.Name    `xml:"testsuites"`
	Suites  []testsuite `xml:"testsuite"`
}

func main() {
	pkgs, order, stray, err := read(bufio.NewScanner(os.Stdin))
	if err != nil {
		fmt.Fprintln(os.Stderr, "go-junit:", err)
		os.Exit(1)
	}
	out := report(pkgs, order, stray)
	if _, err := os.Stdout.WriteString(xml.Header); err != nil {
		os.Exit(1)
	}
	enc := xml.NewEncoder(os.Stdout)
	enc.Indent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "go-junit:", err)
		os.Exit(1)
	}
	fmt.Println()
}

// read collects the events per package and test.
func read(sc *bufio.Scanner) (map[string]*pkgResult, []string, string, error) {
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	pkgs := map[string]*pkgResult{}
	var order []string
	var stray strings.Builder
	for sc.Scan() {
		line := sc.Bytes()
		var ev event
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &ev) != nil {
			appendCapped(&stray, string(line)+"\n")
			continue
		}
		name := ev.Package
		if name == "" {
			name = ev.ImportPath
		}
		if name == "" {
			continue
		}
		p, ok := pkgs[name]
		if !ok {
			p = &pkgResult{tests: map[string]*testResult{}}
			pkgs[name] = p
			order = append(order, name)
		}
		p.add(ev)
	}
	return pkgs, order, stray.String(), sc.Err()
}

func (p *pkgResult) add(ev event) {
	if ev.Test == "" {
		switch ev.Action {
		case "output", "build-output":
			appendCapped(&p.output, ev.Output)
		case "pass", "fail", "skip", "build-fail":
			p.action = ev.Action
		}
		return
	}
	t, ok := p.tests[ev.Test]
	if !ok {
		t = &testResult{}
		p.tests[ev.Test] = t
		p.order = append(p.order, ev.Test)
	}
	switch ev.Action {
	case "output":
		appendCapped(&t.output, ev.Output)
	case "pass", "fail", "skip":
		t.action = ev.Action
		t.elapsed = ev.Elapsed
	}
}

// report builds one testsuite per package that ran or failed.
func report(pkgs map[string]*pkgResult, order []string, stray string) testsuites {
	var out testsuites
	for _, name := range order {
		p := pkgs[name]
		class := strings.Fields(name + " ")[0]
		s := testsuite{Name: name}
		failedTest := false
		for _, tn := range p.order {
			t := p.tests[tn]
			c := testcase{Classname: class, Name: tn, Time: fmt.Sprintf("%.3f", t.elapsed)}
			switch t.action {
			case "pass":
			case "skip":
				c.Skipped = &message{Message: "skipped", Body: t.output.String()}
				s.Skipped++
			case "fail":
				c.Failure = &message{Message: "failed", Body: t.output.String()}
				s.Failures++
				failedTest = true
			default:
				c.Error = &message{Message: "did not finish", Body: t.output.String()}
				s.Errors++
				failedTest = true
			}
			s.Cases = append(s.Cases, c)
		}
		if (p.action == "fail" || p.action == "build-fail") && !failedTest {
			s.Cases = append(s.Cases, testcase{Classname: class, Name: "package " + name, Time: "0.000",
				Error: &message{Message: "package failed", Body: p.output.String()}})
			s.Errors++
		}
		s.Tests = len(s.Cases)
		if s.Tests > 0 {
			out.Suites = append(out.Suites, s)
		}
	}
	if len(out.Suites) == 0 && strings.TrimSpace(stray) != "" {
		out.Suites = append(out.Suites, testsuite{Name: "go test", Tests: 1, Errors: 1, Cases: []testcase{{
			Classname: "go", Name: "go test", Time: "0.000", Error: &message{Message: "go test failed", Body: stray},
		}}})
	}
	return out
}

func appendCapped(b *strings.Builder, s string) {
	if room := maxOutput - b.Len(); room > 0 {
		b.WriteString(s[:min(room, len(s))])
	}
}
