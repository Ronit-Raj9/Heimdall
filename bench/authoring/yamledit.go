package authoring

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// editMapping decodes src as a YAML mapping, applies edit and encodes it
// again, keeping the other keys and their comments.
func editMapping(src []byte, edit func(m *yaml.Node) error) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a YAML mapping")
	}
	if err := edit(doc.Content[0]); err != nil {
		return nil, err
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

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

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

func scalar(v string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	if bytes.ContainsRune([]byte(v), '\n') {
		n.Style = yaml.LiteralStyle
	}
	return n
}

// mapping returns the mapping under key, adding an empty one when absent.
func mapping(m *yaml.Node, key string) *yaml.Node {
	if v := mapValue(m, key); v != nil && v.Kind == yaml.MappingNode {
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setNode(m, key, v)
	return v
}

// sequence returns the sequence under key, adding an empty one when absent.
func sequence(m *yaml.Node, key string) *yaml.Node {
	if v := mapValue(m, key); v != nil && v.Kind == yaml.SequenceNode {
		v.Style = 0
		return v
	}
	v := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	setNode(m, key, v)
	return v
}

// encodeNode turns v into a YAML node.
func encodeNode(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return &n, nil
}
