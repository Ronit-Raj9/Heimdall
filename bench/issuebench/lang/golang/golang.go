// Package golang is the issuebench Language for Go modules tested with
// go test. It registers itself as "go" on import.
package golang

import (
	_ "embed"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

func init() { issuebench.Register(Go{}) }

// gotest2junit is the converter from go test -json events to JUnit XML. It
// is stored with a .txt suffix so the Go build ignores it, and written into
// the environment image as /heimdall/gotest2junit.go.
//
//go:embed gotest2junit.go.txt
var gotest2junit []byte

// Go implements issuebench.Language and issuebench.Toolchain.
type Go struct{}

var (
	_ issuebench.Language  = Go{}
	_ issuebench.Toolchain = Go{}
)

// Name is "go".
func (Go) Name() string { return "go" }

// Detect is true when go.mod exists.
func (Go) Detect(repoDir string) bool {
	st, err := os.Stat(filepath.Join(repoDir, "go.mod"))
	return err == nil && !st.IsDir()
}

// IsTestFile is true for *_test.go and anything under a testdata/ folder.
func (Go) IsTestFile(p string) bool {
	p = strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "./")
	if strings.HasSuffix(p, "_test.go") {
		return true
	}
	for _, d := range strings.Split(path.Dir(p), "/") {
		if d == "testdata" {
			return true
		}
	}
	return false
}

var fallbackImages = []string{"golang:1.24-bookworm", "golang:1.23-bookworm", "golang:1.22-bookworm"}

// BaseImages are the Go images, newest first.
func (Go) BaseImages() []string { return append([]string(nil), fallbackImages...) }

var goDirectiveRE = regexp.MustCompile(`(?m)^go\s+(1\.(\d+))(\.\d+)?\s*$`)

// goDirective returns the "1.N" of go.mod's go line, or "".
func goDirective(repoDir string) (string, int) {
	b, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		return "", 0
	}
	m := goDirectiveRE.FindSubmatch(b)
	if m == nil {
		return "", 0
	}
	minor, _ := strconv.Atoi(string(m[2]))
	return string(m[1]), minor
}

// PickBaseImages puts golang:<go directive>-bookworm first when go.mod asks
// for 1.21 or newer, then the fallback images.
func PickBaseImages(repoDir string) []string {
	v, minor := goDirective(repoDir)
	out := []string{}
	if minor >= 21 {
		out = append(out, "golang:"+v+"-bookworm")
	}
	for _, img := range fallbackImages {
		if len(out) == 0 || out[0] != img {
			out = append(out, img)
		}
	}
	return out
}

// Hints are short facts about the module for the builder agent.
func (Go) Hints(repoDir string) []string {
	var h []string
	if v, _ := goDirective(repoDir); v != "" {
		h = append(h, "go directive: "+v)
	}
	h = append(h, "base images to try in order: "+strings.Join(PickBaseImages(repoDir), ", "))
	for _, f := range []string{"go.sum", "go.work", "vendor/modules.txt"} {
		if _, err := os.Stat(filepath.Join(repoDir, f)); err == nil {
			h = append(h, "has "+f)
		}
	}
	return h
}

// PinEnv keeps the image's Go toolchain and the module proxy.
func (Go) PinEnv(time.Time) []string {
	return []string{"GOTOOLCHAIN=local", "GOFLAGS=-mod=mod", "GOPROXY=https://proxy.golang.org", "GONOSUMDB=", "CGO_ENABLED=1"}
}

const (
	eventsFile = "/tmp/h-go.json"
	junitOut   = `"${BENCH_JUNIT:-/tmp/bench-junit.xml}"`
)

// packageOf turns a test id ("./pkg::TestA") or a test file
// ("pkg/a_test.go") into the package folder and, for an id, the top-level
// test name.
func packageOf(t string) (dir, test string) {
	if d, name, ok := strings.Cut(t, "::"); ok {
		top, _, _ := strings.Cut(name, "/")
		return normDir(d), top
	}
	if strings.HasSuffix(t, ".go") || strings.Contains(t, "/testdata/") || strings.HasPrefix(t, "testdata/") {
		p := "/" + strings.TrimPrefix(t, "./")
		if i := strings.Index(p, "/testdata/"); i >= 0 {
			return normDir(p[1:max(i, 1)]), ""
		}
		return normDir(path.Dir(p[1:])), ""
	}
	return normDir(t), ""
}

