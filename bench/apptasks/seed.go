package apptasks

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Seed limits.
const (
	MaxSeedPerCollection = 25
	MaxSeedRecords       = 200
)

// collectionSchema is what the spec says about one collection's records:
// the body schemas of its create and update operations, merged.
type collectionSchema struct {
	props    map[string]map[string]any
	required []string
}

func collectionSchemas(m *scaffold.MockSpec) map[string]*collectionSchema {
	out := map[string]*collectionSchema{}
	for _, c := range m.Collections() {
		out[c] = &collectionSchema{props: map[string]map[string]any{}}
	}
	for _, op := range m.Operations {
		cs := out[op.Collection]
		if cs == nil || op.BodySchema == nil {
			continue
		}
		if props, ok := op.BodySchema["properties"].(map[string]any); ok {
			for k, v := range props {
				if s, ok := v.(map[string]any); ok {
					if _, have := cs.props[k]; !have {
						cs.props[k] = s
					}
				}
			}
		}
		if op.Kind == scaffold.MockCreate {
			if req, ok := op.BodySchema["required"].([]any); ok {
				for _, r := range req {
					if s, ok := r.(string); ok && s != "id" {
						cs.required = append(cs.required, s)
					}
				}
			}
		}
	}
	return out
}

// ValidateSeed checks seed records against the mock: only its collections,
// objects with a unique id, required create fields present, and declared
// fields of the declared JSON type (and enum).
func ValidateSeed(seed State, m *scaffold.MockSpec) error {
	schemas := collectionSchemas(m)
	total := 0
	for _, coll := range sortedKeys(seed) {
		items := seed[coll]
		cs, ok := schemas[coll]
		if !ok {
			if coll == "_actions" {
				return fmt.Errorf("_actions is the mock's own log; seed only the API's collections")
			}
			return fmt.Errorf("collection %q is not in the API (%s)", coll, strings.Join(m.Collections(), ", "))
		}
		if len(items) > MaxSeedPerCollection {
			return fmt.Errorf("%s: %d records, at most %d", coll, len(items), MaxSeedPerCollection)
		}
		total += len(items)
		ids := map[string]bool{}
		for i, it := range items {
			rec, ok := it.(map[string]any)
			if !ok {
				return fmt.Errorf("%s[%d]: a record must be a JSON object", coll, i)
			}
			id := text(rec["id"])
			switch rec["id"].(type) {
			case string, float64:
			default:
				return fmt.Errorf("%s[%d]: needs an \"id\" (a string or a number)", coll, i)
			}
			if id == "" {
				return fmt.Errorf("%s[%d]: the id is empty", coll, i)
			}
			if ids[id] {
				return fmt.Errorf("%s: id %s is used twice", coll, id)
			}
			ids[id] = true
			for _, r := range cs.required {
				if _, ok := rec[r]; !ok {
					return fmt.Errorf("%s %s: required field %q is missing", coll, id, r)
				}
			}
			for k, v := range rec {
				if s, ok := cs.props[k]; ok {
					if err := typeCheck(s, v); err != nil {
						return fmt.Errorf("%s %s: field %q %v", coll, id, k, err)
					}
				}
			}
		}
	}
	if total > MaxSeedRecords {
		return fmt.Errorf("%d records, at most %d", total, MaxSeedRecords)
	}
	return nil
}

// typeCheck tests a value against a JSON schema's type, enum and, for an
// object, its declared properties (one level deep is enough for seed data).
func typeCheck(s map[string]any, v any) error {
	if v == nil {
		if n, _ := s["nullable"].(bool); n {
			return nil
		}
		if hasType(s, "null") {
			return nil
		}
		if _, typed := s["type"]; !typed {
			return nil
		}
		return fmt.Errorf("is null, the spec says %v", s["type"])
	}
	if enum, ok := s["enum"].([]any); ok && len(enum) > 0 {
		hit := false
		for _, e := range enum {
			a, _ := json.Marshal(e)
			b, _ := json.Marshal(v)
			hit = hit || string(a) == string(b)
		}
		if !hit {
			return fmt.Errorf("is %s, not one of the spec's values %s", short(v), short(enum))
		}
	}
	if _, typed := s["type"]; !typed {
		return nil
	}
	got := jsonType(v)
	if hasType(s, got) || (got == "integer" && hasType(s, "number")) {
		return nil
	}
	return fmt.Errorf("is a %s, the spec says %v", got, s["type"])
}

func hasType(s map[string]any, t string) bool {
	switch x := s["type"].(type) {
	case string:
		return x == t
	case []any:
		for _, y := range x {
			if y == t {
				return true
			}
		}
	}
	return false
}

func jsonType(v any) string {
	switch x := v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func short(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
