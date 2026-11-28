// Package history knows every version a built-in agent file (agents/*.yaml,
// agents/images/**, examples/frameworks/**) has shipped with, by sha256, so
// a project copy that was never edited can be told from one the
// organization changed. history_gen.go is written by gen.py.
package history

//go:generate sh -c "cd ../../.. && python3 -I bench/agents/history/gen.py"

var byHash = func() map[string][]string {
	m := map[string][]string{}
	for _, v := range versions {
		m[v[0]] = append(m[v[0]], v[1])
	}
	return m
}()

// Paths returns the current built-in paths (bench-relative, e.g.
// agents/images/codex/Dockerfile) that once held content with sha256 hex
// sum, or nil when no built-in file ever did.
func Paths(sum string) []string { return byHash[sum] }
