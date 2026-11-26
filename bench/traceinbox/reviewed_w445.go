package traceinbox

import (
	"bytes"
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

// markReviewedWhenGuided marks the instruction reviewed when the accept
// came with a guide, and does nothing otherwise.
func markReviewedWhenGuided(path string, guided bool) error {
	if !guided {
		return nil
	}
	return markInstructionReviewed(path)
}

// markInstructionReviewed sets provenance.instruction_reviewed: true in a
// task.yaml, keeping every other field and comment (wave 4, W4-45). An
// accept with a guide is made on the review screen, where the person sees
// and may edit the instruction, so the instruction counts as reviewed.
func markInstructionReviewed(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("task.yaml is not a mapping")
	}
	m := doc.Content[0]
	prov := mapValue(m, "provenance")
	if prov == nil || prov.Kind != yaml.MappingNode {
		prov = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setNode(m, "provenance", prov)
	}
	setNode(prov, "instruction_reviewed", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
