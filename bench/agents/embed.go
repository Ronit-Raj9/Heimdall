// Package agents embeds the shipped agent configs (agents/*.yaml) and their
// image folders (agents/images/), so the web agent builder can offer them as
// templates without reading the server's source tree at run time.
package agents

import "embed"

// Files holds agents/*.yaml and agents/images/**.
//
//go:embed *.yaml images
var Files embed.FS
