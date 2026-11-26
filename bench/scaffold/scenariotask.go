package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// editMeInstruction is the block task init puts above a copied instruction.
const editMeInstruction = EditMeMarker + `
<!--
Rewrite this file for the new task: the request a user would send, and how
to tell when it is done. It starts as a copy of the task this one was made
from. Say nothing about how the result is checked. Delete this comment and
the EDIT ME line when you are done; ` + "`bifrost-bench task validate`" + ` fails
while the EDIT ME line is here.
-->

`

// ScenarioTaskOptions configures InitScenarioTask.
type ScenarioTaskOptions struct {
	SuiteDir string // the suite folder holding suite.yaml
	ID       string // new task id and folder name
	Kind     string // tool-use, mcp, memory or qa; empty takes the suite's template
	From     string // task whose shape is copied; empty takes the first task of suite.yaml with a folder
	License  string // SPDX id or "proprietary"; empty keeps the source task's
	Force    bool   // replace an existing task folder
	Visual   bool   // X1 vision grading: add output.visual (browser, computer-use, artifact) and move the suite to grader visual (visualtask.go)
}

// InitScenarioTask writes a new task folder <suite>/tasks/<id> in a scenario
// suite (tool-use, mcp, memory, qa) from the shape of an existing task: its
// task.yaml with the new id, its seed data, sessions and controls, and its
// verifier. The source is From, else the first task suite.yaml lists, else
// the template's embedded sample. Mutants are task-specific and are not
// copied. The instruction (every session's, for memory), the checks of a
// state verifier or the expected answer, and the reference script get EDIT
// ME markers, so `task validate` refuses the task until a person rewrote
// them. The id is then added to suite.yaml. Like InitTask, the folder is
// written to a temporary sibling and renamed into place.
func InitScenarioTask(opts ScenarioTaskOptions) (*TaskInitResult, error) {
	if !ValidID(opts.ID) {
		return nil, fmt.Errorf("task id %q: want lowercase letters, digits, '.', '_' or '-'", opts.ID)
	}
	suitePath := filepath.Join(opts.SuiteDir, "suite.yaml")
	suiteYAML, err := os.ReadFile(suitePath)
	if err != nil {
		return nil, fmt.Errorf("read suite: %w", err)
	}
	tmpl, err := scenarioKind(DetectTemplate(suiteYAML), opts.Kind, suitePath)
	if err != nil {
		return nil, err
	}
	res := &TaskInitResult{Dir: filepath.Join(opts.SuiteDir, "tasks", opts.ID), Template: tmpl}
	if _, err := os.Stat(res.Dir); err == nil && !opts.Force {
		return nil, fmt.Errorf("task folder %s exists; pass -force to replace it", res.Dir)
	}
	from, files, err := sourceTask(opts, suiteYAML, tmpl)
	if err != nil {
		return nil, err
	}
	res.From = from
	files, err = reshapeTask(files, opts, tmpl, from, res)
	if err != nil {
		return nil, err
	}
	if files, err = visualTask(opts, tmpl, files, &suiteYAML); err != nil { // X1 vision grading (visualtask.go)
		return nil, err
	}
	var shape scenarioTaskShape
	for _, f := range files {
		if f.Path == "task.yaml" && yaml.Unmarshal(f.Data, &shape) == nil {
			break
		}
	}
	if err := writeFolder(res.Dir, files, shape.RepoDir, opts.Force); err != nil {
		return nil, err
	}
	edited, changed, err := AddTaskID(suiteYAML, opts.ID)
	if err != nil {
		return nil, fmt.Errorf("task written to %s, but: %w", res.Dir, err)
	}
	if changed {
		if err := os.WriteFile(suitePath, edited, 0o644); err != nil {
			return nil, fmt.Errorf("task written to %s, but update %s: %w", res.Dir, suitePath, err)
		}
		res.SuiteEdited = true
	}
	return res, nil
}

// KindMismatchError is a task type that does not match the template the
// suite was written from (wave 4, N78). Its text is plain; the command line
// adds its own flag hint.
type KindMismatchError struct {
	Suite    string // path of suite.yaml
	Detected string // the suite's template
	Wanted   string // the task type asked for
}

func (e *KindMismatchError) Error() string {
	return fmt.Sprintf("This benchmark holds %s tasks, not %s tasks. Choose %s or leave the task type on Automatic.", e.Detected, e.Wanted, e.Detected)
}

// scenarioKind reconciles the suite's template with the -kind flag.
func scenarioKind(detected, kind, suitePath string) (string, error) {
	switch {
	case kind != "" && !IsScenarioTemplate(kind):
		return "", fmt.Errorf("-kind %q: want one of %s", kind, strings.Join(ScenarioTemplates(), ", "))
	case kind != "" && detected != "" && detected != kind:
		return "", &KindMismatchError{Suite: suitePath, Detected: detected, Wanted: kind}
	case kind != "":
		return kind, nil
	case IsScenarioTemplate(detected):
		return detected, nil
	case detected != "":
		return "", fmt.Errorf("%s is a %s suite: its tasks are cut from commits; pass -repo, -base and -reference", suitePath, detected)
	}
	return "", fmt.Errorf("%s: cannot tell the suite's kind; pass -kind %s", suitePath, strings.Join(ScenarioTemplates(), "|"))
}

