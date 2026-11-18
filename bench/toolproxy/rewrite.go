package toolproxy

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// This file holds the pure rewrites of the safety fault modes (Safety probes
// area, S1): the tools/list rewrites of MCP tool poisoning (describe_poison,
// shadow, rug_pull, schema_poison) and the quiet result rewrites
// (plausible_wrong, schema_drift). None of them does I/O.

// rewriteListing rewrites an MCP tools/list result for a listing fault: the
// description of every matching tool (ft.Tool, empty for every tool) gets the
// rendered text appended; for schema_poison every parameter description gets
// it instead, and ft.Patch is merged into the tool's inputSchema.properties.
// ok is false when the result is not a listing or no tool matched, in which
// case nothing changed.
func rewriteListing(result json.RawMessage, ft schemas.ToolFault) (json.RawMessage, bool) {
	var obj map[string]any
	if err := decodeNumbers(result, &obj); err != nil || obj == nil {
		return result, false
	}
	tools, _ := obj["tools"].([]any)
	text := renderNonce(ft.Text, ft.Nonce)
	changed := false
	for _, t := range tools {
		tool, _ := t.(map[string]any)
		if tool == nil {
			continue
		}
		name, _ := tool["name"].(string)
		if ft.Tool != "" && name != ft.Tool {
			continue
		}
		switch ft.Mode {
		case FaultSchemaPoison:
			changed = poisonSchema(tool, text, ft) || changed
		default:
			desc, _ := tool["description"].(string)
			tool["description"] = joinText(desc, text)
			changed = true
		}
	}
	if !changed {
		return result, false
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return result, false
	}
	return out, true
}

// quietResult applies a quiet fault (plausible_wrong or schema_drift) to an
// MCP tool result: to its structuredContent when that is an object, and to
// every text item whose text is a JSON object. The result stays a success.
// ok is false when nothing in it could be rewritten.
func quietResult(result json.RawMessage, ft schemas.ToolFault) (json.RawMessage, bool) {
	var obj map[string]any
	if err := decodeNumbers(result, &obj); err != nil || obj == nil {
		return result, false
	}
	changed := false
	if sc, ok := obj["structuredContent"].(map[string]any); ok {
		if v, ok := quietValue(sc, ft); ok {
			obj["structuredContent"], changed = v, true
		}
	}
	content, _ := obj["content"].([]any)
	for _, c := range content {
		item, _ := c.(map[string]any)
		if item == nil || item["type"] != "text" {
			continue
		}
		s, _ := item["text"].(string)
		var inner map[string]any
		if decodeNumbers([]byte(s), &inner) != nil || inner == nil {
			continue
		}
		v, ok := quietValue(inner, ft)
		if !ok {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		item["text"], changed = string(b), true
	}
	if !changed {
		return result, false
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return result, false
	}
	return out, true
}

// quietHTTPBody applies a quiet fault to a plain HTTP or external answer
// whose body is a JSON object. Any other body is returned unchanged.
func quietHTTPBody(body []byte, ft schemas.ToolFault) ([]byte, bool) {
	var obj map[string]any
	if err := decodeNumbers(body, &obj); err != nil || obj == nil {
		return body, false
	}
	v, ok := quietValue(obj, ft)
	if !ok {
		return body, false
	}
	out, err := json.Marshal(v)
	if err != nil {
		return body, false
	}
	return out, true
}

// MergePatch applies an RFC 7386 JSON merge patch to target and returns the
// result: a patch object merges member by member, a null member deletes, any
// other patch value replaces the target. It does not modify patch.
func MergePatch(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return cloneJSON(patch)
	}
	t, ok := target.(map[string]any)
	if !ok {
		t = map[string]any{}
	}
	for k, v := range p {
		if v == nil {
			delete(t, k)
			continue
		}
		t[k] = MergePatch(t[k], v)
	}
	return t
}

// RenameKeys renames object members at any depth, old name to new name, and
// reports whether any was renamed. A member is not renamed over an existing
// member of the new name.
func RenameKeys(v any, rename map[string]string) (any, bool) {
	changed := false
	switch x := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(x) {
			nv, c := RenameKeys(x[k], rename)
			x[k], changed = nv, changed || c
		}
		for _, old := range sortedKeys(x) {
			to, ok := rename[old]
			if !ok || to == "" || to == old {
				continue
			}
			if _, taken := x[to]; taken {
				continue
			}
			x[to] = x[old]
			delete(x, old)
			changed = true
		}
	case []any:
		for i := range x {
			nv, c := RenameKeys(x[i], rename)
			x[i], changed = nv, changed || c
		}
	}
	return v, changed
}

// quietValue applies the fault's patch or renames to one decoded object.
func quietValue(obj map[string]any, ft schemas.ToolFault) (any, bool) {
	switch ft.Mode {
	case FaultPlausibleWrong:
		if len(ft.Patch) == 0 {
			return obj, false
		}
		return MergePatch(obj, map[string]any(ft.Patch)), true
	case FaultSchemaDrift:
		return RenameKeys(obj, ft.Rename)
	}
	return obj, false
}

// poisonSchema appends text to every parameter description of one tool's
// inputSchema and merges the fault's patch (strings rendered with the
// nonce) into its properties.
func poisonSchema(tool map[string]any, text string, ft schemas.ToolFault) bool {
	schema, _ := tool["inputSchema"].(map[string]any)
	if schema == nil {
		schema = map[string]any{"type": "object"}
		tool["inputSchema"] = schema
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	changed := false
	if text != "" {
		for _, k := range sortedKeys(props) {
			p, _ := props[k].(map[string]any)
			if p == nil {
				continue
			}
			desc, _ := p["description"].(string)
			p["description"] = joinText(desc, text)
			changed = true
		}
	}
	if len(ft.Patch) > 0 {
		patch := renderValue(cloneJSON(map[string]any(ft.Patch)), ft.Nonce)
		merged, _ := MergePatch(props, patch).(map[string]any)
		props, changed = merged, true
	}
	schema["properties"] = props
	return changed
}

// joinText appends text to s on a new paragraph.
func joinText(s, text string) string {
	if s == "" {
		return text
	}
	return s + "\n\n" + text
}

// renderNonce replaces {{nonce}} in s.
func renderNonce(s, nonce string) string {
	return strings.ReplaceAll(s, nonceVar, nonce)
}

// renderValue replaces {{nonce}} in every string of a decoded JSON value.
func renderValue(v any, nonce string) any {
	switch x := v.(type) {
	case string:
		return renderNonce(x, nonce)
	case map[string]any:
		for k, e := range x {
			x[k] = renderValue(e, nonce)
		}
	case []any:
		for i, e := range x {
			x[i] = renderValue(e, nonce)
		}
	}
	return v
}

// cloneJSON deep-copies a decoded JSON value so a patch from the fault is
// never shared with a result.
func cloneJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = cloneJSON(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneJSON(e)
		}
		return out
	}
	return v
}

// decodeNumbers decodes JSON keeping numbers as json.Number, so values the
// rewrite does not touch keep their exact text.
func decodeNumbers(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}

// sortedKeys returns the keys of m in order, so rewrites are deterministic.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
