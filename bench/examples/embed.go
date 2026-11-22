// Package examples embeds the example projects' READMEs that the user guides
// link to, so the server can serve them as in-app guides (GET
// /api/bench/docs/{name}). The example projects themselves are not embedded.
package examples

import _ "embed"

// AcmeNodeREADME is examples/acme-node/README.md, served as the in-app guide
// "example-acme-node" (docs/bring-your-own.md links to it).
//
//go:embed acme-node/README.md
var AcmeNodeREADME []byte
