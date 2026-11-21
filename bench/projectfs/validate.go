package projectfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

// The built-in validators check the project's YAML with the same strict
// decoders the planner and the suite loader use, so a file the website
// saves is one the CLI can load. Other lanes add more via RegisterValidator.
func init() {
	for _, ext := range []string{".yaml", ".yml"} {
		RegisterValidator(AreaAgents, "*"+ext, schemaValidator(suite.SchemaAgent, func() any { return &schemas.AgentConfig{} }))
		RegisterValidator(AreaExperiments, "*"+ext, schemaValidator(suite.SchemaExperiment, func() any { return &schemas.Experiment{} }))
		RegisterValidator(AreaPolicies, "*"+ext, validatePolicy)
		RegisterValidator(AreaBenchmarks, "*/"+suite.SuiteFile, schemaValidator(suite.SchemaSuite, func() any { return &schemas.Suite{} }))
		RegisterValidator(AreaBenchmarks, "*/tasks/*/"+suite.TaskFile, schemaValidator(suite.SchemaTask, func() any { return &schemas.Task{} }))
		RegisterValidator(AreaBenchmarks, "*/probes/*"+ext, schemaValidator(suite.SchemaProbe, func() any { return &schemas.ProbeSpec{} }))
	}
}

func schemaValidator(schema string, newT func() any) Validator {
	return func(a Area, rel string, data []byte) error {
		return suite.DecodeYAML(data, string(a)+"/"+rel, schema, newT())
	}
}

// validatePolicy checks a release policy (a file with policy_version). Other
// YAML in policies/ (schedules, alerts) only has to parse.
func validatePolicy(a Area, rel string, data []byte) error {
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("%s/%s: %v", a, rel, err)
	}
	if _, ok := top["policy_version"]; !ok {
		return nil
	}
	_, err := policy.Parse(data, string(a)+"/"+rel)
	return err
}

// isYAML reports whether rel names a YAML file.
func isYAML(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	return ext == ".yaml" || ext == ".yml"
}

// validateSyntax checks that any YAML file parses, whatever its area.
func validateSyntax(a Area, rel string, data []byte) error {
	if !isYAML(rel) {
		return nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var v any
		err := dec.Decode(&v)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("%s/%s: %v", a, rel, err)
		}
	}
}