// sourceTask returns the id and files of the task whose shape is copied.
func sourceTask(opts ScenarioTaskOptions, suiteYAML []byte, tmpl string) (string, []File, error) {
	from := opts.From
	if from == "" {
		var s struct {
			Tasks []string `yaml:"tasks"`
		}
		if err := yaml.Unmarshal(suiteYAML, &s); err != nil {
			return "", nil, fmt.Errorf("parse suite.yaml: %w", err)
		}
		for _, id := range s.Tasks {
			if id == opts.ID || !ValidID(id) {
				continue
			}
			if _, err := os.Stat(filepath.Join(opts.SuiteDir, "tasks", id, "task.yaml")); err == nil {
				from = id
				break
			}
		}
	}
	if from == "" {
		files, err := embeddedSample(tmpl, filepath.Base(opts.SuiteDir))
		return scenarioSamples[tmpl] + " (template " + tmpl + ")", files, err
	}
	if !ValidID(from) || from == opts.ID {
		return "", nil, fmt.Errorf("-from %q: want another task of the suite", from)
	}
	files, err := readTaskFolder(filepath.Join(opts.SuiteDir, "tasks", from))
	return from, files, err
}

// embeddedSample renders a scenario template's sample task, for a suite whose
// own tasks are all gone.
func embeddedSample(tmpl, suiteID string) ([]File, error) {
	all, err := Suite(SuiteOptions{ID: suiteID, Template: tmpl})
	if err != nil {
		return nil, err
	}
	prefix := "tasks/" + scenarioSamples[tmpl] + "/"
	var out []File
	for _, f := range all {
		if rel, ok := strings.CutPrefix(f.Path, prefix); ok {
			f.Path = rel
			out = append(out, f)
		}
	}
	return out, nil
}

// readTaskFolder reads every regular file of a task folder. A symlink is
// refused: a copied task must hold its own files.
func readTaskFolder(dir string) ([]File, error) {
	var out []File
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file; copy the task by hand", filepath.Join(dir, rel))
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, File{Path: filepath.ToSlash(rel), Data: data, Mode: info.Mode().Perm()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read task %s: %w", dir, err)
	}
	return out, nil
}

// scenarioTaskShape is what reshapeTask reads from the copied task.yaml.
type scenarioTaskShape struct {
	Instruction string `yaml:"instruction"`
	RepoDir     string `yaml:"repo_dir"`
	Sessions    []struct {
		Instruction string `yaml:"instruction"`
	} `yaml:"sessions"`
	Verifier struct {
		Tests  string `yaml:"tests"`
		Answer string `yaml:"answer"`
	} `yaml:"verifier"`
	Controls struct {
		Reference string   `yaml:"reference"`
		Mutants   []string `yaml:"mutants"`
	} `yaml:"controls"`
}

// reshapeTask turns the copied files into the new task: a rewritten
// task.yaml, EDIT ME markers on what must change, mutants dropped.
func reshapeTask(files []File, opts ScenarioTaskOptions, tmpl, from string, res *TaskInitResult) ([]File, error) {
	byPath := map[string]int{}
	for i, f := range files {
		byPath[f.Path] = i
	}
	ti, ok := byPath["task.yaml"]
	if !ok {
		return nil, fmt.Errorf("task %s has no task.yaml", from)
	}
	var shape scenarioTaskShape
	if err := yaml.Unmarshal(files[ti].Data, &shape); err != nil {
		return nil, fmt.Errorf("parse task.yaml of %s: %w", from, err)
	}
	meta, err := rewriteTaskYAML(files[ti].Data, opts, tmpl, from)
	if err != nil {
		return nil, fmt.Errorf("task.yaml of %s: %w", from, err)
	}
	files[ti].Data = meta
	mark := func(rel string, edit func([]byte) []byte) {
		rel = path.Clean(rel)
		if i, ok := byPath[rel]; ok && rel != "." {
			if !slices.Contains(res.EditMe, rel) {
				files[i].Data = edit(files[i].Data)
				res.EditMe = append(res.EditMe, rel)
			}
		}
	}
	mark(shape.Instruction, markInstruction)
	for _, s := range shape.Sessions {
		mark(s.Instruction, markInstruction)
	}
	if stateTemplate(tmpl) {
		mark(path.Join(shape.Verifier.Tests, "checks.py"), markComment("write the checks this task's final state must pass"))
	} else if tmpl == TemplateArtifact {
		mark(path.Join(shape.Verifier.Tests, "verify.py"), markComment("write the checks this task's artifact files must pass"))
	} else if shape.Verifier.Answer != "" {
		mark(shape.Verifier.Answer, markComment("write this task's expected answer and how it is matched"))
	}
	if strings.HasSuffix(shape.Controls.Reference, ".sh") {
		mark(shape.Controls.Reference, markScript)
	}
	mark("verifier/requirements.md", markRequirements)
	out := files[:0]
	for _, f := range files {
		if slices.Contains(shape.Controls.Mutants, f.Path) {
			continue
		}
		out = append(out, f)
	}
	if len(res.EditMe) == 0 {
		res.Warnings = append(res.Warnings, "no instruction, checks or answer file found to mark; review every file of the new task")
	}
	sortFiles(out)
	return out, nil
}

