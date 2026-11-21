package suite

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// LibraryEnv names the server-wide benchmark library: a folder holding
// benchmarks/<suite-id>/ and agents/*.yaml that every organization reads as
// a read-only built-in layer (deploy/hosted/docker-compose.yml mounts it at
// the same path in the server and the runners). Unset keeps the library off.
const LibraryEnv = "HEIMDALL_LIBRARY_DIR"

// LibraryDir is the absolute library folder from LibraryEnv, or "" when it
// is unset or not a folder.
func LibraryDir() string {
	dir := strings.TrimSpace(os.Getenv(LibraryEnv))
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return ""
	}
	return filepath.Clean(abs)
}

// LibraryBenchmarks is the benchmarks folder of a library root ("" for "").
func LibraryBenchmarks(lib string) string {
	if lib == "" {
		return ""
	}
	return filepath.Join(lib, "benchmarks")
}

// LibraryAgents is the agents folder of a library root ("" for "").
func LibraryAgents(lib string) string {
	if lib == "" {
		return ""
	}
	return filepath.Join(lib, "agents")
}

// Layered lists the suite loaders of an organization whose own packs live
// in orgBenchmarks, with the library's benchmarks folder libBenchmarks as a
// read-only built-in layer ("" leaves the library out and equals Builtin).
// An id found in both resolves by PickSuiteDir.
func Layered(orgBenchmarks, libBenchmarks string) []schemas.SuiteLoader {
	l := newLocalLoader(orgBenchmarks)
	l.library = libBenchmarks
	return []schemas.SuiteLoader{l}
}

// PickSuiteDir picks the folder suite id loads from: the organization's copy
// under orgBenchmarks or the library's under libBenchmarks. A pack in only
// one place loads from there. In both, the organization's copy wins only
// when it lists more tasks; otherwise the library's does. fromLibrary
// reports the library pick. A pack in neither resolves to the org folder,
// so the error names the org's suites.
func PickSuiteDir(orgBenchmarks, libBenchmarks, id string) (dir string, fromLibrary bool) {
	orgDir := filepath.Join(orgBenchmarks, id)
	if libBenchmarks == "" || id == "" || strings.ContainsAny(id, `/\@`) || id == "." || id == ".." {
		return orgDir, false
	}
	libDir := filepath.Join(libBenchmarks, id)
	libN, libOK := suiteTaskCount(libDir)
	if !libOK {
		return orgDir, false
	}
	orgN, orgOK := suiteTaskCount(orgDir)
	if orgOK && orgN > libN {
		return orgDir, false
	}
	return libDir, true
}

// IsLibrarySuite reports that suite id of the organization whose packs live
// in orgBenchmarks loads from the server's built-in library (LibraryEnv): it
// is read-only on the web and in suite jobs.
func IsLibrarySuite(orgBenchmarks, id string) bool {
	_, lib := PickSuiteDir(orgBenchmarks, LibraryBenchmarks(LibraryDir()), id)
	return lib
}

// ErrLibraryReadOnly is the refusal of a change to a built-in library suite.
const ErrLibraryReadOnly = "Built-in benchmark, read-only"

// suiteTaskCount reads the task list length of dir/suite.yaml without
// loading the pack. ok is false when there is no readable suite.yaml.
func suiteTaskCount(dir string) (n int, ok bool) {
	raw, err := os.ReadFile(filepath.Join(dir, SuiteFile))
	if err != nil {
		return 0, false
	}
	var head struct {
		Tasks []string `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(raw, &head); err != nil {
		return 0, true
	}
	return len(head.Tasks), true
}
