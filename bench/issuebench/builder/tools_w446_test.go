package builder

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/lang/golang"
	_ "github.com/maximhq/bifrost/bench/issuebench/lang/node"
	"github.com/maximhq/bifrost/bench/issuebench/lang/python"
)

func TestSystemPromptPythonUnchanged(t *testing.T) {
	if got := systemPromptFor(python.Python{}); got != systemPrompt {
		t.Fatalf("Python prompt changed:\n%s", got)
	}
	if got := systemPromptFor(fakeLang{}); got != systemPrompt {
		t.Fatal("a language without a Toolchain did not get the Python prompt")
	}
	if !strings.Contains(systemPrompt, issuebench.PythonBuilderNotes) {
		t.Fatal("PythonBuilderNotes is not the paragraph of systemPrompt")
	}
	if got := installFilesFor(python.Python{}); strings.Join(got, ",") != "README.md,README.rst,CONTRIBUTING.md,pyproject.toml,setup.cfg,setup.py,tox.ini,noxfile.py,requirements.txt,requirements-dev.txt,.python-version" {
		t.Fatalf("Python install files changed: %v", got)
	}
}

func TestSystemPromptForNode(t *testing.T) {
	js := issuebench.LanguageFor("javascript")
	p := systemPromptFor(js)
	for _, want := range []string{"Node project.", "<untrusted-data>", "Do not edit tracked repository files", "- Network reaches package registries only.", "Heimdall checks your work itself"} {
		if !strings.Contains(p, want) {
			t.Errorf("Node prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "pytest") {
		t.Error("Node prompt still talks about pytest")
	}
	if f := installFilesFor(js); !strings.Contains(strings.Join(f, ","), "package.json") {
		t.Errorf("install files %v", f)
	}
}

func TestDockerfileForSupportFiles(t *testing.T) {
	py := DockerfileFor(python.Python{}, "python:3.12-slim", nil, []string{"pip install -e ."})
	if py != Dockerfile("python:3.12-slim", nil, []string{"pip install -e ."}) {
		t.Fatal("Python Dockerfile changed")
	}
	d := DockerfileFor(golang.Go{}, "golang:1.24-bookworm", nil, []string{"go mod download"})
	want := "COPY heimdall/ /heimdall/\nCOPY . " + Workdir + "\nRUN rm -rf " + Workdir + "/heimdall\n"
	if !strings.Contains(d, want) {
		t.Fatalf("Go Dockerfile lacks the support copy:\n%s", d)
	}
}

func TestStateChangingForNode(t *testing.T) {
	js := issuebench.LanguageFor("javascript")
	if stateChangingFor(js, "npx jest --listTests") {
		t.Error("npx was kept as an install step")
	}
	if !stateChangingFor(js, "npm ci") {
		t.Error("npm ci was dropped")
	}
	if !stateChangingFor(python.Python{}, "pip install -e .") || stateChangingFor(python.Python{}, "cat setup.cfg") {
		t.Error("Python steps changed")
	}
}

type w446Sandbox struct{ cmds []string }

func (s *w446Sandbox) Exec(_ context.Context, cmd string, _ time.Duration) (int, string, error) {
	s.cmds = append(s.cmds, cmd)
	return 0, "", nil
}
func (s *w446Sandbox) Snapshot(context.Context) (string, error) { return "", nil }
func (s *w446Sandbox) Rollback(context.Context, string) error   { return nil }
func (s *w446Sandbox) Close(context.Context) error              { return nil }

func TestInstallSupportWritesConverter(t *testing.T) {
	sb := &w446Sandbox{}
	if err := installSupport(context.Background(), sb, python.Python{}); err != nil || len(sb.cmds) != 0 {
		t.Fatalf("Python wrote support files: %v %v", sb.cmds, err)
	}
	if err := installSupport(context.Background(), sb, golang.Go{}); err != nil {
		t.Fatal(err)
	}
	if len(sb.cmds) != 1 || !strings.Contains(sb.cmds[0], "base64 -d > /heimdall/'gotest2junit.go'") {
		t.Fatalf("cmds %v", sb.cmds)
	}
}
