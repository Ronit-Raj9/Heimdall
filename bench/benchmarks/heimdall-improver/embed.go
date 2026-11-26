// Package improversuite embeds the improver task suite heimdall-improver,
// so a server can copy it into a project that has never seen it.
package improversuite

import "embed"

// FS holds every file of this folder: suite.yaml, README.md, the tasks
// folder (the empty workspace's .keep file included) and, once the suite is
// admitted and its lock committed, suite.lock. A fixed list of names would
// leave a committed suite.lock out of the build.
//
//go:embed all:*
var FS embed.FS
