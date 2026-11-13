// Package reporters writes an analysis in the formats the CLI and CI read:
// report.json and report.md (DESIGN.md 16.8). It also maps a verdict to the
// `check` exit code.
package reporters

import "github.com/maximhq/bifrost/bench/schemas"

// Version is the version of every reporter in this package.
const Version = "1.0.0"

// Builtin lists the reporters that ship. Each call returns fresh values.
func Builtin() []schemas.Reporter {
	return []schemas.Reporter{newJSONReporter(), newMarkdownReporter()}
}
