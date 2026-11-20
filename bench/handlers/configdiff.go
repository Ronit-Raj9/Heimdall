package handlers

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/maximhq/bifrost/bench/agentdiff"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ConfigDiff lists every agent-config field that differs between the first
// two arms, as dotted paths of the config's JSON form (env.FOO,
// notes.user_file). It compares the resolved configs in the manifest, so an
// arm that swaps the whole agent file shows every field it changes. When the
// resolved configs are identical or absent, the arms' `set:` overrides are
// compared instead. It is pure.
func ConfigDiff(m *schemas.Manifest) ([]schemas.ConfigChange, error) {
	arms := armIDs(m)
	out := []schemas.ConfigChange{}
	if len(arms) < 2 {
		return out, nil
	}
	a, err := flattenJSON(m.Arms[arms[0]].Config)
	if err != nil {
		return nil, fmt.Errorf("config of arm %s: %w", arms[0], err)
	}
	b, err := flattenJSON(m.Arms[arms[1]].Config)
	if err != nil {
		return nil, fmt.Errorf("config of arm %s: %w", arms[1], err)
	}
	out = diffFlat(a, b)
	if len(out) == 0 {
		out = diffFlat(m.Experiment.Arms[arms[0]].Set, m.Experiment.Arms[arms[1]].Set)
	}
	return out, nil
}

// diffFlat returns the changed paths of two flat maps, sorted by path. A path
// missing on one side has a null value there.
func diffFlat(a, b map[string]any) []schemas.ConfigChange {
	paths := make([]string, 0, len(a)+len(b))
	for p := range a {
		paths = append(paths, p)
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	out := []schemas.ConfigChange{}
	for _, p := range paths {
		if !reflect.DeepEqual(a[p], b[p]) {
			// Compared on the real values, shown redacted: the verdict
			// is readable by viewers and public orgs (4-B-13).
			out = append(out, schemas.ConfigChange{Path: p, A: agentdiff.PublicLeaf(p, a[p]), B: agentdiff.PublicLeaf(p, b[p])})
		}
	}
	return out
}

// flattenJSON turns a value's JSON form into dotted path -> leaf. Objects
// are walked; arrays and scalars are leaves.
func flattenJSON(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	out := map[string]any{}
	flattenInto(out, "", tree)
	return out, nil
}

// flattenInto writes the leaves of tree under prefix.
func flattenInto(out map[string]any, prefix string, tree any) {
	obj, ok := tree.(map[string]any)
	if !ok || len(obj) == 0 {
		if prefix != "" {
			out[prefix] = tree
		}
		return
	}
	for k, v := range obj {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		flattenInto(out, path, v)
	}
}
