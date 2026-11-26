// Package improversuite embeds the improver task suite heimdall-improver,
// so a server can copy it into a project that has never seen it.
package improversuite

import "embed"

// FS holds suite.yaml, README.md and the tasks folder (the empty
// workspace's .keep file included).
//
//go:embed suite.yaml README.md all:tasks
var FS embed.FS
