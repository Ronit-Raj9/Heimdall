// Package steprules reads a run's step timeline and answers two questions:
// does this run match a rule, and at which steps (behaviour checks); and
// where do two runs first do something different (divergence). It reads
// only schemas.StepRecord rows and never changes a score.
package steprules

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Step classes. A step can be in several.
const (
	ClassTestRun   = "test_run"      // a command or shell tool request that runs tests
	ClassEdit      = "edit"          // file_change, or an edit tool request
	ClassRead      = "read"          // read tool or read command
	ClassShell     = "shell"         // command, or a shell tool request
	ClassToolError = "tool_error"    // IsError on tool_execution, tool_request or command, or ExitCode != 0
	ClassClaim     = "success_claim" // the last model_turn whose Summary holds a ClaimWords word
	ClassModelTurn = "model_turn"
	ClassRetry     = "retry" // model_attempt rows (Bifrost retries and fallbacks)
)

// allClasses lists the classes in the order error sentences name them.
var allClasses = []string{ClassTestRun, ClassEdit, ClassRead, ClassShell, ClassToolError, ClassClaim, ClassModelTurn, ClassRetry}

// Classes holds the editable word lists. DefaultClasses copies the lists of
// metrics/patternevents.go exactly and adds the runners it misses.
type Classes struct {
	TestCommands []string `json:"test_commands" yaml:"test_commands"`
	EditTools    []string `json:"edit_tools" yaml:"edit_tools"`
	ReadTools    []string `json:"read_tools" yaml:"read_tools"`
	ReadCommands []string `json:"read_commands" yaml:"read_commands"`
	ShellTools   []string `json:"shell_tools" yaml:"shell_tools"`
	ClaimWords   []string `json:"claim_words" yaml:"claim_words"`
}

// DefaultClasses returns the built-in word lists.
func DefaultClasses() Classes {
	return Classes{
		TestCommands: []string{"pytest", "py.test", "python -m pytest", "unittest", "npm test", "go test", "make test",
			"cargo test", "npx jest", "npx vitest", "vitest", "jest", "yarn test", "pnpm test", "mvn test", "gradle test",
			"./gradlew test", "tox", "nox", "ctest", "bundle exec rspec", "rspec", "phpunit", "dotnet test"},
		EditTools:    []string{"edit", "write", "patch", "replace", "create_file", "insert"},
		ReadTools:    []string{"read", "view", "open", "grep", "glob", "search", "list", "cat"},
		ReadCommands: []string{"cat", "head", "tail", "less", "more", "sed", "awk", "grep", "rg", "nl", "wc", "ls", "find"},
		ShellTools:   []string{"bash", "exec", "shell", "terminal", "command", "run_command", "execute_command"},
		ClaimWords:   []string{"done", "fixed", "all tests pass", "tests pass", "completed", "resolved", "implemented", "successfully"},
	}
}

// Merge returns c with every non-empty list of o in place of its own (lists
// replace, they are not joined).
func (c Classes) Merge(o Classes) Classes {
	pick := func(a, b []string) []string {
		if len(b) > 0 {
			return b
		}
		return a
	}
	return Classes{
		TestCommands: pick(c.TestCommands, o.TestCommands),
		EditTools:    pick(c.EditTools, o.EditTools),
		ReadTools:    pick(c.ReadTools, o.ReadTools),
		ReadCommands: pick(c.ReadCommands, o.ReadCommands),
		ShellTools:   pick(c.ShellTools, o.ShellTools),
		ClaimWords:   pick(c.ClaimWords, o.ClaimWords),
	}
}

func (c Classes) empty() bool {
	return len(c.TestCommands)+len(c.EditTools)+len(c.ReadTools)+len(c.ReadCommands)+len(c.ShellTools)+len(c.ClaimWords) == 0
}

var spaceRun = regexp.MustCompile(`\s+`)

// normText lowercases and folds runs of white space to one space.
func normText(s string) string {
	return strings.TrimSpace(spaceRun.ReplaceAllString(strings.ToLower(s), " "))
}

// hasWord reports whether text holds w with no letter, digit or underscore
// directly before or after it.
func hasWord(text, w string) bool {
	w = normText(w)
	if w == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(text[from:], w)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(w)
		if (i == 0 || !wordByte(text[i-1])) && (end == len(text) || !wordByte(text[end])) {
			return true
		}
		from = i + 1
	}
}

func wordByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func nameHas(name string, words []string) bool {
	n := strings.ToLower(name)
	if n == "" {
		return false
	}
	for _, w := range words {
		if w = strings.ToLower(w); w != "" && strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// isShell reports whether a step is a command, or a tool request or result
// of a shell tool.
func (c Classes) isShell(s schemas.StepRecord) bool {
	switch s.Kind {
	case schemas.StepCommand:
		return true
	case schemas.StepToolRequest, schemas.StepToolExecution:
		return nameHas(s.ToolName, c.ShellTools)
	}
	return false
}

// commandText is the command a shell step ran, or "" for any other step.
func (c Classes) commandText(s schemas.StepRecord) string {
	if s.Kind == schemas.StepToolExecution || !c.isShell(s) {
		return ""
	}
	return s.Summary
}

// Of returns the classes of one step, sorted and unique. A model_turn whose
// Summary holds a claim word is a claim candidate; Eval keeps only the last
// candidate of the run as the success_claim step.
func (c Classes) Of(s schemas.StepRecord) []string {
	set := map[string]bool{}
	shell := c.isShell(s)
	if shell {
		set[ClassShell] = true
	}
	if t := c.commandText(s); t != "" {
		n := normText(t)
		for _, w := range c.TestCommands {
			if hasWord(n, w) {
				set[ClassTestRun] = true
				break
			}
		}
		if f := strings.Fields(n); len(f) > 0 {
			prog := path.Base(f[0])
			for _, r := range c.ReadCommands {
				if prog == strings.ToLower(r) {
					set[ClassRead] = true
				}
			}
		}
	}
	switch s.Kind {
	case schemas.StepFileChange:
		set[ClassEdit] = true
	case schemas.StepToolRequest, schemas.StepToolExecution:
		if !shell && !strings.Contains(strings.ToLower(s.ToolName), "todo") {
			if nameHas(s.ToolName, c.EditTools) {
				set[ClassEdit] = true
			} else if nameHas(s.ToolName, c.ReadTools) {
				set[ClassRead] = true
			}
		}
	case schemas.StepModelTurn:
		set[ClassModelTurn] = true
		n := normText(s.Summary)
		for _, w := range c.ClaimWords {
			if hasWord(n, w) {
				set[ClassClaim] = true
				break
			}
		}
	case schemas.StepModelAttempt:
		set[ClassRetry] = true
	}
	if isErrorStep(s) {
		set[ClassToolError] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// isErrorStep: IsError on a tool or command row, or a non-zero exit code.
func isErrorStep(s schemas.StepRecord) bool {
	switch s.Kind {
	case schemas.StepToolExecution, schemas.StepToolRequest, schemas.StepCommand:
		if s.IsError {
			return true
		}
	}
	return s.ExitCode != nil && *s.ExitCode != 0
}
