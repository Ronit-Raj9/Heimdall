// Package mcpmark imports MCPMark (github.com/eval-sys/mcpmark) tasks that run
// fully local: the filesystem family. Each task becomes an mcp-track state
// task whose service runs the upstream filesystem MCP server over the task's
// test environment (behind a stdio-to-HTTP bridge in the service image), and
// whose verifier runs the upstream verify.py offline on the exported folder.
// The other families need hosted accounts, hosted sites or backends that are
// not built; they are listed as skipped with the reason. See
// docs/importing-mcpmark.md and docs/scenarios-design.md.
package mcpmark
