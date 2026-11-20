package suite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/spec"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

// Schema names, one per file in bench/spec (<name>.schema.json).
const (
	SchemaSuite      = "suite"
	SchemaTask       = "task"
	SchemaProbe      = "probe"
	SchemaAgent      = "agent"
	SchemaExperiment = "experiment"
	SchemaPolicy     = "policy"
)

// schemaBaseURL is the $id prefix every file in bench/spec declares.
const schemaBaseURL = "https://github.com/maximhq/bifrost/bench/spec/"

var allSchemas = []string{SchemaSuite, SchemaTask, SchemaProbe, SchemaAgent, SchemaExperiment, SchemaPolicy}

var (
	compileOnce sync.Once
	compiled    map[string]*jsonschema.Schema
	compileErr  error
)

// DecodeYAMLFile reads path and decodes it with DecodeYAML, naming the file by
// display in every error.
func DecodeYAMLFile(path, display, schema string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: read: %w", display, err)
	}
	return DecodeYAML(data, display, schema, out)
}

// DecodeYAML validates one YAML document against spec/<schema>.schema.json and
// then decodes it into out with unknown keys rejected. Validation runs first
// because its errors name the exact field; the strict decode is the second
// line of defence for a schema that drifted from its Go type. file is used
// only in error messages.
func DecodeYAML(data []byte, file, schema string, out any) error {
	sch, err := compiledSchema(schema)
	if err != nil {
		return err
	}
	doc, err := yamlToJSONValue(data)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if err := sch.Validate(doc); err != nil {
		return fmt.Errorf("%s: %w", file, describeValidation(err))
	}
	if err := decodeStrict(data, out); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}

// ValidateYAMLUnder validates one YAML document against
// spec/<schema>.schema.json and reports only the failures at, below or above
// one of the dotted paths (never at the document root). The planner uses it
// for an agent config built from a base file and an arm's overrides: the
// overridden values never exist as a file the CLI could check, while the base
// file's own problems are reported where that file is loaded. file is used
// only in error messages.
func ValidateYAMLUnder(data []byte, file, schema string, paths []string) error {
	sch, err := compiledSchema(schema)
	if err != nil {
		return err
	}
	doc, err := yamlToJSONValue(data)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	verr := sch.Validate(doc)
	var ve *jsonschema.ValidationError
	if verr == nil || !errors.As(verr, &ve) {
		if verr != nil {
			return fmt.Errorf("%s: validate: %w", file, verr)
		}
		return nil
	}
	related := func(loc []string) bool {
		if len(loc) == 0 {
			return false
		}
		l := strings.Join(loc, ".")
		for _, p := range paths {
			if l == p || strings.HasPrefix(l, p+".") || strings.HasPrefix(p, l+".") {
				return true
			}
		}
		return false
	}
	pr := message.NewPrinter(language.English)
	var msgs []string
	seen := map[string]bool{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		if !related(e.InstanceLocation) {
			return
		}
		for _, m := range leafMessages(e, pr) {
			if !seen[m] {
				seen[m] = true
				msgs = append(msgs, m)
			}
		}
	}
	walk(ve)
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("%s: schema: %s", file, strings.Join(msgs, "; "))
}

// ValidateSchemas compiles every embedded schema. A failure is a bug in
// bench/spec, so tests and `doctor` call it to fail fast.
func ValidateSchemas() error {
	for _, name := range allSchemas {
		if _, err := compiledSchema(name); err != nil {
			return err
		}
	}
	return nil
}

// compiledSchema returns the compiled schema for name. The schemas are
// embedded and immutable, so they are compiled once per process.
func compiledSchema(name string) (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		compiled, compileErr = compileAll()
	})
	if compileErr != nil {
		return nil, compileErr
	}
	sch, ok := compiled[name]
	if !ok {
		return nil, fmt.Errorf("unknown schema %q", name)
	}
	return sch, nil
}

func compileAll() (map[string]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	for _, name := range allSchemas {
		file := name + ".schema.json"
		raw, err := spec.FS.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read spec/%s: %w", file, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("parse spec/%s: %w", file, err)
		}
		if err := c.AddResource(schemaBaseURL+file, doc); err != nil {
			return nil, fmt.Errorf("add spec/%s: %w", file, err)
		}
	}
	out := make(map[string]*jsonschema.Schema, len(allSchemas))
	for _, name := range allSchemas {
		sch, err := c.Compile(schemaBaseURL + name + ".schema.json")
		if err != nil {
			return nil, fmt.Errorf("compile spec/%s.schema.json: %w", name, err)
		}
		out[name] = sch
	}
	return out, nil
}

// decodeStrict decodes exactly one YAML document into out and rejects keys
// that out does not declare.
func decodeStrict(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("empty document")
		}
		return fmt.Errorf("decode: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("decode: more than one YAML document")
	}
	return nil
}

// yamlToJSONValue turns a YAML document into the generic JSON value the
// validator expects (objects, arrays, json.Number, strings, booleans, null).
func yamlToJSONValue(data []byte) (any, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	b, err := json.Marshal(normalizeYAML(raw))
	if err != nil {
		return nil, fmt.Errorf("convert yaml to json: %w", err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("convert yaml to json: %w", err)
	}
	return v, nil
}

// normalizeYAML converts maps with non-string keys, which yaml.v3 produces for
// keys like `1:`, into string-keyed maps so they can be marshalled to JSON.
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeYAML(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalizeYAML(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeYAML(val)
		}
		return out
	default:
		return v
	}
}

// describeValidation flattens a validation error into one line per failing
// field, for example "controls.exploit: required field missing".
func describeValidation(err error) error {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return fmt.Errorf("validate: %w", err)
	}
	p := message.NewPrinter(language.English)
	var msgs []string
	seen := map[string]bool{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		for _, m := range leafMessages(e, p) {
			if !seen[m] {
				seen[m] = true
				msgs = append(msgs, m)
			}
		}
	}
	walk(ve)
	if len(msgs) == 0 {
		return fmt.Errorf("schema: %s", ve.Error())
	}
	return fmt.Errorf("schema: %s", strings.Join(msgs, "; "))
}

func leafMessages(e *jsonschema.ValidationError, p *message.Printer) []string {
	switch k := e.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		out := make([]string, 0, len(k.Properties))
		for _, prop := range k.Properties {
			out = append(out, fieldPath(append(append([]string(nil), e.InstanceLocation...), prop))+": unknown field")
		}
		return out
	case *kind.Required:
		out := make([]string, 0, len(k.Missing))
		for _, m := range k.Missing {
			out = append(out, fieldPath(append(append([]string(nil), e.InstanceLocation...), m))+": required field missing")
		}
		return out
	default:
		return []string{fieldPath(e.InstanceLocation) + ": " + e.ErrorKind.LocalizedString(p)}
	}
}

// fieldPath renders an instance location as a dotted YAML path with array
// indexes in brackets: ["controls", "mutants", "0"] -> controls.mutants[0].
func fieldPath(loc []string) string {
	if len(loc) == 0 {
		return "(document)"
	}
	var sb strings.Builder
	for _, tok := range loc {
		if _, err := strconv.Atoi(tok); err == nil && sb.Len() > 0 {
			sb.WriteString("[" + tok + "]")
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(tok)
	}
	return sb.String()
}
