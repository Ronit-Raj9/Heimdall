// Package node is the issuebench Language for JavaScript and TypeScript
// repositories tested with jest or vitest. It registers itself twice, as
// "javascript" and "typescript", on import.
package node

import (
	_ "embed"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

func init() {
	issuebench.Register(Node{name: "javascript"})
	issuebench.Register(Node{name: "typescript"})
}

// results2junit is the converter from jest or vitest JSON to JUnit XML,
// written to /heimdall/results2junit.js in the environment image.
//
//go:embed results2junit.js
var results2junit []byte

// Node implements issuebench.Language and issuebench.Toolchain.
type Node struct{ name string }

var (
	_ issuebench.Language  = Node{}
	_ issuebench.Toolchain = Node{}
)

// Name is "javascript" or "typescript".
func (n Node) Name() string { return n.name }

var runners = []string{"jest", "vitest", "ts-jest", "@jest/core"}

type packageJSON struct {
	Dependencies    map[string]any    `json:"dependencies"`
	DevDependencies map[string]any    `json:"devDependencies"`
	Scripts         map[string]string `json:"scripts"`
	Engines         map[string]string `json:"engines"`
}

func readPackage(dir string) (packageJSON, bool) {
	var p packageJSON
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return p, false
	}
	if json.Unmarshal(b, &p) != nil {
		return p, false
	}
	return p, true
}

// runner names the test runner the package uses, or "".
func (p packageJSON) runner() string {
	for _, r := range runners {
		if _, ok := p.DevDependencies[r]; ok {
			return runnerName(r)
		}
		if _, ok := p.Dependencies[r]; ok {
			return runnerName(r)
		}
	}
	t := p.Scripts["test"]
	switch {
	case strings.Contains(t, "vitest"):
		return "vitest"
	case strings.Contains(t, "jest"):
		return "jest"
	}
	return ""
}

func runnerName(dep string) string {
	if dep == "vitest" {
		return "vitest"
	}
	return "jest"
}

// Detect is true when package.json names jest, vitest, ts-jest or
// @jest/core as a dependency, or its test script runs jest or vitest.
func (Node) Detect(repoDir string) bool {
	p, ok := readPackage(repoDir)
	return ok && p.runner() != ""
}

var testExts = []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"}

// IsTestFile is true for *.test.* and *.spec.* sources and anything under
// __tests__/, never under node_modules/.
func (Node) IsTestFile(p string) bool {
	p = strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "./")
	dirs := strings.Split(path.Dir(p), "/")
	for _, d := range dirs {
		if d == "node_modules" {
			return false
		}
	}
	base := path.Base(p)
	for _, ext := range testExts {
		if strings.HasSuffix(base, ".test"+ext) || strings.HasSuffix(base, ".spec"+ext) {
			return true
		}
	}
	for _, d := range dirs {
		if d == "__tests__" {
			return true
		}
	}
	return false
}

// BaseImages are the slim Node images, newest first.
func (Node) BaseImages() []string {
	return []string{"node:22-bookworm-slim", "node:20-bookworm-slim", "node:18-bookworm-slim"}
}

var lockFiles = []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml"}

// Hints are short facts about how the package installs and tests itself.
func (Node) Hints(repoDir string) []string {
	p, ok := readPackage(repoDir)
	if !ok {
		return nil
	}
	var h []string
	if v := p.Engines["node"]; v != "" {
		h = append(h, "engines.node: "+v)
	}
	for _, f := range lockFiles {
		if _, err := os.Stat(filepath.Join(repoDir, f)); err == nil {
			h = append(h, "lock file "+f)
		}
	}
	if t := p.Scripts["test"]; t != "" {
		h = append(h, "scripts.test: "+t)
	}
	if r := p.runner(); r != "" {
		h = append(h, "test runner: "+r)
	}
	return h
}

// PinEnv keeps npm quiet and resolves packages as they were at the given time.
func (Node) PinEnv(at time.Time) []string {
	env := []string{"CI=true", "NPM_CONFIG_UPDATE_NOTIFIER=false", "NPM_CONFIG_FUND=false"}
	if at.IsZero() {
		return env
	}
	return append(env, "npm_config_before="+at.UTC().Format(time.RFC3339))
}

