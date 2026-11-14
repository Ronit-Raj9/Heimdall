// Package spec holds the JSON Schemas (draft 2020-12) for every YAML file a
// person writes for Bifrost Bench: suite.yaml, task.yaml, probes/<id>.yaml,
// agents/<id>.yaml, experiments/<name>.yaml and policies/<name>.yaml.
//
// The schemas are the source of truth for those files, as
// transports/config.schema.json is for Bifrost. Each one mirrors the YAML form
// of a Go type in bench/schemas (Suite, Task, ProbeSpec, AgentConfig,
// Experiment) and sets additionalProperties to false on every struct, so a
// misspelled key fails validation instead of being ignored. A change to one of
// those Go types changes its schema in the same commit.
package spec

import "embed"

// FS holds the schema files at the root, named <kind>.schema.json, for example
// "task.schema.json". The suite loader compiles them once and validates every
// document before decoding it.
//
//go:embed *.schema.json
var FS embed.FS
