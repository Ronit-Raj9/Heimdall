package scaffold

// X1 vision grading (docs/visual-grading.md): `task init -visual` adds an
// output.visual block to a new browser, computer-use or artifact task and
// moves the suite to grader visual, which still runs the suite's verifier
// command first, so every other task is graded exactly as before.

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

// visualBlocks are the output.visual blocks task init writes per template.
// The reference does not exist yet: `task validate` refuses the task until
// the author makes it with `bifrost-bench visual render` or `visual
// capture` from a correct result.
var visualBlocks = map[string]string{
	"browser": `# EDIT ME: what the bench screenshots after the session, and the hidden
# reference it is compared with. Make the reference from a correct result:
# bifrost-bench visual capture -browser <browser container> -url <url> -out verifier/reference.png
capture: {kind: browser, url: "http://webapp:8000/", width: 1280, height: 800}
reference: verifier/reference.png
method: ssim
threshold: 0.99
`,
	"computer-use": `# EDIT ME: the bench calls the desktop's screenshot tool after the session
# and compares the screen with the hidden reference. Make the reference from
# a correct result:
# bifrost-bench visual capture -desktop <desktop container> -out verifier/reference.png
capture: {kind: desktop}
reference: verifier/reference.png
method: ssim
threshold: 0.99
`,
	"artifact": `# EDIT ME: the bench renders the artifact (svg, html) or reads it (png, jpg)
# and compares it with the hidden reference. Make the reference from a
# correct result:
# bifrost-bench visual render <file> -width 1280 -height 800 -out verifier/reference.png
capture: {kind: artifact, artifact: ARTIFACT}
reference: verifier/reference.png
method: pixel
threshold: 0.001
tolerance: 16
`,
}

var graderLineRe = regexp.MustCompile(`(?m)^grader:[ \t]*verifier_command[ \t]*$`)

// visualTask adds output.visual to the task.yaml among files when opts asks
// for it, and switches suiteYAML to grader visual.
func visualTask(opts ScenarioTaskOptions, tmpl string, files []File, suiteYAML *[]byte) ([]File, error) {
	if !opts.Visual {
		return files, nil
	}
	block, ok := visualBlocks[tmpl]
	if !ok {
		return nil, fmt.Errorf("-visual applies to browser, computer-use and artifact tasks, not %s", tmpl)
	}
	switch {
	case graderLineRe.Match(*suiteYAML):
		*suiteYAML = graderLineRe.ReplaceAll(*suiteYAML, []byte("grader: visual # X1 vision grading: runs the verifier command, then output.visual"))
	case !regexp.MustCompile(`(?m)^grader:[ \t]*visual\b`).Match(*suiteYAML):
		return nil, errors.New("-visual needs a suite on grader verifier_command or visual")
	}
	for i, f := range files {
		if f.Path != "task.yaml" {
			continue
		}
		out, err := addVisualBlock(f.Data, block, *suiteYAML)
		if err != nil {
			return nil, fmt.Errorf("task.yaml: %w", err)
		}
		files[i].Data = out
		return files, nil
	}
	return nil, errors.New("the copied task has no task.yaml")
}

// addVisualBlock sets output.visual in a task.yaml, keeping every other
// field and comment. An artifact block captures the task's first artifact,
// else the suite's.
func addVisualBlock(src []byte, block string, suiteYAML []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping")
	}
	m := doc.Content[0]
	if bytes.Contains([]byte(block), []byte("ARTIFACT")) {
		id := firstArtifact(m)
		if id == "" {
			var suite yaml.Node
			if yaml.Unmarshal(suiteYAML, &suite) == nil && len(suite.Content) > 0 {
				id = firstArtifact(suite.Content[0])
			}
		}
		if id == "" {
			return nil, errors.New("no artifact to capture: declare output.artifacts first")
		}
		block = string(bytes.ReplaceAll([]byte(block), []byte("ARTIFACT"), []byte(id)))
	}
	var vis yaml.Node
	if err := yaml.Unmarshal([]byte(block), &vis); err != nil {
		return nil, err
	}
	out := mapValue(m, "output")
	if out == nil || out.Kind != yaml.MappingNode {
		out = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setNode(m, "output", out)
	}
	val := vis.Content[0]
	head := val.HeadComment
	if head == "" && len(val.Content) > 0 {
		head = val.Content[0].HeadComment
		val.Content[0].HeadComment = ""
	}
	setNode(out, "visual", val)
	for i := 0; i+1 < len(out.Content); i += 2 {
		if out.Content[i].Value == "visual" {
			out.Content[i].HeadComment = head
		}
	}
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

// firstArtifact is the id of the first entry of output.artifacts in m.
func firstArtifact(m *yaml.Node) string {
	out := mapValue(m, "output")
	if out == nil || out.Kind != yaml.MappingNode {
		return ""
	}
	arts := mapValue(out, "artifacts")
	if arts == nil || arts.Kind != yaml.SequenceNode || len(arts.Content) == 0 {
		return ""
	}
	if id := mapValue(arts.Content[0], "id"); id != nil {
		return id.Value
	}
	return ""
}
