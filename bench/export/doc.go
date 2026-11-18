// Package export writes Heimdall's tasks, results and traces in formats other
// tools read (docs/export.md):
//
//   - Harbor task folders (task.toml, instruction.md, environment/,
//     solution/, tests/), so a suite runs under Harbor and the tools built
//     on it, and imports back with `import harbor`;
//   - Every Eval Ever aggregate records (one per agent config, model and
//     suite), validated against the schema vendored in schemas/eee;
//   - a flat run table (JSONL or CSV, one row per sealed run);
//   - OTLP trace presets: the endpoint path, header names, encoding and
//     attribute mapping each observability backend documents, applied on
//     top of traceexport's OTLP/JSON.
//
// Everything here reads data the bench already sealed or locked; nothing
// calls a model or a third-party service. Formats were checked against the
// upstream documents named in docs/export.md on the date given there; what
// could not be checked is marked unverified in the code and the doc.
package export
