package issuebench

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type w446Plain struct{}

func (w446Plain) Name() string                                       { return "w446plain" }
func (w446Plain) Detect(string) bool                                 { return false }
func (w446Plain) IsTestFile(string) bool                             { return false }
func (w446Plain) BaseImages() []string                               { return nil }
func (w446Plain) Hints(string) []string                              { return nil }
func (w446Plain) PinEnv(time.Time) []string                          { return nil }
func (w446Plain) TestCommand([]string) string                        { return "" }
func (w446Plain) ParseResults([]byte) (map[string]TestStatus, error) { return nil, nil }

func TestToolchainOfFallsBackToPython(t *testing.T) {
	if _, ok := ToolchainOf(w446Plain{}).(PythonDefaults); !ok {
		t.Fatal("a language without a Toolchain did not get PythonDefaults")
	}
	if _, ok := ToolchainOf(nil).(PythonDefaults); !ok {
		t.Fatal("a nil language did not get PythonDefaults")
	}
	if !reflect.DeepEqual(PythonDefaults{}.EgressHosts(), DefaultEgressHosts) {
		t.Fatalf("PythonDefaults egress %v, want %v", PythonDefaults{}.EgressHosts(), DefaultEgressHosts)
	}
	p := PythonDefaults{}
	if !p.ValidTestCommand("python -m pytest {tests}") || p.ValidTestCommand("npx jest {tests}") {
		t.Error("ValidTestCommand is not today's pytest check")
	}
	if !p.ManifestFile("requirements-dev.txt") || p.ManifestFile("notes.txt") {
		t.Error("ManifestFile is not today's requirements check")
	}
	if len(p.SupportFiles()) != 0 {
		t.Error("Python has support files")
	}
}

func TestLanguageForIgnoresCase(t *testing.T) {
	Register(w446Plain{})
	if LanguageFor("W446Plain") == nil || LanguageFor("w446plain") == nil {
		t.Fatal("LanguageFor did not find the registered language")
	}
	if LanguageFor("") != nil || LanguageFor("cobol") != nil {
		t.Fatal("LanguageFor found an unknown language")
	}
}

func TestParseJUnitOutput(t *testing.T) {
	out := []byte("noise\n<testsuites><testsuite name=\"x\"><testcase classname=\"a\" name=\"b\"/><testcase classname=\"a\" name=\"c\"><failure/></testcase><testcase classname=\"a\" name=\"d\"><error/></testcase><testcase classname=\"a\" name=\"e\"><skipped/></testcase></testsuite></testsuites>\ntrailing")
	got, err := ParseJUnitOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]TestStatus{"a::b": TestPass, "a::c": TestFail, "a::d": TestError, "a::e": TestSkip}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if _, err := ParseJUnitOutput([]byte("nothing")); err == nil {
		t.Fatal("no error without a report")
	}
}

type w446Tool struct{ w446Plain }

func (w446Tool) EgressHosts() []string           { return []string{"registry.example.org"} }
func (w446Tool) InstallFiles() []string          { return nil }
func (w446Tool) BuilderNotes() string            { return "" }
func (w446Tool) ValidTestCommand(string) bool    { return true }
func (w446Tool) ReadOnlyCommands() []string      { return nil }
func (w446Tool) ProtectedFiles() []string        { return nil }
func (w446Tool) SupportFiles() map[string][]byte { return map[string][]byte{"conv.js": []byte("x")} }
func (w446Tool) ManifestFile(string) bool        { return false }
func (w446Tool) Name() string                    { return "w446tool" }

func TestWriteSupportFiles(t *testing.T) {
	dir := t.TempDir()
	if names, err := WriteSupportFiles(dir, w446Plain{}); err != nil || names != nil {
		t.Fatalf("Python wrote %v %v", names, err)
	}
	names, err := WriteSupportFiles(dir, w446Tool{})
	if err != nil || !reflect.DeepEqual(names, []string{"conv.js"}) {
		t.Fatalf("names %v err %v", names, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "heimdall", "conv.js")); string(b) != "x" {
		t.Fatal("support file not written")
	}
	if _, err := WriteSupportFiles(dir, w446Tool{}); err == nil || !strings.Contains(err.Error(), "own heimdall folder") {
		t.Fatalf("an existing heimdall folder was not refused: %v", err)
	}
}

func TestWorkRequestHostsAddLanguage(t *testing.T) {
	Register(w446Tool{})
	t.Setenv(EgressAllowEnv, "mirror.example.com, BAD_HOST")
	r := WorkRequest{Candidate: Candidate{Language: "w446tool"}}
	got := r.Hosts()
	want := append(append([]string{}, DefaultEgressHosts...), "registry.example.org", "mirror.example.com")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Hosts = %v, want %v", got, want)
	}
	py := WorkRequest{EgressHosts: []string{"pypi.org"}}
	if got := py.Hosts(); !reflect.DeepEqual(got, []string{"pypi.org", "mirror.example.com"}) {
		t.Fatalf("explicit hosts = %v", got)
	}
}