func normDir(d string) string {
	d = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(d), "./"), "/")
	if d == "" || d == "." {
		return "./"
	}
	return "./" + d
}

// TestCommand runs go test -json per package with a -run filter of the
// given ids' top-level tests (a package given by a file runs all its
// tests; no ids runs ./...), converts the events to JUnit at $BENCH_JUNIT,
// prints it and exits with the worst exit code. The builder's placeholders
// "{tests}" and "<test files>" become a loop over the files' folders.
func (Go) TestCommand(tests []string) string {
	var b strings.Builder
	b.WriteString("rm -f " + eventsFile + "; rc=0; M=$(go list -m 2>/dev/null | head -n 1); ")
	run := func(dir, filter string) {
		b.WriteString("go test -json -count=1 " + issuebench.ShellQuote(dir))
		if filter != "" {
			b.WriteString(" -run " + issuebench.ShellQuote(filter))
		}
		b.WriteString(" >> " + eventsFile + "; c=$?; [ $c -gt $rc ] && rc=$c; ")
	}
	var placeholders []string
	byDir := map[string][]string{}
	all := map[string]bool{}
	var dirs []string
	for _, t := range tests {
		if t == "{tests}" || t == "<test files>" {
			placeholders = append(placeholders, t)
			continue
		}
		d, name := packageOf(t)
		if _, ok := byDir[d]; !ok {
			byDir[d] = nil
			dirs = append(dirs, d)
		}
		if name == "" {
			all[d] = true
		} else if !contains(byDir[d], name) {
			byDir[d] = append(byDir[d], name)
		}
	}
	sort.Strings(dirs)
	switch {
	case len(dirs) == 0 && len(placeholders) == 0:
		run("./...", "")
	default:
		for _, d := range dirs {
			filter := ""
			if !all[d] {
				names := append([]string(nil), byDir[d]...)
				sort.Strings(names)
				for i, n := range names {
					names[i] = regexp.QuoteMeta(n)
				}
				filter = "^(" + strings.Join(names, "|") + ")$"
			}
			run(d, filter)
		}
		for _, p := range placeholders {
			b.WriteString(`for d in $(for f in ` + p + `; do dirname "$f"; done | sort -u); do go test -json -count=1 "./${d#./}" >> ` + eventsFile + `; c=$?; [ $c -gt $rc ] && rc=$c; done; `)
		}
	}
	b.WriteString(`( cd /heimdall && GOFLAGS= go run gotest2junit.go -module "$M" ) < ` + eventsFile + ` > ` + junitOut + `; `)
	b.WriteString(`cat ` + junitOut + `; exit $rc`)
	return b.String()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ParseResults reads the JUnit report the test command printed.
func (Go) ParseResults(output []byte) (map[string]issuebench.TestStatus, error) {
	return issuebench.ParseJUnitOutput(output)
}

// EgressHosts are the module proxy, the checksum database and Debian.
func (Go) EgressHosts() []string {
	return []string{"proxy.golang.org", "sum.golang.org", "storage.googleapis.com", "deb.debian.org"}
}

// InstallFiles are go.mod, go.sum and go.work.
func (Go) InstallFiles() []string { return []string{"go.mod", "go.sum", "go.work"} }

// BuilderNotes is the Go paragraph of the builder system prompt.
func (Go) BuilderNotes() string {
	return "Go module. Run go mod download first; the tests run with no network, so every module must be in the image. Run one test with go test ./pkg -run '^TestName$'. Do not change go.mod or go.sum. Submit a test command with go test in it."
}

// ValidTestCommand is true when the command runs go test.
func (Go) ValidTestCommand(cmd string) bool { return strings.Contains(cmd, "go test") }

// ReadOnlyCommands are Python's plain words; go test only looks too.
func (Go) ReadOnlyCommands() []string { return issuebench.PythonDefaults{}.ReadOnlyCommands() }

// ProtectedFiles are go.mod and go.sum.
func (Go) ProtectedFiles() []string { return []string{"go.mod", "go.sum"} }

// SupportFiles holds gotest2junit.go.
func (Go) SupportFiles() map[string][]byte {
	return map[string][]byte{"gotest2junit.go": append([]byte(nil), gotest2junit...)}
}

// ManifestFile is true for go.mod and go.sum.
func (Go) ManifestFile(p string) bool {
	base := path.Base(strings.ReplaceAll(p, `\`, "/"))
	return base == "go.mod" || base == "go.sum"
}