// TestCommand runs vitest when it is installed, else jest, on the files of
// the given test ids (the part before "::"; all tests when empty), converts
// the runner's JSON to JUnit at $BENCH_JUNIT, prints it and exits with the
// runner's exit code.
func (Node) TestCommand(tests []string) string {
	seen := map[string]bool{}
	var files []string
	for _, t := range tests {
		f, _, _ := strings.Cut(t, "::")
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, issuebench.ShellQuote(f))
	}
	args := ""
	if len(files) > 0 {
		args = " " + strings.Join(files, " ")
	}
	return `R=jest; [ -x node_modules/.bin/vitest ] && R=vitest; ` +
		`if [ "$R" = vitest ]; then npx --no-install vitest run --reporter=json --outputFile=/tmp/h-results.json` + args + `; ` +
		`else npx --no-install jest --ci --json --outputFile=/tmp/h-results.json` + args + `; fi; rc=$?; ` +
		`node /heimdall/results2junit.js /tmp/h-results.json > "${BENCH_JUNIT:-/tmp/bench-junit.xml}"; ` +
		`cat "${BENCH_JUNIT:-/tmp/bench-junit.xml}"; exit $rc`
}

// ParseResults reads the JUnit report the test command printed.
func (Node) ParseResults(output []byte) (map[string]issuebench.TestStatus, error) {
	return issuebench.ParseJUnitOutput(output)
}

// EgressHosts are the npm and yarn registries and Debian.
func (Node) EgressHosts() []string {
	return []string{"registry.npmjs.org", "registry.yarnpkg.com", "deb.debian.org", "security.debian.org"}
}

// InstallFiles are the manifest, lock and config files the builder reads.
func (Node) InstallFiles() []string {
	return []string{"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", ".nvmrc", "tsconfig.json"}
}

// BuilderNotes is the Node paragraph of the builder system prompt.
func (Node) BuilderNotes() string {
	return "Node project. Install with the lock file: npm ci, yarn install --frozen-lockfile, or corepack enable then pnpm install --frozen-lockfile. Never npm install a new package. Run one test file with npx jest <file> or npx vitest run <file>. TypeScript needs no build step for jest with ts-jest or for vitest. Submit the test command with jest or vitest in it."
}

// ValidTestCommand is true when the command runs jest or vitest.
func (Node) ValidTestCommand(cmd string) bool {
	return strings.Contains(cmd, "jest") || strings.Contains(cmd, "vitest")
}

// ReadOnlyCommands are Python's words plus node, npx, cat and ls.
func (Node) ReadOnlyCommands() []string {
	return appendNew(issuebench.PythonDefaults{}.ReadOnlyCommands(), "node", "npx", "cat", "ls")
}

func appendNew(list []string, more ...string) []string {
	out := append([]string(nil), list...)
	for _, m := range more {
		found := false
		for _, o := range out {
			if o == m {
				found = true
				break
			}
		}
		if !found {
			out = append(out, m)
		}
	}
	return out
}

// ProtectedFiles are the runner and compiler configs restored before grading.
func (Node) ProtectedFiles() []string {
	return []string{"jest.config.js", "jest.config.ts", "jest.config.mjs", "jest.config.cjs", "vitest.config.ts", "vitest.config.js", "vitest.config.mts", "package.json", "babel.config.js", "tsconfig.json"}
}

// SupportFiles holds results2junit.js.
func (Node) SupportFiles() map[string][]byte {
	return map[string][]byte{"results2junit.js": append([]byte(nil), results2junit...)}
}

// ManifestFile is true for package.json and the lock files.
func (Node) ManifestFile(p string) bool {
	base := path.Base(strings.ReplaceAll(p, `\`, "/"))
	if base == "package.json" {
		return true
	}
	i := sort.SearchStrings(sortedLocks, base)
	return i < len(sortedLocks) && sortedLocks[i] == base
}

var sortedLocks = func() []string {
	s := append([]string(nil), lockFiles...)
	sort.Strings(s)
	return s
}()
