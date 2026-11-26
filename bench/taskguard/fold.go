package taskguard

import "strings"

var quoteFolder = strings.NewReplacer("‘", "'", "’", "'", "“", "\"", "”", "\"")

// fold is the guard's own normalizer: lowercase, curly quotes to straight,
// every run of whitespace to one space, trimmed.
func fold(s string) string {
	s = quoteFolder.Replace(strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}