// rewriteTaskYAML sets the new id, a placeholder title, version 1.0.0, no
// mutants and task init provenance, keeping every other field and comment.
func rewriteTaskYAML(src []byte, opts ScenarioTaskOptions, tmpl, from string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping")
	}
	m := doc.Content[0]
	m.HeadComment = fmt.Sprintf("Written by `bifrost-bench task init` (%s) from the shape of task %s.\n"+
		"Edit every file that holds an EDIT ME marker, review the rest, then run\n"+
		"`bifrost-bench task validate` and `bifrost-bench task admit`.", tmpl, from)
	doc.HeadComment = ""
	if len(m.Content) > 0 {
		m.Content[0].HeadComment = ""
	}
	setScalar(m, "id", opts.ID)
	setScalar(m, "version", "1.0.0")
	setScalar(m, "title", "Task "+opts.ID)
	if controls := mapValue(m, "controls"); controls != nil && controls.Kind == yaml.MappingNode {
		if mutants := mapValue(controls, "mutants"); mutants != nil {
			*mutants = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		}
	}
	license := opts.License
	if prov := mapValue(m, "provenance"); license == "" && prov != nil && prov.Kind == yaml.MappingNode {
		if l := mapValue(prov, "license"); l != nil {
			license = l.Value
		}
	}
	if license == "" {
		license = "proprietary"
	}
	prov := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setScalar(prov, "source", "copied by task init from task "+from)
	setScalar(prov, "license", license)
	setScalar(prov, "created_by", "task init")
	setNode(m, "provenance", prov)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setNode replaces the value of key in mapping m, or appends the pair.
func setNode(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			val.LineComment = m.Content[i+1].LineComment
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

func setScalar(m *yaml.Node, key, value string) {
	setNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// markInstruction puts the EDIT ME block on top of an instruction, unless
// the copied file still holds one.
func markInstruction(b []byte) []byte {
	if hasEditMe(b) {
		return b
	}
	return append([]byte(editMeInstruction), b...)
}

// markRequirements puts an EDIT ME line on top of a copied requirements map.
func markRequirements(b []byte) []byte {
	if hasEditMe(b) {
		return b
	}
	return append([]byte(EditMeMarker+" map every check of this task to the instruction sentence it checks, then delete this line.\n\n"), b...)
}

// hasEditMe reports whether b holds a line task validate refuses as an EDIT
// ME marker.
func hasEditMe(b []byte) bool {
	for _, line := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, EditMeMarker) || strings.HasPrefix(l, "# EDIT ME") {
			return true
		}
	}
	return false
}

// markComment puts a "# EDIT ME" line, which task validate refuses, on top of
// a file whose comments start with "#" (checks.py, answer.yaml).
func markComment(what string) func([]byte) []byte {
	return func(b []byte) []byte {
		if hasEditMe(b) {
			return b
		}
		line := "# EDIT ME: " + what + ", then delete this line. `bifrost-bench task validate` fails while it is here.\n"
		return append([]byte(line), b...)
	}
}

// markScript puts the EDIT ME line under a script's shebang. Admission, not
// validation, proves the script: the verifier must pass it.
func markScript(b []byte) []byte {
	if hasEditMe(b) {
		return b
	}
	line := []byte("# EDIT ME: make the calls a correct agent makes in this task, then delete this line.\n")
	if bytes.HasPrefix(b, []byte("#!")) {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			return slices.Concat(b[:i+1], line, b[i+1:])
		}
	}
	return append(line, b...)
}

// writeFolder writes files into a temporary sibling of dir and renames it
// into place, so a failure leaves nothing behind. The folder emptyDir is
// created even when no file lands in it (an empty workspace).
func writeFolder(dir string, files []File, emptyDir string, force bool) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".tmp-")
	if err != nil {
		return fmt.Errorf("create task folder: %w", err)
	}
	defer os.RemoveAll(tmp)
	// MkdirTemp creates 0700; the grader's containers read the task folder
	// as another user.
	if err := os.Chmod(tmp, 0o755); err != nil {
		return fmt.Errorf("create task folder: %w", err)
	}
	if err := WriteFiles(tmp, files, true); err != nil {
		return err
	}
	if emptyDir != "" {
		if err := checkRel(path.Clean(emptyDir)); err != nil {
			return fmt.Errorf("repo_dir %q: %w", emptyDir, err)
		}
		if err := os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(path.Clean(emptyDir))), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", emptyDir, err)
		}
	}
	if force {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("replace %s: %w", dir, err)
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("move task into place: %w", err)
	}
	return nil
}
