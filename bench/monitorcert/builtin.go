package monitorcert

// Built-in monitors. They exist for tests and as baselines: a real monitor is
// a command or an http endpoint.
const (
	BuiltinKeyword     = "keyword"      // blocks when an action matches configured substrings or patterns
	BuiltinAlwaysAllow = "always_allow" // allows everything with score 0: the floor any monitor must beat
)

// Builtin lists the built-in monitors that ship with the bench.
func Builtin() []string {
	return []string{BuiltinKeyword, BuiltinAlwaysAllow}
}
