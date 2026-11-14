// Package ci holds the bench's pull-request gate: check.sh and the composite
// action in this folder run `bifrost-bench check` on every pull request that
// changes an agent. The Go side is only the guard on the recorded bundle the
// gate replays. `bifrost-bench check -exp` reads sealed runs without
// re-hashing them, so the test beside this file verifies every seal of the
// bundle: a bundle edited by hand fails CI before any verdict is read from it.
package ci

// RecordedRoot is the recorded bundle's runs root, relative to the bench root.
// Every folder in it with a manifest.json is one sealed experiment.
const RecordedRoot = "testdata/recorded"
