package replayrec

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

var textFold = strings.NewReplacer(
	"“", `"`, "”", `"`, "„", `"`, "‟", `"`, "«", `"`, "»", `"`,
	"‘", "'", "’", "'", "‚", "'", "‛", "'",
	`\n`, "\n", `\t`, "\t", `\"`, `"`,
	"*", "", "_", "", "`", "",
)

// NormalizeText folds what does not change an answer's meaning when two
// texts are compared: Unicode NFKC, curly quotes to straight, backslash
// escapes \n \t \" to their characters, markdown emphasis (*, _, `)
// removed, every run of whitespace to one space, a leading or trailing
// "..." removed, then trimmed. Case is kept. It is a copy of the rules of
// investigator.Normalize.
func NormalizeText(s string) string {
	s = norm.NFKC.String(s)
	s = textFold.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	for _, e := range []string{"...", "…"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, e))
		s = strings.TrimSpace(strings.TrimSuffix(s, e))
	}
	return strings.TrimSpace(s)
}
