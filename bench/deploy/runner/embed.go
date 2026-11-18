// Package runnerdeploy embeds the runner install script so the hosted server
// can serve it at GET /api/bench/runners/install.sh (Runners area,
// docs/runners.md).
package runnerdeploy

import _ "embed"

// InstallScript is deploy/runner/install.sh.
//
//go:embed install.sh
var InstallScript []byte
