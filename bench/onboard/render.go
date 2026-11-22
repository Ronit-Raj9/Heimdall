package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// taskMeta is what the preview reads from a draft task's task.yaml.
type taskMeta struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Instruction string `yaml:"instruction"`
	Split       string `yaml:"split"`
	Verifier    struct {
		Tests    string   `yaml:"tests"`
		Required []string `yaml:"required"`
		Answer   string   `yaml:"answer"`
		Checks   string   `yaml:"checks"`
	} `yaml:"verifier"`
	Controls struct {
		Reference string   `yaml:"reference"`
		Empty     string   `yaml:"empty"`
		Mutants   []string `yaml:"mutants"`
		Exploit   string   `yaml:"exploit"`
	} `yaml:"controls"`
	Provenance struct {
		Source  string `yaml:"source"`
		License string `yaml:"license"`
	} `yaml:"provenance"`
}

// answerMeta is a task's hidden verifier/answer.yaml.
type answerMeta struct {
	Match     string   `yaml:"match"`
	Expected  []string `yaml:"expected"`
	Normalize []string `yaml:"normalize"`
	Rubric    string   `yaml:"rubric"`
	Wrong     []string `yaml:"wrong_answers"`
}

func readTaskMeta(dir string) (*taskMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "task.yaml"))
	if err != nil {
		return nil, err
	}
	var m taskMeta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("task.yaml: %w", err)
	}
	return &m, nil
}

// instructionText is the instruction exactly as the agent sees it: the
// named file when it exists in the task folder, else the inline text.
func instructionText(dir string, m *taskMeta) string {
	ins := strings.TrimSpace(m.Instruction)
	if ins != "" && !strings.ContainsAny(ins, "\n") && len(ins) < 200 {
		if p, err := within(dir, ins); err == nil {
			if b, err := os.ReadFile(p); err == nil {
				return string(b)
			}
		}
	}
	return m.Instruction
}

// within joins rel under dir and refuses an escape.
func within(dir, rel string) (string, error) {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	r, err := filepath.Rel(dir, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the task", rel)
	}
	return p, nil
}

func quoteList(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = fmt.Sprintf("%q", cut(strings.Join(strings.Fields(v), " "), 120))
	}
	return strings.Join(q, ", ")
}

// CheckSentences renders a task's checks as plain sentences from its files:
// the hidden answer spec, the hidden tests or a checks file. grader is the
// suite's grader.
func CheckSentences(dir string, m *taskMeta, grader string) []string {
	var out []string
	if m.Verifier.Answer != "" {
		if p, err := within(dir, m.Verifier.Answer); err == nil {
			if b, err := os.ReadFile(p); err == nil {
				var a answerMeta
				if yaml.Unmarshal(b, &a) == nil {
					out = append(out, answerSentences(a, grader)...)
				}
			}
		}
	}
	if m.Verifier.Checks != "" {
		out = append(out, checksFileSentence(dir, m.Verifier.Checks))
	} else if _, err := os.Stat(filepath.Join(dir, "checks.yaml")); err == nil {
		out = append(out, checksFileSentence(dir, "checks.yaml"))
	}
	if m.Verifier.Answer == "" && m.Verifier.Tests != "" {
		n := len(listFiles(filepath.Join(dir, filepath.FromSlash(m.Verifier.Tests)), 1000))
		s := fmt.Sprintf("The hidden tests in %s/ (%d files) must pass after the agent's work.", strings.TrimSuffix(m.Verifier.Tests, "/"), n)
		out = append(out, s)
		if len(m.Verifier.Required) > 0 {
			out = append(out, "These tests must pass: "+strings.Join(m.Verifier.Required, ", ")+".")
		}
	}
	if len(out) == 0 {
		out = append(out, "The benchmark's grader ("+grader+") decides; this task names no answer, tests or checks file of its own.")
	}
	return out
}

func answerSentences(a answerMeta, grader string) []string {
	if grader == "judge" {
		var out []string
		if strings.TrimSpace(a.Rubric) != "" {
			out = append(out, fmt.Sprintf("A pinned judge model grades the reply against this rubric: %q.", cut(strings.Join(strings.Fields(a.Rubric), " "), 300)))
		} else {
			out = append(out, "A pinned judge model grades the reply against the reference answer.")
		}
		if len(a.Expected) > 0 {
			out = append(out, "Reference answer the judge sees: "+quoteList(a.Expected[:1])+".")
		}
		return out
	}
	loose := ""
	for _, n := range a.Normalize {
		if n == "lower" {
			loose = " (case and spacing ignored)"
		}
	}
	switch a.Match {
	case "numeric":
		return []string{"The final answer must be the number " + quoteList(a.Expected) + "."}
	case "contains_all":
		return []string{"The final answer must contain all of " + quoteList(a.Expected) + loose + "."}
	case "regex":
		return []string{"The final answer must match the pattern " + quoteList(a.Expected) + "."}
	case "choice":
		return []string{"The final answer must pick the choice " + quoteList(a.Expected) + "."}
	}
	if len(a.Expected) > 1 {
		return []string{"The final answer must equal one of " + quoteList(a.Expected) + loose + "."}
	}
	return []string{"The final answer must equal " + quoteList(a.Expected) + loose + "."}
}

func checksFileSentence(dir, rel string) string {
	p, err := within(dir, rel)
	if err != nil {
		return "Checks file " + rel + " is outside the task."
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "Checks file " + rel + " is missing."
	}
	var doc struct {
		Checks []yaml.Node `yaml:"checks"`
	}
	_ = yaml.Unmarshal(b, &doc)
	return fmt.Sprintf("The %d checks in %s (tool calls, arguments, order, end state or rubric) must pass.", len(doc.Checks), rel)
}

// controlsOf lists a task's controls with their origin; origins names the
// origin of a control path when the source knows it (default "source").
func controlsOf(m *taskMeta, origins map[string]string) []ControlInfo {
	origin := func(p string) string {
		if o := origins[p]; o != "" {
			return o
		}
		return "source"
	}
	var out []ControlInfo
	if m.Controls.Reference != "" {
		out = append(out, ControlInfo{Kind: "reference", Path: m.Controls.Reference, Origin: origin(m.Controls.Reference)})
	}
	if m.Controls.Empty != "" {
		out = append(out, ControlInfo{Kind: "empty", Path: m.Controls.Empty, Origin: origin(m.Controls.Empty)})
	}
	for _, p := range m.Controls.Mutants {
		out = append(out, ControlInfo{Kind: "wrong_answer", Path: p, Origin: origin(p)})
	}
	if m.Controls.Exploit != "" {
		out = append(out, ControlInfo{Kind: "exploit", Path: m.Controls.Exploit, Origin: origin(m.Controls.Exploit)})
	}
	if out == nil {
		out = []ControlInfo{}
	}
	return out
}
