package golang

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/issuebench"
)

func TestGoTestCommandRegex(t *testing.T) {
	cmd := Go{}.TestCommand([]string{"./pkg::TestB", "./pkg::TestA/sub", "./pkg::TestA", "./::TestRoot"})
	if !strings.Contains(cmd, "go test -json -count=1 ./pkg -run '^(TestA|TestB)$' >> /tmp/h-go.json") {
		t.Errorf("package ./pkg not grouped into one run regex: %s", cmd)
	}
	if !strings.Contains(cmd, "go test -json -count=1 ./ -run '^(TestRoot)$'") {
		t.Errorf("module root package missing: %s", cmd)
	}
	if strings.Count(cmd, "go test -json") != 2 {
		t.Errorf("want one go test per package: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "exit $rc") || !strings.Contains(cmd, "go run gotest2junit.go") {
		t.Errorf("command does not convert and keep the worst exit code: %s", cmd)
	}
	if all := (Go{}).TestCommand(nil); !strings.Contains(all, "go test -json -count=1 ./... >> /tmp/h-go.json") {
		t.Errorf("empty ids should run ./...: %s", all)
	}
	if f := (Go{}).TestCommand([]string{"internal/x/a_test.go"}); !strings.Contains(f, "go test -json -count=1 ./internal/x >>") {
		t.Errorf("a test file should run its whole package: %s", f)
	}
	if p := (Go{}).TestCommand([]string{"{tests}"}); !strings.Contains(p, "for f in {tests};") {
		t.Errorf("placeholder must stay for the builder: %s", p)
	}
}

func TestGoParseResults(t *testing.T) {
	out, err := os.ReadFile("testdata/junit.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Go{}.ParseResults(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]issuebench.TestStatus{"./pkg::TestA": issuebench.TestPass, "./pkg::TestB": issuebench.TestFail, "./pkg::TestB/sub": issuebench.TestFail}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestGotest2JUnit(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gotest2junit.go"), Go{}.SupportFiles()["gotest2junit.go"], 0o644); err != nil {
		t.Fatal(err)
	}
	events, err := os.ReadFile("testdata/events.json")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goBin, "run", "gotest2junit.go", "-module", "example.com/m")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GO111MODULE=on")
	cmd.Stdin = bytes.NewReader(events)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go run: %v", err)
	}
	got, err := Go{}.ParseResults(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := map[string]issuebench.TestStatus{
		"./pkg::TestA": issuebench.TestPass, "./pkg::TestB": issuebench.TestFail,
		"./pkg::TestB/sub": issuebench.TestFail, "./::TestRoot": issuebench.TestSkip,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestGoDetectAndImages(t *testing.T) {
	dir := t.TempDir()
	if (Go{}).Detect(dir) {
		t.Error("detected without go.mod")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.22.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !(Go{}).Detect(dir) {
		t.Error("go.mod not detected")
	}
	imgs := PickBaseImages(dir)
	if imgs[0] != "golang:1.22-bookworm" || len(imgs) != 3 {
		t.Errorf("PickBaseImages = %v", imgs)
	}
	if !(Go{}).IsTestFile("a/b_test.go") || !(Go{}).IsTestFile("a/testdata/x.txt") || (Go{}).IsTestFile("a/b.go") {
		t.Error("IsTestFile is wrong")
	}
	if issuebench.LanguageFor("Go") == nil {
		t.Error(`LanguageFor("Go") is nil`)
	}
}
