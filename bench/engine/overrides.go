package engine

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

// ResolveAgentConfig reads an agent YAML document, applies an arm's dotted-path
// overrides and decodes the result strictly into schemas.AgentConfig.
//
// The overrides are applied to a generic map first, so an override can set a
// field the base file leaves out. Every path is checked against the YAML
// shape of AgentConfig before it is applied: notes.user_file exists,
// notes.userfile does not, and a typo never silently creates a field. Keys
// below a map field (env.FOO, files./tmp/x) are free.
func ResolveAgentConfig(doc []byte, set map[string]any) (schemas.AgentConfig, error) {
	var cfg schemas.AgentConfig
	var tree map[string]any
	if err := yaml.Unmarshal(doc, &tree); err != nil {
		return cfg, fmt.Errorf("parse agent config: %w", err)
	}
	if tree == nil {
		tree = map[string]any{}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := CheckOverridePath(k); err != nil {
			return cfg, err
		}
		if err := setPath(tree, strings.Split(k, "."), set[k]); err != nil {
			return cfg, fmt.Errorf("override %s: %w", k, err)
		}
	}
	out, err := yaml.Marshal(tree)
	if err != nil {
		return cfg, fmt.Errorf("encode agent config: %w", err)
	}
	if len(set) > 0 {
		// The overrides can produce a value the agent schema refuses (an enum
		// typo such as scripted.kind: nooop) that the Go types accept, so the
		// overridden values are checked against the agent schema before freezing.
		if err := suite.ValidateYAMLUnder(out, "effective agent config", suite.SchemaAgent, keys); err != nil {
			return cfg, err
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(out))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("decode agent config: %w", err)
	}
	return cfg, nil
}

// CheckOverridePath reports whether a dotted path names a field of the
// AgentConfig YAML shape.
func CheckOverridePath(path string) error {
	if path == "" {
		return errors.New("empty override path")
	}
	segs := strings.Split(path, ".")
	t := reflect.TypeOf(schemas.AgentConfig{})
	for i, seg := range segs {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if seg == "" {
			return fmt.Errorf("override path %q: empty segment", path)
		}
		switch t.Kind() {
		case reflect.Struct:
			f, ok := yamlField(t, seg)
			if !ok {
				return fmt.Errorf("override path %q: agent config has no field %q under %q", path, seg, prefix(segs, i))
			}
			t = f.Type
		case reflect.Map:
			if t.Key().Kind() != reflect.String {
				return fmt.Errorf("override path %q: %q is not a string-keyed map", path, prefix(segs, i))
			}
			t = t.Elem()
		default:
			return fmt.Errorf("override path %q: %q is a %s and has no field %q", path, prefix(segs, i), t.Kind(), seg)
		}
	}
	return nil
}

func prefix(segs []string, i int) string {
	if i == 0 {
		return "(root)"
	}
	return strings.Join(segs[:i], ".")
}

func yamlField(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("yaml")
		key := strings.Split(tag, ",")[0]
		if key == "-" {
			continue
		}
		if key == "" {
			key = strings.ToLower(f.Name)
		}
		if key == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func setPath(tree map[string]any, segs []string, v any) error {
	cur := tree
	for i, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg]
		if !ok || next == nil {
			m := map[string]any{}
			cur[seg] = m
			cur = m
			continue
		}
		m, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("%q holds a %T, not a mapping", strings.Join(segs[:i+1], "."), next)
		}
		cur = m
	}
	cur[segs[len(segs)-1]] = v
	return nil
}
