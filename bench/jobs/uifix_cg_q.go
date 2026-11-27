package jobs

import "strings"

// QMaxLen bounds Filter.Q.
const QMaxLen = 200

// likePattern is the LIKE pattern for a text filter: lower case, cut to
// QMaxLen, with %, _ and the escape character escaped. ok is false for an
// empty filter.
func likePattern(q string) (string, bool) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return "", false
	}
	if len(q) > QMaxLen {
		q = q[:QMaxLen]
	}
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return "%" + q + "%", true
}
